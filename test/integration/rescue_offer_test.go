//go:build integration

package integration

import (
	"context"
	"food-delivery-mvp/internal/domain/errs"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "food-delivery-mvp/internal/adapter/postgres"

	domainorder "food-delivery-mvp/internal/domain/order"
	orderusecase "food-delivery-mvp/internal/usecase/order"
	partnerusecase "food-delivery-mvp/internal/usecase/partner"
)

const rescueDiscountPercent = 40

// seedRescueOffer inserts a rescue offer directly, bypassing partner
// validation — tests that need one already in a specific state (expired,
// nearly-exhausted) build it exactly that way rather than creating it live
// and waiting.
func seedRescueOffer(t *testing.T, pool *pgxpool.Pool, venueID, menuItemID uuid.UUID, remaining int, startsAt, endsAt time.Time) uuid.UUID {
	t.Helper()
	offerID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO rescue_offers (id, venue_id, menu_item_id, discount_percent, initial_quantity, remaining_quantity, starts_at, ends_at)
		VALUES ($1, $2, $3, $4, $5, $5, $6, $7)
	`, offerID, venueID, menuItemID, rescueDiscountPercent, remaining, startsAt, endsAt)
	require.NoError(t, err)
	return offerID
}

// seedCartWithRescueItem gives clientID a cart line pinned to a rescue
// offer — the discounted price is computed the same way the domain type
// itself would, so the test's own expectations line up with what checkout
// re-derives.
func seedCartWithRescueItem(t *testing.T, pool *pgxpool.Pool, clientID, venueID, menuItemID, offerID uuid.UUID, quantity int, discountedPriceMinor int64) {
	t.Helper()
	ctx := context.Background()

	cartID := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO carts (id, client_id, venue_id) VALUES ($1, $2, $3)`, cartID, clientID, venueID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO cart_items (id, cart_id, menu_item_id, quantity, price_minor_snapshot, rescue_offer_id)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, uuid.New(), cartID, menuItemID, quantity, discountedPriceMinor, offerID)
	require.NoError(t, err)
}

func newCheckoutService(pool *pgxpool.Pool) *orderusecase.CheckoutService {
	return orderusecase.NewCheckoutService(
		pgadapter.NewCartRepository(pool),
		pgadapter.NewVenueRepository(pool),
		pgadapter.NewMenuRepository(pool),
		pgadapter.NewRescueOfferRepository(pool),
		pgadapter.NewOrderRepository(pool),
		pgadapter.NewIdempotencyRepository(pool),
		pgadapter.NewOutboxRepository(pool),
		pgadapter.NewTxManager(pool),
	)
}

// TestPlaceOrder_RescueOfferExpiredBetweenCartAddAndCheckout is docs/PLAN.md
// 5.5's own mandated test: the offer's window closes entirely while the
// item sits in the cart. Checkout must fail with RESCUE_OFFER_EXPIRED and
// touch nothing.
func TestPlaceOrder_RescueOfferExpiredBetweenCartAddAndCheckout(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	venueID := seedOpenVenue(t, pool)
	menuItemID := seedMenuItem(t, pool, venueID, 10)
	clientID := uuid.New()

	// A window that already closed, as if the offer ended while this item
	// sat in the cart.
	offerID := seedRescueOffer(t, pool, venueID, menuItemID, 5,
		time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
	discounted := int64(itemPriceMinor) * int64(100-rescueDiscountPercent) / 100
	seedCartWithRescueItem(t, pool, clientID, venueID, menuItemID, offerID, 1, discounted)

	svc := newCheckoutService(pool)
	_, _, err := svc.PlaceOrder(ctx, clientID, uuid.New().String(), "addr", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeRescueOfferExpired, code)

	var stockQty int
	require.NoError(t, pool.QueryRow(ctx, `SELECT stock_qty FROM menu_items WHERE id = $1`, menuItemID).Scan(&stockQty))
	assert.Equal(t, 10, stockQty, "a rejected checkout must not touch stock")

	var remaining int
	require.NoError(t, pool.QueryRow(ctx, `SELECT remaining_quantity FROM rescue_offers WHERE id = $1`, offerID).Scan(&remaining))
	assert.Equal(t, 5, remaining, "a rejected checkout must not touch the offer's remaining count either")
}

// TestPlaceOrder_ConcurrentCheckoutsOnLastRescueUnit is docs/PLAN.md 5.5's
// other mandated test: two customers try to redeem the last unit of the
// same rescue offer at the same time. Regular stock is left plentiful on
// purpose, so INSUFFICIENT_STOCK can never fire here — the only thing
// under test is whether rescue_offers.remaining_quantity's own row lock
// (RescueOfferRepository.LockForCheckout, docs/PLAN.md 5.5) prevents both
// checkouts from claiming the same last discounted unit. Since a request
// for more than what remains SUCCEEDS as a full-price fallback rather than
// failing (docs/PLAN.md 5.5's own line-splitting behavior), the loser here
// does not error — it simply pays full price, which this test asserts
// directly.
func TestPlaceOrder_ConcurrentCheckoutsOnLastRescueUnit(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	venueID := seedOpenVenue(t, pool)
	menuItemID := seedMenuItem(t, pool, venueID, 100) // ample regular stock
	offerID := seedRescueOffer(t, pool, venueID, menuItemID, 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	discounted := int64(itemPriceMinor) * int64(100-rescueDiscountPercent) / 100

	clientA, clientB := uuid.New(), uuid.New()
	seedCartWithRescueItem(t, pool, clientA, venueID, menuItemID, offerID, 1, discounted)
	seedCartWithRescueItem(t, pool, clientB, venueID, menuItemID, offerID, 1, discounted)

	// Both carts reference the same menu item, so delayingMenuItemLookup
	// (checkout_race_test.go) still forces the two transactions to overlap
	// for real, the same way it does for the plain-stock version of this
	// test — a rescue offer belongs to exactly one menu item, so there is
	// no way to exercise its lock without also touching that item's own
	// stock lock at the same time.
	svc := orderusecase.NewCheckoutService(
		pgadapter.NewCartRepository(pool),
		pgadapter.NewVenueRepository(pool),
		&delayingMenuItemLookup{real: pgadapter.NewMenuRepository(pool), delay: checkoutHoldDelay},
		pgadapter.NewRescueOfferRepository(pool),
		pgadapter.NewOrderRepository(pool),
		pgadapter.NewIdempotencyRepository(pool),
		pgadapter.NewOutboxRepository(pool),
		pgadapter.NewTxManager(pool),
	)

	type placement struct {
		order *domainorder.Order
		err   error
	}
	var atGate sync.WaitGroup
	atGate.Add(2)
	start := make(chan struct{})
	results := make(chan placement, 2)

	place := func(clientID uuid.UUID) {
		atGate.Done()
		<-start
		o, _, err := svc.PlaceOrder(ctx, clientID, uuid.New().String(), "addr", "+70000000000", "")
		results <- placement{order: o, err: err}
	}
	go place(clientA)
	go place(clientB)
	atGate.Wait()
	close(start)

	r1 := <-results
	r2 := <-results

	require.NoError(t, r1.err, "the platform never rejects a rescue shortfall outright, it falls back to full price")
	require.NoError(t, r2.err)

	prices := []int64{r1.order.Items[0].UnitPriceMinor, r2.order.Items[0].UnitPriceMinor}
	assert.Contains(t, prices, discounted, "exactly one order must have won the last discounted unit")
	assert.Contains(t, prices, int64(itemPriceMinor), "the other must have fallen back to full price")
	assert.NotEqual(t, prices[0], prices[1], "both orders winning (or both losing) means the lock did not do its job")

	var remaining int
	require.NoError(t, pool.QueryRow(ctx, `SELECT remaining_quantity FROM rescue_offers WHERE id = $1`, offerID).Scan(&remaining))
	assert.Equal(t, 0, remaining, "the single unit must be consumed exactly once, never left at 1 or driven negative")
}

// TestCreateRescueOffer_OverlappingWindowRejected is docs/PLAN.md 5.5's own
// mandated test: the database's exclusion constraint, not application code,
// is what actually guarantees two live offers on the same item can never
// coexist.
func TestCreateRescueOffer_OverlappingWindowRejected(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	venueID := seedOpenVenue(t, pool)
	menuItemID := seedMenuItem(t, pool, venueID, 10)
	svc := partnerusecase.NewRescueOfferService(pgadapter.NewRescueOfferRepository(pool))

	now := time.Now()
	_, err := svc.CreateOffer(ctx, venueID, partnerusecase.NewRescueOfferRequest{
		MenuItemID:      menuItemID,
		DiscountPercent: 30,
		Quantity:        5,
		StartsAt:        now,
		EndsAt:          now.Add(3 * time.Hour),
	})
	require.NoError(t, err)

	// A second window overlapping the first by an hour on the same item.
	_, err = svc.CreateOffer(ctx, venueID, partnerusecase.NewRescueOfferRequest{
		MenuItemID:      menuItemID,
		DiscountPercent: 50,
		Quantity:        2,
		StartsAt:        now.Add(2 * time.Hour),
		EndsAt:          now.Add(4 * time.Hour),
	})

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeRescueOfferOverlap, code)
}

// TestCreateRescueOffer_NonOverlappingWindowSucceeds is the negative-space
// check for the overlap test above: a window that does NOT overlap an
// existing one on the same item must succeed, proving the exclusion
// constraint rejects the right thing and nothing more.
func TestCreateRescueOffer_NonOverlappingWindowSucceeds(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	venueID := seedOpenVenue(t, pool)
	menuItemID := seedMenuItem(t, pool, venueID, 10)
	svc := partnerusecase.NewRescueOfferService(pgadapter.NewRescueOfferRepository(pool))

	now := time.Now()
	_, err := svc.CreateOffer(ctx, venueID, partnerusecase.NewRescueOfferRequest{
		MenuItemID: menuItemID, DiscountPercent: 30, Quantity: 5,
		StartsAt: now, EndsAt: now.Add(time.Hour),
	})
	require.NoError(t, err)

	_, err = svc.CreateOffer(ctx, venueID, partnerusecase.NewRescueOfferRequest{
		MenuItemID: menuItemID, DiscountPercent: 50, Quantity: 2,
		StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour), // starts exactly when the first ends
	})
	require.NoError(t, err)
}

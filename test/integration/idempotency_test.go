//go:build integration

package integration

import (
	"context"
	"food-delivery-mvp/internal/domain/errs"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "food-delivery-mvp/internal/adapter/postgres"

	domainorder "food-delivery-mvp/internal/domain/order"
	orderusecase "food-delivery-mvp/internal/usecase/order"
)

// TestPlaceOrder_IdempotentRetry_ReturnsSameOrderWithoutDuplicating covers
// the ordinary case from docs/PLAN.md 5.2: a client resends the exact same
// request (same Idempotency-Key, same body) after, say, losing the first
// response. The retry arrives with an EMPTY cart, because the first
// attempt already succeeded and cleared it — this is exactly the scenario
// that would misfire as CART_EMPTY if PlaceOrder checked the cart before
// checking the idempotency key.
func TestPlaceOrder_IdempotentRetry_ReturnsSameOrderWithoutDuplicating(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	venueID := seedOpenVenue(t, pool)
	menuItemID := seedMenuItem(t, pool, venueID, 10)
	clientID := uuid.New()
	seedCartWithItem(t, pool, clientID, venueID, menuItemID, 2)

	svc := orderusecase.NewCheckoutService(
		pgadapter.NewCartRepository(pool),
		pgadapter.NewVenueRepository(pool),
		pgadapter.NewMenuRepository(pool),
		pgadapter.NewRescueOfferRepository(pool),
		pgadapter.NewOrderRepository(pool),
		pgadapter.NewIdempotencyRepository(pool),
		pgadapter.NewOutboxRepository(pool),
		pgadapter.NewTxManager(pool),
	)

	const key = "retry-key"
	first, replayed, err := svc.PlaceOrder(ctx, clientID, key, "addr", "+70000000000", "")
	require.NoError(t, err)
	assert.False(t, replayed)

	second, replayed, err := svc.PlaceOrder(ctx, clientID, key, "addr", "+70000000000", "")
	require.NoError(t, err, "a retry with the same key and body must succeed even though the cart is now empty")
	assert.True(t, replayed)
	assert.Equal(t, first.ID, second.ID, "the retry must return the original order, not create a new one")

	var orderCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE client_id = $1`, clientID).Scan(&orderCount))
	assert.Equal(t, 1, orderCount, "exactly one order must exist, no matter how many times the same request is retried")

	var stockQty int
	require.NoError(t, pool.QueryRow(ctx, `SELECT stock_qty FROM menu_items WHERE id = $1`, menuItemID).Scan(&stockQty))
	assert.Equal(t, 8, stockQty, "stock must be decremented once (10 - 2), not once per retry")
}

// TestPlaceOrder_SameKeyDifferentBody_ReturnsConflict covers docs/PLAN.md
// 5.2's other idempotency case: reusing a key with a different request is
// a client bug, reported as IDEMPOTENCY_KEY_CONFLICT rather than silently
// acting on either body.
func TestPlaceOrder_SameKeyDifferentBody_ReturnsConflict(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	venueID := seedOpenVenue(t, pool)
	menuItemID := seedMenuItem(t, pool, venueID, 10)
	clientID := uuid.New()
	seedCartWithItem(t, pool, clientID, venueID, menuItemID, 1)

	svc := orderusecase.NewCheckoutService(
		pgadapter.NewCartRepository(pool),
		pgadapter.NewVenueRepository(pool),
		pgadapter.NewMenuRepository(pool),
		pgadapter.NewRescueOfferRepository(pool),
		pgadapter.NewOrderRepository(pool),
		pgadapter.NewIdempotencyRepository(pool),
		pgadapter.NewOutboxRepository(pool),
		pgadapter.NewTxManager(pool),
	)

	const key = "conflict-key"
	_, _, err := svc.PlaceOrder(ctx, clientID, key, "first address", "+70000000000", "")
	require.NoError(t, err)

	_, _, err = svc.PlaceOrder(ctx, clientID, key, "a completely different address", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeIdempotencyKeyConflict, code)
}

// TestPlaceOrder_ConcurrentDuplicateSubmissions_OnlyOneOrderCreated covers
// the case TestPlaceOrder_IdempotentRetry_* cannot: what happens when the
// SAME request arrives twice at nearly the same instant (a real network
// retry storm, not a deliberate later resend). It proves out
// IdempotencyRepository.Claim's doc comment — the unique index on
// (client_id, key) makes Postgres itself serialize the two INSERTs, so the
// loser's read-back always sees the winner's fully-linked, committed row —
// by asserting both concurrent calls resolve to the very same order, with
// no error on either side, and only one order row ever exists.
func TestPlaceOrder_ConcurrentDuplicateSubmissions_OnlyOneOrderCreated(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	venueID := seedOpenVenue(t, pool)
	menuItemID := seedMenuItem(t, pool, venueID, 10) // ample stock: this test is not about stock contention
	clientID := uuid.New()
	seedCartWithItem(t, pool, clientID, venueID, menuItemID, 1)

	svc := orderusecase.NewCheckoutService(
		pgadapter.NewCartRepository(pool),
		pgadapter.NewVenueRepository(pool),
		pgadapter.NewMenuRepository(pool),
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

	const key = "same-network-retry-key"
	var atGate sync.WaitGroup
	atGate.Add(2)
	start := make(chan struct{})
	results := make(chan placement, 2)

	place := func() {
		atGate.Done()
		<-start
		o, _, err := svc.PlaceOrder(ctx, clientID, key, "addr", "+70000000000", "")
		results <- placement{order: o, err: err}
	}
	go place()
	go place()
	atGate.Wait()
	close(start)

	r1 := <-results
	r2 := <-results

	require.NoError(t, r1.err, "neither concurrent submission of the identical request should fail")
	require.NoError(t, r2.err)
	assert.Equal(t, r1.order.ID, r2.order.ID, "both must resolve to the same order")

	var orderCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE client_id = $1`, clientID).Scan(&orderCount))
	assert.Equal(t, 1, orderCount)
}

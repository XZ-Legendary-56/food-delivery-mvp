//go:build integration

package integration

import (
	"context"
	"food-delivery-mvp/internal/domain/errs"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "food-delivery-mvp/internal/adapter/postgres"
	domaincatalog "food-delivery-mvp/internal/domain/catalog"

	domainorder "food-delivery-mvp/internal/domain/order"
	orderusecase "food-delivery-mvp/internal/usecase/order"
)

// checkoutHoldDelay is how long a checkout artificially holds its
// menu_items row lock in these tests (see delayingMenuItemLookup). It only
// needs to be comfortably longer than a bare lock+query round trip against
// a local container — a few milliseconds — so 300ms leaves plenty of
// margin without making the test slow.
const checkoutHoldDelay = 300 * time.Millisecond

// delayingMenuItemLookup wraps the real Postgres-backed MenuItemLookup and
// sleeps for delay right after LockForCheckout acquires its row locks, but
// before returning — i.e. still inside the caller's open transaction, so
// the FOR UPDATE locks stay held for the whole sleep. This turns "two
// goroutines calling PlaceOrder at nearly the same instant" into "one of
// them is guaranteed to still be inside its transaction when the other
// tries to lock the same row", which is what actually exercises Postgres's
// locking rather than just hoping for an unlucky race window.
type delayingMenuItemLookup struct {
	real  orderusecase.MenuItemLookup
	delay time.Duration
}

func (d *delayingMenuItemLookup) Get(ctx context.Context, id uuid.UUID) (*domaincatalog.MenuItem, error) {
	return d.real.Get(ctx, id)
}

func (d *delayingMenuItemLookup) GetMany(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domaincatalog.MenuItem, error) {
	return d.real.GetMany(ctx, ids)
}

func (d *delayingMenuItemLookup) LockForCheckout(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domaincatalog.MenuItem, error) {
	result, err := d.real.LockForCheckout(ctx, ids)
	if err != nil {
		return nil, err
	}
	time.Sleep(d.delay)
	return result, nil
}

func (d *delayingMenuItemLookup) DecrementStock(ctx context.Context, id uuid.UUID, quantity int) (bool, error) {
	return d.real.DecrementStock(ctx, id, quantity)
}

var _ orderusecase.MenuItemLookup = (*delayingMenuItemLookup)(nil)

// TestPlaceOrder_ConcurrentCheckoutsOnLastUnit is docs/PLAN.md 10.3's
// mandatory test: two customers try to buy the last unit of the same item
// at the same time. Exactly one must succeed; the other must fail with
// INSUFFICIENT_STOCK; the item's stock must end at exactly 0, never
// negative and never still 1.
func TestPlaceOrder_ConcurrentCheckoutsOnLastUnit(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	venueID := seedOpenVenue(t, pool)
	menuItemID := seedMenuItem(t, pool, venueID, 1) // exactly one unit in stock

	clientA, clientB := uuid.New(), uuid.New()
	seedCartWithItem(t, pool, clientA, venueID, menuItemID, 1)
	seedCartWithItem(t, pool, clientB, venueID, menuItemID, 1)

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
		order   *domainorder.Order
		err     error
		elapsed time.Duration
	}

	// Both goroutines block on start until both have reached the gate, so
	// they call PlaceOrder as close to simultaneously as the Go scheduler
	// allows — this maximizes the chance neither transaction has even
	// begun before the other starts, but the real proof of overlap is the
	// timing assertion below, not this gate by itself.
	var atGate sync.WaitGroup
	atGate.Add(2)
	start := make(chan struct{})
	results := make(chan placement, 2)

	place := func(clientID uuid.UUID) {
		atGate.Done()
		<-start
		t0 := time.Now()
		o, _, err := svc.PlaceOrder(ctx, clientID, "race-test-"+clientID.String(), "addr", "+70000000000", "")
		results <- placement{order: o, err: err, elapsed: time.Since(t0)}
	}

	go place(clientA)
	go place(clientB)
	atGate.Wait()
	close(start)

	first := <-results
	second := <-results

	// --- Proof this was a genuine race, not two sequential calls ---
	//
	// delayingMenuItemLookup holds each transaction's row lock for
	// checkoutHoldDelay. Whichever goroutine's transaction acquires the
	// lock first therefore keeps it — and blocks the other's own
	// LockForCheckout call at the Postgres level — for that entire
	// checkoutHoldDelay. So the SLOWER of the two calls must itself have
	// spent at least checkoutHoldDelay just waiting for the first
	// transaction to finish, on top of its own processing time.
	//
	// If these two calls had not actually overlapped — e.g. a bug made
	// them run one after the other instead of concurrently — there would
	// be no lock for the second call to wait on, and both calls would
	// each take roughly one checkoutHoldDelay independently, with no
	// dependency between them. The assertion below (comparing the slower
	// call's duration against a threshold that only a genuine wait could
	// produce) is what a purely-sequential bug would fail.
	slower := first.elapsed
	if second.elapsed > slower {
		slower = second.elapsed
	}
	assert.GreaterOrEqual(t, slower, checkoutHoldDelay+checkoutHoldDelay/2,
		"the losing checkout must have genuinely blocked on the winner's row lock, not merely run after it")

	// --- Proof of correctness: no oversell, no double-sell ---
	winner, loser := first, second
	if winner.err != nil {
		winner, loser = second, first
	}
	require.NoError(t, winner.err, "exactly one of the two concurrent checkouts must succeed")
	require.Error(t, loser.err, "the other must fail, not also succeed and oversell the item")
	code, ok := errs.CodeOf(loser.err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeInsufficientStock, code)

	var stockQty int
	require.NoError(t, pool.QueryRow(ctx, `SELECT stock_qty FROM menu_items WHERE id = $1`, menuItemID).Scan(&stockQty))
	assert.Equal(t, 0, stockQty, "the single unit must be sold exactly once: not left at 1, not driven negative")

	var orderCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE venue_id = $1`, venueID).Scan(&orderCount))
	assert.Equal(t, 1, orderCount, "only the winning checkout may have created an order row")

	var menuVersion int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT menu_version FROM venues WHERE id = $1`, venueID).Scan(&menuVersion))
	assert.Equal(t, int64(2), menuVersion, "stock changed once, so menu_version must have moved exactly once past its starting value of 1")
}

// TestLockForCheckout_OppositeOrderDoesNotDeadlock is what actually proves
// the claim behind docs/PLAN.md 9's "sort by id" rule, at the level where the
// rule lives: two transactions lock the SAME two rows in OPPOSITE order —
// exactly the shape a deadlock needs (A holds X, wants Y; B holds Y, wants
// X) if MenuRepository.LockForCheckout did not force its own ORDER BY id
// regardless of the order ids arrives in. If that ORDER BY were removed,
// Postgres's deadlock detector would eventually abort one of these two
// transactions with an explicit "deadlock detected" error (it runs on a
// ~1s timer by default) — this test's timeout is long enough to let that
// detector fire rather than mask the bug as a slow pass.
func TestLockForCheckout_OppositeOrderDoesNotDeadlock(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	venueID := seedOpenVenue(t, pool)
	itemX := seedMenuItem(t, pool, venueID, 10)
	itemY := seedMenuItem(t, pool, venueID, 10)

	menuItems := pgadapter.NewMenuRepository(pool)
	txManager := pgadapter.NewTxManager(pool)

	lockBothAndHold := func(ids []uuid.UUID) error {
		return txManager.WithinTx(ctx, func(ctx context.Context) error {
			if _, err := menuItems.LockForCheckout(ctx, ids); err != nil {
				return err
			}
			time.Sleep(checkoutHoldDelay)
			return nil
		})
	}

	var atGate sync.WaitGroup
	atGate.Add(2)
	start := make(chan struct{})
	errCh := make(chan error, 2)

	run := func(ids []uuid.UUID) {
		atGate.Done()
		<-start
		errCh <- lockBothAndHold(ids)
	}

	go run([]uuid.UUID{itemX, itemY})
	go run([]uuid.UUID{itemY, itemX}) // deliberately reversed relative to the other goroutine
	atGate.Wait()
	close(start)

	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			assert.NoError(t, err, "a deadlock here means LockForCheckout is not sorting ids before locking")
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for a lock-holder goroutine — the transactions likely deadlocked")
		}
	}
}

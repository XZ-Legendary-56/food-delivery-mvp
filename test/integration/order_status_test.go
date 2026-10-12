//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	pgadapter "food-delivery-mvp/internal/adapter/postgres"

	domainorder "food-delivery-mvp/internal/domain/order"
	orderusecase "food-delivery-mvp/internal/usecase/order"
)

// TestCancelOrder_PersistsAgainstRealSchema places an order and cancels it
// through orderusecase.Service backed by the real Postgres schema, not a
// mock repository. Unit tests already cover the state machine and the
// service's own logic with a fake Repository; what only a real database
// can catch is orders.status's own CHECK constraint rejecting whatever
// literal string domainorder.Status actually holds — exactly the class of
// bug a lint-driven rename can introduce silently (see this project's own
// misspell-autofix incident on the rescue offers' cancellation-timestamp
// column, docs/PLAN.md 10.1 stage) and that a mocked repository can never
// surface, because a mock has no constraint to violate.
func TestCancelOrder_PersistsAgainstRealSchema(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	venueID := seedOpenVenue(t, pool)
	menuItemID := seedMenuItem(t, pool, venueID, 10)
	clientID := uuid.New()
	seedCartWithItem(t, pool, clientID, venueID, menuItemID, 1)

	checkout := orderusecase.NewCheckoutService(
		pgadapter.NewCartRepository(pool),
		pgadapter.NewVenueRepository(pool),
		pgadapter.NewMenuRepository(pool),
		pgadapter.NewRescueOfferRepository(pool),
		pgadapter.NewOrderRepository(pool),
		pgadapter.NewIdempotencyRepository(pool),
		pgadapter.NewOutboxRepository(pool),
		pgadapter.NewTxManager(pool),
	)
	o, _, err := checkout.PlaceOrder(ctx, clientID, uuid.New().String(), "addr", "+70000000000", "")
	require.NoError(t, err)

	orders := orderusecase.NewOrderService(
		pgadapter.NewOrderRepository(pool),
		pgadapter.NewOutboxRepository(pool),
		pgadapter.NewTxManager(pool),
	)
	canceled, err := orders.CancelOrder(ctx, clientID, o.ID)
	require.NoError(t, err, "the UPDATE must satisfy orders.status's CHECK constraint")
	require.Equal(t, domainorder.StatusCancelled, canceled.Status)

	reloaded, err := orders.GetOrder(ctx, clientID, o.ID)
	require.NoError(t, err)
	require.Equal(t, domainorder.StatusCancelled, reloaded.Status)
}

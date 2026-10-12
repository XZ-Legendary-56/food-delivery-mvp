package order_test

import (
	"context"
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/usecase/order"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"

	domainorder "food-delivery-mvp/internal/domain/order"
)

const testIdempotencyKey = "test-idempotency-key"

// alwaysOpenVenue is open every hour of every day, so tests that are not
// about scheduling do not have to worry about what time they happen to run.
func alwaysOpenVenue(id uuid.UUID, minOrderAmountMinor int64) domaincatalog.Venue {
	var schedule []domaincatalog.ScheduleEntry
	for d := time.Sunday; d <= time.Saturday; d++ {
		schedule = append(schedule, domaincatalog.ScheduleEntry{Weekday: d, OpensAt: 0, ClosesAt: 24 * time.Hour})
	}
	return domaincatalog.Venue{
		ID:                  id,
		AcceptingOrders:     true,
		MinOrderAmountMinor: minOrderAmountMinor,
		Schedule:            schedule,
	}
}

// expectFreshClaim sets up idempotency.Claim to report a brand-new key —
// the common case for tests that are not about idempotency itself.
func expectFreshClaim(idem *MockIdempotencyRepository) {
	idem.EXPECT().
		Claim(gomock.Any(), gomock.Any(), testIdempotencyKey, gomock.Any(), gomock.Any()).
		Return(order.IdempotencyClaim{Claimed: true}, nil)
}

// noLockedRescueOffers stubs RescueOfferRepository.LockForCheckout to
// report "nothing to lock" — the common case for tests whose cart lines
// carry no RescueOfferID at all (docs/PLAN.md 5.5's own rescue-specific
// checkout tests set up a real locked offer instead).
func noLockedRescueOffers(m *MockRescueOfferRepository) {
	m.EXPECT().
		LockForCheckout(gomock.Any(), gomock.Any()).
		Return(map[uuid.UUID]domaincatalog.RescueOffer{}, nil)
}

func TestPlaceOrder_EmptyCart(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	orders := NewMockRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID := uuid.New()
	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(nil, nil)

	svc := order.NewCheckoutService(carts, venues, menuItems, rescueOffers, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeCartEmpty, code)
}

func TestPlaceOrder_VenueNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	orders := NewMockRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID := uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, uuid.New(), 1, 1000, nil))

	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(nil, nil)

	svc := order.NewCheckoutService(carts, venues, menuItems, rescueOffers, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

func TestPlaceOrder_VenueNotAcceptingOrders(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	orders := NewMockRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID := uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, uuid.New(), 1, 1000, nil))

	venue := alwaysOpenVenue(venueID, 0)
	venue.AcceptingOrders = false
	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	// LockForCheckout must not be called: the venue check fails first.

	svc := order.NewCheckoutService(carts, venues, menuItems, rescueOffers, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeVenueNotAcceptingOrders, code)
}

func TestPlaceOrder_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	orders := NewMockRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 2, 10000, nil))

	venue := alwaysOpenVenue(venueID, 15000)
	stock := 5
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 10000, IsAvailable: true, StockQty: &stock}

	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)
	noLockedRescueOffers(rescueOffers)
	menuItems.EXPECT().DecrementStock(gomock.Any(), menuItemID, 2).Return(true, nil)
	venues.EXPECT().BumpMenuVersion(gomock.Any(), venueID).Return(nil)
	orders.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, o *domainorder.Order) error {
		assert.Equal(t, int64(20000), o.TotalMinor())
		assert.Equal(t, domainorder.StatusCreated, o.Status)
		return nil
	})
	outboxRepo.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)
	idem.EXPECT().LinkOrder(gomock.Any(), clientID, testIdempotencyKey, gomock.Any()).Return(nil)
	carts.EXPECT().Clear(gomock.Any(), clientID).Return(nil)

	svc := order.NewCheckoutService(carts, venues, menuItems, rescueOffers, orders, idem, outboxRepo, tx)
	o, replayed, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "no onions")

	require.NoError(t, err)
	assert.False(t, replayed, "a brand-new order must not be reported as a replay")
	assert.Equal(t, int64(20000), o.TotalMinor())
}

func TestPlaceOrder_PriceChanged_NoStockOrOrderSideEffects(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	orders := NewMockRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 1, 10000, nil))

	venue := alwaysOpenVenue(venueID, 0)
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 12000, IsAvailable: true}

	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)
	noLockedRescueOffers(rescueOffers)
	// No DecrementStock, BumpMenuVersion, Create, LinkOrder or Clear: a
	// failed checkout must have zero side effects.

	svc := order.NewCheckoutService(carts, venues, menuItems, rescueOffers, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodePriceChanged, code)
}

func TestPlaceOrder_InsufficientStock(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	orders := NewMockRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 3, 10000, nil))

	venue := alwaysOpenVenue(venueID, 0)
	stock := 1
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 10000, IsAvailable: true, StockQty: &stock}

	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)
	noLockedRescueOffers(rescueOffers)

	svc := order.NewCheckoutService(carts, venues, menuItems, rescueOffers, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeInsufficientStock, code)
}

func TestPlaceOrder_MinOrderAmountNotReached_StockUntouched(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	orders := NewMockRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 1, 10000, nil))

	venue := alwaysOpenVenue(venueID, 50000) // minimum far above this cart's total
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 10000, IsAvailable: true}

	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)
	noLockedRescueOffers(rescueOffers)
	// DecrementStock must not be called: the order is rejected before
	// anything is written.

	svc := order.NewCheckoutService(carts, venues, menuItems, rescueOffers, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeMinOrderAmountNotReached, code)
}

func TestPlaceOrder_UnlimitedStockDoesNotBumpMenuVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	orders := NewMockRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 1, 10000, nil))

	venue := alwaysOpenVenue(venueID, 0)
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Soup of the day", PriceMinor: 10000, IsAvailable: true, StockQty: nil}

	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)
	noLockedRescueOffers(rescueOffers)
	menuItems.EXPECT().DecrementStock(gomock.Any(), menuItemID, 1).Return(false, nil)
	// BumpMenuVersion must not be called: nothing about the menu actually
	// changed for an unlimited-stock item.
	orders.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	outboxRepo.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)
	idem.EXPECT().LinkOrder(gomock.Any(), clientID, testIdempotencyKey, gomock.Any()).Return(nil)
	carts.EXPECT().Clear(gomock.Any(), clientID).Return(nil)

	svc := order.NewCheckoutService(carts, venues, menuItems, rescueOffers, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.NoError(t, err)
}

func TestPlaceOrder_IdempotentReplay_ReturnsSameOrderWithoutTouchingCartOrStock(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	orders := NewMockRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, existingOrderID := uuid.New(), uuid.New()
	existingOrder := &domainorder.Order{ID: existingOrderID, ClientID: clientID, Status: domainorder.StatusCreated}

	idem.EXPECT().
		Claim(gomock.Any(), clientID, testIdempotencyKey, gomock.Any(), gomock.Any()).
		Return(order.IdempotencyClaim{Claimed: false, HashMatches: true, OrderID: existingOrderID}, nil)
	orders.EXPECT().Get(gomock.Any(), existingOrderID).Return(existingOrder, nil)
	// No CartRepository, VenueLookup, MenuItemLookup or LinkOrder calls at
	// all: a replay must not re-run any part of checkout, precisely
	// because the cart it would need is typically already gone (the
	// original, successful attempt cleared it).

	svc := order.NewCheckoutService(carts, venues, menuItems, rescueOffers, orders, idem, outboxRepo, tx)
	o, replayed, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.NoError(t, err)
	assert.True(t, replayed, "a replayed order must be reported as such, for the HTTP layer to answer 200 instead of 201")
	assert.Same(t, existingOrder, o)
}

func TestPlaceOrder_SameKeyDifferentBody_Conflict(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	orders := NewMockRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID := uuid.New()
	idem.EXPECT().
		Claim(gomock.Any(), clientID, testIdempotencyKey, gomock.Any(), gomock.Any()).
		Return(order.IdempotencyClaim{Claimed: false, HashMatches: false}, nil)
	// No OrderRepository.Get either: a hash mismatch is a conflict, not a
	// lookup.

	svc := order.NewCheckoutService(carts, venues, menuItems, rescueOffers, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "a different address", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeIdempotencyKeyConflict, code)
}

// TestPlaceOrder_RescueOfferExpired_NoSideEffects covers docs/PLAN.md 5.5's
// "the offer ended while the item sat in the cart" case: a cart line whose
// snapshot assumed a rescue offer, but the specific offer's window is no
// longer valid by checkout time.
func TestPlaceOrder_RescueOfferExpired_NoSideEffects(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	orders := NewMockRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID, offerID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 2, 27540, &offerID))

	venue := alwaysOpenVenue(venueID, 0)
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 45900, IsAvailable: true}
	expiredOffer := domaincatalog.RescueOffer{
		ID: offerID, MenuItemID: menuItemID, DiscountPercent: 40, RemainingQuantity: 5,
		StartsAt: time.Now().Add(-2 * time.Hour), EndsAt: time.Now().Add(-time.Hour), // window already closed
	}

	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)
	rescueOffers.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{offerID}).
		Return(map[uuid.UUID]domaincatalog.RescueOffer{offerID: expiredOffer}, nil)
	// No DecrementStock, DecrementRemaining, Create, LinkOrder or Clear: a
	// failed checkout must have zero side effects.

	svc := order.NewCheckoutService(carts, venues, menuItems, rescueOffers, orders, idem, outboxRepo, tx)
	_, _, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeRescueOfferExpired, code)
}

// TestPlaceOrder_RescuePartialCoverage_SplitsAndSucceeds is docs/PLAN.md 5.5's
// own mandated test at the use-case level: requesting more than a still-live
// offer has left succeeds, producing two order_items and decrementing both
// counters by their own amounts (full quantity off stock_qty, only the
// eligible quantity off the offer's remaining_quantity).
func TestPlaceOrder_RescuePartialCoverage_SplitsAndSucceeds(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	venues := NewMockVenueLookup(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	orders := NewMockRepository(ctrl)
	idem := NewMockIdempotencyRepository(ctrl)
	outboxRepo := NewMockOutboxRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID, offerID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 5, 27540, &offerID))

	venue := alwaysOpenVenue(venueID, 0)
	stock := 10
	liveItem := domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 45900, IsAvailable: true, StockQty: &stock}
	liveOffer := domaincatalog.RescueOffer{
		ID: offerID, MenuItemID: menuItemID, DiscountPercent: 40, RemainingQuantity: 3, // less than the 5 requested
		StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Now().Add(time.Hour),
	}

	expectFreshClaim(idem)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	venues.EXPECT().Get(gomock.Any(), venueID).Return(&venue, nil)
	menuItems.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: liveItem}, nil)
	rescueOffers.EXPECT().
		LockForCheckout(gomock.Any(), []uuid.UUID{offerID}).
		Return(map[uuid.UUID]domaincatalog.RescueOffer{offerID: liveOffer}, nil)
	menuItems.EXPECT().DecrementStock(gomock.Any(), menuItemID, 5).Return(true, nil)
	rescueOffers.EXPECT().DecrementRemaining(gomock.Any(), offerID, 3).Return(nil)
	venues.EXPECT().BumpMenuVersion(gomock.Any(), venueID).Return(nil)
	orders.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, o *domainorder.Order) error {
		require.Len(t, o.Items, 2)
		assert.Equal(t, int64(3*27540+2*45900), o.TotalMinor())
		return nil
	})
	outboxRepo.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(nil)
	idem.EXPECT().LinkOrder(gomock.Any(), clientID, testIdempotencyKey, gomock.Any()).Return(nil)
	carts.EXPECT().Clear(gomock.Any(), clientID).Return(nil)

	svc := order.NewCheckoutService(carts, venues, menuItems, rescueOffers, orders, idem, outboxRepo, tx)
	o, replayed, err := svc.PlaceOrder(context.Background(), clientID, testIdempotencyKey, "addr", "+70000000000", "")

	require.NoError(t, err)
	assert.False(t, replayed)
	assert.Equal(t, int64(3*27540+2*45900), o.TotalMinor())
}

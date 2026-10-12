package order_test

import (
	"context"
	"errors"
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/usecase/order"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"

	domainorder "food-delivery-mvp/internal/domain/order"
)

// passthroughTx makes m.WithinTx just call fn with the given ctx, the same
// way the real TxManager does once a transaction is open — the point of
// these tests is the use-case's own logic, not TxManager's, which already
// has its own tests where it is implemented (adapter/postgres).
func passthroughTx(m *MockTxManager) {
	m.EXPECT().
		WithinTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		}).
		AnyTimes()
}

// noActiveRescueOffers stubs RescueOfferRepository.GetActiveForItems to
// report "nothing active" for any input, any number of times — the
// permissive default for tests that are not themselves about rescue
// offers (docs/PLAN.md 5.5's own tests live in checkout_service_test.go and
// cart_service_test.go's dedicated rescue cases).
func noActiveRescueOffers(m *MockRescueOfferRepository) {
	m.EXPECT().
		GetActiveForItems(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(map[uuid.UUID]domaincatalog.RescueOffer{}, nil).
		AnyTimes()
}

func TestGetCart_NoCartYet(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)

	clientID := uuid.New()
	carts.EXPECT().Get(gomock.Any(), clientID).Return(nil, nil)
	// menuItems.GetMany must not be called: there is nothing to snapshot
	// when there is no cart at all.

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	view, err := svc.GetCart(context.Background(), clientID)

	require.NoError(t, err)
	assert.Nil(t, view.Cart)
}

func TestGetCart_WithItems(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 2, 10000, nil))

	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	menuItems.EXPECT().
		GetMany(gomock.Any(), []uuid.UUID{menuItemID}).
		Return(map[uuid.UUID]domaincatalog.MenuItem{
			menuItemID: {ID: menuItemID, Name: "Margherita", IsAvailable: true, PriceMinor: 10000},
		}, nil)

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	view, err := svc.GetCart(context.Background(), clientID)

	require.NoError(t, err)
	require.NotNil(t, view.Cart)
	assert.Equal(t, "Margherita", view.MenuItems[menuItemID].Name)
}

func TestAddItem_NewCart(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	item := &domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 10000, IsAvailable: true}

	menuItems.EXPECT().Get(gomock.Any(), menuItemID).Return(item, nil)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(nil, nil)
	carts.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, cart *domainorder.Cart) error {
		assert.Equal(t, venueID, cart.VenueID, "a brand new cart must be pinned to the added item's venue")
		require.Len(t, cart.Items, 1)
		assert.Equal(t, 2, cart.Items[0].Quantity)
		return nil
	})
	menuItems.EXPECT().GetMany(gomock.Any(), gomock.Any()).Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: *item}, nil)

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	view, err := svc.AddItem(context.Background(), clientID, menuItemID, 2)

	require.NoError(t, err)
	assert.Equal(t, venueID, view.Cart.VenueID)
}

// TestAddItem_ActiveRescueOffer_SnapshotsDiscountedPriceAndOfferID covers
// docs/PLAN.md 5.5's soft-check side: adding an item that currently has an
// active rescue offer must snapshot the discounted price and remember
// which offer it came from, not the item's plain price.
func TestAddItem_ActiveRescueOffer_SnapshotsDiscountedPriceAndOfferID(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	item := &domaincatalog.MenuItem{ID: menuItemID, VenueID: venueID, Name: "Margherita", PriceMinor: 10000, IsAvailable: true}
	offer := domaincatalog.RescueOffer{ID: uuid.New(), MenuItemID: menuItemID, DiscountPercent: 40, RemainingQuantity: 3}

	menuItems.EXPECT().Get(gomock.Any(), menuItemID).Return(item, nil)
	rescueOffers.EXPECT().
		GetActiveForItems(gomock.Any(), []uuid.UUID{menuItemID}, gomock.Any()).
		Return(map[uuid.UUID]domaincatalog.RescueOffer{menuItemID: offer}, nil)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(nil, nil)
	carts.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, cart *domainorder.Cart) error {
		require.Len(t, cart.Items, 1)
		assert.Equal(t, int64(6000), cart.Items[0].PriceMinorSnapshot, "must snapshot the discounted price, not the plain one")
		require.NotNil(t, cart.Items[0].RescueOfferID)
		assert.Equal(t, offer.ID, *cart.Items[0].RescueOfferID)
		return nil
	})
	rescueOffers.EXPECT().GetActiveForItems(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(map[uuid.UUID]domaincatalog.RescueOffer{}, nil) // the final menuItemSnapshot re-fetch
	menuItems.EXPECT().GetMany(gomock.Any(), gomock.Any()).Return(map[uuid.UUID]domaincatalog.MenuItem{menuItemID: *item}, nil)

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	_, err := svc.AddItem(context.Background(), clientID, menuItemID, 1)

	require.NoError(t, err)
}

func TestAddItem_MenuItemNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)

	menuItemID := uuid.New()
	menuItems.EXPECT().Get(gomock.Any(), menuItemID).Return(nil, nil)
	// No WithinTx expectation: a missing item must fail before any
	// transaction is even opened.

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	_, err := svc.AddItem(context.Background(), uuid.New(), menuItemID, 1)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

func TestAddItem_UnavailableFailsSoftCheckBeforeAnyTx(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)

	menuItemID := uuid.New()
	menuItems.EXPECT().Get(gomock.Any(), menuItemID).
		Return(&domaincatalog.MenuItem{ID: menuItemID, IsAvailable: false}, nil)

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	_, err := svc.AddItem(context.Background(), uuid.New(), menuItemID, 1)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeItemUnavailable, code)
}

func TestAddItem_DifferentVenueRejectedInsideTx(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID := uuid.New()
	existingVenueID, otherVenueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	existingCart := domainorder.NewCart(clientID, existingVenueID)

	item := &domaincatalog.MenuItem{ID: menuItemID, VenueID: otherVenueID, IsAvailable: true, PriceMinor: 5000}
	menuItems.EXPECT().Get(gomock.Any(), menuItemID).Return(item, nil)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(existingCart, nil)
	// Save must not be called: the domain rejects the add before there is
	// anything to persist.

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	_, err := svc.AddItem(context.Background(), clientID, menuItemID, 1)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeCartVenueMismatch, code)
}

func TestUpdateItemQuantity_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID, lineID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(lineID, venueID, menuItemID, 1, 5000, nil))

	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	menuItems.EXPECT().Get(gomock.Any(), menuItemID).Return(&domaincatalog.MenuItem{ID: menuItemID, PriceMinor: 5000, IsAvailable: true}, nil)
	carts.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, c *domainorder.Cart) error {
		assert.Equal(t, 4, c.Items[0].Quantity)
		return nil
	})
	menuItems.EXPECT().GetMany(gomock.Any(), gomock.Any()).Return(map[uuid.UUID]domaincatalog.MenuItem{}, nil)

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	_, err := svc.UpdateItemQuantity(context.Background(), clientID, lineID, 4)

	require.NoError(t, err)
}

func TestUpdateItemQuantity_CartNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID := uuid.New()
	carts.EXPECT().Get(gomock.Any(), clientID).Return(nil, nil)

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	_, err := svc.UpdateItemQuantity(context.Background(), clientID, uuid.New(), 2)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

func TestUpdateItemQuantity_LineNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID := uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	_, err := svc.UpdateItemQuantity(context.Background(), clientID, uuid.New(), 2)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

func TestRemoveItem_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID, menuItemID, lineID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(lineID, venueID, menuItemID, 1, 5000, nil))

	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	carts.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, c *domainorder.Cart) error {
		assert.Empty(t, c.Items)
		return nil
	})
	menuItems.EXPECT().GetMany(gomock.Any(), gomock.Any()).Return(map[uuid.UUID]domaincatalog.MenuItem{}, nil)

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	_, err := svc.RemoveItem(context.Background(), clientID, lineID)

	require.NoError(t, err)
}

func TestRemoveItem_CartNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID := uuid.New()
	carts.EXPECT().Get(gomock.Any(), clientID).Return(nil, nil)

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	_, err := svc.RemoveItem(context.Background(), clientID, uuid.New())

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

func TestRemoveItem_LineNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)
	passthroughTx(tx)

	clientID, venueID := uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	_, err := svc.RemoveItem(context.Background(), clientID, uuid.New())

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

func TestGetCart_MenuItemLookupErrorPropagates(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)

	clientID, venueID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := domainorder.NewCart(clientID, venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 1, 1000, nil))

	boom := errors.New("connection reset")
	carts.EXPECT().Get(gomock.Any(), clientID).Return(cart, nil)
	menuItems.EXPECT().GetMany(gomock.Any(), gomock.Any()).Return(nil, boom)

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	_, err := svc.GetCart(context.Background(), clientID)

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestClearCart_DelegatesToRepository(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)

	clientID := uuid.New()
	carts.EXPECT().Clear(gomock.Any(), clientID).Return(nil)

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	err := svc.ClearCart(context.Background(), clientID)

	require.NoError(t, err)
}

func TestClearCart_PropagatesRepositoryError(t *testing.T) {
	ctrl := gomock.NewController(t)
	carts := NewMockCartRepository(ctrl)
	menuItems := NewMockMenuItemLookup(ctrl)
	rescueOffers := NewMockRescueOfferRepository(ctrl)
	noActiveRescueOffers(rescueOffers)
	tx := NewMockTxManager(ctrl)

	clientID := uuid.New()
	boom := errors.New("connection reset")
	carts.EXPECT().Clear(gomock.Any(), clientID).Return(boom)

	svc := order.NewCartService(carts, menuItems, rescueOffers, tx)
	err := svc.ClearCart(context.Background(), clientID)

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

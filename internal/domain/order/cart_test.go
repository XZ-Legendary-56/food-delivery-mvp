package order_test

import (
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/domain/order"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCart_AddItem_NewLine(t *testing.T) {
	venueID, lineID, menuItemID := uuid.New(), uuid.New(), uuid.New()
	cart := order.NewCart(uuid.New(), venueID)

	err := cart.AddItem(lineID, venueID, menuItemID, 2, 10000, nil)

	require.NoError(t, err)
	require.Len(t, cart.Items, 1)
	assert.Equal(t, order.CartItem{ID: lineID, MenuItemID: menuItemID, Quantity: 2, PriceMinorSnapshot: 10000}, cart.Items[0])
}

func TestCart_AddItem_SameItemAccumulatesQuantityAndKeepsLineID(t *testing.T) {
	venueID, menuItemID := uuid.New(), uuid.New()
	cart := order.NewCart(uuid.New(), venueID)
	firstLineID := uuid.New()

	require.NoError(t, cart.AddItem(firstLineID, venueID, menuItemID, 2, 10000, nil))
	require.NoError(t, cart.AddItem(uuid.New(), venueID, menuItemID, 3, 10000, nil))

	require.Len(t, cart.Items, 1, "adding the same menu item twice must not create a second line")
	assert.Equal(t, 5, cart.Items[0].Quantity)
	assert.Equal(t, firstLineID, cart.Items[0].ID, "accumulating onto an existing line must not change its ID")
}

func TestCart_AddItem_DifferentVenueRejected(t *testing.T) {
	cart := order.NewCart(uuid.New(), uuid.New())

	err := cart.AddItem(uuid.New(), uuid.New(), uuid.New(), 1, 10000, nil)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeCartVenueMismatch, code)
	assert.Empty(t, cart.Items, "a rejected add must not partially modify the cart")
}

func TestCart_TotalMinor_SumsLines(t *testing.T) {
	venueID := uuid.New()
	cart := order.NewCart(uuid.New(), venueID)
	require.NoError(t, cart.AddItem(uuid.New(), venueID, uuid.New(), 2, 10000, nil)) // 20000
	require.NoError(t, cart.AddItem(uuid.New(), venueID, uuid.New(), 1, 5000, nil))  // 5000

	assert.Equal(t, int64(25000), cart.TotalMinor())
}

func TestCart_EnsureNotEmpty(t *testing.T) {
	empty := order.NewCart(uuid.New(), uuid.New())
	err := empty.EnsureNotEmpty()
	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeCartEmpty, code)

	venueID := uuid.New()
	nonEmpty := order.NewCart(uuid.New(), venueID)
	require.NoError(t, nonEmpty.AddItem(uuid.New(), venueID, uuid.New(), 1, 1000, nil))
	assert.NoError(t, nonEmpty.EnsureNotEmpty())
}

func TestCart_UpdateQuantity(t *testing.T) {
	venueID, lineID := uuid.New(), uuid.New()
	cart := order.NewCart(uuid.New(), venueID)
	require.NoError(t, cart.AddItem(lineID, venueID, uuid.New(), 1, 1000, nil))

	require.NoError(t, cart.UpdateQuantity(lineID, 5, 1000, nil))
	assert.Equal(t, 5, cart.Items[0].Quantity)
}

func TestCart_UpdateQuantity_NotFound(t *testing.T) {
	cart := order.NewCart(uuid.New(), uuid.New())

	err := cart.UpdateQuantity(uuid.New(), 2, 1000, nil)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

func TestCart_UpdateQuantity_RejectsNonPositive(t *testing.T) {
	venueID, lineID := uuid.New(), uuid.New()
	cart := order.NewCart(uuid.New(), venueID)
	require.NoError(t, cart.AddItem(lineID, venueID, uuid.New(), 1, 1000, nil))

	err := cart.UpdateQuantity(lineID, 0, 1000, nil)

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeValidationError, code)
	assert.Equal(t, 1, cart.Items[0].Quantity, "a rejected update must not change the line")
}

func TestCart_RemoveItem(t *testing.T) {
	venueID, keepID, removeID := uuid.New(), uuid.New(), uuid.New()
	cart := order.NewCart(uuid.New(), venueID)
	require.NoError(t, cart.AddItem(keepID, venueID, uuid.New(), 1, 1000, nil))
	require.NoError(t, cart.AddItem(removeID, venueID, uuid.New(), 1, 2000, nil))

	require.NoError(t, cart.RemoveItem(removeID))

	require.Len(t, cart.Items, 1)
	assert.Equal(t, keepID, cart.Items[0].ID)
}

func TestCart_RemoveItem_NotFound(t *testing.T) {
	cart := order.NewCart(uuid.New(), uuid.New())

	err := cart.RemoveItem(uuid.New())

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeNotFound, code)
}

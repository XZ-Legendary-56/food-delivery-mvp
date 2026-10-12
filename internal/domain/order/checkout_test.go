package order_test

import (
	"errors"
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/domain/order"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"
)

// okLine builds a CheckoutLine that passes every check, using catalog's own
// EnsureAvailable/EnsurePriceUnchanged so the test only fabricates a line
// that would genuinely be considered fine, not just Err: nil by fiat.
func okLine(name string, quantity int, priceMinor int64) order.CheckoutLine {
	mi := domaincatalog.MenuItem{ID: uuid.New(), Name: name, PriceMinor: priceMinor, IsAvailable: true}
	err := mi.EnsureAvailable(quantity)
	if err == nil {
		err = mi.EnsurePriceUnchanged(priceMinor)
	}
	return order.CheckoutLine{MenuItemID: mi.ID, Name: name, Quantity: quantity, UnitPriceMinor: priceMinor, Err: err}
}

func priceChangedLine(name string, quantity int, oldPrice, newPrice int64) order.CheckoutLine {
	mi := domaincatalog.MenuItem{ID: uuid.New(), Name: name, PriceMinor: newPrice, IsAvailable: true}
	return order.CheckoutLine{
		MenuItemID: mi.ID, Name: name, Quantity: quantity, UnitPriceMinor: newPrice,
		Err: mi.EnsurePriceUnchanged(oldPrice),
	}
}

func unavailableLine(name string, quantity int) order.CheckoutLine {
	mi := domaincatalog.MenuItem{ID: uuid.New(), Name: name, IsAvailable: false}
	return order.CheckoutLine{MenuItemID: mi.ID, Name: name, Quantity: quantity, Err: mi.EnsureAvailable(quantity)}
}

func insufficientStockLine(name string, quantity, available int) order.CheckoutLine {
	mi := domaincatalog.MenuItem{ID: uuid.New(), Name: name, IsAvailable: true, StockQty: &available}
	return order.CheckoutLine{MenuItemID: mi.ID, Name: name, Quantity: quantity, Err: mi.EnsureAvailable(quantity)}
}

func TestCheckout_EmptyCart(t *testing.T) {
	_, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), nil, "addr", "+70000000000", "", time.Now())

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeCartEmpty, code)
}

func TestCheckout_AllLinesClean_BuildsOrder(t *testing.T) {
	lines := []order.CheckoutLine{
		okLine("Margherita", 2, 45900),
		okLine("Cola", 1, 15900),
	}
	now := time.Now()
	orderID, clientID, venueID := uuid.New(), uuid.New(), uuid.New()

	o, err := order.Checkout(orderID, clientID, venueID, lines, "addr", "+70000000000", "no onions", now)

	require.NoError(t, err)
	assert.Equal(t, orderID, o.ID)
	assert.Equal(t, order.StatusCreated, o.Status)
	require.Len(t, o.Items, 2)
	assert.Equal(t, int64(45900*2+15900), o.TotalMinor())
	for i, l := range lines {
		assert.Equal(t, l.MenuItemID, o.Items[i].MenuItemID)
		assert.Equal(t, l.Name, o.Items[i].NameSnapshot)
		assert.Equal(t, l.UnitPriceMinor, o.Items[i].UnitPriceMinor)
		assert.NotEqual(t, uuid.Nil, o.Items[i].ID, "each order line must get its own fresh id")
	}
}

func TestCheckout_PriceChanged_ReportsEveryMismatchedLine(t *testing.T) {
	lines := []order.CheckoutLine{
		priceChangedLine("Margherita", 1, 45900, 49900),
		okLine("Cola", 1, 15900),
		priceChangedLine("Pepperoni", 1, 55900, 59900),
	}

	_, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), lines, "addr", "+70000000000", "", time.Now())

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodePriceChanged, code)

	domainErr, ok := errs.As(err)
	require.True(t, ok)
	assert.Len(t, domainErr.Details, 2, "both mismatched lines must be reported, not just the first")
}

func TestCheckout_PriceChangeOutranksStockShortage(t *testing.T) {
	// Both problems exist at once; the customer needs to fix the price
	// issue regardless of stock, so only PRICE_CHANGED should come back.
	lines := []order.CheckoutLine{
		priceChangedLine("Margherita", 1, 45900, 49900),
		insufficientStockLine("Cola", 5, 1),
	}

	_, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), lines, "addr", "+70000000000", "", time.Now())

	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodePriceChanged, code)
}

func TestCheckout_UnavailableOutranksInsufficientStock(t *testing.T) {
	lines := []order.CheckoutLine{
		unavailableLine("Margherita", 1),
		insufficientStockLine("Cola", 5, 1),
	}

	_, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), lines, "addr", "+70000000000", "", time.Now())

	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeItemUnavailable, code)
}

func TestCheckout_InsufficientStockAlone(t *testing.T) {
	lines := []order.CheckoutLine{
		okLine("Margherita", 1, 45900),
		insufficientStockLine("Cola", 5, 1),
	}

	_, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), lines, "addr", "+70000000000", "", time.Now())

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeInsufficientStock, code)

	domainErr, ok := errs.As(err)
	require.True(t, ok)
	require.Len(t, domainErr.Details, 1)
	assert.Equal(t, 5, domainErr.Details[0]["requested"])
	assert.Equal(t, 1, domainErr.Details[0]["available"])
}

// TestCheckout_RescuePartialCoverage_SplitsIntoTwoLines is docs/PLAN.md 5.5's
// own mandated test: ordering more than a rescue offer still has left must
// succeed, not fail — the order simply ends up with two order_items, one at
// the discounted price for whatever the offer could still cover, one at
// full price for the rest.
func TestCheckout_RescuePartialCoverage_SplitsIntoTwoLines(t *testing.T) {
	offerID := uuid.New()
	line := order.CheckoutLine{
		MenuItemID:           uuid.New(),
		Name:                 "Margherita",
		Quantity:             5,
		UnitPriceMinor:       45900,
		RescueOfferID:        &offerID,
		RescueQuantity:       3,
		RescueUnitPriceMinor: 27540,
	}

	o, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), []order.CheckoutLine{line}, "addr", "+70000000000", "", time.Now())

	require.NoError(t, err)
	require.Len(t, o.Items, 2, "a partially covered rescue line must become two order_items")

	var rescueItem, fullPriceItem *order.Item
	for i := range o.Items {
		if o.Items[i].RescueOfferID != nil {
			rescueItem = &o.Items[i]
		} else {
			fullPriceItem = &o.Items[i]
		}
	}
	require.NotNil(t, rescueItem)
	require.NotNil(t, fullPriceItem)
	assert.Equal(t, offerID, *rescueItem.RescueOfferID)
	assert.Equal(t, 3, rescueItem.Quantity)
	assert.Equal(t, int64(27540), rescueItem.UnitPriceMinor)
	assert.Equal(t, 2, fullPriceItem.Quantity)
	assert.Equal(t, int64(45900), fullPriceItem.UnitPriceMinor)
	assert.Equal(t, int64(3*27540+2*45900), o.TotalMinor())
}

// TestCheckout_RescueFullCoverage_SingleDiscountedLine covers the other,
// more common case: the offer can cover the whole line, so there is
// nothing to split — one order_item, fully at the discounted price.
func TestCheckout_RescueFullCoverage_SingleDiscountedLine(t *testing.T) {
	offerID := uuid.New()
	line := order.CheckoutLine{
		MenuItemID:           uuid.New(),
		Name:                 "Margherita",
		Quantity:             2,
		UnitPriceMinor:       45900,
		RescueOfferID:        &offerID,
		RescueQuantity:       2,
		RescueUnitPriceMinor: 27540,
	}

	o, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), []order.CheckoutLine{line}, "addr", "+70000000000", "", time.Now())

	require.NoError(t, err)
	require.Len(t, o.Items, 1)
	assert.Equal(t, offerID, *o.Items[0].RescueOfferID)
	assert.Equal(t, 2, o.Items[0].Quantity)
	assert.Equal(t, int64(27540), o.Items[0].UnitPriceMinor)
}

// TestCheckout_RescueOfferExpired_BlocksTheWholeOrder covers docs/PLAN.md
// 5.5's other rescue conflict: the offer's own window ended entirely
// (distinct from just running low), which the use-case reports as Err
// exactly like any other per-line conflict — Checkout must still fail the
// whole order, not silently drop the discount.
func TestCheckout_RescueOfferExpired_BlocksTheWholeOrder(t *testing.T) {
	lines := []order.CheckoutLine{
		okLine("Cola", 1, 15900),
		{
			MenuItemID: uuid.New(),
			Name:       "Margherita",
			Quantity:   2,
			Err: errs.NewWithDetails(errs.CodeRescueOfferExpired, "the rescue offer for \"Margherita\" has ended",
				[]map[string]any{{"menuItemId": uuid.New(), "newPriceMinor": int64(45900)}}),
		},
	}

	_, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), lines, "addr", "+70000000000", "", time.Now())

	require.Error(t, err)
	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeRescueOfferExpired, code)
}

// TestCheckout_RescueOfferExpiredOutranksPriceChanged mirrors the existing
// priority tests: an expired rescue deal is reported ahead of an unrelated
// plain price change on another line, matching Checkout's documented
// priority order.
func TestCheckout_RescueOfferExpiredOutranksPriceChanged(t *testing.T) {
	lines := []order.CheckoutLine{
		priceChangedLine("Cola", 1, 15900, 16900),
		{
			MenuItemID: uuid.New(),
			Name:       "Margherita",
			Quantity:   1,
			Err:        errs.New(errs.CodeRescueOfferExpired, "the rescue offer has ended"),
		},
	}

	_, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), lines, "addr", "+70000000000", "", time.Now())

	code, ok := errs.CodeOf(err)
	require.True(t, ok)
	assert.Equal(t, errs.CodeRescueOfferExpired, code)
}

func TestCheckout_UnexpectedLineErrorFailsFast(t *testing.T) {
	boom := errors.New("database is on fire")
	lines := []order.CheckoutLine{
		okLine("Margherita", 1, 45900),
		{MenuItemID: uuid.New(), Name: "Cola", Quantity: 1, Err: boom},
	}

	_, err := order.Checkout(uuid.New(), uuid.New(), uuid.New(), lines, "addr", "+70000000000", "", time.Now())

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

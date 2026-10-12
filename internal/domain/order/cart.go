package order

import (
	"food-delivery-mvp/internal/domain/errs"

	"github.com/google/uuid"
)

// CartItem is one line in a cart. ID identifies the line itself (what the
// public API's PATCH/DELETE /cart/items/{itemId} addresses) — separate from
// MenuItemID, since a menu item can only appear once per cart but the line
// referencing it still needs its own stable address. PriceMinorSnapshot is
// the price shown to the customer when the item was added (docs/PLAN.md 9:
// cart_items), compared against the live menu price at checkout so a
// change can be reported as PRICE_CHANGED instead of silently charging a
// different amount.
//
// RescueOfferID records which rescue offer (docs/PLAN.md 5.5), if any,
// PriceMinorSnapshot was based on when this line was last added to or
// updated — nil means the snapshot is the item's plain price. It exists so
// checkout can re-check that SPECIFIC offer's current state (still live?
// window ended? remaining shrunk?) rather than just asking "is there some
// active offer now", which could not tell a genuinely expired deal apart
// from one that never applied to begin with.
type CartItem struct {
	ID                 uuid.UUID
	MenuItemID         uuid.UUID
	Quantity           int
	PriceMinorSnapshot int64
	RescueOfferID      *uuid.UUID
}

// Total is this line's snapshot price times quantity.
func (i CartItem) Total() int64 {
	return i.PriceMinorSnapshot * int64(i.Quantity)
}

// Cart holds the items a client is assembling for a single venue. A cart is
// pinned to the venue it was created for (docs/PLAN.md 5.1): adding an item
// from a different venue is an error, not an automatic merge or a silent
// switch, because a silent switch would leave the customer ordering from a
// venue they no longer meant to.
type Cart struct {
	ClientID uuid.UUID
	VenueID  uuid.UUID
	Items    []CartItem
}

// NewCart starts an empty cart pinned to venueID.
func NewCart(clientID, venueID uuid.UUID) *Cart {
	return &Cart{ClientID: clientID, VenueID: venueID}
}

// AddItem adds quantity units of menuItemID at priceMinorSnapshot (possibly
// a rescue-discounted price, tracked via rescueOfferID — see CartItem's own
// doc comment), under itemID if this becomes a new line (the caller
// generates itemID — see domain/order.Item for why ids are supplied rather
// than self-generated). If menuItemID is already in the cart, quantity is
// added to the existing line, and its price/rescue offer are overwritten to
// the values passed in — the caller always resolves them fresh against
// live data (docs/PLAN.md 5.5: offer activity is computed at read time, never
// stored stale), so the merged line must reflect that fresh resolution, not
// whatever was true when the earlier quantity was added. venueID must match
// the cart's pinned venue, or this returns CART_VENUE_MISMATCH.
func (c *Cart) AddItem(itemID, venueID, menuItemID uuid.UUID, quantity int, priceMinorSnapshot int64, rescueOfferID *uuid.UUID) error {
	if venueID != c.VenueID {
		return errs.New(errs.CodeCartVenueMismatch,
			"cart is pinned to a different venue; clear it first")
	}

	for i, existing := range c.Items {
		if existing.MenuItemID == menuItemID {
			c.Items[i].Quantity += quantity
			c.Items[i].PriceMinorSnapshot = priceMinorSnapshot
			c.Items[i].RescueOfferID = rescueOfferID
			return nil
		}
	}
	c.Items = append(c.Items, CartItem{
		ID:                 itemID,
		MenuItemID:         menuItemID,
		Quantity:           quantity,
		PriceMinorSnapshot: priceMinorSnapshot,
		RescueOfferID:      rescueOfferID,
	})
	return nil
}

// UpdateQuantity sets the quantity of the line identified by itemID, along
// with a freshly resolved price/rescue offer (see AddItem's own comment for
// why the caller always re-resolves these rather than keeping the old
// ones). Returns NOT_FOUND if no line has that ID, or VALIDATION_ERROR if
// quantity is not positive (a quantity of zero is a removal, done via
// RemoveItem instead, so the caller's intent stays explicit).
func (c *Cart) UpdateQuantity(itemID uuid.UUID, quantity int, priceMinorSnapshot int64, rescueOfferID *uuid.UUID) error {
	if quantity < 1 {
		return errs.New(errs.CodeValidationError, "quantity must be at least 1")
	}
	for i := range c.Items {
		if c.Items[i].ID == itemID {
			c.Items[i].Quantity = quantity
			c.Items[i].PriceMinorSnapshot = priceMinorSnapshot
			c.Items[i].RescueOfferID = rescueOfferID
			return nil
		}
	}
	return errs.New(errs.CodeNotFound, "cart item not found")
}

// RemoveItem drops the line identified by itemID. Returns NOT_FOUND if no
// line has that ID.
func (c *Cart) RemoveItem(itemID uuid.UUID) error {
	for i, item := range c.Items {
		if item.ID == itemID {
			c.Items = append(c.Items[:i], c.Items[i+1:]...)
			return nil
		}
	}
	return errs.New(errs.CodeNotFound, "cart item not found")
}

// TotalMinor sums every line's Total().
func (c *Cart) TotalMinor() int64 {
	var total int64
	for _, item := range c.Items {
		total += item.Total()
	}
	return total
}

// EnsureNotEmpty returns CART_EMPTY if the cart has no items. Checked before
// checkout: an empty cart cannot become an order.
func (c *Cart) EnsureNotEmpty() error {
	if len(c.Items) == 0 {
		return errs.New(errs.CodeCartEmpty, "cart is empty")
	}
	return nil
}

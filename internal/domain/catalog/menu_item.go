package catalog

import (
	"fmt"
	"food-delivery-mvp/internal/domain/errs"

	"github.com/google/uuid"
)

// MenuItem is one position on a venue's menu (docs/PLAN.md 9: menu_items).
// StockQty nil means unlimited stock, typical for items cooked to order;
// a non-nil value is a hard cap that orders decrement.
type MenuItem struct {
	ID                 uuid.UUID
	VenueID            uuid.UUID
	CategoryID         uuid.UUID
	Name               string
	Description        string
	PriceMinor         int64
	IsAvailable        bool
	StockQty           *int
	CookingTimeMinutes int
	// Source and ExternalID trace where this item's data comes from
	// (docs/PLAN.md 9): "platform" items are managed by hand through the
	// partner API, "integration" items are pushed by PUT /menu from the
	// venue's own system and keep that system's id in ExternalID.
	Source     string
	ExternalID *string
	// RescueOffer is the item's currently active rescue offer, or nil.
	// Unlike IsOpen on Venue (a pure function of data the type already
	// holds), this cannot be computed from fields on MenuItem itself — it
	// requires a live query against rescue_offers — so whoever loads a
	// MenuItem for display attaches it as a separate step, the same way
	// VenueRepository.attachSchedules is a separate step from the main
	// venue query, rather than folding it into scanMenuItem.
	RescueOffer *RescueOffer
}

// EnsureAvailable returns ITEM_UNAVAILABLE if the item was switched off by
// hand (the stop list), or INSUFFICIENT_STOCK if fewer than quantity units
// remain. These stay two different codes on purpose (docs/PLAN.md 9): "we are
// not selling this today" and "one portion left instead of three" need
// different messages shown to the customer.
func (m MenuItem) EnsureAvailable(quantity int) error {
	if !m.IsAvailable {
		return errs.NewWithDetails(errs.CodeItemUnavailable,
			fmt.Sprintf("menu item %q is unavailable", m.Name),
			[]map[string]any{{"menuItemId": m.ID, "name": m.Name}})
	}
	if m.StockQty != nil && *m.StockQty < quantity {
		return errs.NewWithDetails(errs.CodeInsufficientStock,
			fmt.Sprintf("only %d of menu item %q left, requested %d", *m.StockQty, m.Name, quantity),
			[]map[string]any{{"menuItemId": m.ID, "requested": quantity, "available": *m.StockQty}})
	}
	return nil
}

// EnsurePriceUnchanged returns PRICE_CHANGED if snapshotPriceMinor no
// longer matches the item's current price — the cart holds a price snapshot
// from when the item was added, and checkout must catch drift instead of
// silently charging the new price.
func (m MenuItem) EnsurePriceUnchanged(snapshotPriceMinor int64) error {
	if m.PriceMinor != snapshotPriceMinor {
		return errs.NewWithDetails(errs.CodePriceChanged,
			fmt.Sprintf("price of %q changed from %d to %d", m.Name, snapshotPriceMinor, m.PriceMinor),
			[]map[string]any{{"menuItemId": m.ID, "oldPriceMinor": snapshotPriceMinor, "newPriceMinor": m.PriceMinor}})
	}
	return nil
}

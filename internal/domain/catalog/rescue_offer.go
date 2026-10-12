package catalog

import (
	"food-delivery-mvp/internal/domain/errs"
	"time"

	"github.com/google/uuid"
)

// RescueOffer is a time-boxed discount on a fixed quantity of one menu item
// (docs/PLAN.md 5.5) — a standalone entity, not a field on MenuItem, because it
// has its own window and its own remaining count independent of the item's
// regular stock_qty.
type RescueOffer struct {
	ID                uuid.UUID
	VenueID           uuid.UUID
	MenuItemID        uuid.UUID
	DiscountPercent   int
	InitialQuantity   int
	RemainingQuantity int
	StartsAt          time.Time
	EndsAt            time.Time
	CancelledAt       *time.Time
}

// WindowValid reports whether the offer's own time window is currently
// open and it has not been canceled — deliberately separate from
// RemainingQuantity: checkout treats "the window itself ended" (a whole
// different deal than the one the customer's cart remembered,
// RESCUE_OFFER_EXPIRED) very differently from "the window is fine but the
// quantity ran low" (a normal, successful line split, docs/PLAN.md 5.5).
func (r RescueOffer) WindowValid(now time.Time) bool {
	if r.CancelledAt != nil {
		return false
	}
	return !now.Before(r.StartsAt) && now.Before(r.EndsAt)
}

// IsActive reports whether the offer's own window and remaining count allow
// it to be used right now. This is only two of docs/PLAN.md 5.5's four
// activity conditions — the other two (the item itself is not stopped/out
// of stock, and the venue is open and accepting orders) are properties of
// other types, so whoever needs the full answer combines this with
// MenuItem.EnsureAvailable and Venue.EnsureCanOrder rather than this method
// trying to know about either.
func (r RescueOffer) IsActive(now time.Time) bool {
	return r.WindowValid(now) && r.RemainingQuantity > 0
}

// DiscountedPrice applies docs/PLAN.md 5.5's exact rounding rule:
// floor(price*(100-discount)/100), rounding down in the customer's favor.
// Go's integer division already truncates toward zero, which is floor for
// non-negative operands — no math.Floor needed.
func (r RescueOffer) DiscountedPrice(baseMinor int64) int64 {
	return baseMinor * int64(100-r.DiscountPercent) / 100
}

// ValidateDiscountPercent returns RESCUE_INVALID_DISCOUNT if percent is
// outside docs/PLAN.md 5.5's allowed 1-90 range.
func ValidateDiscountPercent(percent int) error {
	if percent < 1 || percent > 90 {
		return errs.New(errs.CodeRescueInvalidDiscount, "discountPercent must be between 1 and 90")
	}
	return nil
}

// ValidateRescueWindow returns RESCUE_INVALID_WINDOW if endsAt does not
// come after startsAt, or the window is already entirely in the past.
func ValidateRescueWindow(startsAt, endsAt, now time.Time) error {
	if !endsAt.After(startsAt) {
		return errs.New(errs.CodeRescueInvalidWindow, "endsAt must be after startsAt")
	}
	if !endsAt.After(now) {
		return errs.New(errs.CodeRescueInvalidWindow, "the window is entirely in the past")
	}
	return nil
}

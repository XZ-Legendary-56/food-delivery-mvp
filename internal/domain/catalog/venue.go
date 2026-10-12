package catalog

import (
	"food-delivery-mvp/internal/domain/errs"
	"time"

	"github.com/google/uuid"
)

// Venue is a restaurant on the platform (docs/PLAN.md 9: venues).
type Venue struct {
	ID                  uuid.UUID
	PartnerID           uuid.UUID
	Name                string
	Description         string
	Cuisine             string
	MinOrderAmountMinor int64
	AcceptingOrders     bool
	MenuVersion         int64
	Schedule            []ScheduleEntry
	// WebhookURL is where stage 9's outbox delivers order events, nil if
	// the venue never set one. The signing secret is deliberately not a
	// field here: it is shown to the partner exactly once, right after
	// being generated, never read back (docs/PLAN.md 6.5 / partner.yaml
	// PartnerVenueWithSecret) — carrying it on this widely-read type would
	// make "don't leak it" a rule every future caller has to remember.
	WebhookURL *string
}

// EnsureCanOrder returns VENUE_NOT_ACCEPTING_ORDERS or VENUE_CLOSED if the
// venue cannot take an order at "now". docs/PLAN.md 5.2 requires this check to
// run twice — softly when a customer adds an item to the cart, and again
// inside the checkout transaction — since the venue's state can change in
// the time between the two.
func (v Venue) EnsureCanOrder(now time.Time) error {
	if !v.AcceptingOrders {
		return errs.New(errs.CodeVenueNotAcceptingOrders, "venue is not accepting orders right now")
	}
	if !IsOpenAt(v.Schedule, now) {
		return errs.New(errs.CodeVenueClosed, "venue is closed right now")
	}
	return nil
}

// IsOpen reports whether v's weekly schedule has a window covering now.
// This is deliberately independent of AcceptingOrders — the API shows them
// as two separate fields (api/openapi/public.yaml Venue.isOpen), because
// "closed for the night" and "temporarily paused by the venue" are
// different facts a customer should be told apart. EnsureCanOrder is the
// method that combines both into a single business gate.
func (v Venue) IsOpen(now time.Time) bool {
	return IsOpenAt(v.Schedule, now)
}

// NextOpensAt returns when v's schedule next opens after now, or nil if v
// has no schedule at all. Meaningful only when IsOpen(now) is false.
func (v Venue) NextOpensAt(now time.Time) *time.Time {
	return NextOpenAfter(v.Schedule, now)
}

// EnsureMinOrderReached returns MIN_ORDER_AMOUNT_NOT_REACHED if totalMinor
// is below the venue's minimum order amount.
func (v Venue) EnsureMinOrderReached(totalMinor int64) error {
	if totalMinor < v.MinOrderAmountMinor {
		return errs.Newf(errs.CodeMinOrderAmountNotReached,
			"order total %d is below the venue's minimum of %d", totalMinor, v.MinOrderAmountMinor)
	}
	return nil
}

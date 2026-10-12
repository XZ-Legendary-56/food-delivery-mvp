package order

import (
	"food-delivery-mvp/internal/domain/errs"
)

// Status is an order's position in its lifecycle (docs/PLAN.md 5.4, 9).
type Status string

const (
	StatusCreated    Status = "created"
	StatusConfirmed  Status = "confirmed"
	StatusCooking    Status = "cooking"
	StatusReady      Status = "ready"
	StatusDelivering Status = "delivering"
	StatusDelivered  Status = "delivered"
	StatusRejected   Status = "rejected"
	StatusCancelled  Status = "cancelled" //nolint:misspell // wire value fixed by the orders.status CHECK constraint and both OpenAPI specs (docs/PLAN.md 5.4/9); renaming it would break the schema and the generated clients, not just this constant
)

// Actor identifies who triggered a status change, for order_status_history.actor.
type Actor string

const (
	ActorCustomer Actor = "customer"
	ActorVenue    Actor = "venue"
	ActorSystem   Actor = "system"
)

// transitions is the explicit table of allowed status changes from
// docs/PLAN.md 5.4:
//
//	                  ┌─→ rejected        (venue declined, with a reason)
//	created ─→ confirmed ─→ cooking ─→ ready ─→ delivering ─→ delivered
//	    │          │
//	    └──────────┴─→ canceled         (customer canceled, only before cooking)
//
// It is a lookup table rather than an if/switch chain on purpose: the whole
// state machine is visible in one place, and adding or removing an edge is
// a one-line change with no risk of an unrelated branch shadowing it.
// delivered, rejected and canceled have no entry, so they are terminal —
// any transition attempted from them falls through to the zero value
// (false) in CanTransition below.
var transitions = map[Status]map[Status]bool{
	StatusCreated: {
		StatusConfirmed: true,
		StatusRejected:  true,
		StatusCancelled: true,
	},
	StatusConfirmed: {
		StatusCooking:   true,
		StatusCancelled: true,
	},
	StatusCooking: {
		StatusReady: true,
	},
	StatusReady: {
		StatusDelivering: true,
	},
	StatusDelivering: {
		StatusDelivered: true,
	},
}

// CanTransition reports whether moving an order from status "from" directly
// to "to" is allowed by the state machine.
func CanTransition(from, to Status) bool {
	return transitions[from][to]
}

// ValidateTransition returns nil if from -> to is an allowed edge, or an
// *errs.Error with errs.CodeOrderInvalidStateTransition otherwise.
func ValidateTransition(from, to Status) error {
	if CanTransition(from, to) {
		return nil
	}
	return errs.Newf(errs.CodeOrderInvalidStateTransition,
		"cannot transition order from %q to %q", from, to)
}

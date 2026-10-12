package order

import (
	"food-delivery-mvp/internal/domain/errs"
	"time"

	"github.com/google/uuid"
)

// Item is one line of a placed order. Name and price are copied at order
// time (docs/PLAN.md 9: order_items) rather than looked up live, so editing the
// menu later cannot rewrite an order that was already placed.
type Item struct {
	ID             uuid.UUID
	MenuItemID     uuid.UUID
	RescueOfferID  *uuid.UUID // set when this line was bought at a rescue-offer price
	NameSnapshot   string
	UnitPriceMinor int64
	Quantity       int
}

// Total is this line's contribution to the order total, in minor units.
func (i Item) Total() int64 {
	return i.UnitPriceMinor * int64(i.Quantity)
}

// StatusChange is one row of order_status_history: where a transition came
// from, where it went, and who asked for it. From is nil for the very
// first entry — an order's own creation is not a transition between two
// statuses, it is arrival at the first one — matching
// api/openapi/public.yaml's OrderStatusHistoryEntry.fromStatus, which is
// nullable for exactly this reason.
type StatusChange struct {
	From      *Status
	To        Status
	Actor     Actor
	CreatedAt time.Time
}

// Order is the order aggregate. Its status only ever changes through
// TransitionTo, so status.go's transition table is the single place that
// decides what is allowed.
type Order struct {
	ID              uuid.UUID
	ClientID        uuid.UUID
	VenueID         uuid.UUID
	Status          Status
	Items           []Item
	DeliveryAddress string
	CustomerPhone   string
	Comment         string
	ETAMinutes      *int
	RejectionReason string
	ExternalOrderID string
	History         []StatusChange
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// New builds a fresh order in StatusCreated, with one History entry
// recording that creation (From: nil, To: StatusCreated, Actor:
// ActorCustomer — placing an order is the customer's own action). An order
// with no items is rejected up front: it is never a valid aggregate, not
// just a rare one.
func New(id, clientID, venueID uuid.UUID, items []Item, deliveryAddress, customerPhone, comment string, now time.Time) (*Order, error) {
	if len(items) == 0 {
		return nil, errs.New(errs.CodeCartEmpty, "order must have at least one item")
	}
	return &Order{
		ID:              id,
		ClientID:        clientID,
		VenueID:         venueID,
		Status:          StatusCreated,
		Items:           items,
		DeliveryAddress: deliveryAddress,
		CustomerPhone:   customerPhone,
		Comment:         comment,
		History: []StatusChange{
			{From: nil, To: StatusCreated, Actor: ActorCustomer, CreatedAt: now},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// TotalMinor sums every line's Total().
func (o *Order) TotalMinor() int64 {
	var total int64
	for _, item := range o.Items {
		total += item.Total()
	}
	return total
}

// TransitionTo moves the order to newStatus if the state machine allows it
// (status.go), recording the change in History. It leaves the order
// untouched and returns an error otherwise.
func (o *Order) TransitionTo(newStatus Status, actor Actor, now time.Time) error {
	if err := ValidateTransition(o.Status, newStatus); err != nil {
		return err
	}
	// from is its own variable, not &o.Status, because o.Status is
	// overwritten two lines down — a pointer straight into the field would
	// have every past History entry silently change value with it.
	from := o.Status
	o.History = append(o.History, StatusChange{
		From:      &from,
		To:        newStatus,
		Actor:     actor,
		CreatedAt: now,
	})
	o.Status = newStatus
	o.UpdatedAt = now
	return nil
}

// Reject transitions the order to StatusRejected and records why the venue
// declined it.
func (o *Order) Reject(reason string, now time.Time) error {
	if err := o.TransitionTo(StatusRejected, ActorVenue, now); err != nil {
		return err
	}
	o.RejectionReason = reason
	return nil
}

// Cancel transitions the order to StatusCancelled on the customer's behalf.
// The state machine (status.go) already limits this to StatusCreated and
// StatusConfirmed, matching "customer canceled, only before cooking"
// (docs/PLAN.md 5.4) without this method needing to know that rule itself.
func (o *Order) Cancel(now time.Time) error {
	return o.TransitionTo(StatusCancelled, ActorCustomer, now)
}

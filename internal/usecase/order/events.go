package order

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	domainorder "food-delivery-mvp/internal/domain/order"
)

// eventOrderCreated and eventOrderCancelled are the only two events this
// project emits (docs/PLAN.md 7.4) — the two moments a venue's own system
// needs to hear about without polling.
const (
	eventOrderCreated   = "order.created"
	eventOrderCancelled = "order.cancelled"
	eventVersion        = 1
	aggregateTypeOrder  = "order"
)

// orderItemPayload is one order_items row as it appears in a webhook body.
type orderItemPayload struct {
	MenuItemID     uuid.UUID `json:"menu_item_id"`
	Name           string    `json:"name"`
	UnitPriceMinor int64     `json:"unit_price_minor"`
	Quantity       int       `json:"quantity"`
}

// orderCreatedPayload is order.created's payload — everything a venue's
// system needs to start preparing the order without calling back into this
// API first.
type orderCreatedPayload struct {
	OrderID         uuid.UUID          `json:"order_id"`
	VenueID         uuid.UUID          `json:"venue_id"`
	ClientID        uuid.UUID          `json:"client_id"`
	Status          string             `json:"status"`
	TotalMinor      int64              `json:"total_minor"`
	DeliveryAddress string             `json:"delivery_address"`
	CustomerPhone   string             `json:"customer_phone"`
	Comment         string             `json:"comment,omitempty"`
	Items           []orderItemPayload `json:"items"`
	CreatedAt       time.Time          `json:"created_at"`
}

// orderCancelledPayload is order.cancelled's payload — just enough for the
// venue to stop preparing an order it may have already started on.
type orderCancelledPayload struct {
	OrderID     uuid.UUID `json:"order_id"`
	VenueID     uuid.UUID `json:"venue_id"`
	CancelledAt time.Time `json:"canceled_at"`
}

// newOrderCreatedEvent builds the OutboxEvent for o, ready for
// OutboxRepository.Enqueue in the same transaction as orders.Create.
func newOrderCreatedEvent(o *domainorder.Order) (OutboxEvent, error) {
	items := make([]orderItemPayload, len(o.Items))
	for i, item := range o.Items {
		items[i] = orderItemPayload{
			MenuItemID:     item.MenuItemID,
			Name:           item.NameSnapshot,
			UnitPriceMinor: item.UnitPriceMinor,
			Quantity:       item.Quantity,
		}
	}
	payload, err := json.Marshal(orderCreatedPayload{
		OrderID:         o.ID,
		VenueID:         o.VenueID,
		ClientID:        o.ClientID,
		Status:          string(o.Status),
		TotalMinor:      o.TotalMinor(),
		DeliveryAddress: o.DeliveryAddress,
		CustomerPhone:   o.CustomerPhone,
		Comment:         o.Comment,
		Items:           items,
		CreatedAt:       o.CreatedAt,
	})
	if err != nil {
		return OutboxEvent{}, fmt.Errorf("marshal order.created payload: %w", err)
	}
	return OutboxEvent{
		Type:          eventOrderCreated,
		AggregateType: aggregateTypeOrder,
		AggregateID:   o.ID,
		Payload:       payload,
		OccurredAt:    o.CreatedAt,
	}, nil
}

// newOrderCancelledEvent builds the OutboxEvent for o's cancellation. o must
// already have Cancel applied (its History's last entry is the cancellation
// itself), so occurredAt comes from that entry rather than time.Now() —
// the event's timestamp matches exactly when the transition was recorded.
func newOrderCancelledEvent(o *domainorder.Order) (OutboxEvent, error) {
	occurredAt := o.UpdatedAt
	payload, err := json.Marshal(orderCancelledPayload{
		OrderID:     o.ID,
		VenueID:     o.VenueID,
		CancelledAt: occurredAt,
	})
	if err != nil {
		return OutboxEvent{}, fmt.Errorf("marshal order.cancelled payload: %w", err)
	}
	return OutboxEvent{
		Type:          eventOrderCancelled,
		AggregateType: aggregateTypeOrder,
		AggregateID:   o.ID,
		Payload:       payload,
		OccurredAt:    occurredAt,
	}, nil
}

package partner

import (
	"context"
	"fmt"
	"food-delivery-mvp/internal/domain/errs"
	"time"

	"github.com/google/uuid"

	domainorder "food-delivery-mvp/internal/domain/order"
)

// OrderService backs the partner order-fulfillment endpoints (docs/PLAN.md
// 5.3 items 6-8): polling, accept/reject, and status advancement — all
// through the state machine domain/order already defines and tests, never
// re-decided here.
type OrderService struct {
	orders OrderRepository
}

func NewOrderService(orders OrderRepository) *OrderService {
	return &OrderService{orders: orders}
}

// ListOrders returns venueID's orders (docs/PLAN.md 5.3 item 6's polling
// backstop for the webhook stage 9 adds).
func (s *OrderService) ListOrders(ctx context.Context, venueID uuid.UUID, status *domainorder.Status, since *time.Time, limit int) ([]domainorder.Order, error) {
	orders, err := s.orders.ListForVenue(ctx, venueID, status, since, limit)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return orders, nil
}

// getOwned fetches orderID and checks it belongs to venueID, reporting
// NOT_FOUND either way — same "don't reveal it exists" reasoning as the
// public API's own OrderService.GetOrder.
func (s *OrderService) getOwned(ctx context.Context, venueID, orderID uuid.UUID) (*domainorder.Order, error) {
	o, err := s.orders.Get(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}
	if o == nil || o.VenueID != venueID {
		return nil, errs.New(errs.CodeNotFound, "order not found")
	}
	return o, nil
}

func (s *OrderService) GetOrder(ctx context.Context, venueID, orderID uuid.UUID) (*domainorder.Order, error) {
	return s.getOwned(ctx, venueID, orderID)
}

// AcceptOrder moves an order from created to confirmed with an ETA — the
// state machine (domain/order/status.go) is what actually limits this to
// StatusCreated, this method does not re-decide it.
func (s *OrderService) AcceptOrder(ctx context.Context, venueID, orderID uuid.UUID, etaMinutes int, externalOrderID *string) (*domainorder.Order, error) {
	o, err := s.getOwned(ctx, venueID, orderID)
	if err != nil {
		return nil, err
	}
	if err := o.TransitionTo(domainorder.StatusConfirmed, domainorder.ActorVenue, time.Now()); err != nil {
		return nil, err
	}
	o.ETAMinutes = &etaMinutes
	if externalOrderID != nil {
		o.ExternalOrderID = *externalOrderID
	}
	if err := s.orders.ApplyTransition(ctx, o); err != nil {
		return nil, fmt.Errorf("persist accept: %w", err)
	}
	return o, nil
}

// RejectOrder declines an order, recording why.
func (s *OrderService) RejectOrder(ctx context.Context, venueID, orderID uuid.UUID, reason string) (*domainorder.Order, error) {
	o, err := s.getOwned(ctx, venueID, orderID)
	if err != nil {
		return nil, err
	}
	if err := o.Reject(reason, time.Now()); err != nil {
		return nil, err
	}
	if err := s.orders.ApplyTransition(ctx, o); err != nil {
		return nil, fmt.Errorf("persist reject: %w", err)
	}
	return o, nil
}

// AdvanceStatus moves an order to the next status in {cooking, ready,
// delivering, delivered}; a disallowed jump fails with
// ORDER_INVALID_STATE_TRANSITION straight from the state machine.
func (s *OrderService) AdvanceStatus(ctx context.Context, venueID, orderID uuid.UUID, newStatus domainorder.Status) (*domainorder.Order, error) {
	o, err := s.getOwned(ctx, venueID, orderID)
	if err != nil {
		return nil, err
	}
	if err := o.TransitionTo(newStatus, domainorder.ActorVenue, time.Now()); err != nil {
		return nil, err
	}
	if err := s.orders.ApplyTransition(ctx, o); err != nil {
		return nil, fmt.Errorf("persist status change: %w", err)
	}
	return o, nil
}

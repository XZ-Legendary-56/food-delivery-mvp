package order

import (
	"context"
	"fmt"
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/usecase"
	"time"

	"github.com/google/uuid"

	domainorder "food-delivery-mvp/internal/domain/order"
)

// Service answers order status queries and cancellation (docs/PLAN.md 5.1
// items 5-6). It is separate from CheckoutService: placing an order
// touches the cart, the catalog and stock; these two operations only ever
// touch the order itself.
type Service struct {
	orders    Repository
	outbox    OutboxRepository
	txManager usecase.TxManager
}

// NewOrderService keeps its own descriptive name (rather than plain New)
// so it reads consistently alongside this package's other constructors,
// NewCartService and NewCheckoutService, even though its return type is
// just Service — order is this whole package's own subject, so Service
// unqualified is what it is, but the constructor still says which of the
// package's three services it builds.
func NewOrderService(orders Repository, outbox OutboxRepository, txManager usecase.TxManager) *Service {
	return &Service{orders: orders, outbox: outbox, txManager: txManager}
}

// GetOrder returns clientID's order, or errs.CodeNotFound if orderID does
// not exist or belongs to a different client — the two are reported
// identically on purpose, so a client can never use this endpoint to probe
// whether some other client's order id exists.
func (s *Service) GetOrder(ctx context.Context, clientID, orderID uuid.UUID) (*domainorder.Order, error) {
	o, err := s.orders.Get(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}
	if o == nil || o.ClientID != clientID {
		return nil, errs.New(errs.CodeNotFound, "order not found")
	}
	return o, nil
}

// CancelOrder cancels clientID's order on their own behalf. The state
// machine (domain/order/status.go) is what actually limits this to
// StatusCreated and StatusConfirmed ("only before cooking", docs/PLAN.md
// 5.4) — this method does not encode that rule itself, it just persists
// whatever domainorder.Order.Cancel decides.
//
// Read and write run in one transaction so the two can't drift apart, but
// this does not lock the order row: two concurrent cancel/status-change
// requests for the same order are not one of docs/PLAN.md's required
// scenarios, and the worst a lost update here causes is a duplicated
// history row, not a stock or money error — unlike checkout, where the
// analogous gap is exactly what docs/PLAN.md 9 asks to close with FOR UPDATE.
func (s *Service) CancelOrder(ctx context.Context, clientID, orderID uuid.UUID) (*domainorder.Order, error) {
	var result *domainorder.Order
	err := s.txManager.WithinTx(ctx, func(ctx context.Context) error {
		o, err := s.orders.Get(ctx, orderID)
		if err != nil {
			return fmt.Errorf("get order: %w", err)
		}
		if o == nil || o.ClientID != clientID {
			return errs.New(errs.CodeNotFound, "order not found")
		}

		if err := o.Cancel(time.Now()); err != nil {
			return err
		}
		change := o.History[len(o.History)-1]
		if err := s.orders.AppendStatusChange(ctx, o.ID, change); err != nil {
			return fmt.Errorf("persist cancellation: %w", err)
		}
		event, err := newOrderCancelledEvent(o)
		if err != nil {
			return err
		}
		if err := s.outbox.Enqueue(ctx, event); err != nil {
			return fmt.Errorf("enqueue order.cancelled event: %w", err)
		}

		result = o
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

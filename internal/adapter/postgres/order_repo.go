package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	domainorder "food-delivery-mvp/internal/domain/order"
	orderusecase "food-delivery-mvp/internal/usecase/order"
	partnerusecase "food-delivery-mvp/internal/usecase/partner"
)

// OrderRepository implements orderusecase.Repository and
// partnerusecase.OrderRepository on orders, order_items and
// order_status_history.
type OrderRepository struct {
	pool *pgxpool.Pool
}

func NewOrderRepository(pool *pgxpool.Pool) *OrderRepository {
	return &OrderRepository{pool: pool}
}

var (
	_ orderusecase.Repository        = (*OrderRepository)(nil)
	_ partnerusecase.OrderRepository = (*OrderRepository)(nil)
)

// Create inserts o, its items, and every entry already in o.History (at
// minimum the one Order.New itself adds for the initial "created" state).
// All three inserts run through QuerierFromContext, so whatever transaction
// CheckoutService.PlaceOrder opened (stock already decremented in it) is
// what they land in — an order is never visible without the stock it
// consumed also being gone, or vice versa.
func (r *OrderRepository) Create(ctx context.Context, o *domainorder.Order) error {
	q := QuerierFromContext(ctx, r.pool)

	if _, err := q.Exec(ctx, `
		INSERT INTO orders (
			id, client_id, venue_id, status, total_minor,
			delivery_address, customer_phone, comment, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, o.ID, o.ClientID, o.VenueID, string(o.Status), o.TotalMinor(),
		o.DeliveryAddress, o.CustomerPhone, o.Comment, o.CreatedAt, o.UpdatedAt); err != nil {
		return fmt.Errorf("insert order: %w", err)
	}

	for _, item := range o.Items {
		if _, err := q.Exec(ctx, `
			INSERT INTO order_items (
				id, order_id, menu_item_id, rescue_offer_id, name_snapshot, unit_price_minor, quantity
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, item.ID, o.ID, item.MenuItemID, item.RescueOfferID, item.NameSnapshot, item.UnitPriceMinor, item.Quantity); err != nil {
			return fmt.Errorf("insert order item %s: %w", item.MenuItemID, err)
		}
	}

	for _, change := range o.History {
		var fromStatus *string
		if change.From != nil {
			s := string(*change.From)
			fromStatus = &s
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO order_status_history (id, order_id, from_status, to_status, actor, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, uuid.New(), o.ID, fromStatus, string(change.To), string(change.Actor), change.CreatedAt); err != nil {
			return fmt.Errorf("insert order status history: %w", err)
		}
	}

	return nil
}

// Get returns nil, nil if id does not exist.
func (r *OrderRepository) Get(ctx context.Context, id uuid.UUID) (*domainorder.Order, error) {
	q := QuerierFromContext(ctx, r.pool)

	var o domainorder.Order
	var status string
	var rejectionReason, externalOrderID *string
	err := q.QueryRow(ctx, `
		SELECT id, client_id, venue_id, status, delivery_address, customer_phone, comment,
		       eta_minutes, rejection_reason, external_order_id, created_at, updated_at
		FROM orders WHERE id = $1
	`, id).Scan(&o.ID, &o.ClientID, &o.VenueID, &status, &o.DeliveryAddress, &o.CustomerPhone, &o.Comment,
		&o.ETAMinutes, &rejectionReason, &externalOrderID, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query order: %w", err)
	}
	o.Status = domainorder.Status(status)
	if rejectionReason != nil {
		o.RejectionReason = *rejectionReason
	}
	if externalOrderID != nil {
		o.ExternalOrderID = *externalOrderID
	}

	itemRows, err := q.Query(ctx, `
		SELECT id, menu_item_id, rescue_offer_id, name_snapshot, unit_price_minor, quantity
		FROM order_items WHERE order_id = $1
		ORDER BY name_snapshot
	`, id)
	if err != nil {
		return nil, fmt.Errorf("query order items: %w", err)
	}
	for itemRows.Next() {
		var item domainorder.Item
		if err := itemRows.Scan(&item.ID, &item.MenuItemID, &item.RescueOfferID, &item.NameSnapshot, &item.UnitPriceMinor, &item.Quantity); err != nil {
			itemRows.Close()
			return nil, fmt.Errorf("scan order item: %w", err)
		}
		o.Items = append(o.Items, item)
	}
	itemRows.Close()
	if err := itemRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order items: %w", err)
	}

	historyRows, err := q.Query(ctx, `
		SELECT from_status, to_status, actor, created_at
		FROM order_status_history WHERE order_id = $1
		ORDER BY created_at
	`, id)
	if err != nil {
		return nil, fmt.Errorf("query order status history: %w", err)
	}
	for historyRows.Next() {
		var change domainorder.StatusChange
		var fromStatus *string
		var toStatus, actor string
		if err := historyRows.Scan(&fromStatus, &toStatus, &actor, &change.CreatedAt); err != nil {
			historyRows.Close()
			return nil, fmt.Errorf("scan order status history: %w", err)
		}
		if fromStatus != nil {
			s := domainorder.Status(*fromStatus)
			change.From = &s
		}
		change.To = domainorder.Status(toStatus)
		change.Actor = domainorder.Actor(actor)
		o.History = append(o.History, change)
	}
	historyRows.Close()
	if err := historyRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order status history: %w", err)
	}

	return &o, nil
}

// AppendStatusChange updates orders.status/updated_at and inserts the
// matching order_status_history row, from one already-applied
// domainorder.StatusChange.
func (r *OrderRepository) AppendStatusChange(ctx context.Context, orderID uuid.UUID, change domainorder.StatusChange) error {
	q := QuerierFromContext(ctx, r.pool)

	tag, err := q.Exec(ctx, `
		UPDATE orders SET status = $1, updated_at = $2 WHERE id = $3
	`, string(change.To), change.CreatedAt, orderID)
	if err != nil {
		return fmt.Errorf("update order status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("order %s not found when appending status change", orderID)
	}

	var fromStatus *string
	if change.From != nil {
		s := string(*change.From)
		fromStatus = &s
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO order_status_history (id, order_id, from_status, to_status, actor, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, uuid.New(), orderID, fromStatus, string(change.To), string(change.Actor), change.CreatedAt); err != nil {
		return fmt.Errorf("insert order status history: %w", err)
	}
	return nil
}

// GetWebhookForOrder resolves orderID to its venue's webhook_url and
// webhook_secret, for adapter/webhook.Publisher's own VenueWebhookLookup
// port. ok is false when the order does not exist or its venue has no
// webhook_url configured — "nothing to deliver to" is not this method's
// error to report, the caller decides what that means.
func (r *OrderRepository) GetWebhookForOrder(ctx context.Context, orderID uuid.UUID) (url string, secret string, ok bool, err error) {
	q := QuerierFromContext(ctx, r.pool)

	var webhookURL, webhookSecret *string
	dbErr := q.QueryRow(ctx, `
		SELECT v.webhook_url, v.webhook_secret
		FROM orders o
		JOIN venues v ON v.id = o.venue_id
		WHERE o.id = $1
	`, orderID).Scan(&webhookURL, &webhookSecret)
	if errors.Is(dbErr, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if dbErr != nil {
		return "", "", false, fmt.Errorf("query webhook for order %s: %w", orderID, dbErr)
	}
	if webhookURL == nil || *webhookURL == "" {
		return "", "", false, nil
	}
	secret = ""
	if webhookSecret != nil {
		secret = *webhookSecret
	}
	return *webhookURL, secret, true, nil
}

// ListForVenue returns venueID's orders, newest first. It fetches matching
// ids in one query, then reuses Get per order for items/history — a small,
// bounded N+1 (limit caps it at 200) rather than the kind docs/PLAN.md 6.6
// forbids for the customer-facing catalog listing; this is a partner
// polling endpoint, not a page a customer waits on.
func (r *OrderRepository) ListForVenue(ctx context.Context, venueID uuid.UUID, status *domainorder.Status, since *time.Time, limit int) ([]domainorder.Order, error) {
	q := QuerierFromContext(ctx, r.pool)

	conditions := []string{"venue_id = $1"}
	args := []any{venueID}
	if status != nil {
		args = append(args, string(*status))
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	if since != nil {
		args = append(args, *since)
		conditions = append(conditions, fmt.Sprintf("updated_at >= $%d", len(args)))
	}
	args = append(args, limit)
	limitArg := len(args)

	rows, err := q.Query(ctx, fmt.Sprintf(`
		SELECT id FROM orders WHERE %s ORDER BY created_at DESC LIMIT $%d
	`, strings.Join(conditions, " AND "), limitArg), args...)
	if err != nil {
		return nil, fmt.Errorf("query orders: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan order id: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orders: %w", err)
	}

	result := make([]domainorder.Order, 0, len(ids))
	for _, id := range ids {
		o, err := r.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if o != nil {
			result = append(result, *o)
		}
	}
	return result, nil
}

// ApplyTransition persists o's mutable fields and appends its latest
// History entry as a new order_status_history row.
func (r *OrderRepository) ApplyTransition(ctx context.Context, o *domainorder.Order) error {
	q := QuerierFromContext(ctx, r.pool)

	var rejectionReason *string
	if o.RejectionReason != "" {
		rejectionReason = &o.RejectionReason
	}
	var externalOrderID *string
	if o.ExternalOrderID != "" {
		externalOrderID = &o.ExternalOrderID
	}

	tag, err := q.Exec(ctx, `
		UPDATE orders SET status = $1, eta_minutes = $2, rejection_reason = $3, external_order_id = $4, updated_at = $5
		WHERE id = $6
	`, string(o.Status), o.ETAMinutes, rejectionReason, externalOrderID, o.UpdatedAt, o.ID)
	if err != nil {
		return fmt.Errorf("update order: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("order %s not found when applying transition", o.ID)
	}

	if len(o.History) == 0 {
		return nil
	}
	change := o.History[len(o.History)-1]
	var fromStatus *string
	if change.From != nil {
		s := string(*change.From)
		fromStatus = &s
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO order_status_history (id, order_id, from_status, to_status, actor, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, uuid.New(), o.ID, fromStatus, string(change.To), string(change.Actor), change.CreatedAt); err != nil {
		return fmt.Errorf("insert order status history: %w", err)
	}
	return nil
}

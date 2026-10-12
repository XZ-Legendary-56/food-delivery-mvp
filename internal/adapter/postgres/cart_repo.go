package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	domainorder "food-delivery-mvp/internal/domain/order"
	orderusecase "food-delivery-mvp/internal/usecase/order"
)

// CartRepository implements orderusecase.CartRepository on carts and
// cart_items.
type CartRepository struct {
	pool *pgxpool.Pool
}

func NewCartRepository(pool *pgxpool.Pool) *CartRepository {
	return &CartRepository{pool: pool}
}

var _ orderusecase.CartRepository = (*CartRepository)(nil)

// Get returns nil, nil if clientID has no cart row yet.
func (r *CartRepository) Get(ctx context.Context, clientID uuid.UUID) (*domainorder.Cart, error) {
	q := QuerierFromContext(ctx, r.pool)

	rows, err := q.Query(ctx, `
		SELECT c.venue_id, ci.id, ci.menu_item_id, ci.quantity, ci.price_minor_snapshot, ci.rescue_offer_id
		FROM carts c
		LEFT JOIN cart_items ci ON ci.cart_id = c.id
		WHERE c.client_id = $1
	`, clientID)
	if err != nil {
		return nil, fmt.Errorf("query cart: %w", err)
	}
	defer rows.Close()

	var cart *domainorder.Cart
	for rows.Next() {
		var venueID uuid.UUID
		var itemID, menuItemID *uuid.UUID
		var quantity *int
		var priceMinor *int64
		var rescueOfferID *uuid.UUID
		if err := rows.Scan(&venueID, &itemID, &menuItemID, &quantity, &priceMinor, &rescueOfferID); err != nil {
			return nil, fmt.Errorf("scan cart row: %w", err)
		}
		if cart == nil {
			cart = domainorder.NewCart(clientID, venueID)
		}
		if itemID != nil {
			cart.Items = append(cart.Items, domainorder.CartItem{
				ID:                 *itemID,
				MenuItemID:         *menuItemID,
				Quantity:           *quantity,
				PriceMinorSnapshot: *priceMinor,
				RescueOfferID:      rescueOfferID,
			})
		}
	}
	return cart, rows.Err()
}

// Save upserts the cart row and replaces its item set entirely: delete
// every existing cart_items row for it, then insert cart.Items as they are
// now. A cart holds at most a handful of lines, so diffing old vs. new
// rows to patch only what changed would add complexity for no measurable
// benefit — delete-and-reinsert is the simpler correct choice here, unlike
// order_items, which is never rewritten after checkout at all.
func (r *CartRepository) Save(ctx context.Context, cart *domainorder.Cart) error {
	q := QuerierFromContext(ctx, r.pool)

	var cartID uuid.UUID
	err := q.QueryRow(ctx, `
		INSERT INTO carts (client_id, venue_id)
		VALUES ($1, $2)
		ON CONFLICT (client_id) DO UPDATE SET venue_id = EXCLUDED.venue_id, updated_at = now()
		RETURNING id
	`, cart.ClientID, cart.VenueID).Scan(&cartID)
	if err != nil {
		return fmt.Errorf("upsert cart: %w", err)
	}

	if _, err := q.Exec(ctx, `DELETE FROM cart_items WHERE cart_id = $1`, cartID); err != nil {
		return fmt.Errorf("clear cart items: %w", err)
	}

	for _, item := range cart.Items {
		if _, err := q.Exec(ctx, `
			INSERT INTO cart_items (id, cart_id, menu_item_id, quantity, price_minor_snapshot, rescue_offer_id)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, item.ID, cartID, item.MenuItemID, item.Quantity, item.PriceMinorSnapshot, item.RescueOfferID); err != nil {
			return fmt.Errorf("insert cart item: %w", err)
		}
	}
	return nil
}

// Clear deletes the cart row (cart_items cascade with it, per
// migrations/00006_carts.sql). A no-op if clientID has no cart.
func (r *CartRepository) Clear(ctx context.Context, clientID uuid.UUID) error {
	q := QuerierFromContext(ctx, r.pool)
	if _, err := q.Exec(ctx, `DELETE FROM carts WHERE client_id = $1`, clientID); err != nil {
		return fmt.Errorf("delete cart: %w", err)
	}
	return nil
}

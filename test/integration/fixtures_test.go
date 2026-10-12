//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// itemPriceMinor is the price used for every seeded menu item in this
// package's tests — its exact value plays no role in what any test proves,
// only that carts and menu items agree on it.
const itemPriceMinor = 50000

// seedPartner inserts one partner and returns its id.
func seedPartner(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO partners (id, name) VALUES ($1, 'Test Partner')`, id)
	if err != nil {
		t.Fatalf("seed partner: %v", err)
	}
	return id
}

// seedOpenVenue inserts a venue that is accepting orders, with no minimum
// order amount and a schedule open every hour of every day — an empty
// schedule would NOT do (catalog.IsOpenAt returns false when there is
// nothing to match against, so a venue with no schedule rows reads as
// closed every moment, not open every moment; see
// catalog.TestIsOpenAt_NoEntries), so this seeds one row per weekday
// spanning the whole day instead.
func seedOpenVenue(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	partnerID := seedPartner(t, pool)
	venueID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO venues (id, partner_id, name, cuisine, min_order_amount_minor, accepting_orders)
		VALUES ($1, $2, 'Test Venue', 'Test', 0, true)
	`, venueID, partnerID); err != nil {
		t.Fatalf("seed venue: %v", err)
	}

	for weekday := 0; weekday < 7; weekday++ {
		if _, err := pool.Exec(ctx, `
			INSERT INTO venue_schedules (id, venue_id, weekday, opens_at, closes_at)
			VALUES ($1, $2, $3, '00:00:00', '23:59:59')
		`, uuid.New(), venueID, weekday); err != nil {
			t.Fatalf("seed venue schedule: %v", err)
		}
	}

	return venueID
}

// seedMenuItem inserts one menu item, each in its own freshly named
// category, for venueID with the given stock, and returns the item's id.
// A fresh category per call (rather than one shared "Test Category") is
// what lets a test seed several menu items for the same venue: menu
// categories are unique per (venue_id, name).
func seedMenuItem(t *testing.T, pool *pgxpool.Pool, venueID uuid.UUID, stockQty int) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	categoryID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO menu_categories (id, venue_id, name) VALUES ($1, $2, $3)
	`, categoryID, venueID, "Test Category "+categoryID.String()); err != nil {
		t.Fatalf("seed menu category: %v", err)
	}

	itemID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO menu_items (id, venue_id, category_id, name, price_minor, is_available, stock_qty)
		VALUES ($1, $2, $3, 'Test Item', $4, true, $5)
	`, itemID, venueID, categoryID, itemPriceMinor, stockQty); err != nil {
		t.Fatalf("seed menu item: %v", err)
	}
	return itemID
}

// seedCartWithItem gives clientID a cart at venueID holding one line:
// quantity units of menuItemID, priced at itemPriceMinor.
func seedCartWithItem(t *testing.T, pool *pgxpool.Pool, clientID, venueID, menuItemID uuid.UUID, quantity int) {
	t.Helper()
	ctx := context.Background()

	cartID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO carts (id, client_id, venue_id) VALUES ($1, $2, $3)
	`, cartID, clientID, venueID); err != nil {
		t.Fatalf("seed cart: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO cart_items (id, cart_id, menu_item_id, quantity, price_minor_snapshot)
		VALUES ($1, $2, $3, $4, $5)
	`, uuid.New(), cartID, menuItemID, quantity, itemPriceMinor); err != nil {
		t.Fatalf("seed cart item: %v", err)
	}
}

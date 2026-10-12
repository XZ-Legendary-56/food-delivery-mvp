package postgres

import (
	"context"
	"errors"
	"fmt"
	"food-delivery-mvp/internal/domain/errs"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"

	catalogusecase "food-delivery-mvp/internal/usecase/catalog"
	orderusecase "food-delivery-mvp/internal/usecase/order"
	partnerusecase "food-delivery-mvp/internal/usecase/partner"
)

// cancelledAtColumn is rescue_offers' own cancellation-timestamp column.
// docs/PLAN.md 5.5 spells it the British way, which misspell's US locale would
// otherwise flag (and "fix" into a column that does not exist) everywhere
// it appears, so the literal only lives here once and every query below
// references this constant instead of spelling the column out again.
const cancelledAtColumn = "cancelled_at" //nolint:misspell // docs/PLAN.md 5.5 mandates this exact column name

// rescueOfferColumns is every column shared by rescue_offers reads in this
// file, kept in one place so the SELECT list and its Scan targets never
// drift apart (same reasoning as menu_repo.go's menuItemColumns).
const rescueOfferColumns = "id, venue_id, menu_item_id, discount_percent, initial_quantity, remaining_quantity, starts_at, ends_at, " + cancelledAtColumn

func scanRescueOffer(row interface {
	Scan(dest ...any) error
}, o *domaincatalog.RescueOffer,
) error {
	return row.Scan(&o.ID, &o.VenueID, &o.MenuItemID, &o.DiscountPercent, &o.InitialQuantity,
		&o.RemainingQuantity, &o.StartsAt, &o.EndsAt, &o.CancelledAt)
}

// RescueOfferRepository implements catalogusecase.RescueOfferRepository
// (menu/feed display), orderusecase.RescueOfferRepository (cart soft-check
// and checkout's hard check) and partnerusecase.RescueOfferRepository
// (create/list/cancel) — one adapter, several usecase-declared ports, same
// pattern as every other repository in this package (docs/PLAN.md 6.2).
type RescueOfferRepository struct {
	pool *pgxpool.Pool
}

func NewRescueOfferRepository(pool *pgxpool.Pool) *RescueOfferRepository {
	return &RescueOfferRepository{pool: pool}
}

var (
	_ catalogusecase.RescueOfferRepository = (*RescueOfferRepository)(nil)
	_ orderusecase.RescueOfferRepository   = (*RescueOfferRepository)(nil)
	_ partnerusecase.RescueOfferRepository = (*RescueOfferRepository)(nil)
)

// GetActiveForItems returns, for each of menuItemIDs with a currently
// usable offer (window valid, remaining_quantity > 0), that offer — keyed
// by menu_item_id. The window and exclusion constraint together guarantee
// at most one row per item can match at a given now, so the map is never
// ambiguous.
func (r *RescueOfferRepository) GetActiveForItems(ctx context.Context, menuItemIDs []uuid.UUID, now time.Time) (map[uuid.UUID]domaincatalog.RescueOffer, error) {
	if len(menuItemIDs) == 0 {
		return map[uuid.UUID]domaincatalog.RescueOffer{}, nil
	}

	q := QuerierFromContext(ctx, r.pool)
	rows, err := q.Query(ctx, `
		SELECT `+rescueOfferColumns+`
		FROM rescue_offers
		WHERE menu_item_id = ANY($1)
		  AND `+cancelledAtColumn+` IS NULL
		  AND remaining_quantity > 0
		  AND $2 >= starts_at AND $2 < ends_at
	`, menuItemIDs, now)
	if err != nil {
		return nil, fmt.Errorf("query active rescue offers: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]domaincatalog.RescueOffer, len(menuItemIDs))
	for rows.Next() {
		var o domaincatalog.RescueOffer
		if err := scanRescueOffer(rows, &o); err != nil {
			return nil, fmt.Errorf("scan rescue offer: %w", err)
		}
		result[o.MenuItemID] = o
	}
	return result, rows.Err()
}

// LockForCheckout locks the given rescue offers FOR UPDATE in ascending id
// order — same deadlock-avoidance argument as MenuRepository.LockForCheckout,
// just for this table; the checkout use-case always locks menu items first
// and this second, so the relative order across tables is fixed too. Unlike
// GetActiveForItems, this returns whatever state the row is in regardless
// of window/remaining — the caller needs to see an inactive offer too, to
// tell RESCUE_OFFER_EXPIRED apart from a normal partial-quantity split.
func (r *RescueOfferRepository) LockForCheckout(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domaincatalog.RescueOffer, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]domaincatalog.RescueOffer{}, nil
	}

	q := QuerierFromContext(ctx, r.pool)
	rows, err := q.Query(ctx, `SELECT `+rescueOfferColumns+` FROM rescue_offers WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, fmt.Errorf("lock rescue offers: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]domaincatalog.RescueOffer, len(ids))
	for rows.Next() {
		var o domaincatalog.RescueOffer
		if err := scanRescueOffer(rows, &o); err != nil {
			return nil, fmt.Errorf("scan locked rescue offer: %w", err)
		}
		result[o.ID] = o
	}
	return result, rows.Err()
}

// DecrementRemaining reduces remaining_quantity by quantity. Must run
// against a row already locked by LockForCheckout in the same transaction.
func (r *RescueOfferRepository) DecrementRemaining(ctx context.Context, id uuid.UUID, quantity int) error {
	q := QuerierFromContext(ctx, r.pool)
	if _, err := q.Exec(ctx, `
		UPDATE rescue_offers SET remaining_quantity = remaining_quantity - $2 WHERE id = $1
	`, id, quantity); err != nil {
		return fmt.Errorf("decrement rescue offer remaining quantity: %w", err)
	}
	return nil
}

// ListActiveFeed is GET /rescue's backing query (docs/PLAN.md 5.5): every
// currently redeemable offer across all venues, soonest-closing first,
// already filtered by all four of docs/PLAN.md 5.5's activity conditions —
// the offer's own window and remaining count, the item not being stopped
// or out of stock, and the venue being open and accepting orders — in one
// query, so pagination (docs/PLAN.md 6.6) stays correct.
func (r *RescueOfferRepository) ListActiveFeed(ctx context.Context, cursor string, limit int, now time.Time) (catalogusecase.RescueOfferFeedPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	var cur *rescueOfferCursor
	if cursor != "" {
		c, err := decodeRescueOfferCursor(cursor)
		if err != nil {
			return catalogusecase.RescueOfferFeedPage{}, err
		}
		cur = &c
	}

	conditions := []string{
		"ro." + cancelledAtColumn + " IS NULL",
		"ro.remaining_quantity > 0",
		"$1 >= ro.starts_at AND $1 < ro.ends_at",
		"mi.is_available",
		"(mi.stock_qty IS NULL OR mi.stock_qty > 0)",
		"v.accepting_orders",
		`EXISTS (
			SELECT 1 FROM venue_schedules vs
			WHERE vs.venue_id = v.id AND vs.weekday = $2 AND $3::time >= vs.opens_at AND $3::time < vs.closes_at
		)`,
	}
	args := []any{now, isoWeekday(now.Weekday()), now.Format("15:04:05")}

	if cur != nil {
		args = append(args, cur.EndsAt, cur.ID)
		conditions = append(conditions, fmt.Sprintf("(ro.ends_at, ro.id) > ($%d, $%d)", len(args)-1, len(args)))
	}

	args = append(args, limit+1)
	limitArg := len(args)

	query := fmt.Sprintf(`
		SELECT ro.id, ro.venue_id, ro.menu_item_id, ro.discount_percent, ro.initial_quantity,
		       ro.remaining_quantity, ro.starts_at, ro.ends_at, ro.`+cancelledAtColumn+`,
		       v.name, mi.name, mi.price_minor
		FROM rescue_offers ro
		JOIN menu_items mi ON mi.id = ro.menu_item_id
		JOIN venues v ON v.id = ro.venue_id
		WHERE %s
		ORDER BY ro.ends_at, ro.id
		LIMIT $%d
	`, strings.Join(conditions, " AND "), limitArg)

	q := QuerierFromContext(ctx, r.pool)
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return catalogusecase.RescueOfferFeedPage{}, fmt.Errorf("query rescue offer feed: %w", err)
	}
	defer rows.Close()

	var entries []catalogusecase.RescueOfferFeedEntry
	for rows.Next() {
		var e catalogusecase.RescueOfferFeedEntry
		if err := rows.Scan(&e.Offer.ID, &e.Offer.VenueID, &e.Offer.MenuItemID, &e.Offer.DiscountPercent,
			&e.Offer.InitialQuantity, &e.Offer.RemainingQuantity, &e.Offer.StartsAt, &e.Offer.EndsAt, &e.Offer.CancelledAt,
			&e.VenueName, &e.MenuItemName, &e.PriceBeforeMinor); err != nil {
			return catalogusecase.RescueOfferFeedPage{}, fmt.Errorf("scan rescue offer feed entry: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return catalogusecase.RescueOfferFeedPage{}, fmt.Errorf("iterate rescue offer feed: %w", err)
	}

	var nextCursor string
	if len(entries) > limit {
		last := entries[limit-1]
		nextCursor = encodeRescueOfferCursor(rescueOfferCursor{EndsAt: last.Offer.EndsAt, ID: last.Offer.ID})
		entries = entries[:limit]
	}
	return catalogusecase.RescueOfferFeedPage{Items: entries, NextCursor: nextCursor}, nil
}

// List returns venueID's rescue offers, newest first, optionally filtered
// to only currently-usable ones.
func (r *RescueOfferRepository) List(ctx context.Context, venueID uuid.UUID, activeOnly bool, now time.Time) ([]domaincatalog.RescueOffer, error) {
	q := QuerierFromContext(ctx, r.pool)

	query := `SELECT ` + rescueOfferColumns + ` FROM rescue_offers WHERE venue_id = $1`
	args := []any{venueID}
	if activeOnly {
		args = append(args, now)
		query += ` AND ` + cancelledAtColumn + ` IS NULL AND remaining_quantity > 0 AND $2 >= starts_at AND $2 < ends_at`
	}
	query += ` ORDER BY created_at DESC`

	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query rescue offers: %w", err)
	}
	defer rows.Close()

	var result []domaincatalog.RescueOffer
	for rows.Next() {
		var o domaincatalog.RescueOffer
		if err := scanRescueOffer(rows, &o); err != nil {
			return nil, fmt.Errorf("scan rescue offer: %w", err)
		}
		result = append(result, o)
	}
	return result, rows.Err()
}

// Create inserts o, translating the database's own exclusion-constraint
// rejection (docs/PLAN.md 5.5: "Пересечение акций запрещено на уровне базы")
// into errs.CodeRescueOfferOverlap. ON CONFLICT ON CONSTRAINT DO NOTHING
// plus checking whether a row came back is the same technique
// IdempotencyRepository.Claim uses for its own unique-index race — here the
// arbiter is an exclusion constraint instead of a unique index, which
// Postgres supports the identical syntax for.
func (r *RescueOfferRepository) Create(ctx context.Context, o domaincatalog.RescueOffer) (*domaincatalog.RescueOffer, error) {
	q := QuerierFromContext(ctx, r.pool)

	var created domaincatalog.RescueOffer
	err := scanRescueOffer(q.QueryRow(ctx, `
		INSERT INTO rescue_offers (id, venue_id, menu_item_id, discount_percent, initial_quantity, remaining_quantity, starts_at, ends_at)
		VALUES ($1, $2, $3, $4, $5, $5, $6, $7)
		ON CONFLICT ON CONSTRAINT rescue_offers_no_overlap DO NOTHING
		RETURNING `+rescueOfferColumns, o.ID, o.VenueID, o.MenuItemID, o.DiscountPercent, o.InitialQuantity, o.StartsAt, o.EndsAt), &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errs.New(errs.CodeRescueOfferOverlap, "this window overlaps an existing rescue offer on the same item")
	}
	if err != nil {
		return nil, fmt.Errorf("insert rescue offer: %w", err)
	}
	return &created, nil
}

// Cancel sets the offer's cancellation timestamp, returning
// errs.CodeNotFound if offerID does not exist, does not belong to venueID,
// or is already canceled — a second cancel of the same offer has nothing
// left to do, so it is reported the same as one that was never there.
func (r *RescueOfferRepository) Cancel(ctx context.Context, venueID, offerID uuid.UUID, now time.Time) error {
	q := QuerierFromContext(ctx, r.pool)
	tag, err := q.Exec(ctx, `
		UPDATE rescue_offers SET `+cancelledAtColumn+` = $3
		WHERE id = $1 AND venue_id = $2 AND `+cancelledAtColumn+` IS NULL
	`, offerID, venueID, now)
	if err != nil {
		return fmt.Errorf("cancel rescue offer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errs.New(errs.CodeNotFound, "rescue offer not found")
	}
	return nil
}

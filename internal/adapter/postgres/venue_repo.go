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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"

	catalogusecase "food-delivery-mvp/internal/usecase/catalog"
	orderusecase "food-delivery-mvp/internal/usecase/order"
	partnerusecase "food-delivery-mvp/internal/usecase/partner"
)

const defaultVenuePageLimit = 20

// VenueRepository implements catalogusecase.VenueRepository and
// orderusecase.VenueLookup on venues and venue_schedules — the same "one
// adapter, several usecase-declared ports" pattern MenuRepository uses,
// since both ports are ultimately reads (and one write) of the venues
// table.
type VenueRepository struct {
	pool *pgxpool.Pool
}

func NewVenueRepository(pool *pgxpool.Pool) *VenueRepository {
	return &VenueRepository{pool: pool}
}

var (
	_ catalogusecase.VenueRepository = (*VenueRepository)(nil)
	_ orderusecase.VenueLookup       = (*VenueRepository)(nil)
	_ partnerusecase.VenueRepository = (*VenueRepository)(nil)
)

// List returns a page of venues ordered by (name, id) — a stable tiebreak
// so keyset pagination never skips or repeats a row when two venues share
// a name. Every filter (cuisine, name prefix, "open now", cursor) is
// pushed into the WHERE clause: docs/PLAN.md 6.6 requires cursor pagination
// to stay correct, which is only possible if filtering happens in the same
// query as the LIMIT, not afterwards in Go.
func (r *VenueRepository) List(ctx context.Context, filter catalogusecase.ListVenuesFilter, now time.Time) (catalogusecase.VenuePage, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = defaultVenuePageLimit
	}

	var cur *venueCursor
	if filter.Cursor != "" {
		c, err := decodeVenueCursor(filter.Cursor)
		if err != nil {
			return catalogusecase.VenuePage{}, err
		}
		cur = &c
	}

	var conditions []string
	var args []any

	if filter.Cuisine != "" {
		args = append(args, filter.Cuisine)
		conditions = append(conditions, fmt.Sprintf("cuisine = $%d", len(args)))
	}
	if filter.NamePrefix != "" {
		args = append(args, filter.NamePrefix)
		conditions = append(conditions, fmt.Sprintf("lower(name) LIKE lower($%d) || '%%'", len(args)))
	}
	if filter.OnlyOpen {
		// Mirrors domaincatalog.Venue.EnsureCanOrder in SQL: this is a
		// filter predicate that must run in the database for pagination to
		// stay correct (see the function comment), so the same "open and
		// accepting orders" rule necessarily exists in two places. The
		// value actually shown to the client (Venue.isOpen) still comes
		// from the one Go implementation, via newVenueView — this SQL only
		// decides which rows qualify, never what is displayed.
		args = append(args, isoWeekday(now.Weekday()))
		weekdayArg := len(args)
		args = append(args, now.Format("15:04:05"))
		timeArg := len(args)
		conditions = append(conditions, fmt.Sprintf(`accepting_orders AND EXISTS (
			SELECT 1 FROM venue_schedules vs
			WHERE vs.venue_id = v.id AND vs.weekday = $%d AND $%d::time >= vs.opens_at AND $%d::time < vs.closes_at
		)`, weekdayArg, timeArg, timeArg))
	}
	if cur != nil {
		args = append(args, cur.Name)
		nameArg := len(args)
		args = append(args, cur.ID)
		idArg := len(args)
		conditions = append(conditions, fmt.Sprintf("(name, id) > ($%d, $%d)", nameArg, idArg))
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Fetch one extra row so its presence tells us whether there is a next
	// page, without a separate COUNT query.
	args = append(args, limit+1)
	limitArg := len(args)

	query := fmt.Sprintf(`
		SELECT id, partner_id, name, description, cuisine, min_order_amount_minor, accepting_orders, menu_version, webhook_url
		FROM venues v
		%s
		ORDER BY name, id
		LIMIT $%d
	`, where, limitArg)

	q := QuerierFromContext(ctx, r.pool)
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return catalogusecase.VenuePage{}, fmt.Errorf("query venues: %w", err)
	}
	defer rows.Close()

	var venues []domaincatalog.Venue
	for rows.Next() {
		var v domaincatalog.Venue
		if err := rows.Scan(&v.ID, &v.PartnerID, &v.Name, &v.Description, &v.Cuisine, &v.MinOrderAmountMinor, &v.AcceptingOrders, &v.MenuVersion, &v.WebhookURL); err != nil {
			return catalogusecase.VenuePage{}, fmt.Errorf("scan venue: %w", err)
		}
		venues = append(venues, v)
	}
	if err := rows.Err(); err != nil {
		return catalogusecase.VenuePage{}, fmt.Errorf("iterate venues: %w", err)
	}

	var nextCursor string
	if len(venues) > limit {
		last := venues[limit-1]
		nextCursor = encodeVenueCursor(venueCursor{Name: last.Name, ID: last.ID})
		venues = venues[:limit]
	}

	if err := r.attachSchedules(ctx, venues); err != nil {
		return catalogusecase.VenuePage{}, err
	}

	return catalogusecase.VenuePage{Items: venues, NextCursor: nextCursor}, nil
}

// GetByID returns nil, nil if id does not exist.
func (r *VenueRepository) GetByID(ctx context.Context, id uuid.UUID) (*domaincatalog.Venue, error) {
	q := QuerierFromContext(ctx, r.pool)

	var v domaincatalog.Venue
	err := q.QueryRow(ctx, `
		SELECT id, partner_id, name, description, cuisine, min_order_amount_minor, accepting_orders, menu_version, webhook_url
		FROM venues WHERE id = $1
	`, id).Scan(&v.ID, &v.PartnerID, &v.Name, &v.Description, &v.Cuisine, &v.MinOrderAmountMinor, &v.AcceptingOrders, &v.MenuVersion, &v.WebhookURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query venue: %w", err)
	}

	venues := []domaincatalog.Venue{v}
	if err := r.attachSchedules(ctx, venues); err != nil {
		return nil, err
	}
	return &venues[0], nil
}

// Get is GetByID under the name orderusecase.VenueLookup declares —
// checkout needs exactly the same venue-plus-schedule read catalog's own
// GetVenue use-case does, so there is nothing to implement separately.
func (r *VenueRepository) Get(ctx context.Context, id uuid.UUID) (*domaincatalog.Venue, error) {
	return r.GetByID(ctx, id)
}

// BumpMenuVersion increases venues.menu_version by one. Called by checkout
// whenever it actually changes stock (docs/PLAN.md 6.6).
func (r *VenueRepository) BumpMenuVersion(ctx context.Context, id uuid.UUID) error {
	q := QuerierFromContext(ctx, r.pool)
	tag, err := q.Exec(ctx, `UPDATE venues SET menu_version = menu_version + 1, updated_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("bump menu version: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errs.New(errs.CodeNotFound, "venue not found")
	}
	return nil
}

// UpdateProfile applies patch's non-nil fields, replacing the whole weekly
// schedule when patch.Schedule is set. Both the profile row and the
// schedule replace must land in the same transaction as each other — the
// caller (partner.VenueService via TxManager) is what makes that true, by
// wrapping this whole call so both statements share one QuerierFromContext
// transaction.
func (r *VenueRepository) UpdateProfile(ctx context.Context, id uuid.UUID, patch partnerusecase.VenueProfilePatch) error {
	q := QuerierFromContext(ctx, r.pool)

	tag, err := q.Exec(ctx, `
		UPDATE venues SET
			description = COALESCE($2, description),
			accepting_orders = COALESCE($3, accepting_orders),
			min_order_amount_minor = COALESCE($4, min_order_amount_minor),
			webhook_url = COALESCE($5, webhook_url),
			webhook_secret = COALESCE($6, webhook_secret),
			updated_at = now()
		WHERE id = $1
	`, id, patch.Description, patch.AcceptingOrders, patch.MinOrderAmountMinor, patch.WebhookURL, patch.WebhookSecret)
	if err != nil {
		return fmt.Errorf("update venue profile: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errs.New(errs.CodeNotFound, "venue not found")
	}

	if patch.Schedule != nil {
		if _, err := q.Exec(ctx, `DELETE FROM venue_schedules WHERE venue_id = $1`, id); err != nil {
			return fmt.Errorf("clear venue schedule: %w", err)
		}
		for _, e := range *patch.Schedule {
			opens := pgtype.Time{Microseconds: int64(e.OpensAt / time.Microsecond), Valid: true}
			closes := pgtype.Time{Microseconds: int64(e.ClosesAt / time.Microsecond), Valid: true}
			if _, err := q.Exec(ctx, `
				INSERT INTO venue_schedules (id, venue_id, weekday, opens_at, closes_at)
				VALUES ($1, $2, $3, $4, $5)
			`, uuid.New(), id, isoWeekday(e.Weekday), opens, closes); err != nil {
				return fmt.Errorf("insert venue schedule: %w", err)
			}
		}
	}
	return nil
}

// attachSchedules loads every schedule row for venues in a single query
// (WHERE venue_id = ANY($1)) and appends each to its venue. docs/PLAN.md 6.6
// forbids a listing from turning into one query per row; this is how List
// and GetByID both honor that.
func (r *VenueRepository) attachSchedules(ctx context.Context, venues []domaincatalog.Venue) error {
	if len(venues) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(venues))
	index := make(map[uuid.UUID]int, len(venues))
	for i, v := range venues {
		ids[i] = v.ID
		index[v.ID] = i
	}

	q := QuerierFromContext(ctx, r.pool)
	rows, err := q.Query(ctx, `
		SELECT venue_id, weekday, opens_at, closes_at
		FROM venue_schedules
		WHERE venue_id = ANY($1)
	`, ids)
	if err != nil {
		return fmt.Errorf("query venue schedules: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var venueID uuid.UUID
		var weekday int
		var opens, closes pgtype.Time
		if err := rows.Scan(&venueID, &weekday, &opens, &closes); err != nil {
			return fmt.Errorf("scan venue schedule: %w", err)
		}
		i, ok := index[venueID]
		if !ok {
			continue
		}
		venues[i].Schedule = append(venues[i].Schedule, domaincatalog.ScheduleEntry{
			Weekday:  goWeekday(weekday),
			OpensAt:  time.Duration(opens.Microseconds) * time.Microsecond,
			ClosesAt: time.Duration(closes.Microseconds) * time.Microsecond,
		})
	}
	return rows.Err()
}

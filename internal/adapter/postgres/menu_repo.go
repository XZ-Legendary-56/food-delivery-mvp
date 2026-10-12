package postgres

import (
	"context"
	"errors"
	"fmt"
	"food-delivery-mvp/internal/domain/errs"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"

	catalogusecase "food-delivery-mvp/internal/usecase/catalog"
	orderusecase "food-delivery-mvp/internal/usecase/order"
	partnerusecase "food-delivery-mvp/internal/usecase/partner"
)

// menuItemColumns is every column shared by menu_items reads in this file,
// so the SELECT list and its Scan targets never drift apart from each
// other across the four read methods below.
const menuItemColumns = "id, venue_id, category_id, name, description, price_minor, is_available, stock_qty, cooking_time_minutes, source, external_id"

func scanMenuItem(row interface {
	Scan(dest ...any) error
}, mi *domaincatalog.MenuItem,
) error {
	return row.Scan(&mi.ID, &mi.VenueID, &mi.CategoryID, &mi.Name, &mi.Description, &mi.PriceMinor,
		&mi.IsAvailable, &mi.StockQty, &mi.CookingTimeMinutes, &mi.Source, &mi.ExternalID)
}

// MenuRepository implements catalogusecase.MenuRepository (menu structure
// and versioning), orderusecase.MenuItemLookup (single-item lookups for the
// cart) and partnerusecase.MenuRepository (partner menu management). All
// three ports read/write the same menu_items/menu_categories tables — one
// repository backing several usecase-declared interfaces is exactly the
// point of docs/PLAN.md 6.2's rule: each module owns the shape of the port it
// needs, and an adapter is free to satisfy several of them at once.
type MenuRepository struct {
	pool *pgxpool.Pool
}

func NewMenuRepository(pool *pgxpool.Pool) *MenuRepository {
	return &MenuRepository{pool: pool}
}

var (
	_ catalogusecase.MenuRepository = (*MenuRepository)(nil)
	_ orderusecase.MenuItemLookup   = (*MenuRepository)(nil)
	_ partnerusecase.MenuRepository = (*MenuRepository)(nil)
)

// GetMenuVersion is the cheap half of docs/PLAN.md 6.6's ETag fast path: one
// primary-key lookup, no join, so a 304 costs almost nothing.
func (r *MenuRepository) GetMenuVersion(ctx context.Context, venueID uuid.UUID) (int64, error) {
	q := QuerierFromContext(ctx, r.pool)

	var version int64
	err := q.QueryRow(ctx, `SELECT menu_version FROM venues WHERE id = $1`, venueID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errs.New(errs.CodeNotFound, "venue not found")
	}
	if err != nil {
		return 0, fmt.Errorf("query menu version: %w", err)
	}
	return version, nil
}

// GetMenu assembles the full menu in three fixed queries — venue, then all
// its categories, then all its items — never one query per category or
// item (docs/PLAN.md 6.6).
func (r *MenuRepository) GetMenu(ctx context.Context, venueID uuid.UUID) (catalogusecase.Menu, error) {
	q := QuerierFromContext(ctx, r.pool)

	var version int64
	err := q.QueryRow(ctx, `SELECT menu_version FROM venues WHERE id = $1`, venueID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return catalogusecase.Menu{}, errs.New(errs.CodeNotFound, "venue not found")
	}
	if err != nil {
		return catalogusecase.Menu{}, fmt.Errorf("query venue: %w", err)
	}

	categories, order, err := r.categoriesForVenue(ctx, venueID)
	if err != nil {
		return catalogusecase.Menu{}, err
	}

	itemRows, err := q.Query(ctx, `SELECT `+menuItemColumns+` FROM menu_items WHERE venue_id = $1 ORDER BY name`, venueID)
	if err != nil {
		return catalogusecase.Menu{}, fmt.Errorf("query menu items: %w", err)
	}
	for itemRows.Next() {
		var mi domaincatalog.MenuItem
		if err := scanMenuItem(itemRows, &mi); err != nil {
			itemRows.Close()
			return catalogusecase.Menu{}, fmt.Errorf("scan menu item: %w", err)
		}
		if cat, ok := categories[mi.CategoryID]; ok {
			cat.Items = append(cat.Items, mi)
		}
	}
	itemRows.Close()
	if err := itemRows.Err(); err != nil {
		return catalogusecase.Menu{}, fmt.Errorf("iterate menu items: %w", err)
	}

	menu := catalogusecase.Menu{VenueID: venueID, MenuVersion: version}
	for _, id := range order {
		menu.Categories = append(menu.Categories, *categories[id])
	}
	return menu, nil
}

func (r *MenuRepository) categoriesForVenue(ctx context.Context, venueID uuid.UUID) (map[uuid.UUID]*catalogusecase.MenuCategory, []uuid.UUID, error) {
	q := QuerierFromContext(ctx, r.pool)
	catRows, err := q.Query(ctx, `
		SELECT id, name, position
		FROM menu_categories
		WHERE venue_id = $1
		ORDER BY position, name
	`, venueID)
	if err != nil {
		return nil, nil, fmt.Errorf("query menu categories: %w", err)
	}
	defer catRows.Close()

	categories := make(map[uuid.UUID]*catalogusecase.MenuCategory)
	var order []uuid.UUID
	for catRows.Next() {
		var c catalogusecase.MenuCategory
		if err := catRows.Scan(&c.ID, &c.Name, &c.Position); err != nil {
			return nil, nil, fmt.Errorf("scan menu category: %w", err)
		}
		categories[c.ID] = &c
		order = append(order, c.ID)
	}
	return categories, order, catRows.Err()
}

// Get returns nil, nil if id does not exist.
func (r *MenuRepository) Get(ctx context.Context, id uuid.UUID) (*domaincatalog.MenuItem, error) {
	q := QuerierFromContext(ctx, r.pool)

	var mi domaincatalog.MenuItem
	err := scanMenuItem(q.QueryRow(ctx, `SELECT `+menuItemColumns+` FROM menu_items WHERE id = $1`, id), &mi)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query menu item: %w", err)
	}
	return &mi, nil
}

// GetMany batches a lookup for several ids in one query.
func (r *MenuRepository) GetMany(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domaincatalog.MenuItem, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]domaincatalog.MenuItem{}, nil
	}

	q := QuerierFromContext(ctx, r.pool)
	rows, err := q.Query(ctx, `SELECT `+menuItemColumns+` FROM menu_items WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("query menu items: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]domaincatalog.MenuItem, len(ids))
	for rows.Next() {
		var mi domaincatalog.MenuItem
		if err := scanMenuItem(rows, &mi); err != nil {
			return nil, fmt.Errorf("scan menu item: %w", err)
		}
		result[mi.ID] = mi
	}
	return result, rows.Err()
}

// LockForCheckout locks menu_items rows FOR UPDATE, scanning them in
// ascending id order via ORDER BY. Postgres acquires FOR UPDATE locks in
// the order rows are produced by the query, so two concurrent checkouts
// that share items — however their carts happened to list them — always
// request their locks in the same relative order as each other. One
// blocks and waits for the other; neither can end up waiting on a lock the
// other is itself waiting on, which is what a deadlock would require
// (docs/PLAN.md 9). Rows for ids that do not exist are simply absent from the
// result, same as Get/GetMany.
func (r *MenuRepository) LockForCheckout(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domaincatalog.MenuItem, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]domaincatalog.MenuItem{}, nil
	}

	q := QuerierFromContext(ctx, r.pool)
	rows, err := q.Query(ctx, `SELECT `+menuItemColumns+` FROM menu_items WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, fmt.Errorf("lock menu items: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]domaincatalog.MenuItem, len(ids))
	for rows.Next() {
		var mi domaincatalog.MenuItem
		if err := scanMenuItem(rows, &mi); err != nil {
			return nil, fmt.Errorf("scan locked menu item: %w", err)
		}
		result[mi.ID] = mi
	}
	return result, rows.Err()
}

// DecrementStock reduces a finite-stock item's stock_qty by quantity.
// "AND stock_qty IS NOT NULL" makes an unlimited-stock item a no-op by
// construction — there is no separate Go branch that has to remember to
// skip it. This must run against a row already locked by LockForCheckout
// in the same transaction: on its own, "read stock, check it, then write
// stock" is a classic race, and the lock is what turns it into a single
// atomic step from every other transaction's point of view.
func (r *MenuRepository) DecrementStock(ctx context.Context, id uuid.UUID, quantity int) (bool, error) {
	q := QuerierFromContext(ctx, r.pool)
	tag, err := q.Exec(ctx, `
		UPDATE menu_items
		SET stock_qty = stock_qty - $2, updated_at = now()
		WHERE id = $1 AND stock_qty IS NOT NULL
	`, id, quantity)
	if err != nil {
		return false, fmt.Errorf("decrement stock: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// GetFullMenu is partner's own read of the menu — same rows as GetMenu,
// wrapped in the partner package's own Menu/MenuCategory shape (a small,
// deliberate duplicate of catalogusecase.Menu/MenuCategory — see
// partnerusecase.Menu's doc comment for why partner cannot just import
// usecase/catalog to reuse it).
func (r *MenuRepository) GetFullMenu(ctx context.Context, venueID uuid.UUID) (partnerusecase.Menu, error) {
	q := QuerierFromContext(ctx, r.pool)

	var version int64
	err := q.QueryRow(ctx, `SELECT menu_version FROM venues WHERE id = $1`, venueID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return partnerusecase.Menu{}, errs.New(errs.CodeNotFound, "venue not found")
	}
	if err != nil {
		return partnerusecase.Menu{}, fmt.Errorf("query venue: %w", err)
	}

	catRows, err := q.Query(ctx, `SELECT id, name, position FROM menu_categories WHERE venue_id = $1 ORDER BY position, name`, venueID)
	if err != nil {
		return partnerusecase.Menu{}, fmt.Errorf("query menu categories: %w", err)
	}
	categories := make(map[uuid.UUID]*partnerusecase.MenuCategory)
	var order []uuid.UUID
	for catRows.Next() {
		var c partnerusecase.MenuCategory
		if err := catRows.Scan(&c.ID, &c.Name, &c.Position); err != nil {
			catRows.Close()
			return partnerusecase.Menu{}, fmt.Errorf("scan menu category: %w", err)
		}
		categories[c.ID] = &c
		order = append(order, c.ID)
	}
	catRows.Close()
	if err := catRows.Err(); err != nil {
		return partnerusecase.Menu{}, fmt.Errorf("iterate menu categories: %w", err)
	}

	itemRows, err := q.Query(ctx, `SELECT `+menuItemColumns+` FROM menu_items WHERE venue_id = $1 ORDER BY name`, venueID)
	if err != nil {
		return partnerusecase.Menu{}, fmt.Errorf("query menu items: %w", err)
	}
	for itemRows.Next() {
		var mi domaincatalog.MenuItem
		if err := scanMenuItem(itemRows, &mi); err != nil {
			itemRows.Close()
			return partnerusecase.Menu{}, fmt.Errorf("scan menu item: %w", err)
		}
		if cat, ok := categories[mi.CategoryID]; ok {
			cat.Items = append(cat.Items, mi)
		}
	}
	itemRows.Close()
	if err := itemRows.Err(); err != nil {
		return partnerusecase.Menu{}, fmt.Errorf("iterate menu items: %w", err)
	}

	menu := partnerusecase.Menu{MenuVersion: version}
	for _, id := range order {
		menu.Categories = append(menu.Categories, *categories[id])
	}
	return menu, nil
}

// Sync upserts categories (matched by (venue_id, name)) and items (matched
// by (venue_id, external_id)) from a full partner menu sync, then bumps
// menu_version once for the whole call. Categories/items from a previous
// sync that are absent here are left untouched, per PUT /menu's own
// contract — this only ever inserts or updates rows it was told about.
func (r *MenuRepository) Sync(ctx context.Context, venueID uuid.UUID, categories []partnerusecase.MenuSyncCategory) error {
	q := QuerierFromContext(ctx, r.pool)

	for _, cat := range categories {
		var categoryID uuid.UUID
		err := q.QueryRow(ctx, `
			INSERT INTO menu_categories (id, venue_id, name, position)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (venue_id, name) DO UPDATE SET position = EXCLUDED.position
			RETURNING id
		`, uuid.New(), venueID, cat.Name, cat.Position).Scan(&categoryID)
		if err != nil {
			return fmt.Errorf("upsert menu category %q: %w", cat.Name, err)
		}

		for _, item := range cat.Items {
			if _, err := q.Exec(ctx, `
				INSERT INTO menu_items (
					id, venue_id, category_id, name, description, price_minor,
					is_available, stock_qty, cooking_time_minutes, source, external_id
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'integration', $10)
				ON CONFLICT (venue_id, external_id) WHERE external_id IS NOT NULL DO UPDATE SET
					category_id = EXCLUDED.category_id,
					name = EXCLUDED.name,
					description = EXCLUDED.description,
					price_minor = EXCLUDED.price_minor,
					is_available = EXCLUDED.is_available,
					stock_qty = EXCLUDED.stock_qty,
					cooking_time_minutes = EXCLUDED.cooking_time_minutes,
					updated_at = now()
			`, uuid.New(), venueID, categoryID, item.Name, item.Description, item.PriceMinor,
				item.IsAvailable, item.StockQty, item.CookingTimeMinutes, item.ExternalID); err != nil {
				return fmt.Errorf("upsert menu item %q: %w", item.ExternalID, err)
			}
		}
	}

	if err := r.bumpMenuVersion(ctx, venueID); err != nil {
		return err
	}
	return nil
}

// CreateItem adds one item to an existing category, failing with
// errs.CodeNotFound if categoryID does not belong to venueID.
func (r *MenuRepository) CreateItem(ctx context.Context, venueID uuid.UUID, item partnerusecase.NewMenuItem) (*domaincatalog.MenuItem, error) {
	q := QuerierFromContext(ctx, r.pool)

	var categoryExists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM menu_categories WHERE id = $1 AND venue_id = $2)`,
		item.CategoryID, venueID).Scan(&categoryExists); err != nil {
		return nil, fmt.Errorf("check menu category: %w", err)
	}
	if !categoryExists {
		return nil, errs.New(errs.CodeNotFound, "menu category not found")
	}

	source := "platform"
	if item.ExternalID != nil {
		source = "integration"
	}

	id := uuid.New()
	if _, err := q.Exec(ctx, `
		INSERT INTO menu_items (
			id, venue_id, category_id, name, description, price_minor,
			is_available, stock_qty, cooking_time_minutes, source, external_id
		) VALUES ($1, $2, $3, $4, $5, $6, true, $7, $8, $9, $10)
	`, id, venueID, item.CategoryID, item.Name, item.Description, item.PriceMinor,
		item.StockQty, item.CookingTimeMinutes, source, item.ExternalID); err != nil {
		return nil, fmt.Errorf("insert menu item: %w", err)
	}
	if err := r.bumpMenuVersion(ctx, venueID); err != nil {
		return nil, err
	}
	return r.getOwned(ctx, venueID, id)
}

// UpdateItem changes an item's structural fields, failing with
// errs.CodeNotFound if itemID (or patch.CategoryID, when set) does not
// belong to venueID.
func (r *MenuRepository) UpdateItem(ctx context.Context, venueID, itemID uuid.UUID, patch partnerusecase.MenuItemPatch) (*domaincatalog.MenuItem, error) {
	q := QuerierFromContext(ctx, r.pool)

	if patch.CategoryID != nil {
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM menu_categories WHERE id = $1 AND venue_id = $2)`,
			*patch.CategoryID, venueID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("check menu category: %w", err)
		}
		if !exists {
			return nil, errs.New(errs.CodeNotFound, "menu category not found")
		}
	}

	tag, err := q.Exec(ctx, `
		UPDATE menu_items SET
			category_id = COALESCE($3, category_id),
			name = COALESCE($4, name),
			description = COALESCE($5, description),
			price_minor = COALESCE($6, price_minor),
			cooking_time_minutes = COALESCE($7, cooking_time_minutes),
			updated_at = now()
		WHERE id = $1 AND venue_id = $2
	`, itemID, venueID, patch.CategoryID, patch.Name, patch.Description, patch.PriceMinor, patch.CookingTimeMinutes)
	if err != nil {
		return nil, fmt.Errorf("update menu item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, errs.New(errs.CodeNotFound, "menu item not found")
	}
	if err := r.bumpMenuVersion(ctx, venueID); err != nil {
		return nil, err
	}
	return r.getOwned(ctx, venueID, itemID)
}

// UpdateAvailability bulk-updates the stop-list flag and/or stock for
// several items in one call, skipping ids that do not belong to venueID —
// the use-case reports which ones those were.
func (r *MenuRepository) UpdateAvailability(ctx context.Context, venueID uuid.UUID, updates []partnerusecase.AvailabilityUpdate) ([]domaincatalog.MenuItem, error) {
	q := QuerierFromContext(ctx, r.pool)

	var result []domaincatalog.MenuItem
	changed := false
	for _, u := range updates {
		tag, err := q.Exec(ctx, `
			UPDATE menu_items SET
				is_available = COALESCE($3, is_available),
				stock_qty = CASE WHEN $4 THEN $5 ELSE stock_qty END,
				updated_at = now()
			WHERE id = $1 AND venue_id = $2
		`, u.MenuItemID, venueID, u.IsAvailable, u.StockQtySet, u.StockQty)
		if err != nil {
			return nil, fmt.Errorf("update availability for %s: %w", u.MenuItemID, err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		changed = true
		mi, err := r.getOwned(ctx, venueID, u.MenuItemID)
		if err != nil {
			return nil, err
		}
		result = append(result, *mi)
	}

	if changed {
		if err := r.bumpMenuVersion(ctx, venueID); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (r *MenuRepository) getOwned(ctx context.Context, venueID, itemID uuid.UUID) (*domaincatalog.MenuItem, error) {
	q := QuerierFromContext(ctx, r.pool)
	var mi domaincatalog.MenuItem
	err := scanMenuItem(q.QueryRow(ctx, `SELECT `+menuItemColumns+` FROM menu_items WHERE id = $1 AND venue_id = $2`, itemID, venueID), &mi)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errs.New(errs.CodeNotFound, "menu item not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query menu item: %w", err)
	}
	return &mi, nil
}

func (r *MenuRepository) bumpMenuVersion(ctx context.Context, venueID uuid.UUID) error {
	q := QuerierFromContext(ctx, r.pool)
	if _, err := q.Exec(ctx, `UPDATE venues SET menu_version = menu_version + 1, updated_at = now() WHERE id = $1`, venueID); err != nil {
		return fmt.Errorf("bump menu version: %w", err)
	}
	return nil
}

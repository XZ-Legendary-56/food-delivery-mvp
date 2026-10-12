// Package catalog holds the catalog use-cases (venue listing/detail, menu)
// and the repository ports they declare for themselves (docs/PLAN.md 6.2:
// usecase depends on domain and on the interfaces it declares, never the
// other way round).
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"
)

// ListVenuesFilter is ListVenues' input. Empty/zero fields mean "no filter".
type ListVenuesFilter struct {
	Cuisine    string
	NamePrefix string
	OnlyOpen   bool // schedule open AND accepting orders — see VenueRepository.List
	Cursor     string
	Limit      int
}

// VenuePage is one page of venues plus an opaque cursor for the next one.
// NextCursor is "" when there is no next page.
type VenuePage struct {
	Items      []domaincatalog.Venue
	NextCursor string
}

// VenueRepository is the port the ListVenues/GetVenue use-cases need from
// storage. Cursor encoding is the repository's own business (it is the one
// that picks the keyset ordering), so callers only ever pass the opaque
// string back and forth.
type VenueRepository interface {
	// List returns a page of venues matching filter. now drives the
	// OnlyOpen filter — evaluated once by the caller so a single request
	// filters and displays against one consistent instant, not "now" read
	// twice with a gap in between. Rows already carry their full Schedule,
	// batch-loaded so a page never fans out into one query per venue
	// (docs/PLAN.md 6.6).
	List(ctx context.Context, filter ListVenuesFilter, now time.Time) (VenuePage, error)
	// GetByID returns nil, nil if no venue has this id.
	GetByID(ctx context.Context, id uuid.UUID) (*domaincatalog.Venue, error)
}

// MenuCategory is one category of a venue's menu, with its items.
type MenuCategory struct {
	ID       uuid.UUID
	Name     string
	Position int
	Items    []domaincatalog.MenuItem
}

// Menu is a venue's full menu, structure plus live availability, per
// docs/PLAN.md 6.6 — MenuVersion is what the ETag is built from.
type Menu struct {
	VenueID     uuid.UUID
	MenuVersion int64
	Categories  []MenuCategory
}

// MenuRepository is the port GetMenu needs from storage. GetMenuVersion and
// GetMenu are deliberately separate methods, not one call with a flag: the
// ETag fast path (docs/PLAN.md 6.6) must cost one primary-key lookup, not the
// full menu assembly, and splitting them is what makes that possible.
type MenuRepository interface {
	// GetMenuVersion returns errs.CodeNotFound if venueID does not exist.
	GetMenuVersion(ctx context.Context, venueID uuid.UUID) (int64, error)
	// GetMenu returns errs.CodeNotFound if venueID does not exist.
	GetMenu(ctx context.Context, venueID uuid.UUID) (Menu, error)
}

// RescueOfferFeedEntry is one row of GET /rescue's cross-venue feed
// (docs/PLAN.md 5.5) — a rescue offer plus enough about its venue and item to
// show a card without a follow-up request per row.
type RescueOfferFeedEntry struct {
	Offer        domaincatalog.RescueOffer
	VenueName    string
	MenuItemName string
	// PriceBefore is the item's plain price at read time — the discount is
	// derived from it via Offer.DiscountedPrice, not stored separately.
	PriceBeforeMinor int64
}

// RescueOfferFeedPage is ListActiveFeed's output — same cursor-pagination
// shape as VenuePage, one page plus an opaque cursor for the next.
type RescueOfferFeedPage struct {
	Items      []RescueOfferFeedEntry
	NextCursor string
}

// RescueOfferRepository is the catalog use-case's own view of rescue offers
// (docs/PLAN.md 5.5): enough to attach an active offer to a menu item and to
// serve the standalone cross-venue feed.
type RescueOfferRepository interface {
	// GetActiveForItems returns, for each of menuItemIDs that currently has
	// an active offer (window valid and remaining_quantity > 0), that
	// offer — keyed by menu_item_id. Ids with none are simply absent.
	// Never one query per item (docs/PLAN.md 6.6).
	GetActiveForItems(ctx context.Context, menuItemIDs []uuid.UUID, now time.Time) (map[uuid.UUID]domaincatalog.RescueOffer, error)

	// ListActiveFeed returns a page of active offers across every venue,
	// soonest-closing first (docs/PLAN.md 5.5), already filtered down to
	// offers a customer could actually redeem right now: the item itself
	// is not stopped or out of stock, and the venue is open and accepting
	// orders — the other two of docs/PLAN.md 5.5's four activity conditions,
	// pushed into this query for the same reason VenueRepository.List
	// pushes its OnlyOpen filter into SQL rather than filtering in Go
	// (docs/PLAN.md 6.6: filtering after the LIMIT would corrupt pagination).
	ListActiveFeed(ctx context.Context, cursor string, limit int, now time.Time) (RescueOfferFeedPage, error)
}

// Package partner holds the partner-facing use-cases (docs/PLAN.md 5.3: venue
// profile, menu management, availability, order fulfillment) and the ports
// they declare for themselves — same rule as every other usecase package
// (docs/PLAN.md 6.2): depends on domain and on interfaces it owns, never on
// another module's usecase package.
package partner

import (
	"context"
	"time"

	"github.com/google/uuid"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"
	domainorder "food-delivery-mvp/internal/domain/order"
)

// APIKeyRepository resolves the venue behind a partner API key. This
// project's own assumption (README "Допущения и упрощения"): one partner
// owns exactly one venue, so a key hash resolves to a venue directly with
// no ambiguity about which of a partner's venues it means — see
// adapter/postgres's implementation for the query this backs.
type APIKeyRepository interface {
	// ResolveVenueByKeyHash returns errs.CodeUnauthorized if no
	// non-revoked key has this hash.
	ResolveVenueByKeyHash(ctx context.Context, keyHash string) (uuid.UUID, error)
}

// VenueProfilePatch is PATCH /venue's optional fields — nil means "leave
// this alone". WebhookSecret is set by VenueService (not the caller)
// whenever WebhookURL changes, since generating it is a security-relevant
// decision the use-case owns, not the repository.
type VenueProfilePatch struct {
	Description         *string
	AcceptingOrders     *bool
	MinOrderAmountMinor *int64
	WebhookURL          *string
	WebhookSecret       *string
	Schedule            *[]domaincatalog.ScheduleEntry
}

// VenueRepository is partner's own view of a venue: enough to show and
// update its profile. adapter/postgres's VenueRepository satisfies this
// alongside catalogusecase.VenueRepository and orderusecase.VenueLookup —
// one adapter type, several usecase-declared ports (docs/PLAN.md 6.2).
type VenueRepository interface {
	// Get returns nil, nil if id does not exist.
	Get(ctx context.Context, id uuid.UUID) (*domaincatalog.Venue, error)
	UpdateProfile(ctx context.Context, id uuid.UUID, patch VenueProfilePatch) error
}

// MenuCategory and Menu are partner's own read shape for GET
// implied by PUT /menu's response — a small, deliberate duplicate of
// catalogusecase.Menu/MenuCategory rather than an import of usecase/catalog
// (docs/PLAN.md 6.2: partner must declare its own ports/types, not depend on
// another module's usecase layer).
type MenuCategory struct {
	ID       uuid.UUID
	Name     string
	Position int
	Items    []domaincatalog.MenuItem
}

type Menu struct {
	MenuVersion int64
	Categories  []MenuCategory
}

// MenuSyncItem and MenuSyncCategory are PUT /menu's input shape.
type MenuSyncItem struct {
	ExternalID         string
	Name               string
	Description        string
	PriceMinor         int64
	CookingTimeMinutes int
	IsAvailable        bool
	StockQty           *int
}

type MenuSyncCategory struct {
	Name     string
	Position int
	Items    []MenuSyncItem
}

// NewMenuItem is POST /menu/items's input.
type NewMenuItem struct {
	CategoryID         uuid.UUID
	Name               string
	Description        string
	PriceMinor         int64
	CookingTimeMinutes int
	StockQty           *int
	ExternalID         *string
}

// MenuItemPatch is PATCH /menu/items/{id}'s input — nil means unchanged.
type MenuItemPatch struct {
	CategoryID         *uuid.UUID
	Name               *string
	Description        *string
	PriceMinor         *int64
	CookingTimeMinutes *int
}

// AvailabilityUpdate is one line of POST /menu/availability's input.
// StockQtySet distinguishes "clear stock to unlimited" (StockQtySet=true,
// StockQty=nil) from "leave stock alone" (StockQtySet=false) — a plain
// *int alone cannot tell those apart.
type AvailabilityUpdate struct {
	MenuItemID  uuid.UUID
	IsAvailable *bool
	StockQtySet bool
	StockQty    *int
}

// MenuRepository is partner's menu management surface. adapter/postgres's
// MenuRepository satisfies this alongside catalogusecase.MenuRepository and
// orderusecase.MenuItemLookup.
type MenuRepository interface {
	GetFullMenu(ctx context.Context, venueID uuid.UUID) (Menu, error)
	// Sync upserts categories (by name) and items (by external_id) scoped
	// to venueID, then bumps menu_version once.
	Sync(ctx context.Context, venueID uuid.UUID, categories []MenuSyncCategory) error
	// CreateItem returns errs.CodeNotFound if item.CategoryID does not
	// belong to venueID.
	CreateItem(ctx context.Context, venueID uuid.UUID, item NewMenuItem) (*domaincatalog.MenuItem, error)
	// UpdateItem returns errs.CodeNotFound if itemID, or patch.CategoryID
	// when set, does not belong to venueID.
	UpdateItem(ctx context.Context, venueID, itemID uuid.UUID, patch MenuItemPatch) (*domaincatalog.MenuItem, error)
	// UpdateAvailability applies every update whose MenuItemID belongs to
	// venueID and returns those items' new state; ids that do not belong
	// to venueID are silently skipped, and the use-case reports them.
	UpdateAvailability(ctx context.Context, venueID uuid.UUID, updates []AvailabilityUpdate) ([]domaincatalog.MenuItem, error)
}

// RescueOfferRepository is partner's own management surface for rescue
// offers (docs/PLAN.md 5.3 item 9 / 5.5): create, list, cancel — scoped to one
// venue, same as every other partner-facing repository.
type RescueOfferRepository interface {
	// List returns venueID's rescue offers. activeOnly, when true, filters
	// to offers whose window and remaining count are currently usable
	// (RescueOffer.IsActive) — pushed into the query rather than filtered
	// in Go for the same docs/PLAN.md 6.6 reason every other listing is.
	List(ctx context.Context, venueID uuid.UUID, activeOnly bool, now time.Time) ([]domaincatalog.RescueOffer, error)
	// Create inserts a new offer scoped to venueID. A window that overlaps
	// an existing live offer on the same menu item fails with
	// errs.CodeRescueOfferOverlap — the database's own exclusion
	// constraint is what actually guarantees this even under concurrent
	// requests (docs/PLAN.md 5.5), so this method's only job is translating
	// that constraint violation into the domain error.
	Create(ctx context.Context, o domaincatalog.RescueOffer) (*domaincatalog.RescueOffer, error)
	// Cancel sets canceled_at on the offer, returning errs.CodeNotFound if
	// offerID does not exist or does not belong to venueID.
	Cancel(ctx context.Context, venueID, offerID uuid.UUID, now time.Time) error
}

// OrderRepository is partner's own order-fulfillment surface.
type OrderRepository interface {
	// ListForVenue returns venueID's orders, newest first. status and
	// since are optional filters; a nil status means "any status".
	ListForVenue(ctx context.Context, venueID uuid.UUID, status *domainorder.Status, since *time.Time, limit int) ([]domainorder.Order, error)
	// Get returns nil, nil if id does not exist.
	Get(ctx context.Context, id uuid.UUID) (*domainorder.Order, error)
	// ApplyTransition persists o's current Status/ETAMinutes/
	// RejectionReason/ExternalOrderID/UpdatedAt and appends
	// o.History[len(o.History)-1] as the new order_status_history row.
	// Call it right after a domainorder.Order method (TransitionTo/
	// Accept-shaped call/Reject) has already applied the change in memory.
	ApplyTransition(ctx context.Context, o *domainorder.Order) error
}

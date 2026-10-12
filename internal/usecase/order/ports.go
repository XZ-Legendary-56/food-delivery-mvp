// Package order holds the cart use-cases (docs/PLAN.md 6.3: Cart lives in the
// "order" domain module, alongside Order itself) and the ports they declare
// for themselves.
package order

import (
	"context"
	"time"

	"github.com/google/uuid"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"
	domainorder "food-delivery-mvp/internal/domain/order"
)

// MenuItemLookup is the cart use-case's own view of the catalog: just
// enough to price-check and availability-check a cart line. It returns
// domain/catalog's own MenuItem type (allowed — usecase may depend on any
// domain package) rather than importing usecase/catalog, so this package
// never depends on another module's use-case layer, only on domain
// (docs/PLAN.md 6.2). adapter/postgres satisfies this with the same
// repository that backs catalog's own MenuRepository.
type MenuItemLookup interface {
	// Get returns nil, nil if id does not exist.
	Get(ctx context.Context, id uuid.UUID) (*domaincatalog.MenuItem, error)
	// GetMany batches a lookup for several ids in one query (docs/PLAN.md
	// 6.6: no per-line query when assembling a cart's live availability).
	// Ids that do not exist are simply absent from the result map.
	GetMany(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domaincatalog.MenuItem, error)

	// LockForCheckout is checkout's hard availability/price check
	// (docs/PLAN.md 5.2): it locks the given menu items FOR UPDATE and
	// returns their state as of that lock, always acquiring the locks in
	// ascending id order regardless of the order ids is given in
	// (docs/PLAN.md 9 — this is what keeps two concurrent checkouts that
	// share items from deadlocking on each other). Must only be called
	// inside a CheckoutService.txManager.WithinTx, or the lock is released
	// the moment the call returns and protects nothing. Ids that do not
	// exist are simply absent from the result.
	LockForCheckout(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domaincatalog.MenuItem, error)

	// DecrementStock reduces a finite-stock item's stock by quantity.
	// changed is false for an unlimited-stock item (StockQty == nil, never
	// decremented) or an id that no longer exists. Must run against a row
	// already locked by LockForCheckout in the same transaction, or the
	// read-then-write it performs is not protected from a concurrent
	// checkout doing the same thing.
	DecrementStock(ctx context.Context, id uuid.UUID, quantity int) (changed bool, err error)
}

// VenueLookup is checkout's own narrow view of a venue: enough to enforce
// its business rules and to keep the venue's ETag fresh when a checkout
// changes its stock.
type VenueLookup interface {
	// Get returns nil, nil if id does not exist.
	Get(ctx context.Context, id uuid.UUID) (*domaincatalog.Venue, error)
	// BumpMenuVersion increases venues.menu_version by one. Call it
	// whenever a checkout actually changes stock (docs/PLAN.md 6.6: the
	// version must move in the same transaction as anything that changes
	// what the menu shows, or a client's cached ETag stays valid for a
	// menu that just changed).
	BumpMenuVersion(ctx context.Context, id uuid.UUID) error
}

// Repository persists and retrieves orders.
type Repository interface {
	// Create inserts the order, its items, and its History entries.
	Create(ctx context.Context, o *domainorder.Order) error
	// Get returns nil, nil if id does not exist. Used to answer GetOrder,
	// to replay an order on an idempotent checkout retry, and as the read
	// half of Service.CancelOrder.
	Get(ctx context.Context, id uuid.UUID) (*domainorder.Order, error)
	// AppendStatusChange persists one status transition already applied to
	// an in-memory Order (via Order.TransitionTo/Cancel/Reject): it updates
	// orders.status/updated_at and inserts the matching
	// order_status_history row from change. Call it with the last element
	// of Order.History right after a transition succeeds.
	AppendStatusChange(ctx context.Context, orderID uuid.UUID, change domainorder.StatusChange) error
}

// IdempotencyClaim is the result of trying to claim an idempotency key for
// a checkout.
type IdempotencyClaim struct {
	// Claimed is true if this call now owns (clientID, key): no one has
	// used it before. The caller should proceed with a normal checkout
	// using the orderID it just passed to Claim.
	Claimed bool
	// HashMatches is only meaningful when !Claimed: does the stored
	// request match the one being retried right now?
	HashMatches bool
	// OrderID is the order already linked to this key, when !Claimed &&
	// HashMatches. Zero otherwise.
	OrderID uuid.UUID
}

// IdempotencyRepository backs the Idempotency-Key mechanism (docs/PLAN.md
// 5.2): a repeat of the same request must return the same order instead of
// creating a second one, and the same key with a different request body is
// a conflict.
type IdempotencyRepository interface {
	// Claim tries to atomically own (clientID, key), storing requestHash so
	// a future retry can tell whether it is the same request. Must run
	// inside the same transaction as the rest of checkout: if that
	// transaction rolls back (the checkout failed for any reason), the
	// claim rolls back with it, and the key is free to retry — a failed
	// attempt must never permanently burn a key.
	//
	// order_id starts NULL and is filled in by LinkOrder once an order
	// actually exists — idempotency_keys.order_id has a (non-deferrable)
	// foreign key to orders, so it cannot be set to an order's id before
	// that order's own INSERT has run, even inside the same transaction.
	//
	// expiresAt is recorded for a future cleanup job (docs/PLAN.md does not
	// ask for one yet, so none exists) — nothing reads it back today, so
	// an "expired" key still counts as used.
	Claim(ctx context.Context, clientID uuid.UUID, key, requestHash string, expiresAt time.Time) (IdempotencyClaim, error)

	// LinkOrder records that (clientID, key) resulted in orderID. Call it
	// right before committing the transaction Claim ran in — a key must
	// never be observable by another transaction as claimed but linked to
	// nothing, since that is indistinguishable from "still being
	// processed" and this project has no polling/waiting story for that.
	LinkOrder(ctx context.Context, clientID uuid.UUID, key string, orderID uuid.UUID) error
}

// OutboxEvent is this package's own narrow view of what it takes to enqueue
// an event (docs/PLAN.md 6.5) — deliberately not usecase/outbox.Event: that
// would make this package depend on another module's use-case layer just to
// write one row (docs/PLAN.md 6.2). adapter/postgres's OutboxRepository is the
// one place that knows both this shape and outbox.Event's.
type OutboxEvent struct {
	Type          string
	AggregateType string
	AggregateID   uuid.UUID
	Payload       []byte
	OccurredAt    time.Time
}

// OutboxRepository lets checkout and cancellation enqueue an event (docs/PLAN.md
// 6.5: order.created, order.cancelled) inside the very same transaction as
// the write that caused it — so an event is never recorded for an order that
// didn't actually get created, and never silently skipped for one that did.
type OutboxRepository interface {
	Enqueue(ctx context.Context, e OutboxEvent) error
}

// RescueOfferRepository is checkout and the cart's own view of rescue
// offers (docs/PLAN.md 5.5) — enough to soft-check one at cart-add/update time
// and to hard-check + consume one inside the checkout transaction.
type RescueOfferRepository interface {
	// GetActiveForItems returns, for each of menuItemIDs that currently has
	// an active offer (window valid and remaining_quantity > 0), that
	// offer — keyed by menu_item_id. Ids with none are simply absent.
	// Never one query per item (docs/PLAN.md 6.6).
	GetActiveForItems(ctx context.Context, menuItemIDs []uuid.UUID, now time.Time) (map[uuid.UUID]domaincatalog.RescueOffer, error)

	// LockForCheckout locks the given rescue offers FOR UPDATE, in
	// ascending id order — same reasoning and same deadlock-avoidance
	// argument as MenuItemLookup.LockForCheckout, just for a different
	// table. Must only be called inside the checkout transaction, always
	// after locking menu items (a fixed lock order across every checkout
	// is what avoids a cross-table deadlock between two concurrent
	// checkouts). Ids that do not exist are simply absent from the result.
	LockForCheckout(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]domaincatalog.RescueOffer, error)

	// DecrementRemaining reduces remaining_quantity by quantity. Must run
	// against a row already locked by LockForCheckout in the same
	// transaction, in the same transaction as MenuItemLookup.DecrementStock
	// (docs/PLAN.md 5.5: both counters move together or not at all).
	DecrementRemaining(ctx context.Context, id uuid.UUID, quantity int) error
}

// CartRepository persists a client's cart as a whole. Save replaces the
// entire item set atomically rather than patching individual rows — see
// adapter/postgres's implementation for why that is the simpler and
// therefore preferred choice for a cart's size.
type CartRepository interface {
	// Get returns nil, nil if clientID has no cart yet.
	Get(ctx context.Context, clientID uuid.UUID) (*domainorder.Cart, error)
	Save(ctx context.Context, cart *domainorder.Cart) error
	// Clear deletes the cart (and its items) entirely. A no-op if there is
	// no cart for clientID.
	Clear(ctx context.Context, clientID uuid.UUID) error
}

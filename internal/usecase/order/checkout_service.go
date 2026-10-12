package order

import (
	"context"
	"fmt"
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/usecase"
	"time"

	"github.com/google/uuid"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"

	domainorder "food-delivery-mvp/internal/domain/order"
)

// idempotencyKeyTTL is how long an Idempotency-Key is honored. docs/PLAN.md
// does not name a duration, and nothing in this project reaps expired keys
// yet (idempotency_keys.expires_at is recorded for that future job, not
// enforced today) — 24 hours is this project's own assumption, chosen as
// comfortably longer than any plausible client retry window.
const idempotencyKeyTTL = 24 * time.Hour

// CheckoutService places an order from a client's cart (docs/PLAN.md 5.1 item
// 4). It is a separate service from CartService on purpose: cart mutation
// and checkout have very different transactional needs — checkout is the
// only place in this package that ever decrements menu_items.stock_qty,
// under row locks, and everything it touches must agree inside one
// transaction or not happen at all.
type CheckoutService struct {
	carts        CartRepository
	venues       VenueLookup
	menuItems    MenuItemLookup
	rescueOffers RescueOfferRepository
	orders       Repository
	idempotency  IdempotencyRepository
	outbox       OutboxRepository
	txManager    usecase.TxManager
}

func NewCheckoutService(carts CartRepository, venues VenueLookup, menuItems MenuItemLookup, rescueOffers RescueOfferRepository, orders Repository, idempotency IdempotencyRepository, outbox OutboxRepository, txManager usecase.TxManager) *CheckoutService {
	return &CheckoutService{carts: carts, venues: venues, menuItems: menuItems, rescueOffers: rescueOffers, orders: orders, idempotency: idempotency, outbox: outbox, txManager: txManager}
}

// PlaceOrder turns clientID's cart into an order, clearing the cart on
// success. Every check that can be invalidated by a concurrent request —
// venue availability, price, stock — runs again inside the transaction
// (docs/PLAN.md 5.2), even though the cart/menu endpoints already ran the
// same checks softly while the customer was building their cart: time has
// passed since then, and either one can have changed.
//
// idempotencyKey scopes everything below it: claiming the key happens
// first, and the cart is deliberately NOT read before that. A retried
// request with the same key arrives with an EMPTY cart whenever the
// original attempt already succeeded and cleared it — reading the cart
// first would misreport that as CART_EMPTY instead of replaying the
// original order.
//
// replayed reports whether this call returned an order created by an
// earlier request (true) or created one just now (false) — the public API
// answers 200 vs 201 based on it (api/openapi/public.yaml createOrder).
func (s *CheckoutService) PlaceOrder(ctx context.Context, clientID uuid.UUID, idempotencyKey, deliveryAddress, customerPhone, comment string) (*domainorder.Order, bool, error) {
	requestHash := domainorder.HashCheckoutRequest(deliveryAddress, customerPhone, comment)

	var placed *domainorder.Order
	var wasReplayed bool
	err := s.txManager.WithinTx(ctx, func(ctx context.Context) error {
		claim, err := s.idempotency.Claim(ctx, clientID, idempotencyKey, requestHash, time.Now().Add(idempotencyKeyTTL))
		if err != nil {
			return fmt.Errorf("claim idempotency key: %w", err)
		}
		if !claim.Claimed {
			if !claim.HashMatches {
				return errs.New(errs.CodeIdempotencyKeyConflict,
					"this idempotency key was already used with a different request")
			}
			existing, err := s.orders.Get(ctx, claim.OrderID)
			if err != nil {
				return fmt.Errorf("get order for replayed idempotency key: %w", err)
			}
			if existing == nil {
				return fmt.Errorf("idempotency key %s references missing order %s", idempotencyKey, claim.OrderID)
			}
			placed = existing
			wasReplayed = true
			return nil
		}

		cart, err := s.carts.Get(ctx, clientID)
		if err != nil {
			return fmt.Errorf("get cart: %w", err)
		}
		if cart == nil {
			return errs.New(errs.CodeCartEmpty, "cart is empty")
		}
		if err := cart.EnsureNotEmpty(); err != nil {
			return err
		}

		venue, err := s.venues.Get(ctx, cart.VenueID)
		if err != nil {
			return fmt.Errorf("get venue: %w", err)
		}
		if venue == nil {
			return errs.New(errs.CodeNotFound, "venue not found")
		}
		now := time.Now()
		if err := venue.EnsureCanOrder(now); err != nil {
			return err
		}

		ids := make([]uuid.UUID, len(cart.Items))
		for i, item := range cart.Items {
			ids[i] = item.MenuItemID
		}
		// The line that makes concurrent checkouts safe: see
		// MenuItemLookup.LockForCheckout's doc comment and
		// adapter/postgres's implementation for why locking in id order
		// prevents a deadlock here.
		locked, err := s.menuItems.LockForCheckout(ctx, ids)
		if err != nil {
			return fmt.Errorf("lock menu items: %w", err)
		}

		// Rescue offers get their own lock, always taken after menu items
		// (docs/PLAN.md 5.5: same locking and id-ordering rules as the regular
		// stock lock) — a fixed lock order across every checkout, not just
		// within one table, is what keeps two concurrent checkouts that
		// touch both a shared item and a shared offer from deadlocking on
		// each other.
		var rescueIDs []uuid.UUID
		for _, item := range cart.Items {
			if item.RescueOfferID != nil {
				rescueIDs = append(rescueIDs, *item.RescueOfferID)
			}
		}
		lockedOffers, err := s.rescueOffers.LockForCheckout(ctx, rescueIDs)
		if err != nil {
			return fmt.Errorf("lock rescue offers: %w", err)
		}

		lines := make([]domainorder.CheckoutLine, len(cart.Items))
		for i, item := range cart.Items {
			lines[i] = s.buildCheckoutLine(item, locked, lockedOffers, now)
		}

		orderID := uuid.New()
		o, err := domainorder.Checkout(orderID, clientID, cart.VenueID, lines, deliveryAddress, customerPhone, comment, now)
		if err != nil {
			return err
		}
		if err := venue.EnsureMinOrderReached(o.TotalMinor()); err != nil {
			return err
		}

		stockChanged := false
		for _, item := range cart.Items {
			changed, err := s.menuItems.DecrementStock(ctx, item.MenuItemID, item.Quantity)
			if err != nil {
				return fmt.Errorf("decrement stock for %s: %w", item.MenuItemID, err)
			}
			stockChanged = stockChanged || changed
		}
		// Rescue stock is a second, independent counter on top of the same
		// regular stock just decremented above (docs/PLAN.md 5.5: "заказ
		// уменьшает и menu_items.stock_qty, и rescue_offers.remaining_quantity")
		// — only for the eligible portion of a line, never the full-price
		// remainder.
		for _, l := range lines {
			if l.RescueOfferID != nil && l.RescueQuantity > 0 {
				if err := s.rescueOffers.DecrementRemaining(ctx, *l.RescueOfferID, l.RescueQuantity); err != nil {
					return fmt.Errorf("decrement rescue offer %s: %w", *l.RescueOfferID, err)
				}
			}
		}
		if stockChanged {
			if err := s.venues.BumpMenuVersion(ctx, cart.VenueID); err != nil {
				return fmt.Errorf("bump menu version: %w", err)
			}
		}

		if err := s.orders.Create(ctx, o); err != nil {
			return fmt.Errorf("create order: %w", err)
		}
		// Enqueued in the same transaction as the order it describes
		// (docs/PLAN.md 6.5): either both are committed together or neither is,
		// so a webhook can never fire for an order that doesn't exist.
		event, err := newOrderCreatedEvent(o)
		if err != nil {
			return err
		}
		if err := s.outbox.Enqueue(ctx, event); err != nil {
			return fmt.Errorf("enqueue order.created event: %w", err)
		}
		if err := s.idempotency.LinkOrder(ctx, clientID, idempotencyKey, o.ID); err != nil {
			return fmt.Errorf("link idempotency key: %w", err)
		}
		if err := s.carts.Clear(ctx, clientID); err != nil {
			return fmt.Errorf("clear cart: %w", err)
		}

		placed = o
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return placed, wasReplayed, nil
}

// buildCheckoutLine turns one cart line into a domainorder.CheckoutLine,
// running the same per-item checks catalog.MenuItem already exposes
// (EnsurePriceUnchanged, then EnsureAvailable) against the just-locked
// state — this is the "hard check" from docs/PLAN.md 5.2. domainorder.Checkout
// only ever sees the resulting error, never the MenuItem itself, keeping
// this package's rule (it declares its own ports, never imports
// usecase/catalog) intact all the way down into the domain layer too.
//
// lockedOffers holds, for every cart item's non-nil RescueOfferID, that
// offer's just-locked row. A line whose cart snapshot assumed a rescue
// price skips the regular EnsurePriceUnchanged check entirely — comparing a
// discounted snapshot against the item's plain price would always
// (wrongly) look like drift — and instead resolves its own rescue-specific
// outcome via buildRescuePricing.
func (s *CheckoutService) buildCheckoutLine(item domainorder.CartItem, locked map[uuid.UUID]domaincatalog.MenuItem, lockedOffers map[uuid.UUID]domaincatalog.RescueOffer, now time.Time) domainorder.CheckoutLine {
	line := domainorder.CheckoutLine{
		MenuItemID: item.MenuItemID,
		Quantity:   item.Quantity,
	}

	mi, ok := locked[item.MenuItemID]
	if !ok {
		line.Err = errs.New(errs.CodeNotFound, "menu item no longer exists")
		return line
	}
	line.Name = mi.Name

	if item.RescueOfferID != nil {
		if err := s.applyRescuePricing(&line, *item.RescueOfferID, mi, lockedOffers, now); err != nil {
			line.Err = err
			return line
		}
	} else {
		line.UnitPriceMinor = item.PriceMinorSnapshot
		if err := mi.EnsurePriceUnchanged(item.PriceMinorSnapshot); err != nil {
			line.Err = err
			return line
		}
	}

	line.Err = mi.EnsureAvailable(item.Quantity)
	return line
}

// applyRescuePricing resolves a line whose cart snapshot assumed offerID.
// Returns a RESCUE_OFFER_EXPIRED error if the offer's own window is no
// longer valid (it was canceled, or now falls outside [starts_at,
// ends_at)) — the entire deal the cart remembered is gone, a different and
// more fundamental problem than the offer simply running low. A live
// window with less remaining stock than requested is NOT an error: it sets
// line.RescueQuantity to whatever the offer can still cover (down to zero)
// and lets domainorder.Checkout build the resulting split — docs/PLAN.md 5.5
// describes exactly this outcome as a normal, successful order.
func (s *CheckoutService) applyRescuePricing(line *domainorder.CheckoutLine, offerID uuid.UUID, mi domaincatalog.MenuItem, lockedOffers map[uuid.UUID]domaincatalog.RescueOffer, now time.Time) error {
	line.UnitPriceMinor = mi.PriceMinor

	offer, ok := lockedOffers[offerID]
	if !ok || !offer.WindowValid(now) {
		endsAt := offer.EndsAt
		return errs.NewWithDetails(errs.CodeRescueOfferExpired,
			fmt.Sprintf("the rescue offer for %q has ended", mi.Name),
			[]map[string]any{{"menuItemId": mi.ID, "newPriceMinor": mi.PriceMinor, "offerEndedAt": endsAt}})
	}

	eligible := line.Quantity
	if offer.RemainingQuantity < eligible {
		eligible = offer.RemainingQuantity
	}
	line.RescueOfferID = &offerID
	line.RescueQuantity = eligible
	line.RescueUnitPriceMinor = offer.DiscountedPrice(mi.PriceMinor)
	return nil
}

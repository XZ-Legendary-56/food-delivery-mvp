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

// CartService implements the cart use-cases (docs/PLAN.md 5.1 item 3).
type CartService struct {
	carts        CartRepository
	menuItems    MenuItemLookup
	rescueOffers RescueOfferRepository
	txManager    usecase.TxManager
}

// NewCartService builds a CartService. Mutations run through txManager
// (docs/PLAN.md 6.4) because each one is a read-modify-write of the whole
// cart: two requests for the same client racing between the read and the
// write would otherwise silently lose one of them.
func NewCartService(carts CartRepository, menuItems MenuItemLookup, rescueOffers RescueOfferRepository, txManager usecase.TxManager) *CartService {
	return &CartService{carts: carts, menuItems: menuItems, rescueOffers: rescueOffers, txManager: txManager}
}

// resolvePricing is the soft-check pricing decision shared by AddItem and
// UpdateItemQuantity (docs/PLAN.md 5.5: rescue activity is computed at read
// time, so both mutation paths always re-resolve it fresh rather than
// keeping whatever was true on an earlier call). If item currently has an
// active rescue offer, the line snapshots the discounted price and that
// offer's id; otherwise it snapshots the item's plain price with no offer.
func (s *CartService) resolvePricing(ctx context.Context, item *domaincatalog.MenuItem) (priceMinor int64, rescueOfferID *uuid.UUID, err error) {
	offers, err := s.rescueOffers.GetActiveForItems(ctx, []uuid.UUID{item.ID}, time.Now())
	if err != nil {
		return 0, nil, fmt.Errorf("look up rescue offer: %w", err)
	}
	offer, ok := offers[item.ID]
	if !ok {
		return item.PriceMinor, nil, nil
	}
	return offer.DiscountedPrice(item.PriceMinor), &offer.ID, nil
}

// CartView is what every cart use-case returns: the cart itself (nil if the
// client has none yet) plus a fresh snapshot of each line's menu item —
// docs/PLAN.md's public API shows this as CartItem.isAvailable ("may have
// changed since the item was added") and needs the item's current name, so
// it is always re-fetched on read, never stored on the cart itself.
type CartView struct {
	ClientID  uuid.UUID
	Cart      *domainorder.Cart
	MenuItems map[uuid.UUID]domaincatalog.MenuItem // keyed by CartItem.MenuItemID
}

// GetCart returns the client's cart, or an empty view if they have none yet.
func (s *CartService) GetCart(ctx context.Context, clientID uuid.UUID) (CartView, error) {
	cart, err := s.carts.Get(ctx, clientID)
	if err != nil {
		return CartView{}, fmt.Errorf("get cart: %w", err)
	}
	view := CartView{ClientID: clientID, Cart: cart}
	if cart == nil {
		return view, nil
	}
	menuItems, err := s.menuItemSnapshot(ctx, cart)
	if err != nil {
		return CartView{}, err
	}
	view.MenuItems = menuItems
	return view, nil
}

// menuItemSnapshot re-fetches every line's menu item in one batch call
// (docs/PLAN.md 6.6), never one query per line, and attaches each item's
// currently active rescue offer (if any) for display — this is always the
// LIVE offer, independent of which one (if any) the line's own
// PriceMinorSnapshot/RescueOfferID was resolved against; docs/PLAN.md 5.5
// computes activity at read time, and a cart view is exactly a read.
func (s *CartService) menuItemSnapshot(ctx context.Context, cart *domainorder.Cart) (map[uuid.UUID]domaincatalog.MenuItem, error) {
	ids := make([]uuid.UUID, len(cart.Items))
	for i, item := range cart.Items {
		ids[i] = item.MenuItemID
	}
	items, err := s.menuItems.GetMany(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("look up cart item menu items: %w", err)
	}

	offers, err := s.rescueOffers.GetActiveForItems(ctx, ids, time.Now())
	if err != nil {
		return nil, fmt.Errorf("look up rescue offers: %w", err)
	}
	for id, mi := range items {
		if offer, ok := offers[id]; ok {
			offer := offer
			mi.RescueOffer = &offer
			items[id] = mi
		}
	}
	return items, nil
}

// AddItem adds quantity units of menuItemID to clientID's cart, starting a
// new cart pinned to the item's venue if they do not have one yet. This is
// the "soft" availability check from docs/PLAN.md 5.2 — checked here so the
// customer learns about a problem immediately, and checked again,
// authoritatively, inside the checkout transaction (docs/PLAN.md 5.2, stage 7)
// because stock can change in between.
func (s *CartService) AddItem(ctx context.Context, clientID, menuItemID uuid.UUID, quantity int) (CartView, error) {
	item, err := s.menuItems.Get(ctx, menuItemID)
	if err != nil {
		return CartView{}, fmt.Errorf("look up menu item: %w", err)
	}
	if item == nil {
		return CartView{}, errs.New(errs.CodeNotFound, "menu item not found")
	}
	if err := item.EnsureAvailable(quantity); err != nil {
		return CartView{}, err
	}
	priceMinor, rescueOfferID, err := s.resolvePricing(ctx, item)
	if err != nil {
		return CartView{}, err
	}

	var cart *domainorder.Cart
	err = s.txManager.WithinTx(ctx, func(ctx context.Context) error {
		var txErr error
		cart, txErr = s.carts.Get(ctx, clientID)
		if txErr != nil {
			return fmt.Errorf("get cart: %w", txErr)
		}
		if cart == nil {
			cart = domainorder.NewCart(clientID, item.VenueID)
		}
		if txErr := cart.AddItem(uuid.New(), item.VenueID, item.ID, quantity, priceMinor, rescueOfferID); txErr != nil {
			return txErr
		}
		return s.carts.Save(ctx, cart)
	})
	if err != nil {
		return CartView{}, err
	}

	menuItems, err := s.menuItemSnapshot(ctx, cart)
	if err != nil {
		return CartView{}, err
	}
	return CartView{ClientID: clientID, Cart: cart, MenuItems: menuItems}, nil
}

// UpdateItemQuantity changes the quantity of the cart line identified by
// itemID (cart_items.id, not the menu item id). The price/rescue-offer
// snapshot is re-resolved fresh against the line's menu item, same as
// AddItem — docs/PLAN.md 5.5 computes offer activity at read time, so a
// quantity bump must not keep whatever discount (or lack of one) applied
// when the line was last touched.
func (s *CartService) UpdateItemQuantity(ctx context.Context, clientID, itemID uuid.UUID, quantity int) (CartView, error) {
	var cart *domainorder.Cart
	err := s.txManager.WithinTx(ctx, func(ctx context.Context) error {
		var txErr error
		cart, txErr = s.carts.Get(ctx, clientID)
		if txErr != nil {
			return fmt.Errorf("get cart: %w", txErr)
		}
		if cart == nil {
			return errs.New(errs.CodeNotFound, "cart not found")
		}

		var menuItemID uuid.UUID
		found := false
		for _, line := range cart.Items {
			if line.ID == itemID {
				menuItemID = line.MenuItemID
				found = true
				break
			}
		}
		if !found {
			return errs.New(errs.CodeNotFound, "cart item not found")
		}
		item, txErr := s.menuItems.Get(ctx, menuItemID)
		if txErr != nil {
			return fmt.Errorf("look up menu item: %w", txErr)
		}
		if item == nil {
			return errs.New(errs.CodeNotFound, "menu item not found")
		}
		priceMinor, rescueOfferID, txErr := s.resolvePricing(ctx, item)
		if txErr != nil {
			return txErr
		}

		if txErr := cart.UpdateQuantity(itemID, quantity, priceMinor, rescueOfferID); txErr != nil {
			return txErr
		}
		return s.carts.Save(ctx, cart)
	})
	if err != nil {
		return CartView{}, err
	}

	menuItems, err := s.menuItemSnapshot(ctx, cart)
	if err != nil {
		return CartView{}, err
	}
	return CartView{ClientID: clientID, Cart: cart, MenuItems: menuItems}, nil
}

// RemoveItem drops the cart line identified by itemID.
func (s *CartService) RemoveItem(ctx context.Context, clientID, itemID uuid.UUID) (CartView, error) {
	var cart *domainorder.Cart
	err := s.txManager.WithinTx(ctx, func(ctx context.Context) error {
		var txErr error
		cart, txErr = s.carts.Get(ctx, clientID)
		if txErr != nil {
			return fmt.Errorf("get cart: %w", txErr)
		}
		if cart == nil {
			return errs.New(errs.CodeNotFound, "cart not found")
		}
		if txErr := cart.RemoveItem(itemID); txErr != nil {
			return txErr
		}
		return s.carts.Save(ctx, cart)
	})
	if err != nil {
		return CartView{}, err
	}

	menuItems, err := s.menuItemSnapshot(ctx, cart)
	if err != nil {
		return CartView{}, err
	}
	return CartView{ClientID: clientID, Cart: cart, MenuItems: menuItems}, nil
}

// ClearCart empties the client's cart. A single delete needs no
// transaction of its own.
func (s *CartService) ClearCart(ctx context.Context, clientID uuid.UUID) error {
	if err := s.carts.Clear(ctx, clientID); err != nil {
		return fmt.Errorf("clear cart: %w", err)
	}
	return nil
}

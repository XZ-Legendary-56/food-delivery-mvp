// Package public implements api/openapi/public.yaml's generated
// publicapi.StrictServerInterface. Handlers only translate between the
// wire format and the usecase layer — no business rule lives here
// (docs/PLAN.md 10.2: a handler never decides what an error means, and none
// of these decide what's available or what something costs either).
package public

import (
	"food-delivery-mvp/internal/generated/publicapi"
	"time"

	"github.com/google/uuid"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"
	domainorder "food-delivery-mvp/internal/domain/order"

	catalogusecase "food-delivery-mvp/internal/usecase/catalog"
	orderusecase "food-delivery-mvp/internal/usecase/order"
)

// currency is hardcoded because the project stores no currency column
// (docs/schema-decisions.md: "Валюта в MVP одна, поле для неё не заводим")
// — every Money the API returns is in the platform's one currency.
const currency = "RUB"

func money(amountMinor int64) publicapi.Money {
	return publicapi.Money{AmountMinor: amountMinor, Currency: currency}
}

func toVenue(v catalogusecase.VenueView) publicapi.Venue {
	out := publicapi.Venue{
		Id:              v.ID,
		Name:            v.Name,
		Cuisine:         v.Cuisine,
		MinOrderAmount:  money(v.MinOrderAmountMinor),
		AcceptingOrders: v.AcceptingOrders,
		IsOpen:          v.IsOpen,
		NextOpensAt:     v.NextOpensAt,
	}
	if v.Description != "" {
		out.Description = &v.Description
	}
	return out
}

func toVenueDetail(v catalogusecase.VenueView) publicapi.VenueDetail {
	base := toVenue(v)
	return publicapi.VenueDetail{
		Id:              base.Id,
		Name:            base.Name,
		Description:     base.Description,
		Cuisine:         base.Cuisine,
		MinOrderAmount:  base.MinOrderAmount,
		AcceptingOrders: base.AcceptingOrders,
		IsOpen:          base.IsOpen,
		NextOpensAt:     base.NextOpensAt,
		Schedule:        toScheduleEntries(v.Schedule),
	}
}

func toScheduleEntries(entries []domaincatalog.ScheduleEntry) []publicapi.VenueScheduleEntry {
	out := make([]publicapi.VenueScheduleEntry, len(entries))
	for i, e := range entries {
		out[i] = publicapi.VenueScheduleEntry{
			Weekday:  isoWeekdayOf(e.Weekday),
			OpensAt:  formatTimeOfDay(e.OpensAt),
			ClosesAt: formatTimeOfDay(e.ClosesAt),
		}
	}
	return out
}

// isoWeekdayOf converts a Go time.Weekday (Sunday=0..Saturday=6) to the
// Monday=0..Sunday=6 convention the API uses (see
// api/openapi/public.yaml VenueScheduleEntry.weekday and
// adapter/postgres/weekday.go, which does the same conversion at the DB
// boundary — every boundary between Go's convention and the wire/DB one
// converts explicitly, never by raw int cast).
func isoWeekdayOf(w time.Weekday) int {
	return (int(w) + 6) % 7
}

func formatTimeOfDay(d time.Duration) string {
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	s := int((d % time.Minute) / time.Second)
	return time.Date(0, 1, 1, h, m, s, 0, time.UTC).Format("15:04:05")
}

// toRescueOfferSummary builds the wire summary shown alongside a menu item
// or cart line that currently has one (docs/PLAN.md 5.5). priceBeforeMinor is
// the item's plain price; the discounted price is derived from it via
// RescueOffer.DiscountedPrice, never stored separately.
func toRescueOfferSummary(o domaincatalog.RescueOffer, priceBeforeMinor int64) publicapi.RescueOfferSummary {
	return publicapi.RescueOfferSummary{
		Id:                o.ID,
		DiscountPercent:   o.DiscountPercent,
		PriceBefore:       money(priceBeforeMinor),
		PriceAfter:        money(o.DiscountedPrice(priceBeforeMinor)),
		RemainingQuantity: o.RemainingQuantity,
		EndsAt:            o.EndsAt,
	}
}

func toMenuItem(mi domaincatalog.MenuItem) publicapi.MenuItem {
	out := publicapi.MenuItem{
		Id:          mi.ID,
		CategoryId:  uuid.Nil, // set by the caller, which knows the category it came from
		Name:        mi.Name,
		Price:       money(mi.PriceMinor),
		IsAvailable: mi.IsAvailable,
		StockQty:    mi.StockQty,
	}
	if mi.Description != "" {
		out.Description = &mi.Description
	}
	if mi.RescueOffer != nil {
		summary := toRescueOfferSummary(*mi.RescueOffer, mi.PriceMinor)
		out.RescueOffer = &summary
	}
	return out
}

func toMenu(m catalogusecase.Menu) publicapi.Menu {
	categories := make([]publicapi.MenuCategory, len(m.Categories))
	for i, c := range m.Categories {
		items := make([]publicapi.MenuItem, len(c.Items))
		for j, mi := range c.Items {
			item := toMenuItem(mi)
			item.CategoryId = c.ID
			items[j] = item
		}
		position := c.Position
		categories[i] = publicapi.MenuCategory{
			Id:       c.ID,
			Name:     c.Name,
			Position: &position,
			Items:    items,
		}
	}
	return publicapi.Menu{
		VenueId:     m.VenueID,
		MenuVersion: m.MenuVersion,
		Categories:  categories,
	}
}

func toCart(view orderusecase.CartView) publicapi.Cart {
	out := publicapi.Cart{
		ClientId: view.ClientID,
		Items:    []publicapi.CartItem{},
		Subtotal: money(0),
	}
	if view.Cart == nil {
		return out
	}

	out.VenueId = &view.Cart.VenueID
	out.Items = make([]publicapi.CartItem, len(view.Cart.Items))
	for i, item := range view.Cart.Items {
		out.Items[i] = toCartItem(item, view.MenuItems[item.MenuItemID])
	}
	out.Subtotal = money(view.Cart.TotalMinor())
	return out
}

// toCartItem builds the wire CartItem for a cart line. mi is the item's
// current row (its name, live price, availability) — the zero value if it
// has vanished from the menu entirely since being added, in which case the
// line is reported unavailable with an empty name rather than failing the
// whole cart response.
func toCartItem(item domainorder.CartItem, mi domaincatalog.MenuItem) publicapi.CartItem {
	out := publicapi.CartItem{
		Id:          item.ID,
		MenuItemId:  item.MenuItemID,
		Name:        mi.Name,
		Quantity:    item.Quantity,
		UnitPrice:   money(item.PriceMinorSnapshot),
		LineTotal:   money(item.Total()),
		IsAvailable: mi.EnsureAvailable(item.Quantity) == nil,
	}
	if mi.RescueOffer != nil {
		summary := toRescueOfferSummary(*mi.RescueOffer, mi.PriceMinor)
		out.RescueOffer = &summary
	}
	return out
}

func toRescueOfferFeedEntry(e catalogusecase.RescueOfferFeedEntry) publicapi.RescueOfferFeedEntry {
	summary := toRescueOfferSummary(e.Offer, e.PriceBeforeMinor)
	return publicapi.RescueOfferFeedEntry{
		Id:                summary.Id,
		DiscountPercent:   summary.DiscountPercent,
		PriceBefore:       summary.PriceBefore,
		PriceAfter:        summary.PriceAfter,
		RemainingQuantity: summary.RemainingQuantity,
		EndsAt:            summary.EndsAt,
		VenueId:           e.Offer.VenueID,
		VenueName:         e.VenueName,
		MenuItemId:        e.Offer.MenuItemID,
		MenuItemName:      e.MenuItemName,
	}
}

func toOrder(o *domainorder.Order) publicapi.Order {
	out := publicapi.Order{
		Id:              o.ID,
		VenueId:         o.VenueID,
		Status:          publicapi.OrderStatus(o.Status),
		DeliveryAddress: o.DeliveryAddress,
		CustomerPhone:   o.CustomerPhone,
		EtaMinutes:      o.ETAMinutes,
		Total:           money(o.TotalMinor()),
		CreatedAt:       o.CreatedAt,
		UpdatedAt:       o.UpdatedAt,
	}
	if o.Comment != "" {
		out.Comment = &o.Comment
	}
	if o.RejectionReason != "" {
		out.RejectionReason = &o.RejectionReason
	}

	out.Items = make([]publicapi.OrderItem, len(o.Items))
	for i, item := range o.Items {
		out.Items[i] = toOrderItem(item)
	}

	out.StatusHistory = make([]publicapi.OrderStatusHistoryEntry, len(o.History))
	for i, change := range o.History {
		out.StatusHistory[i] = toOrderStatusHistoryEntry(change)
	}

	return out
}

func toOrderItem(item domainorder.Item) publicapi.OrderItem {
	return publicapi.OrderItem{
		MenuItemId:    item.MenuItemID,
		Name:          item.NameSnapshot,
		Quantity:      item.Quantity,
		UnitPrice:     money(item.UnitPriceMinor),
		LineTotal:     money(item.Total()),
		RescueOfferId: item.RescueOfferID,
	}
}

func toOrderStatusHistoryEntry(change domainorder.StatusChange) publicapi.OrderStatusHistoryEntry {
	out := publicapi.OrderStatusHistoryEntry{
		ToStatus:  publicapi.OrderStatus(change.To),
		Actor:     publicapi.OrderStatusHistoryEntryActor(change.Actor),
		CreatedAt: change.CreatedAt,
	}
	if change.From != nil {
		from := publicapi.OrderStatus(*change.From)
		out.FromStatus = &from
	}
	return out
}

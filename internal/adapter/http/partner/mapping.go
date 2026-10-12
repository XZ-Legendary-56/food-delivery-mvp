package partner

import (
	"food-delivery-mvp/internal/generated/partnerapi"
	"time"

	domaincatalog "food-delivery-mvp/internal/domain/catalog"
	domainorder "food-delivery-mvp/internal/domain/order"

	partnerusecase "food-delivery-mvp/internal/usecase/partner"
)

// currency mirrors adapter/http/public's own constant — duplicated rather
// than imported across sibling adapter packages, same call as the small
// schedule-formatting helpers below.
const currency = "RUB"

func money(amountMinor int64) partnerapi.Money {
	return partnerapi.Money{AmountMinor: amountMinor, Currency: currency}
}

// isoWeekdayOf/formatTimeOfDay duplicate adapter/http/public's own
// (unexported) helpers of the same name: both adapters convert the same
// Go-vs-wire weekday/time representation, but two independent HTTP
// packages reaching into each other for two three-line pure functions
// would be a stranger dependency than just repeating them.
func isoWeekdayOf(w time.Weekday) int {
	return (int(w) + 6) % 7
}

func formatTimeOfDay(d time.Duration) string {
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	s := int((d % time.Minute) / time.Second)
	return time.Date(0, 1, 1, h, m, s, 0, time.UTC).Format("15:04:05")
}

// fromScheduleEntries parses the wire format back into domain entries. It
// only needs to handle well-formed HH:MM:SS strings — the pattern regex in
// partner.yaml already constrains what the generated type accepts, so a
// parse failure here would mean the spec's own pattern was wrong, not that
// this needs to defend against garbage.
func fromScheduleEntries(entries []partnerapi.VenueScheduleEntry) []domaincatalog.ScheduleEntry {
	out := make([]domaincatalog.ScheduleEntry, len(entries))
	for i, e := range entries {
		out[i] = domaincatalog.ScheduleEntry{
			Weekday:  goWeekdayOf(e.Weekday),
			OpensAt:  parseTimeOfDay(e.OpensAt),
			ClosesAt: parseTimeOfDay(e.ClosesAt),
		}
	}
	return out
}

func goWeekdayOf(iso int) time.Weekday {
	return time.Weekday((iso + 1) % 7)
}

func parseTimeOfDay(s string) time.Duration {
	t, err := time.Parse("15:04:05", s)
	if err != nil {
		return 0
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute + time.Duration(t.Second())*time.Second
}

func toScheduleEntries(entries []domaincatalog.ScheduleEntry) []partnerapi.VenueScheduleEntry {
	out := make([]partnerapi.VenueScheduleEntry, len(entries))
	for i, e := range entries {
		out[i] = partnerapi.VenueScheduleEntry{
			Weekday:  isoWeekdayOf(e.Weekday),
			OpensAt:  formatTimeOfDay(e.OpensAt),
			ClosesAt: formatTimeOfDay(e.ClosesAt),
		}
	}
	return out
}

func toPartnerVenue(v *domaincatalog.Venue) partnerapi.PartnerVenue {
	out := partnerapi.PartnerVenue{
		Id:              v.ID,
		Name:            v.Name,
		Cuisine:         v.Cuisine,
		MinOrderAmount:  money(v.MinOrderAmountMinor),
		AcceptingOrders: v.AcceptingOrders,
		MenuVersion:     v.MenuVersion,
		Schedule:        toScheduleEntries(v.Schedule),
		WebhookUrl:      v.WebhookURL,
	}
	if v.Description != "" {
		out.Description = &v.Description
	}
	return out
}

func toPartnerVenueWithSecret(v *domaincatalog.Venue, newSecret string) partnerapi.PartnerVenueWithSecret {
	base := toPartnerVenue(v)
	out := partnerapi.PartnerVenueWithSecret{
		Id:              base.Id,
		Name:            base.Name,
		Description:     base.Description,
		Cuisine:         base.Cuisine,
		MinOrderAmount:  base.MinOrderAmount,
		AcceptingOrders: base.AcceptingOrders,
		MenuVersion:     base.MenuVersion,
		Schedule:        base.Schedule,
		WebhookUrl:      base.WebhookUrl,
	}
	if newSecret != "" {
		out.WebhookSecret = &newSecret
	}
	return out
}

func toPartnerMenuItem(mi domaincatalog.MenuItem) partnerapi.PartnerMenuItem {
	out := partnerapi.PartnerMenuItem{
		Id:                 mi.ID,
		CategoryId:         mi.CategoryID,
		Name:               mi.Name,
		Price:              money(mi.PriceMinor),
		IsAvailable:        mi.IsAvailable,
		StockQty:           mi.StockQty,
		CookingTimeMinutes: mi.CookingTimeMinutes,
		Source:             partnerapi.PartnerMenuItemSource(mi.Source),
		ExternalId:         mi.ExternalID,
	}
	if mi.Description != "" {
		out.Description = &mi.Description
	}
	return out
}

func toPartnerMenu(m partnerusecase.Menu) partnerapi.PartnerMenu {
	categories := make([]partnerapi.PartnerMenuCategory, len(m.Categories))
	for i, c := range m.Categories {
		items := make([]partnerapi.PartnerMenuItem, len(c.Items))
		for j, mi := range c.Items {
			items[j] = toPartnerMenuItem(mi)
		}
		position := c.Position
		categories[i] = partnerapi.PartnerMenuCategory{
			Id:       c.ID,
			Name:     c.Name,
			Position: &position,
			Items:    items,
		}
	}
	return partnerapi.PartnerMenu{MenuVersion: m.MenuVersion, Categories: categories}
}

func toPartnerRescueOffer(o domaincatalog.RescueOffer) partnerapi.RescueOffer {
	return partnerapi.RescueOffer{
		Id:                o.ID,
		MenuItemId:        o.MenuItemID,
		DiscountPercent:   o.DiscountPercent,
		InitialQuantity:   o.InitialQuantity,
		RemainingQuantity: o.RemainingQuantity,
		StartsAt:          o.StartsAt,
		EndsAt:            o.EndsAt,
		CancelledAt:       o.CancelledAt,
	}
}

func toPartnerOrder(o *domainorder.Order) partnerapi.PartnerOrder {
	out := partnerapi.PartnerOrder{
		Id:              o.ID,
		Status:          partnerapi.OrderStatus(o.Status),
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
	if o.ExternalOrderID != "" {
		out.ExternalOrderId = &o.ExternalOrderID
	}
	out.Items = make([]partnerapi.PartnerOrderItem, len(o.Items))
	for i, item := range o.Items {
		out.Items[i] = partnerapi.PartnerOrderItem{
			MenuItemId:    item.MenuItemID,
			Name:          item.NameSnapshot,
			Quantity:      item.Quantity,
			UnitPrice:     money(item.UnitPriceMinor),
			RescueOfferId: item.RescueOfferID,
		}
	}
	return out
}

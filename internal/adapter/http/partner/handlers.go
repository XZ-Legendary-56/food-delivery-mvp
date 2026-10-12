package partner

import (
	"context"
	"errors"
	"food-delivery-mvp/internal/adapter/http/middleware"
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/generated/partnerapi"

	"github.com/google/uuid"

	domainorder "food-delivery-mvp/internal/domain/order"

	partnerusecase "food-delivery-mvp/internal/usecase/partner"
)

// Handlers implements partnerapi.StrictServerInterface. Every method reads
// the venue APIKeyAuth already resolved from context — the generated
// request objects carry no venue/auth field of their own, since
// "security: ApiKeyAuth" is enforced by that middleware, not by the spec's
// request shape.
type Handlers struct {
	Venues       *partnerusecase.VenueService
	Menus        *partnerusecase.MenuService
	Orders       *partnerusecase.OrderService
	RescueOffers *partnerusecase.RescueOfferService
}

var _ partnerapi.StrictServerInterface = (*Handlers)(nil)

func venueIDFromContext(ctx context.Context) (uuid.UUID, error) {
	id, ok := middleware.VenueIDFromContext(ctx)
	if !ok {
		// Unreachable in production: APIKeyAuth always sets this before a
		// handler runs. Reported as a plain error, never a panic
		// (docs/PLAN.md: no panics in working code), so a future refactor
		// that drops the middleware fails loudly as a 500, not silently.
		return uuid.Nil, errors.New("venue id missing from context: APIKeyAuth middleware not applied")
	}
	return id, nil
}

func (h *Handlers) GetPartnerVenue(ctx context.Context, _ partnerapi.GetPartnerVenueRequestObject) (partnerapi.GetPartnerVenueResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	v, err := h.Venues.GetVenue(ctx, venueID)
	if err != nil {
		return nil, err
	}
	return partnerapi.GetPartnerVenue200JSONResponse(toPartnerVenue(v)), nil
}

func (h *Handlers) UpdatePartnerVenue(ctx context.Context, request partnerapi.UpdatePartnerVenueRequestObject) (partnerapi.UpdatePartnerVenueResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}

	req := partnerusecase.UpdateVenueRequest{
		Description:         request.Body.Description,
		AcceptingOrders:     request.Body.AcceptingOrders,
		MinOrderAmountMinor: request.Body.MinOrderAmountMinor,
		WebhookURL:          request.Body.WebhookUrl,
	}
	if request.Body.Schedule != nil {
		schedule := fromScheduleEntries(*request.Body.Schedule)
		req.Schedule = &schedule
	}

	v, newSecret, err := h.Venues.UpdateVenue(ctx, venueID, req)
	if err != nil {
		return nil, err
	}
	return partnerapi.UpdatePartnerVenue200JSONResponse(toPartnerVenueWithSecret(v, newSecret)), nil
}

func (h *Handlers) SyncMenu(ctx context.Context, request partnerapi.SyncMenuRequestObject) (partnerapi.SyncMenuResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}

	categories := make([]partnerusecase.MenuSyncCategory, len(request.Body.Categories))
	for i, c := range request.Body.Categories {
		items := make([]partnerusecase.MenuSyncItem, len(c.Items))
		for j, item := range c.Items {
			if item.PriceMinor < 1 {
				return nil, errs.Newf(errs.CodeValidationError, "priceMinor must be positive for item %q", item.ExternalId)
			}
			cookingTime := 15
			if item.CookingTimeMinutes != nil {
				cookingTime = *item.CookingTimeMinutes
			}
			isAvailable := true
			if item.IsAvailable != nil {
				isAvailable = *item.IsAvailable
			}
			description := ""
			if item.Description != nil {
				description = *item.Description
			}
			items[j] = partnerusecase.MenuSyncItem{
				ExternalID:         item.ExternalId,
				Name:               item.Name,
				Description:        description,
				PriceMinor:         item.PriceMinor,
				CookingTimeMinutes: cookingTime,
				IsAvailable:        isAvailable,
				StockQty:           item.StockQty,
			}
		}
		position := 0
		if c.Position != nil {
			position = *c.Position
		}
		categories[i] = partnerusecase.MenuSyncCategory{Name: c.Name, Position: position, Items: items}
	}

	menu, err := h.Menus.SyncMenu(ctx, venueID, categories)
	if err != nil {
		return nil, err
	}
	return partnerapi.SyncMenu200JSONResponse(toPartnerMenu(menu)), nil
}

func (h *Handlers) CreateMenuItem(ctx context.Context, request partnerapi.CreateMenuItemRequestObject) (partnerapi.CreateMenuItemResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}
	if request.Body.PriceMinor < 1 {
		return nil, errs.New(errs.CodeValidationError, "priceMinor must be positive")
	}

	cookingTime := 15
	if request.Body.CookingTimeMinutes != nil {
		cookingTime = *request.Body.CookingTimeMinutes
	}
	description := ""
	if request.Body.Description != nil {
		description = *request.Body.Description
	}

	mi, err := h.Menus.CreateItem(ctx, venueID, partnerusecase.NewMenuItem{
		CategoryID:         request.Body.CategoryId,
		Name:               request.Body.Name,
		Description:        description,
		PriceMinor:         request.Body.PriceMinor,
		CookingTimeMinutes: cookingTime,
		StockQty:           request.Body.StockQty,
		ExternalID:         request.Body.ExternalId,
	})
	if err != nil {
		return nil, err
	}
	return partnerapi.CreateMenuItem201JSONResponse(toPartnerMenuItem(*mi)), nil
}

func (h *Handlers) UpdateMenuItem(ctx context.Context, request partnerapi.UpdateMenuItemRequestObject) (partnerapi.UpdateMenuItemResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}
	if request.Body.PriceMinor != nil && *request.Body.PriceMinor < 1 {
		return nil, errs.New(errs.CodeValidationError, "priceMinor must be positive")
	}

	mi, err := h.Menus.UpdateItem(ctx, venueID, request.ItemId, partnerusecase.MenuItemPatch{
		CategoryID:         request.Body.CategoryId,
		Name:               request.Body.Name,
		Description:        request.Body.Description,
		PriceMinor:         request.Body.PriceMinor,
		CookingTimeMinutes: request.Body.CookingTimeMinutes,
	})
	if err != nil {
		return nil, err
	}
	return partnerapi.UpdateMenuItem200JSONResponse(toPartnerMenuItem(*mi)), nil
}

func (h *Handlers) UpdateAvailability(ctx context.Context, request partnerapi.UpdateAvailabilityRequestObject) (partnerapi.UpdateAvailabilityResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil || len(request.Body.Items) == 0 {
		return nil, errs.New(errs.CodeValidationError, "items must not be empty")
	}

	updates := make([]partnerusecase.AvailabilityUpdate, len(request.Body.Items))
	for i, item := range request.Body.Items {
		updates[i] = partnerusecase.AvailabilityUpdate{
			MenuItemID:  item.MenuItemId,
			IsAvailable: item.IsAvailable,
			StockQtySet: true,
			StockQty:    item.StockQty,
		}
	}

	items, err := h.Menus.UpdateAvailability(ctx, venueID, updates)
	if err != nil {
		return nil, err
	}

	resp := partnerapi.UpdateAvailabilityResult{Items: make([]partnerapi.AvailabilityUpdate, len(items))}
	for i, mi := range items {
		isAvailable := mi.IsAvailable
		resp.Items[i] = partnerapi.AvailabilityUpdate{
			MenuItemId:  mi.ID,
			IsAvailable: &isAvailable,
			StockQty:    mi.StockQty,
		}
	}
	return partnerapi.UpdateAvailability200JSONResponse(resp), nil
}

func (h *Handlers) ListPartnerOrders(ctx context.Context, request partnerapi.ListPartnerOrdersRequestObject) (partnerapi.ListPartnerOrdersResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	var status *domainorder.Status
	if request.Params.Status != nil {
		s := domainorder.Status(*request.Params.Status)
		status = &s
	}
	limit := 50
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}

	orders, err := h.Orders.ListOrders(ctx, venueID, status, request.Params.Since, limit)
	if err != nil {
		return nil, err
	}
	resp := make(partnerapi.ListPartnerOrders200JSONResponse, len(orders))
	for i, o := range orders {
		resp[i] = toPartnerOrder(&o)
	}
	return resp, nil
}

func (h *Handlers) AcceptOrder(ctx context.Context, request partnerapi.AcceptOrderRequestObject) (partnerapi.AcceptOrderResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}
	if request.Body.EtaMinutes < 1 {
		return nil, errs.New(errs.CodeValidationError, "etaMinutes must be positive")
	}

	o, err := h.Orders.AcceptOrder(ctx, venueID, request.OrderId, request.Body.EtaMinutes, request.Body.ExternalOrderId)
	if err != nil {
		return nil, err
	}
	return partnerapi.AcceptOrder200JSONResponse(toPartnerOrder(o)), nil
}

func (h *Handlers) RejectOrder(ctx context.Context, request partnerapi.RejectOrderRequestObject) (partnerapi.RejectOrderResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil || request.Body.Reason == "" {
		return nil, errs.New(errs.CodeValidationError, "reason must not be empty")
	}

	o, err := h.Orders.RejectOrder(ctx, venueID, request.OrderId, request.Body.Reason)
	if err != nil {
		return nil, err
	}
	return partnerapi.RejectOrder200JSONResponse(toPartnerOrder(o)), nil
}

func (h *Handlers) AdvanceOrderStatus(ctx context.Context, request partnerapi.AdvanceOrderStatusRequestObject) (partnerapi.AdvanceOrderStatusResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}

	o, err := h.Orders.AdvanceStatus(ctx, venueID, request.OrderId, domainorder.Status(request.Body.Status))
	if err != nil {
		return nil, err
	}
	return partnerapi.AdvanceOrderStatus200JSONResponse(toPartnerOrder(o)), nil
}

func (h *Handlers) ListPartnerRescueOffers(ctx context.Context, request partnerapi.ListPartnerRescueOffersRequestObject) (partnerapi.ListPartnerRescueOffersResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	activeOnly := request.Params.ActiveOnly != nil && *request.Params.ActiveOnly

	offers, err := h.RescueOffers.ListOffers(ctx, venueID, activeOnly)
	if err != nil {
		return nil, err
	}

	out := make([]partnerapi.RescueOffer, len(offers))
	for i, o := range offers {
		out[i] = toPartnerRescueOffer(o)
	}
	return partnerapi.ListPartnerRescueOffers200JSONResponse(out), nil
}

func (h *Handlers) CreateRescueOffer(ctx context.Context, request partnerapi.CreateRescueOfferRequestObject) (partnerapi.CreateRescueOfferResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}

	offer, err := h.RescueOffers.CreateOffer(ctx, venueID, partnerusecase.NewRescueOfferRequest{
		MenuItemID:      request.Body.MenuItemId,
		DiscountPercent: request.Body.DiscountPercent,
		Quantity:        request.Body.Quantity,
		StartsAt:        request.Body.StartsAt,
		EndsAt:          request.Body.EndsAt,
	})
	if err != nil {
		return nil, err
	}
	return partnerapi.CreateRescueOffer201JSONResponse(toPartnerRescueOffer(*offer)), nil
}

func (h *Handlers) CancelRescueOffer(ctx context.Context, request partnerapi.CancelRescueOfferRequestObject) (partnerapi.CancelRescueOfferResponseObject, error) {
	venueID, err := venueIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.RescueOffers.CancelOffer(ctx, venueID, request.OfferId); err != nil {
		return nil, err
	}
	return partnerapi.CancelRescueOffer204Response{}, nil
}

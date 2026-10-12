package public

import (
	"context"
	"food-delivery-mvp/internal/domain/errs"
	"food-delivery-mvp/internal/generated/publicapi"

	catalogusecase "food-delivery-mvp/internal/usecase/catalog"
	orderusecase "food-delivery-mvp/internal/usecase/order"
)

// Handlers implements publicapi.StrictServerInterface. Every method just
// maps a request to a use-case call and its result to a wire type — no
// business rule lives here.
type Handlers struct {
	Catalog  *catalogusecase.Service
	Cart     *orderusecase.CartService
	Checkout *orderusecase.CheckoutService
	Orders   *orderusecase.Service
}

var _ publicapi.StrictServerInterface = (*Handlers)(nil)

func (h *Handlers) ListVenues(ctx context.Context, request publicapi.ListVenuesRequestObject) (publicapi.ListVenuesResponseObject, error) {
	filter := catalogusecase.ListVenuesFilter{}
	if request.Params.Cuisine != nil {
		filter.Cuisine = *request.Params.Cuisine
	}
	if request.Params.Q != nil {
		filter.NamePrefix = *request.Params.Q
	}
	if request.Params.IsOpen != nil {
		filter.OnlyOpen = *request.Params.IsOpen
	}
	if request.Params.Cursor != nil {
		filter.Cursor = *request.Params.Cursor
	}
	if request.Params.Limit != nil {
		filter.Limit = *request.Params.Limit
	}

	result, err := h.Catalog.ListVenues(ctx, filter)
	if err != nil {
		return nil, err
	}

	items := make([]publicapi.Venue, len(result.Items))
	for i, v := range result.Items {
		items[i] = toVenue(v)
	}
	page := publicapi.VenueListPage{Items: items}
	if result.NextCursor != "" {
		page.NextCursor = &result.NextCursor
	}
	return publicapi.ListVenues200JSONResponse(page), nil
}

func (h *Handlers) GetVenue(ctx context.Context, request publicapi.GetVenueRequestObject) (publicapi.GetVenueResponseObject, error) {
	view, err := h.Catalog.GetVenue(ctx, request.VenueId)
	if err != nil {
		return nil, err
	}
	return publicapi.GetVenue200JSONResponse(toVenueDetail(view)), nil
}

func (h *Handlers) GetVenueMenu(ctx context.Context, request publicapi.GetVenueMenuRequestObject) (publicapi.GetVenueMenuResponseObject, error) {
	ifNoneMatch := ""
	if request.Params.IfNoneMatch != nil {
		ifNoneMatch = *request.Params.IfNoneMatch
	}

	result, err := h.Catalog.GetMenu(ctx, request.VenueId, ifNoneMatch)
	if err != nil {
		return nil, err
	}
	if result.NotModified {
		return publicapi.GetVenueMenu304Response{}, nil
	}

	etag := result.ETag
	return publicapi.GetVenueMenu200JSONResponse{
		Body:    toMenu(result.Menu),
		Headers: publicapi.GetVenueMenu200ResponseHeaders{ETag: &etag},
	}, nil
}

func (h *Handlers) GetCart(ctx context.Context, request publicapi.GetCartRequestObject) (publicapi.GetCartResponseObject, error) {
	view, err := h.Cart.GetCart(ctx, request.Params.XClientId)
	if err != nil {
		return nil, err
	}
	return publicapi.GetCart200JSONResponse(toCart(view)), nil
}

func (h *Handlers) ClearCart(ctx context.Context, request publicapi.ClearCartRequestObject) (publicapi.ClearCartResponseObject, error) {
	if err := h.Cart.ClearCart(ctx, request.Params.XClientId); err != nil {
		return nil, err
	}
	return publicapi.ClearCart204Response{}, nil
}

func (h *Handlers) AddCartItem(ctx context.Context, request publicapi.AddCartItemRequestObject) (publicapi.AddCartItemResponseObject, error) {
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}
	view, err := h.Cart.AddItem(ctx, request.Params.XClientId, request.Body.MenuItemId, request.Body.Quantity)
	if err != nil {
		return nil, err
	}
	return publicapi.AddCartItem200JSONResponse(toCart(view)), nil
}

func (h *Handlers) UpdateCartItem(ctx context.Context, request publicapi.UpdateCartItemRequestObject) (publicapi.UpdateCartItemResponseObject, error) {
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}
	view, err := h.Cart.UpdateItemQuantity(ctx, request.Params.XClientId, request.ItemId, request.Body.Quantity)
	if err != nil {
		return nil, err
	}
	return publicapi.UpdateCartItem200JSONResponse(toCart(view)), nil
}

func (h *Handlers) RemoveCartItem(ctx context.Context, request publicapi.RemoveCartItemRequestObject) (publicapi.RemoveCartItemResponseObject, error) {
	view, err := h.Cart.RemoveItem(ctx, request.Params.XClientId, request.ItemId)
	if err != nil {
		return nil, err
	}
	return publicapi.RemoveCartItem200JSONResponse(toCart(view)), nil
}

func (h *Handlers) CreateOrder(ctx context.Context, request publicapi.CreateOrderRequestObject) (publicapi.CreateOrderResponseObject, error) {
	if request.Body == nil {
		return nil, errs.New(errs.CodeValidationError, "request body is required")
	}
	if request.Body.DeliveryAddress == "" {
		return nil, errs.New(errs.CodeValidationError, "deliveryAddress must not be empty")
	}
	if request.Body.CustomerPhone == "" {
		return nil, errs.New(errs.CodeValidationError, "customerPhone must not be empty")
	}
	comment := ""
	if request.Body.Comment != nil {
		comment = *request.Body.Comment
	}

	o, replayed, err := h.Checkout.PlaceOrder(ctx, request.Params.XClientId, request.Params.IdempotencyKey,
		request.Body.DeliveryAddress, request.Body.CustomerPhone, comment)
	if err != nil {
		return nil, err
	}

	if replayed {
		return publicapi.CreateOrder200JSONResponse(toOrder(o)), nil
	}
	return publicapi.CreateOrder201JSONResponse(toOrder(o)), nil
}

func (h *Handlers) GetOrder(ctx context.Context, request publicapi.GetOrderRequestObject) (publicapi.GetOrderResponseObject, error) {
	o, err := h.Orders.GetOrder(ctx, request.Params.XClientId, request.OrderId)
	if err != nil {
		return nil, err
	}
	return publicapi.GetOrder200JSONResponse(toOrder(o)), nil
}

func (h *Handlers) CancelOrder(ctx context.Context, request publicapi.CancelOrderRequestObject) (publicapi.CancelOrderResponseObject, error) {
	o, err := h.Orders.CancelOrder(ctx, request.Params.XClientId, request.OrderId)
	if err != nil {
		return nil, err
	}
	return publicapi.CancelOrder200JSONResponse(toOrder(o)), nil
}

func (h *Handlers) ListRescueOffers(ctx context.Context, request publicapi.ListRescueOffersRequestObject) (publicapi.ListRescueOffersResponseObject, error) {
	cursor := ""
	if request.Params.Cursor != nil {
		cursor = *request.Params.Cursor
	}
	limit := 0
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}

	page, err := h.Catalog.ListRescueOffers(ctx, cursor, limit)
	if err != nil {
		return nil, err
	}

	items := make([]publicapi.RescueOfferFeedEntry, len(page.Items))
	for i, e := range page.Items {
		items[i] = toRescueOfferFeedEntry(e)
	}
	out := publicapi.RescueOfferListPage{Items: items}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return publicapi.ListRescueOffers200JSONResponse(out), nil
}

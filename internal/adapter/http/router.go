// Package http assembles the top-level chi router: shared middleware, the
// public API under /api/v1, the partner API under /api/v1/partner, and
// operational plumbing.
package http

import (
	"food-delivery-mvp/internal/adapter/http/httperr"
	"food-delivery-mvp/internal/adapter/http/middleware"
	"food-delivery-mvp/internal/adapter/http/partner"
	"food-delivery-mvp/internal/adapter/http/public"
	"food-delivery-mvp/internal/adapter/postgres"
	"food-delivery-mvp/internal/adapter/webhook"
	"food-delivery-mvp/internal/generated/partnerapi"
	"food-delivery-mvp/internal/generated/publicapi"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	catalogusecase "food-delivery-mvp/internal/usecase/catalog"
	orderusecase "food-delivery-mvp/internal/usecase/order"
	outboxusecase "food-delivery-mvp/internal/usecase/outbox"
	partnerusecase "food-delivery-mvp/internal/usecase/partner"
)

// NewRouter builds the full HTTP handler for the API service, plus the
// background outbox Dispatcher (docs/PLAN.md 6.5/7.4) — returned rather than
// started here, so cmd/api decides when to run it and how to stop it
// alongside the HTTP server on shutdown.
func NewRouter(logger *slog.Logger, pool *pgxpool.Pool) (http.Handler, *outboxusecase.Dispatcher) {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logging(logger))
	r.Use(middleware.Recover(logger))
	r.Use(middleware.CORS)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", handleReadyz(pool))

	outboxRepo := postgres.NewOutboxRepository(pool)
	orders := postgres.NewOrderRepository(pool)
	publisher := webhook.NewPublisher(orders, nil)
	dispatcher := outboxusecase.NewDispatcher(outboxRepo, publisher)

	r.Mount("/api/v1", newPublicAPIRouter(pool, orders, outboxRepo))
	r.Mount("/api/v1/partner", newPartnerAPIRouter(pool, orders))

	return r, dispatcher
}

// newPublicAPIRouter wires the public API's use-cases onto their Postgres
// repositories and mounts the generated strict server. adapter/postgres's
// MenuRepository is passed to both catalog's own MenuRepository port and
// order's MenuItemLookup port — one adapter type satisfying two
// usecase-declared interfaces, per docs/PLAN.md 6.2 (see that type's doc
// comment for why).
func newPublicAPIRouter(pool *pgxpool.Pool, orders *postgres.OrderRepository, outboxRepo *postgres.OutboxRepository) http.Handler {
	venues := postgres.NewVenueRepository(pool)
	menus := postgres.NewMenuRepository(pool)
	carts := postgres.NewCartRepository(pool)
	rescueOffers := postgres.NewRescueOfferRepository(pool)
	idempotency := postgres.NewIdempotencyRepository(pool)
	txManager := postgres.NewTxManager(pool)

	handlers := &public.Handlers{
		Catalog:  catalogusecase.NewService(venues, menus, rescueOffers),
		Cart:     orderusecase.NewCartService(carts, menus, rescueOffers, txManager),
		Checkout: orderusecase.NewCheckoutService(carts, venues, menus, rescueOffers, orders, idempotency, outboxRepo, txManager),
		Orders:   orderusecase.NewOrderService(orders, outboxRepo, txManager),
	}

	strictHandler := publicapi.NewStrictHandlerWithOptions(handlers, nil, publicapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  httperr.WriteRequestError,
		ResponseErrorHandlerFunc: httperr.Write,
	})

	apiRouter := chi.NewRouter()
	return publicapi.HandlerWithOptions(strictHandler, publicapi.ChiServerOptions{
		BaseRouter:       apiRouter,
		ErrorHandlerFunc: httperr.WriteRequestError,
	})
}

// newPartnerAPIRouter wires the partner API. Every route requires
// X-Api-Key (docs/PLAN.md 5.3 item 1), enforced by partner.APIKeyAuth before
// any generated handler runs — it resolves the key to a venue and stores
// it in the request context, which every Handlers method then reads
// instead of taking a venue id as a request parameter.
func newPartnerAPIRouter(pool *pgxpool.Pool, orders *postgres.OrderRepository) http.Handler {
	venues := postgres.NewVenueRepository(pool)
	menus := postgres.NewMenuRepository(pool)
	auth := postgres.NewPartnerAuthRepository(pool)
	txManager := postgres.NewTxManager(pool)

	rescueOffers := postgres.NewRescueOfferRepository(pool)

	handlers := &partner.Handlers{
		Venues:       partnerusecase.NewVenueService(venues, txManager),
		Menus:        partnerusecase.NewMenuService(menus),
		Orders:       partnerusecase.NewOrderService(orders),
		RescueOffers: partnerusecase.NewRescueOfferService(rescueOffers),
	}

	strictHandler := partnerapi.NewStrictHandlerWithOptions(handlers, nil, partnerapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  httperr.WritePartnerRequestError,
		ResponseErrorHandlerFunc: httperr.WritePartner,
	})

	apiRouter := chi.NewRouter()
	apiRouter.Use(partner.APIKeyAuth(partnerusecase.NewAuthService(auth)))
	return partnerapi.HandlerWithOptions(strictHandler, partnerapi.ChiServerOptions{
		BaseRouter:       apiRouter,
		ErrorHandlerFunc: httperr.WritePartnerRequestError,
	})
}

package kitchen

import (
	"encoding/json"
	"net/http"
	"venue-pasta-roma/internal/generated/partnerclient"

	"github.com/go-chi/chi/v5"
)

// NewRouter wires this venue's own tiny HTTP API (docs/PLAN.md 8.2): the
// webhook receiver, the kitchen's own order feed, and manual stock
// adjustment.
func (h *Handler) NewRouter() http.Handler {
	r := chi.NewRouter()
	r.Post("/webhooks/orders", h.HandleWebhook)
	r.Get("/kitchen/orders", h.handleListOrders)
	r.Post("/kitchen/stock", h.handleSetStock)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return r
}

// handleListOrders is GET /kitchen/orders — "the kitchen's own feed",
// docs/PLAN.md 8.2's stand-in for the internal interface a real restaurant's
// staff would look at.
func (h *Handler) handleListOrders(w http.ResponseWriter, _ *http.Request) {
	orders := h.state.ListOrders()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(orders); err != nil {
		h.logger.Error("encode kitchen orders", "error", err)
	}
}

type setStockRequest struct {
	ExternalID string `json:"externalId"`
	StockQty   int    `json:"stockQty"`
}

// handleSetStock is POST /kitchen/stock — a manual stock correction by
// kitchen staff, translated into the platform immediately rather than
// waiting for the next periodic sync (docs/PLAN.md 8.2 item 3: "транслируется
// в платформу").
func (h *Handler) handleSetStock(w http.ResponseWriter, r *http.Request) {
	var req setStockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	platformID, ok := h.state.SetStockByExternalID(req.ExternalID, req.StockQty)
	if !ok {
		http.Error(w, "unknown externalId", http.StatusNotFound)
		return
	}

	isAvailable := req.StockQty > 0
	stockQty := req.StockQty
	resp, err := h.client.UpdateAvailabilityWithResponse(r.Context(), partnerclient.UpdateAvailabilityJSONRequestBody{
		Items: []partnerclient.AvailabilityUpdate{
			{MenuItemId: platformID, IsAvailable: &isAvailable, StockQty: &stockQty},
		},
	})
	if err != nil || resp.JSON200 == nil {
		h.logger.Error("push stock update to platform", "external_id", req.ExternalID, "error", err)
		http.Error(w, "stock updated locally but the platform sync failed", http.StatusBadGateway)
		return
	}

	w.WriteHeader(http.StatusOK)
}

package kitchen

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
	"venue-pasta-roma/internal/generated/partnerclient"

	"github.com/google/uuid"
)

// envelope mirrors docs/PLAN.md 6.5's fixed event format exactly — this
// service only has the spec and the README's documented contract to go on,
// the same as any real integrator would, so it defines its own copy rather
// than importing the platform's internal type for it.
type envelope struct {
	EventID       uuid.UUID       `json:"event_id"`
	EventType     string          `json:"event_type"`
	EventVersion  int             `json:"event_version"`
	OccurredAt    time.Time       `json:"occurred_at"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   uuid.UUID       `json:"aggregate_id"`
	Payload       json.RawMessage `json:"payload"`
}

type orderCreatedPayload struct {
	OrderID         uuid.UUID `json:"order_id"`
	DeliveryAddress string    `json:"delivery_address"`
	Items           []struct {
		MenuItemID uuid.UUID `json:"menu_item_id"`
		Name       string    `json:"name"`
		Quantity   int       `json:"quantity"`
	} `json:"items"`
}

type orderCancelledPayload struct {
	OrderID uuid.UUID `json:"order_id"`
}

// estimateETAMinutes is this emulator's own stand-in for a kitchen actually
// timing itself: a base prep time, plus a little more per dish on the
// order (more plates, more time), plus a little more for every other order
// already in flight (a busier kitchen quotes longer). The exact
// coefficients are this service's own choice — docs/PLAN.md 8.2 asks for "an
// estimate", not a specific formula — clamped to a plausible 10-60 minute
// range so neither a one-item order nor a slammed kitchen produces a
// nonsense number.
func estimateETAMinutes(items []OrderItem, activeOrders int) int {
	const (
		baseMinutes        = 15
		perItemMinutes     = 3
		perActiveOrderLoad = 2
		minETA             = 10
		maxETA             = 60
	)

	totalQuantity := 0
	for _, line := range items {
		totalQuantity += line.Quantity
	}

	eta := baseMinutes + perItemMinutes*totalQuantity + perActiveOrderLoad*activeOrders
	if eta < minETA {
		return minETA
	}
	if eta > maxETA {
		return maxETA
	}
	return eta
}

// verifySignature reports whether signatureHeader is the correct
// HMAC-SHA256 hex digest of body under secret — pulled out as its own pure
// function so it can be unit-tested without spinning up an HTTP server.
func verifySignature(secret string, body []byte, signatureHeader string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(signatureHeader))
}

// Handler serves this venue's own tiny HTTP API (docs/PLAN.md 8.2).
type Handler struct {
	client        *partnerclient.ClientWithResponses
	state         *State
	webhookSecret string
	cookStep      time.Duration
	logger        *slog.Logger
}

func NewHandler(client *partnerclient.ClientWithResponses, state *State, webhookSecret string, cookStep time.Duration, logger *slog.Logger) *Handler {
	return &Handler{client: client, state: state, webhookSecret: webhookSecret, cookStep: cookStep, logger: logger}
}

// HandleWebhook is POST /webhooks/orders: verifies the HMAC signature,
// dedupes by X-Event-Id, and dispatches order.created/order.cancelled.
// docs/PLAN.md 7.4 puts the dedup obligation on the receiver, not the sender —
// this is that half of the contract.
func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}

	if !verifySignature(h.webhookSecret, body, r.Header.Get("X-Signature")) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		http.Error(w, "invalid event body", http.StatusBadRequest)
		return
	}

	if !h.state.MarkEventSeen(env.EventID) {
		h.logger.Info("duplicate webhook delivery ignored", "event_id", env.EventID)
		w.WriteHeader(http.StatusOK)
		return
	}

	ctx := r.Context()
	switch env.EventType {
	case "order.created":
		h.handleOrderCreated(ctx, env.Payload)
	case "order.cancelled":
		h.handleOrderCancelled(env.Payload)
	default:
		h.logger.Info("ignoring unknown event type", "event_type", env.EventType)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleOrderCreated(ctx context.Context, raw json.RawMessage) {
	var p orderCreatedPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		h.logger.Error("invalid order.created payload", "error", err)
		return
	}

	items := make([]OrderItem, len(p.Items))
	for i, line := range p.Items {
		items[i] = OrderItem{PlatformMenuItemID: line.MenuItemID, Name: line.Name, Quantity: line.Quantity}
	}

	if h.state.CheckAndReserve(items) {
		h.acceptOrder(ctx, p.OrderID, items, p.DeliveryAddress)
		return
	}
	h.rejectOrder(ctx, p.OrderID, "insufficient stock in the venue's own system")
}

// acceptOrder reserves nothing new (CheckAndReserve already did, in the
// caller) — it only tries to tell the platform. If that call fails for any
// reason other than the platform already knowing about it (a 409, treated
// as "some earlier attempt actually landed"), the order is tracked as
// pending_accept rather than lost: stock stays reserved, and
// ticker.go's retryPendingActions will keep trying until it succeeds.
func (h *Handler) acceptOrder(ctx context.Context, orderID uuid.UUID, items []OrderItem, deliveryAddress string) {
	eta := estimateETAMinutes(items, h.state.ActiveOrderCount())
	externalOrderID := fmt.Sprintf("PR-%s", orderID.String()[:8])

	resp, err := h.client.AcceptOrderWithResponse(ctx, orderID, partnerclient.AcceptOrderJSONRequestBody{
		EtaMinutes:      eta,
		ExternalOrderId: &externalOrderID,
	})
	order := Order{
		ID:              orderID,
		Items:           items,
		DeliveryAddress: deliveryAddress,
		Accepted:        true,
		ETAMinutes:      eta,
		ExternalOrderID: externalOrderID,
	}

	if err == nil && (resp.JSON200 != nil || resp.JSON409 != nil) {
		order.Status = "confirmed"
		order.NextAdvanceAt = time.Now().Add(h.cookStep)
		h.state.AddOrder(order)
		h.logger.Info("order accepted", "order_id", orderID, "external_order_id", externalOrderID)
		return
	}

	status := 0
	if resp != nil {
		status = resp.StatusCode()
	}
	h.logger.Error("accept order failed, will retry", "order_id", orderID, "error", err, "status", status)
	order.Status = "pending_accept"
	order.Pending = PendingAccept
	order.NextRetryAt = time.Now().Add(h.cookStep)
	h.state.AddOrder(order)
}

// rejectOrder mirrors acceptOrder's own retry logic, but never touches
// stock — CheckAndReserve never reserved any for a rejected order.
func (h *Handler) rejectOrder(ctx context.Context, orderID uuid.UUID, reason string) {
	resp, err := h.client.RejectOrderWithResponse(ctx, orderID, partnerclient.RejectOrderJSONRequestBody{Reason: reason})
	order := Order{ID: orderID, Accepted: false, RejectionReason: reason}

	if err == nil && (resp.JSON200 != nil || resp.JSON409 != nil) {
		order.Status = "rejected"
		h.state.AddOrder(order)
		h.logger.Info("order rejected", "order_id", orderID, "reason", reason)
		return
	}

	status := 0
	if resp != nil {
		status = resp.StatusCode()
	}
	h.logger.Error("reject order failed, will retry", "order_id", orderID, "error", err, "status", status)
	order.Status = "pending_reject"
	order.Pending = PendingReject
	order.NextRetryAt = time.Now().Add(h.cookStep)
	h.state.AddOrder(order)
}

func (h *Handler) handleOrderCancelled(raw json.RawMessage) {
	var p orderCancelledPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		h.logger.Error("invalid order.cancelled payload", "error", err)
		return
	}
	items, ok := h.state.CancelOrder(p.OrderID)
	if !ok {
		// A cancellation for an order this service never accepted (it was
		// rejected, or the webhook for order.created never arrived) — the
		// platform's own state is still the source of truth, nothing more
		// to reconcile here.
		return
	}
	h.state.ReleaseStock(items)
	h.logger.Info("order canceled", "order_id", p.OrderID)
}

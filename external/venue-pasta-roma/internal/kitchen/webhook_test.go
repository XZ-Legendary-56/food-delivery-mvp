package kitchen

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hexHMAC computes the signature independently of webhook.go's own
// verifySignature, so a bug in that function could not accidentally cancel
// itself out against these tests.
func hexHMAC(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

const testWebhookSecret = "test-secret"

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func signedRequest(t *testing.T, secret string, env envelope) *http.Request {
	t.Helper()
	body, err := json.Marshal(env)
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/orders", nil)
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("X-Signature", signBody(secret, body))
	req.Header.Set("X-Event-Id", env.EventID.String())
	return req
}

func signBody(secret string, body []byte) string {
	// Re-derive independently of verifySignature so a bug in verifySignature
	// itself would not silently cancel out against this test.
	return hexHMAC(secret, body)
}

func orderCreatedEnvelope(orderID, itemID uuid.UUID, quantity int) envelope {
	payload, _ := json.Marshal(struct {
		OrderID         uuid.UUID `json:"order_id"`
		DeliveryAddress string    `json:"delivery_address"`
		Items           []struct {
			MenuItemID uuid.UUID `json:"menu_item_id"`
			Name       string    `json:"name"`
			Quantity   int       `json:"quantity"`
		} `json:"items"`
	}{
		OrderID:         orderID,
		DeliveryAddress: "Test street 1",
		Items: []struct {
			MenuItemID uuid.UUID `json:"menu_item_id"`
			Name       string    `json:"name"`
			Quantity   int       `json:"quantity"`
		}{{MenuItemID: itemID, Name: "Carbonara", Quantity: quantity}},
	})
	return envelope{
		EventID:       uuid.New(),
		EventType:     "order.created",
		EventVersion:  1,
		OccurredAt:    time.Now(),
		AggregateType: "order",
		AggregateID:   orderID,
		Payload:       payload,
	}
}

func orderCancelledEnvelope(orderID uuid.UUID) envelope {
	payload, _ := json.Marshal(struct {
		OrderID uuid.UUID `json:"order_id"`
	}{OrderID: orderID})
	return envelope{
		EventID:       uuid.New(),
		EventType:     "order.cancelled",
		EventVersion:  1,
		OccurredAt:    time.Now(),
		AggregateType: "order",
		AggregateID:   orderID,
		Payload:       payload,
	}
}

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	good := hexHMAC("secret", body)

	assert.True(t, verifySignature("secret", body, good))
	assert.False(t, verifySignature("secret", body, "deadbeef"))
	assert.False(t, verifySignature("wrong-secret", body, good))
}

func TestHandleWebhook_InvalidSignature_Rejected(t *testing.T) {
	platform := newFakePlatform(t)
	h := NewHandler(platform.client(t), NewState(), testWebhookSecret, time.Second, discardLogger())

	req := signedRequest(t, "wrong-secret", orderCreatedEnvelope(uuid.New(), uuid.New(), 1))
	rec := httptest.NewRecorder()
	h.HandleWebhook(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, platform.acceptCalls)
}

func TestHandleWebhook_OrderCreated_SufficientStock_Accepts(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()
	itemID := uuid.New()
	state.SetMenu([]MenuItem{{PlatformID: itemID, ExternalID: "pr-carbonara", StockQty: 5}})
	h := NewHandler(platform.client(t), state, testWebhookSecret, time.Second, discardLogger())

	orderID := uuid.New()
	req := signedRequest(t, testWebhookSecret, orderCreatedEnvelope(orderID, itemID, 2))
	rec := httptest.NewRecorder()
	h.HandleWebhook(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, platform.acceptCalls, 1)
	assert.Equal(t, orderID.String(), platform.acceptCalls[0])
	assert.Equal(t, 3, state.StockSnapshot()[0].StockQty)

	tracked := state.GetOrder(orderID)
	require.NotNil(t, tracked)
	assert.Equal(t, "confirmed", tracked.Status)
}

func TestHandleWebhook_OrderCreated_InsufficientStock_Rejects(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()
	itemID := uuid.New()
	state.SetMenu([]MenuItem{{PlatformID: itemID, ExternalID: "pr-carbonara", StockQty: 1}})
	h := NewHandler(platform.client(t), state, testWebhookSecret, time.Second, discardLogger())

	orderID := uuid.New()
	req := signedRequest(t, testWebhookSecret, orderCreatedEnvelope(orderID, itemID, 5))
	rec := httptest.NewRecorder()
	h.HandleWebhook(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, platform.rejectCalls, 1)
	assert.Empty(t, platform.acceptCalls)
	assert.Equal(t, 1, state.StockSnapshot()[0].StockQty, "a rejected order must not touch stock")
}

func TestHandleWebhook_OrderCreated_AcceptCallFails_TracksPendingAndKeepsStockReserved(t *testing.T) {
	platform := newFakePlatform(t)
	platform.acceptResult = fakeServerError
	state := NewState()
	itemID := uuid.New()
	state.SetMenu([]MenuItem{{PlatformID: itemID, ExternalID: "pr-carbonara", StockQty: 5}})
	h := NewHandler(platform.client(t), state, testWebhookSecret, time.Second, discardLogger())

	orderID := uuid.New()
	req := signedRequest(t, testWebhookSecret, orderCreatedEnvelope(orderID, itemID, 2))
	rec := httptest.NewRecorder()
	h.HandleWebhook(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "a failed downstream call must not itself fail the webhook delivery")
	require.Len(t, platform.acceptCalls, 1)

	tracked := state.GetOrder(orderID)
	require.NotNil(t, tracked)
	assert.Equal(t, "pending_accept", tracked.Status)
	assert.Equal(t, PendingAccept, tracked.Pending)
	assert.Equal(t, 3, state.StockSnapshot()[0].StockQty, "stock must stay reserved while accept is only pending, not abandoned")
}

func TestHandleWebhook_OrderCreated_RejectCallFails_TracksPending(t *testing.T) {
	platform := newFakePlatform(t)
	platform.rejectResult = fakeServerError
	state := NewState()
	itemID := uuid.New()
	state.SetMenu([]MenuItem{{PlatformID: itemID, ExternalID: "pr-carbonara", StockQty: 1}})
	h := NewHandler(platform.client(t), state, testWebhookSecret, time.Second, discardLogger())

	orderID := uuid.New()
	req := signedRequest(t, testWebhookSecret, orderCreatedEnvelope(orderID, itemID, 5))
	h.HandleWebhook(httptest.NewRecorder(), req)

	tracked := state.GetOrder(orderID)
	require.NotNil(t, tracked)
	assert.Equal(t, "pending_reject", tracked.Status)
	assert.Equal(t, PendingReject, tracked.Pending)
}

func TestHandleWebhook_DuplicateEventID_ProcessedOnce(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()
	itemID := uuid.New()
	state.SetMenu([]MenuItem{{PlatformID: itemID, ExternalID: "pr-carbonara", StockQty: 5}})
	h := NewHandler(platform.client(t), state, testWebhookSecret, time.Second, discardLogger())

	env := orderCreatedEnvelope(uuid.New(), itemID, 1)
	body, err := json.Marshal(env)
	require.NoError(t, err)
	signature := hexHMAC(testWebhookSecret, body)

	deliver := func() *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/orders", nil)
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.Header.Set("X-Signature", signature)
		rec := httptest.NewRecorder()
		h.HandleWebhook(rec, req)
		return rec
	}

	first := deliver()
	second := deliver()

	assert.Equal(t, http.StatusOK, first.Code)
	assert.Equal(t, http.StatusOK, second.Code)
	assert.Len(t, platform.acceptCalls, 1, "a redelivered event must not be processed twice")
	assert.Equal(t, 4, state.StockSnapshot()[0].StockQty, "stock must be decremented once, not once per delivery")
}

func TestHandleWebhook_OrderCancelled_ReleasesStock(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()
	itemID := uuid.New()
	state.SetMenu([]MenuItem{{PlatformID: itemID, ExternalID: "pr-carbonara", StockQty: 5}})
	h := NewHandler(platform.client(t), state, testWebhookSecret, time.Second, discardLogger())

	orderID := uuid.New()
	acceptReq := signedRequest(t, testWebhookSecret, orderCreatedEnvelope(orderID, itemID, 2))
	h.HandleWebhook(httptest.NewRecorder(), acceptReq)
	require.Equal(t, 3, state.StockSnapshot()[0].StockQty)

	cancelReq := signedRequest(t, testWebhookSecret, orderCancelledEnvelope(orderID))
	rec := httptest.NewRecorder()
	h.HandleWebhook(rec, cancelReq)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 5, state.StockSnapshot()[0].StockQty, "canceling an accepted order must give its stock back")
	assert.Equal(t, "canceled", state.GetOrder(orderID).Status)
}

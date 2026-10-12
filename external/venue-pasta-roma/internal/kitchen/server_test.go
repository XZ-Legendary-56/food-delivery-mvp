package kitchen

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleListOrders_ReturnsTrackedOrders(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()
	state.AddOrder(Order{ID: uuid.New(), Status: "confirmed"})
	h := NewHandler(platform.client(t), state, testWebhookSecret, time.Second, discardLogger())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/kitchen/orders", nil)
	rec := httptest.NewRecorder()
	h.NewRouter().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var got []Order
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Len(t, got, 1)
}

func TestHandleSetStock_UnknownExternalID_NotFound(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()
	h := NewHandler(platform.client(t), state, testWebhookSecret, time.Second, discardLogger())

	body, _ := json.Marshal(setStockRequest{ExternalID: "does-not-exist", StockQty: 5})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/kitchen/stock", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.NewRouter().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, 0, platform.availUpdates)
}

func TestHandleSetStock_UpdatesLocallyAndPushesToPlatform(t *testing.T) {
	platform := newFakePlatform(t)
	state := NewState()
	state.SetMenu([]MenuItem{{PlatformID: uuid.New(), ExternalID: "pr-carbonara", StockQty: 5}})
	h := NewHandler(platform.client(t), state, testWebhookSecret, time.Second, discardLogger())

	body, _ := json.Marshal(setStockRequest{ExternalID: "pr-carbonara", StockQty: 20})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/kitchen/stock", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.NewRouter().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, platform.availUpdates)
	assert.Equal(t, 20, state.StockSnapshot()[0].StockQty)
}

func TestHandleSetStock_InvalidBody_BadRequest(t *testing.T) {
	platform := newFakePlatform(t)
	h := NewHandler(platform.client(t), NewState(), testWebhookSecret, time.Second, discardLogger())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/kitchen/stock", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()
	h.NewRouter().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

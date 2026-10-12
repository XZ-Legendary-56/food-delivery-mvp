package webhook_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"food-delivery-mvp/internal/adapter/webhook"
	"food-delivery-mvp/internal/usecase/outbox"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const testSecret = "shh-its-a-secret"

func TestPublisher_Publish_SignsBodyAndSetsHeaders(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueWebhookLookup(ctrl)

	e := outbox.Event{
		ID:            uuid.New(),
		Type:          "order.created",
		Version:       1,
		OccurredAt:    time.Now(),
		AggregateType: "order",
		AggregateID:   uuid.New(),
		Payload:       json.RawMessage(`{"order_id":"abc"}`),
	}

	var receivedBody []byte
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	venues.EXPECT().GetWebhookForOrder(gomock.Any(), e.AggregateID).Return(server.URL, testSecret, true, nil)

	p := webhook.NewPublisher(venues, server.Client())
	err := p.Publish(t.Context(), e)
	require.NoError(t, err)

	assert.Equal(t, e.ID.String(), receivedHeaders.Get("X-Event-Id"))
	assert.Equal(t, "order.created", receivedHeaders.Get("X-Event-Type"))

	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write(receivedBody)
	wantSignature := hex.EncodeToString(mac.Sum(nil))
	assert.Equal(t, wantSignature, receivedHeaders.Get("X-Signature"),
		"the signature must be over the exact bytes sent, so the receiver can recompute it the same way")

	var body map[string]any
	require.NoError(t, json.Unmarshal(receivedBody, &body))
	assert.Equal(t, e.ID.String(), body["event_id"])
	assert.Equal(t, "order.created", body["event_type"])
	assert.Equal(t, float64(1), body["event_version"])
	assert.Equal(t, "order", body["aggregate_type"])
	assert.Equal(t, e.AggregateID.String(), body["aggregate_id"])
	assert.NotNil(t, body["occurred_at"])
	assert.NotNil(t, body["payload"])
}

func TestPublisher_Publish_NoWebhookConfigured_IsNotAnError(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueWebhookLookup(ctrl)

	e := outbox.Event{ID: uuid.New(), AggregateID: uuid.New()}
	venues.EXPECT().GetWebhookForOrder(gomock.Any(), e.AggregateID).Return("", "", false, nil)

	p := webhook.NewPublisher(venues, nil)
	err := p.Publish(t.Context(), e)

	require.NoError(t, err)
}

func TestPublisher_Publish_NonSuccessStatus_IsAnError(t *testing.T) {
	ctrl := gomock.NewController(t)
	venues := NewMockVenueWebhookLookup(ctrl)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	e := outbox.Event{ID: uuid.New(), AggregateID: uuid.New()}
	venues.EXPECT().GetWebhookForOrder(gomock.Any(), e.AggregateID).Return(server.URL, testSecret, true, nil)

	p := webhook.NewPublisher(venues, server.Client())
	err := p.Publish(t.Context(), e)

	require.Error(t, err)
}

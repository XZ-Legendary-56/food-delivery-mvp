//go:build integration

package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"food-delivery-mvp/internal/adapter/webhook"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "food-delivery-mvp/internal/adapter/postgres"

	domainorder "food-delivery-mvp/internal/domain/order"
	orderusecase "food-delivery-mvp/internal/usecase/order"
	outboxusecase "food-delivery-mvp/internal/usecase/outbox"
)

const outboxWebhookSecret = "integration-test-secret"

// setVenueWebhook points venueID's webhook_url/webhook_secret at url —
// seedOpenVenue itself leaves both null, since most tests in this package
// have nothing to do with webhooks.
func setVenueWebhook(t *testing.T, pool *pgxpool.Pool, venueID uuid.UUID, url string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`UPDATE venues SET webhook_url = $2, webhook_secret = $3 WHERE id = $1`,
		venueID, url, outboxWebhookSecret)
	require.NoError(t, err)
}

func placeTestOrder(t *testing.T, pool *pgxpool.Pool, clientID uuid.UUID) *domainorder.Order {
	t.Helper()
	svc := orderusecase.NewCheckoutService(
		pgadapter.NewCartRepository(pool),
		pgadapter.NewVenueRepository(pool),
		pgadapter.NewMenuRepository(pool),
		pgadapter.NewRescueOfferRepository(pool),
		pgadapter.NewOrderRepository(pool),
		pgadapter.NewIdempotencyRepository(pool),
		pgadapter.NewOutboxRepository(pool),
		pgadapter.NewTxManager(pool),
	)
	o, _, err := svc.PlaceOrder(context.Background(), clientID, uuid.New().String(), "addr", "+70000000000", "")
	require.NoError(t, err)
	return o
}

func outboxEventStatus(t *testing.T, pool *pgxpool.Pool, orderID uuid.UUID) (status string, attempts int) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT status, attempts FROM outbox_events WHERE aggregate_id = $1`, orderID).
		Scan(&status, &attempts)
	require.NoError(t, err)
	return status, attempts
}

// TestOutboxDispatcher_DeliversSignedWebhookOnOrderCreated is docs/PLAN.md
// 6.5/7.4's end-to-end proof: placing an order enqueues an outbox row
// (checked by CheckoutService's own unit tests already), and this test
// picks up from there — the Dispatcher must actually deliver it as a
// correctly signed HTTP request and mark it sent.
func TestOutboxDispatcher_DeliversSignedWebhookOnOrderCreated(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	venueID := seedOpenVenue(t, pool)
	menuItemID := seedMenuItem(t, pool, venueID, 10)
	clientID := uuid.New()
	seedCartWithItem(t, pool, clientID, venueID, menuItemID, 2)

	var receivedBody []byte
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	setVenueWebhook(t, pool, venueID, server.URL)

	o := placeTestOrder(t, pool, clientID)

	outboxRepo := pgadapter.NewOutboxRepository(pool)
	publisher := webhook.NewPublisher(pgadapter.NewOrderRepository(pool), server.Client())
	dispatcher := outboxusecase.NewDispatcher(outboxRepo, publisher)

	n, err := dispatcher.ProcessOnce(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	require.NotNil(t, receivedHeaders, "the webhook receiver must have been called")
	assert.Equal(t, "order.created", receivedHeaders.Get("X-Event-Type"))
	assert.NotEmpty(t, receivedHeaders.Get("X-Event-Id"), "receivers dedupe on X-Event-Id")

	mac := hmac.New(sha256.New, []byte(outboxWebhookSecret))
	mac.Write(receivedBody)
	wantSignature := hex.EncodeToString(mac.Sum(nil))
	assert.Equal(t, wantSignature, receivedHeaders.Get("X-Signature"))

	var body struct {
		EventID     string          `json:"event_id"`
		EventType   string          `json:"event_type"`
		AggregateID string          `json:"aggregate_id"`
		Payload     json.RawMessage `json:"payload"`
	}
	require.NoError(t, json.Unmarshal(receivedBody, &body))
	assert.Equal(t, o.ID.String(), body.AggregateID)
	assert.Equal(t, receivedHeaders.Get("X-Event-Id"), body.EventID)

	status, attempts := outboxEventStatus(t, pool, o.ID)
	assert.Equal(t, "sent", status)
	assert.Equal(t, 0, attempts)
}

// TestOutboxDispatcher_RetriesOnFailureThenGivesUpAfterFiveAttempts covers
// docs/PLAN.md 7.4's other half: a webhook receiver that never succeeds must
// be retried with increasing backoff, then abandoned after the 5th attempt
// — not retried forever. Real backoff delays (30s-30min) would make this
// test far too slow, so the delay itself is not what's being tested here;
// instead outbox_events.next_attempt_at is fast-forwarded directly between
// polls, isolating "does the attempt counter and give-up threshold work"
// from "is the clock right", which Dispatcher's own unit tests already
// cover with a fake clock.
func TestOutboxDispatcher_RetriesOnFailureThenGivesUpAfterFiveAttempts(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	venueID := seedOpenVenue(t, pool)
	menuItemID := seedMenuItem(t, pool, venueID, 10)
	clientID := uuid.New()
	seedCartWithItem(t, pool, clientID, venueID, menuItemID, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	setVenueWebhook(t, pool, venueID, server.URL)

	o := placeTestOrder(t, pool, clientID)

	outboxRepo := pgadapter.NewOutboxRepository(pool)
	publisher := webhook.NewPublisher(pgadapter.NewOrderRepository(pool), server.Client())
	dispatcher := outboxusecase.NewDispatcher(outboxRepo, publisher)

	_, err := dispatcher.ProcessOnce(ctx, 10)
	require.NoError(t, err)
	status, attempts := outboxEventStatus(t, pool, o.ID)
	assert.Equal(t, "pending", status, "attempt 1 of 5 failing must not give up yet")
	assert.Equal(t, 1, attempts)

	// Skip straight to "4 attempts already failed" so this poll is the 5th
	// and last one, without waiting out four real backoff windows. The
	// fast-forwarded timestamp comes from Go's own clock, not SQL's now()
	// — Dispatcher.ProcessOnce compares against time.Now() on this same
	// machine, and comparing against a timestamp the database server
	// computed itself would make this flaky under any host/container clock
	// drift, however small.
	_, err = pool.Exec(ctx, `
		UPDATE outbox_events SET attempts = 4, next_attempt_at = $2 WHERE aggregate_id = $1
	`, o.ID, time.Now().Add(-time.Minute))
	require.NoError(t, err)

	_, err = dispatcher.ProcessOnce(ctx, 10)
	require.NoError(t, err)
	status, attempts = outboxEventStatus(t, pool, o.ID)
	assert.Equal(t, "failed", status, "the 5th consecutive failure must give up, not schedule a 6th try")
	assert.Equal(t, 5, attempts)

	// A failed webhook must never re-appear as "due" again.
	n, err := dispatcher.ProcessOnce(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

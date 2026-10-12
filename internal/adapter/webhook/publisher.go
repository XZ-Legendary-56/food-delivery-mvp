// Package webhook implements outbox.EventPublisher by delivering an event
// as a signed HTTP POST to a venue's own webhook_url (docs/PLAN.md 7.4).
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"food-delivery-mvp/internal/usecase/outbox"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// VenueWebhookLookup is this package's own port (docs/PLAN.md 6.2: an adapter
// may declare one when nothing in usecase already fits — the same reason
// adapter/http/partner declares its own authenticator), resolving an
// order's aggregate id to where and how to sign its webhook.
type VenueWebhookLookup interface {
	// GetWebhookForOrder returns ok=false when the order has no venue
	// webhook configured — not an error, just nothing to deliver.
	GetWebhookForOrder(ctx context.Context, orderID uuid.UUID) (url string, secret string, ok bool, err error)
}

// envelope is docs/PLAN.md 6.5's event format, verbatim — this is the one
// place that shape becomes JSON bytes on the wire, so the field names and
// order documented there (event_id, event_type, event_version, occurred_at,
// aggregate_type, aggregate_id, payload) live here, not scattered across
// callers.
type envelope struct {
	EventID       uuid.UUID       `json:"event_id"`
	EventType     string          `json:"event_type"`
	EventVersion  int             `json:"event_version"`
	OccurredAt    time.Time       `json:"occurred_at"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   uuid.UUID       `json:"aggregate_id"`
	Payload       json.RawMessage `json:"payload"`
}

// Publisher implements outbox.EventPublisher over plain HTTP. Its only
// implementation today; a future broker migration adds a sibling package,
// not a change here (see usecase/outbox's own package doc).
type Publisher struct {
	venues     VenueWebhookLookup
	httpClient *http.Client
}

func NewPublisher(venues VenueWebhookLookup, httpClient *http.Client) *Publisher {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Publisher{venues: venues, httpClient: httpClient}
}

var _ outbox.EventPublisher = (*Publisher)(nil)

// Publish resolves e's venue webhook (by e.AggregateID, an order id — the
// only aggregate type this project has), signs the envelope with the
// venue's own webhook secret, and POSTs it. A venue with no webhook_url
// configured is reported as success: there is truly nothing to deliver,
// which is a fixed fact about the venue, not a failure the Dispatcher
// should ever retry.
func (p *Publisher) Publish(ctx context.Context, e outbox.Event) error {
	url, secret, ok, err := p.venues.GetWebhookForOrder(ctx, e.AggregateID)
	if err != nil {
		return fmt.Errorf("resolve venue webhook for order %s: %w", e.AggregateID, err)
	}
	if !ok {
		return nil
	}

	body, err := json.Marshal(envelope{
		EventID:       e.ID,
		EventType:     e.Type,
		EventVersion:  e.Version,
		OccurredAt:    e.OccurredAt,
		AggregateType: e.AggregateType,
		AggregateID:   e.AggregateID,
		Payload:       e.Payload,
	})
	if err != nil {
		return fmt.Errorf("marshal webhook envelope: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Event-Id", e.ID.String())
	req.Header.Set("X-Event-Type", e.Type)
	req.Header.Set("X-Signature", sign(secret, body))

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("deliver webhook for order %s: %w", e.AggregateID, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook receiver returned status %d for order %s", resp.StatusCode, e.AggregateID)
	}
	return nil
}

// sign computes the hex-encoded HMAC-SHA256 of body using secret, carried
// in X-Signature — the receiver recomputes the same value over the raw body
// it received and compares, which is why signing must happen over the
// exact bytes sent, not a re-marshal of the same struct.
func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

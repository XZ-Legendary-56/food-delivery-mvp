package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// maxAttempts is docs/PLAN.md 7.4's "five attempts, then give up".
const maxAttempts = 5

// retryBackoff holds the four delays between attempts 1→2, 2→3, 3→4 and
// 4→5 — after the 5th attempt fails there is no next delay, the event is
// marked failed instead. Increasing rather than fixed (docs/PLAN.md 7.4)
// because a webhook failure is usually either a brief blip (30s is enough)
// or a real outage (30 minutes gives the venue's system real time to
// recover without hammering it every few seconds in between).
var retryBackoff = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	30 * time.Minute,
}

// Dispatcher polls Repository for due events and hands each to
// EventPublisher, recording the outcome. now is overridable for tests, not
// exported — production code always gets time.Now via NewDispatcher.
type Dispatcher struct {
	repo      Repository
	publisher EventPublisher
	now       func() time.Time
}

func NewDispatcher(repo Repository, publisher EventPublisher) *Dispatcher {
	return &Dispatcher{repo: repo, publisher: publisher, now: time.Now}
}

// ProcessOnce fetches up to limit due events and attempts each once,
// returning how many it looked at. It never opens a database transaction
// around a delivery attempt: an outbound HTTP call can take seconds, and
// holding a row lock (or even just a long-lived connection) for that long
// is not something the fetch/update statements below need to pay for.
//
// Several Dispatcher instances can safely call this concurrently: Repository
// is expected to atomically claim what it returns (adapter/postgres's
// implementation does so with SELECT ... FOR UPDATE SKIP LOCKED plus a
// short lease — see its own doc comment), so two instances never both
// deliver the same event under normal operation. docs/PLAN.md 7.4's own dedup
// contract (receivers keying off X-Event-Id) is the backstop for the rare
// case a claim's lease expires early (a crashed instance) and a second one
// picks the same event back up.
func (d *Dispatcher) ProcessOnce(ctx context.Context, limit int) (int, error) {
	events, err := d.repo.FetchDue(ctx, d.now(), limit)
	if err != nil {
		return 0, fmt.Errorf("fetch due outbox events: %w", err)
	}

	for _, e := range events {
		if err := d.publisher.Publish(ctx, e); err != nil {
			if markErr := d.recordFailure(ctx, e, err); markErr != nil {
				return len(events), markErr
			}
			continue
		}
		if err := d.repo.MarkSent(ctx, e.ID, d.now()); err != nil {
			return len(events), fmt.Errorf("mark event %s sent: %w", e.ID, err)
		}
	}
	return len(events), nil
}

func (d *Dispatcher) recordFailure(ctx context.Context, e Event, publishErr error) error {
	attempts := e.Attempts + 1
	if attempts >= maxAttempts {
		if err := d.repo.MarkFailed(ctx, e.ID, publishErr.Error()); err != nil {
			return fmt.Errorf("mark event %s failed: %w", e.ID, err)
		}
		return nil
	}
	nextAttemptAt := d.now().Add(retryBackoff[attempts-1])
	if err := d.repo.MarkRetry(ctx, e.ID, nextAttemptAt, publishErr.Error()); err != nil {
		return fmt.Errorf("schedule retry for event %s: %w", e.ID, err)
	}
	return nil
}

// Run polls for due events every pollInterval until ctx is canceled — the
// background half of cmd/api described in that file's own doc comment.
// Errors from ProcessOnce are logged, not returned: one bad poll (e.g. a
// transient DB hiccup) must not stop the loop from trying again next tick.
func (d *Dispatcher) Run(ctx context.Context, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := d.ProcessOnce(ctx, 20); err != nil {
				slog.Error("outbox dispatch failed", "error", err)
			}
		}
	}
}

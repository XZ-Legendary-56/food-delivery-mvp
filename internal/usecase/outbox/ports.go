package outbox

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Repository is the Dispatcher's own view of outbox_events — reading due
// work and recording what happened to it. Enqueueing a new event is
// deliberately not here: that happens inside whichever use-case's own
// transaction produced the event (usecase/order declares its own narrow
// Enqueue port for exactly that reason — docs/PLAN.md 6.2, this package must
// not be a dependency of usecase/order just to write one row).
type Repository interface {
	// FetchDue atomically claims up to limit pending events whose
	// next_attempt_at is at or before now — never a query per event, the
	// same docs/PLAN.md 6.6 rule the catalog listing follows. "Atomically
	// claims" means the implementation itself must ensure two concurrent
	// callers (two Dispatcher instances) never both receive the same event;
	// see adapter/postgres's implementation for how.
	FetchDue(ctx context.Context, now time.Time, limit int) ([]Event, error)
	// MarkSent records a successful delivery.
	MarkSent(ctx context.Context, eventID uuid.UUID, sentAt time.Time) error
	// MarkRetry increments attempts and schedules the next try, keeping
	// status 'pending'.
	MarkRetry(ctx context.Context, eventID uuid.UUID, nextAttemptAt time.Time, lastErr string) error
	// MarkFailed sets status 'failed' after attempts are exhausted
	// (docs/PLAN.md 7.4: five tries, then give up — the venue still sees the
	// order through GET /partner/orders).
	MarkFailed(ctx context.Context, eventID uuid.UUID, lastErr string) error
}

// EventPublisher is docs/PLAN.md 6.5's own interface, verbatim: the Dispatcher
// never knows where an event actually goes. Today there is exactly one
// implementation (adapter/webhook.Publisher); a future KafkaPublisher
// would be a new file, not a rewrite of this package.
type EventPublisher interface {
	Publish(ctx context.Context, e Event) error
}

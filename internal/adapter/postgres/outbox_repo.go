package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	orderusecase "food-delivery-mvp/internal/usecase/order"
	outboxusecase "food-delivery-mvp/internal/usecase/outbox"
)

// OutboxRepository implements orderusecase.OutboxRepository (Enqueue, used
// inside checkout/cancel's own transaction) and outboxusecase.Repository
// (FetchDue/MarkSent/MarkRetry/MarkFailed, used by the Dispatcher's poll
// loop) on the single outbox_events table.
type OutboxRepository struct {
	pool *pgxpool.Pool
}

func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{pool: pool}
}

var (
	_ orderusecase.OutboxRepository = (*OutboxRepository)(nil)
	_ outboxusecase.Repository      = (*OutboxRepository)(nil)
)

// fetchDueLease is how long FetchDue's claim on a batch holds before another
// Dispatcher instance is allowed to re-claim it, if the one that claimed it
// never reports an outcome (crashed, lost its DB connection mid-delivery,
// etc.). It only needs to comfortably outlast one delivery attempt — the
// httpClient in adapter/webhook times out well under this.
const fetchDueLease = time.Minute

// Enqueue inserts a pending event, due immediately (next_attempt_at defaults
// to now()). Always called through QuerierFromContext, so it lands in
// whichever transaction the caller (checkout or cancellation) already has
// open — the row exists if and only if the business write that caused it
// was actually committed.
func (r *OutboxRepository) Enqueue(ctx context.Context, e orderusecase.OutboxEvent) error {
	q := QuerierFromContext(ctx, r.pool)
	if _, err := q.Exec(ctx, `
		INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, event_version, payload, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, uuid.New(), e.AggregateType, e.AggregateID, e.Type, 1, e.Payload, e.OccurredAt); err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

// FetchDue atomically claims up to limit pending events due at or before now
// (docs/PLAN.md 6.6: one query, not one per event), so that several Dispatcher
// instances can poll the same table at once without delivering the same
// event twice. Oldest-due-first only decides which rows get claimed — the
// UPDATE ... RETURNING that does the claiming makes no promise about what
// order the claimed rows come back in, so callers must not rely on that.
//
// A plain SELECT ... FOR UPDATE SKIP LOCKED alone would not do that here:
// this is a single autocommitted statement, so any row lock it takes is
// released the instant the statement finishes — long before Publish's HTTP
// call even starts, let alone completes. What actually excludes a second
// instance is the UPDATE ... RETURNING wrapped around it: claiming a row
// means pushing its next_attempt_at fetchDueLease into the future in the
// very same statement that selects it, so no other instance's WHERE
// next_attempt_at <= now() can match it again until the lease expires. The
// FOR UPDATE SKIP LOCKED inside the CTE only matters for the sliver of time
// two instances might run this exact statement simultaneously — the lease
// is what makes the guarantee hold for the whole delivery attempt.
//
// If the instance that claimed a batch crashes before calling
// MarkSent/MarkRetry/MarkFailed, the row stays status='pending' with a
// stale claim: once the lease expires it becomes due again for whichever
// instance polls next, so a crash costs a delay, never a lost event.
func (r *OutboxRepository) FetchDue(ctx context.Context, now time.Time, limit int) ([]outboxusecase.Event, error) {
	q := QuerierFromContext(ctx, r.pool)
	rows, err := q.Query(ctx, `
		WITH claimed AS (
			SELECT id FROM outbox_events
			WHERE status = 'pending' AND next_attempt_at <= $1
			ORDER BY next_attempt_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE outbox_events e
		SET next_attempt_at = $3
		FROM claimed
		WHERE e.id = claimed.id
		RETURNING e.id, e.aggregate_type, e.aggregate_id, e.event_type, e.event_version, e.payload, e.occurred_at, e.attempts
	`, now, limit, now.Add(fetchDueLease))
	if err != nil {
		return nil, fmt.Errorf("query due outbox events: %w", err)
	}
	defer rows.Close()

	var events []outboxusecase.Event
	for rows.Next() {
		var e outboxusecase.Event
		if err := rows.Scan(&e.ID, &e.AggregateType, &e.AggregateID, &e.Type, &e.Version, &e.Payload, &e.OccurredAt, &e.Attempts); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox events: %w", err)
	}
	return events, nil
}

// MarkSent records a successful delivery.
func (r *OutboxRepository) MarkSent(ctx context.Context, eventID uuid.UUID, sentAt time.Time) error {
	q := QuerierFromContext(ctx, r.pool)
	if _, err := q.Exec(ctx, `
		UPDATE outbox_events SET status = 'sent', sent_at = $2 WHERE id = $1
	`, eventID, sentAt); err != nil {
		return fmt.Errorf("mark outbox event sent: %w", err)
	}
	return nil
}

// MarkRetry increments attempts and schedules the next try, keeping status
// 'pending' so the next poll picks it back up once next_attempt_at arrives.
func (r *OutboxRepository) MarkRetry(ctx context.Context, eventID uuid.UUID, nextAttemptAt time.Time, lastErr string) error {
	q := QuerierFromContext(ctx, r.pool)
	if _, err := q.Exec(ctx, `
		UPDATE outbox_events
		SET attempts = attempts + 1, next_attempt_at = $2, last_error = $3
		WHERE id = $1
	`, eventID, nextAttemptAt, lastErr); err != nil {
		return fmt.Errorf("schedule outbox event retry: %w", err)
	}
	return nil
}

// MarkFailed sets status 'failed' after attempts are exhausted (docs/PLAN.md
// 7.4: five tries, then give up).
func (r *OutboxRepository) MarkFailed(ctx context.Context, eventID uuid.UUID, lastErr string) error {
	q := QuerierFromContext(ctx, r.pool)
	if _, err := q.Exec(ctx, `
		UPDATE outbox_events SET status = 'failed', attempts = attempts + 1, last_error = $2
		WHERE id = $1
	`, eventID, lastErr); err != nil {
		return fmt.Errorf("mark outbox event failed: %w", err)
	}
	return nil
}

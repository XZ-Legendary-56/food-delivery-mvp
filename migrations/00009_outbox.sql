-- +goose Up
CREATE TABLE outbox_events (
    -- Also the event_id carried in the X-Event-Id header; consumers deduplicate on it.
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type  TEXT        NOT NULL,
    aggregate_id    UUID        NOT NULL,
    event_type      TEXT        NOT NULL,
    event_version   INTEGER     NOT NULL DEFAULT 1,
    payload         JSONB       NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending',
    attempts        INTEGER     NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT,
    occurred_at     TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ,

    CONSTRAINT outbox_events_status_known    CHECK (status IN ('pending', 'sent', 'failed')),
    CONSTRAINT outbox_events_attempts_valid  CHECK (attempts >= 0),
    CONSTRAINT outbox_events_version_valid   CHECK (event_version > 0)
);

-- The dispatcher only ever scans pending rows that are due, so the index covers
-- exactly that slice instead of the whole table.
CREATE INDEX outbox_events_due_idx
    ON outbox_events (next_attempt_at)
    WHERE status = 'pending';

CREATE INDEX outbox_events_aggregate_idx ON outbox_events (aggregate_type, aggregate_id);

-- +goose Down
DROP TABLE outbox_events;

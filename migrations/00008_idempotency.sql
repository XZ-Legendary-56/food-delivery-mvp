-- +goose Up
CREATE TABLE idempotency_keys (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id    UUID        NOT NULL,
    key          TEXT        NOT NULL,
    -- Hash of the request body. Same key with a different body is a client bug,
    -- and we answer with a conflict rather than returning an unrelated order.
    request_hash TEXT        NOT NULL,
    order_id     UUID        REFERENCES orders (id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,

    CONSTRAINT idempotency_keys_unique UNIQUE (client_id, key)
);

CREATE INDEX idempotency_keys_expiry_idx ON idempotency_keys (expires_at);

-- +goose Down
DROP TABLE idempotency_keys;

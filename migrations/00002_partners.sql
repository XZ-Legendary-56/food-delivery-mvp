-- +goose Up
CREATE TABLE partners (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE partner_api_keys (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    partner_id UUID        NOT NULL REFERENCES partners (id) ON DELETE CASCADE,
    key_hash   TEXT        NOT NULL UNIQUE,
    label      TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);

CREATE INDEX partner_api_keys_partner_idx ON partner_api_keys (partner_id);

-- +goose Down
DROP TABLE partner_api_keys;
DROP TABLE partners;

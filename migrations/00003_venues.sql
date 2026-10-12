-- +goose Up
CREATE TABLE venues (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    partner_id             UUID        NOT NULL REFERENCES partners (id) ON DELETE RESTRICT,
    name                   TEXT        NOT NULL,
    description            TEXT        NOT NULL DEFAULT '',
    cuisine                TEXT        NOT NULL,
    min_order_amount_minor BIGINT      NOT NULL DEFAULT 0,
    accepting_orders       BOOLEAN     NOT NULL DEFAULT TRUE,
    menu_version           BIGINT      NOT NULL DEFAULT 1,
    webhook_url            TEXT,
    webhook_secret         TEXT,
    source                 TEXT        NOT NULL DEFAULT 'platform',
    external_id            TEXT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT venues_min_order_non_negative CHECK (min_order_amount_minor >= 0),
    CONSTRAINT venues_menu_version_positive  CHECK (menu_version > 0),
    CONSTRAINT venues_source_known           CHECK (source IN ('platform', 'integration')),
    -- A webhook is only usable when both the URL and the signing secret are present.
    CONSTRAINT venues_webhook_complete       CHECK (
        (webhook_url IS NULL AND webhook_secret IS NULL)
        OR (webhook_url IS NOT NULL AND webhook_secret IS NOT NULL)
    )
);

CREATE INDEX venues_cuisine_idx ON venues (cuisine);
CREATE INDEX venues_name_prefix_idx ON venues (lower(name) text_pattern_ops);
CREATE UNIQUE INDEX venues_partner_external_uq
    ON venues (partner_id, external_id)
    WHERE external_id IS NOT NULL;

CREATE TABLE venue_schedules (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    venue_id  UUID     NOT NULL REFERENCES venues (id) ON DELETE CASCADE,
    weekday   SMALLINT NOT NULL,
    opens_at  TIME     NOT NULL,
    closes_at TIME     NOT NULL,

    CONSTRAINT venue_schedules_weekday_range CHECK (weekday BETWEEN 0 AND 6),
    -- MVP simplification: no shifts crossing midnight, one interval per weekday.
    CONSTRAINT venue_schedules_order         CHECK (closes_at > opens_at),
    CONSTRAINT venue_schedules_one_per_day   UNIQUE (venue_id, weekday)
);

-- +goose Down
DROP TABLE venue_schedules;
DROP TABLE venues;

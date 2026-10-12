-- +goose Up
CREATE TABLE rescue_offers (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    venue_id           UUID        NOT NULL REFERENCES venues (id) ON DELETE CASCADE,
    menu_item_id       UUID        NOT NULL REFERENCES menu_items (id) ON DELETE CASCADE,
    discount_percent   SMALLINT    NOT NULL,
    initial_quantity   INTEGER     NOT NULL,
    remaining_quantity INTEGER     NOT NULL,
    starts_at          TIMESTAMPTZ NOT NULL,
    ends_at            TIMESTAMPTZ NOT NULL,
    cancelled_at       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT rescue_offers_discount_range   CHECK (discount_percent BETWEEN 1 AND 90),
    CONSTRAINT rescue_offers_initial_positive CHECK (initial_quantity > 0),
    CONSTRAINT rescue_offers_remaining_valid  CHECK (remaining_quantity BETWEEN 0 AND initial_quantity),
    CONSTRAINT rescue_offers_window_order     CHECK (ends_at > starts_at),

    -- Two live offers may not cover the same item at overlapping times.
    -- Enforced by the database, so two concurrent requests cannot both succeed.
    CONSTRAINT rescue_offers_no_overlap EXCLUDE USING gist (
        menu_item_id WITH =,
        tstzrange(starts_at, ends_at) WITH &&
    ) WHERE (cancelled_at IS NULL)
);

CREATE INDEX rescue_offers_venue_idx ON rescue_offers (venue_id);

-- Feed query: live offers ordered by how soon they close.
CREATE INDEX rescue_offers_active_idx
    ON rescue_offers (ends_at)
    WHERE cancelled_at IS NULL AND remaining_quantity > 0;

-- +goose Down
DROP TABLE rescue_offers;

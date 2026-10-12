-- +goose Up
CREATE TABLE menu_categories (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    venue_id   UUID        NOT NULL REFERENCES venues (id) ON DELETE CASCADE,
    name       TEXT        NOT NULL,
    position   INTEGER     NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT menu_categories_name_uq UNIQUE (venue_id, name)
);

CREATE TABLE menu_items (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    venue_id             UUID        NOT NULL REFERENCES venues (id) ON DELETE CASCADE,
    category_id          UUID        NOT NULL REFERENCES menu_categories (id) ON DELETE RESTRICT,
    name                 TEXT        NOT NULL,
    description          TEXT        NOT NULL DEFAULT '',
    price_minor          BIGINT      NOT NULL,
    -- Stop list: the venue switched the item off by hand.
    is_available         BOOLEAN     NOT NULL DEFAULT TRUE,
    -- Stock: NULL means unlimited, a number means a hard cap that orders decrement.
    stock_qty            INTEGER,
    cooking_time_minutes INTEGER     NOT NULL DEFAULT 15,
    source               TEXT        NOT NULL DEFAULT 'platform',
    external_id          TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT menu_items_price_positive  CHECK (price_minor > 0),
    CONSTRAINT menu_items_stock_valid     CHECK (stock_qty IS NULL OR stock_qty >= 0),
    CONSTRAINT menu_items_cooking_valid   CHECK (cooking_time_minutes > 0),
    CONSTRAINT menu_items_source_known    CHECK (source IN ('platform', 'integration'))
);

CREATE INDEX menu_items_venue_idx ON menu_items (venue_id);
CREATE INDEX menu_items_category_idx ON menu_items (category_id);

-- This is the link between our data and the venue's own system: an item imported
-- from an integration keeps the identifier it has on the other side.
CREATE UNIQUE INDEX menu_items_venue_external_uq
    ON menu_items (venue_id, external_id)
    WHERE external_id IS NOT NULL;

-- +goose Down
DROP TABLE menu_items;
DROP TABLE menu_categories;

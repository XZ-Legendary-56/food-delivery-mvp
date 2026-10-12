-- +goose Up
CREATE TABLE orders (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id         UUID        NOT NULL,
    venue_id          UUID        NOT NULL REFERENCES venues (id) ON DELETE RESTRICT,
    status            TEXT        NOT NULL,
    total_minor       BIGINT      NOT NULL,
    delivery_address  TEXT        NOT NULL,
    customer_phone    TEXT        NOT NULL,
    comment           TEXT        NOT NULL DEFAULT '',
    eta_minutes       INTEGER,
    rejection_reason  TEXT,
    -- Identifier of this order inside the venue's own system, when it has one.
    external_order_id TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT orders_total_non_negative CHECK (total_minor >= 0),
    CONSTRAINT orders_eta_positive       CHECK (eta_minutes IS NULL OR eta_minutes > 0),
    CONSTRAINT orders_status_known CHECK (status IN (
        'created', 'confirmed', 'cooking', 'ready',
        'delivering', 'delivered', 'rejected', 'cancelled'
    ))
);

CREATE INDEX orders_client_idx ON orders (client_id, created_at DESC);
CREATE INDEX orders_venue_feed_idx ON orders (venue_id, status, created_at);

CREATE TABLE order_items (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id         UUID    NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    menu_item_id     UUID    NOT NULL REFERENCES menu_items (id) ON DELETE RESTRICT,
    -- Set when this line was bought under a rescue offer. NULL means full price.
    rescue_offer_id  UUID    REFERENCES rescue_offers (id) ON DELETE SET NULL,
    -- Name and price are copied on purpose: editing the menu later must not
    -- rewrite orders that were already placed.
    name_snapshot    TEXT    NOT NULL,
    unit_price_minor BIGINT  NOT NULL,
    quantity         INTEGER NOT NULL,

    CONSTRAINT order_items_quantity_positive CHECK (quantity > 0),
    CONSTRAINT order_items_price_positive    CHECK (unit_price_minor > 0)
);

CREATE INDEX order_items_order_idx ON order_items (order_id);

CREATE TABLE order_status_history (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id    UUID        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    from_status TEXT,
    to_status   TEXT        NOT NULL,
    actor       TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_status_history_actor_known CHECK (actor IN ('customer', 'venue', 'system'))
);

CREATE INDEX order_status_history_order_idx ON order_status_history (order_id, created_at);

-- +goose Down
DROP TABLE order_status_history;
DROP TABLE order_items;
DROP TABLE orders;

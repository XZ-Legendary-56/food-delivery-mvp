-- +goose Up
CREATE TABLE carts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Anonymous customer identity, supplied by the client in the X-Client-Id header.
    client_id  UUID        NOT NULL UNIQUE,
    venue_id   UUID        NOT NULL REFERENCES venues (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE cart_items (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cart_id              UUID        NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
    menu_item_id         UUID        NOT NULL REFERENCES menu_items (id) ON DELETE CASCADE,
    quantity             INTEGER     NOT NULL,
    -- Price seen by the customer when the item was added. Compared against the live
    -- price at checkout so we can report PRICE_CHANGED instead of silently charging more.
    price_minor_snapshot BIGINT      NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT cart_items_quantity_positive CHECK (quantity > 0),
    CONSTRAINT cart_items_price_positive    CHECK (price_minor_snapshot > 0),
    CONSTRAINT cart_items_unique_per_cart   UNIQUE (cart_id, menu_item_id)
);

-- +goose Down
DROP TABLE cart_items;
DROP TABLE carts;

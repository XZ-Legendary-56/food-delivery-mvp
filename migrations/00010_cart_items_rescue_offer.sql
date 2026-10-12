-- +goose Up
-- Records which rescue offer (if any) a cart line's snapshot price assumed,
-- so checkout can look up that SPECIFIC offer's current state later and
-- tell "the deal is gone entirely" (RESCUE_OFFER_EXPIRED) apart from "the
-- deal still exists but shrank" (a successful split, docs/PLAN.md 5.5) — both
-- require knowing which offer this was, not just whether one exists now.
ALTER TABLE cart_items
    ADD COLUMN rescue_offer_id UUID REFERENCES rescue_offers (id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE cart_items DROP COLUMN rescue_offer_id;

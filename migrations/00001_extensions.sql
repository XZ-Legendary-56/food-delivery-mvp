-- +goose Up
-- btree_gist lets a GiST index handle plain equality on scalar types (uuid here).
-- Required by the exclusion constraint on rescue_offers, which mixes an equality
-- check on menu_item_id with an overlap check on a time range.
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- +goose Down
DROP EXTENSION IF EXISTS btree_gist;

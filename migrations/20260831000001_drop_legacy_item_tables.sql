-- +goose Up
-- Run after `go run ./cmd/backfill-item-groups` has copied every legacy
-- receipt_items row into receipt_item_groups/receipt_items_new. Dev-stage
-- restructuring only — no production assignment data depends on the old shape
-- (AssignItemsToUserHandler was never wired up on the frontend), so this drop
-- is not preceded by a data-preserving down path for receipt_user_items.
DROP TABLE IF EXISTS receipt_user_items;
DROP TABLE IF EXISTS receipt_items;
ALTER TABLE receipt_items_new RENAME TO receipt_items;

-- +goose Down
ALTER TABLE receipt_items RENAME TO receipt_items_new;
-- receipt_items and receipt_user_items (legacy shape) are not recreated here —
-- reconstructing quantity/total_price/price_per_item from split unit rows isn't
-- reliably invertible. Roll back further by restoring from a backup if needed.

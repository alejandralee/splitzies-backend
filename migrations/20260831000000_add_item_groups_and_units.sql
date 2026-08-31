-- +goose Up
-- Introduces receipt_item_groups (display-only grouping) and a new receipt_items
-- shape (one row per individually-assignable unit, amount instead of
-- quantity/total_price/price_per_item), plus receipt_item_users (assignment is
-- always an even split among assignees — no stored amount_owed).
--
-- The legacy receipt_items/receipt_user_items tables are left in place here;
-- a one-off Go backfill command (cmd/backfill-item-groups) explodes legacy rows
-- into the new shape (new rows need ULIDs, which Postgres can't generate), then
-- a follow-up migration drops the legacy tables.
CREATE TABLE receipt_item_groups (
    id VARCHAR(26) PRIMARY KEY,
    receipt_id VARCHAR(26) NOT NULL,
    name TEXT NOT NULL,
    display_order INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (receipt_id) REFERENCES receipts(id) ON DELETE CASCADE
);

CREATE TABLE receipt_items_new (
    id VARCHAR(26) PRIMARY KEY,
    receipt_id VARCHAR(26) NOT NULL,
    group_id VARCHAR(26) NOT NULL,
    name TEXT NOT NULL,
    amount REAL NOT NULL,
    display_order INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (receipt_id) REFERENCES receipts(id) ON DELETE CASCADE,
    FOREIGN KEY (group_id) REFERENCES receipt_item_groups(id) ON DELETE CASCADE
);

CREATE TABLE receipt_item_users (
    receipt_item_id VARCHAR(26) NOT NULL,
    receipt_user_id VARCHAR(26) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (receipt_item_id, receipt_user_id),
    FOREIGN KEY (receipt_item_id) REFERENCES receipt_items_new(id) ON DELETE CASCADE,
    FOREIGN KEY (receipt_user_id) REFERENCES receipt_users(id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS receipt_item_users;
DROP TABLE IF EXISTS receipt_items_new;
DROP TABLE IF EXISTS receipt_item_groups;

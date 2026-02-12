-- +goose Up
CREATE TABLE IF NOT EXISTS receipt_users (
    id VARCHAR(26) PRIMARY KEY,
    receipt_id VARCHAR(26) NOT NULL,
    name TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (receipt_id) REFERENCES receipts(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS receipt_user_items (
    id VARCHAR(26) PRIMARY KEY,
    receipt_user_id VARCHAR(26) NOT NULL,
    receipt_item_id VARCHAR(26) NOT NULL,
    amount_paid REAL, -- NULL means equal split, non-NULL means custom amount
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (receipt_user_id) REFERENCES receipt_users(id) ON DELETE CASCADE,
    FOREIGN KEY (receipt_item_id) REFERENCES receipt_items(id) ON DELETE CASCADE,
    UNIQUE(receipt_user_id, receipt_item_id) -- Prevent duplicate assignments
);

-- +goose Down
DROP TABLE IF EXISTS receipt_user_items;
DROP TABLE IF EXISTS receipt_users;

-- +goose Up
CREATE TABLE IF NOT EXISTS receipts (
    id VARCHAR(26) PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS receipt_items (
    id VARCHAR(26) PRIMARY KEY,
    receipt_id VARCHAR(26) NOT NULL,
    name TEXT NOT NULL,
    quantity INTEGER NOT NULL,
    total_price REAL NOT NULL,
    price_per_item REAL NOT NULL,
    FOREIGN KEY (receipt_id) REFERENCES receipts(id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS receipt_items;
DROP TABLE IF EXISTS receipts;

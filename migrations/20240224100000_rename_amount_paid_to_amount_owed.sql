-- +goose Up
ALTER TABLE receipt_user_items RENAME COLUMN amount_paid TO amount_owed;

-- +goose Down
ALTER TABLE receipt_user_items RENAME COLUMN amount_owed TO amount_paid;

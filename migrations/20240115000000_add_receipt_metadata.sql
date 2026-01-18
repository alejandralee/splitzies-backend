-- +goose Up
ALTER TABLE receipts ADD COLUMN currency TEXT;
ALTER TABLE receipts ADD COLUMN receipt_date TEXT;
ALTER TABLE receipts ADD COLUMN title TEXT;

-- +goose Down
ALTER TABLE receipts DROP COLUMN title;
ALTER TABLE receipts DROP COLUMN receipt_date;
ALTER TABLE receipts DROP COLUMN currency;

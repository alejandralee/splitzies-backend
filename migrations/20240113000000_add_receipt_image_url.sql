-- +goose Up
ALTER TABLE receipts ADD COLUMN image_url TEXT;

-- +goose Down
ALTER TABLE receipts DROP COLUMN image_url;

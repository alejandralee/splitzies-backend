-- +goose Up
ALTER TABLE receipts ADD COLUMN ocr_text JSONB;

-- +goose Down
ALTER TABLE receipts DROP COLUMN ocr_text;

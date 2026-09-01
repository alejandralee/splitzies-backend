-- +goose Up
ALTER TABLE receipts ADD COLUMN extras_mode TEXT NOT NULL DEFAULT 'proportional'
    CHECK (extras_mode IN ('proportional', 'even'));

-- +goose Down
ALTER TABLE receipts DROP COLUMN extras_mode;

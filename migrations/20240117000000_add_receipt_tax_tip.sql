-- +goose Up
ALTER TABLE receipts ADD COLUMN tax REAL;
ALTER TABLE receipts ADD COLUMN tip REAL;

-- +goose Down
ALTER TABLE receipts DROP COLUMN tax;
ALTER TABLE receipts DROP COLUMN tip;

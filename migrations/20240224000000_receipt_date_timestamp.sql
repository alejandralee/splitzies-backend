-- +goose Up
-- Change receipt_date from TEXT to TIMESTAMP. Existing values are parsed; NULL/empty become NULL.
ALTER TABLE receipts ALTER COLUMN receipt_date TYPE TIMESTAMP USING (
  CASE
    WHEN receipt_date IS NULL OR trim(receipt_date) = '' THEN NULL
    ELSE receipt_date::timestamp
  END
);

-- +goose Down
ALTER TABLE receipts ALTER COLUMN receipt_date TYPE TEXT USING (receipt_date::text);

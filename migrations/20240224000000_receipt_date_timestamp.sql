-- +goose Up
-- Change receipt_date from TEXT to TIMESTAMP. Try common formats; unparseable values become NULL.
-- PostgreSQL's ::timestamp treats "13/11/16" as MM/DD/YY (month 13 = invalid). We try DD/MM/YY first.
ALTER TABLE receipts ADD COLUMN receipt_date_new TIMESTAMP;

UPDATE receipts SET receipt_date_new = CASE
  WHEN receipt_date IS NULL OR trim(receipt_date) = '' THEN NULL
  WHEN receipt_date ~ '^\d{4}-\d{2}-\d{2}' THEN receipt_date::timestamp
  WHEN receipt_date ~ '^\d{1,2}/\d{1,2}/\d{2}$' THEN to_timestamp(receipt_date, 'DD/MM/YY')
  WHEN receipt_date ~ '^\d{1,2}/\d{1,2}/\d{4}$' THEN to_timestamp(receipt_date, 'DD/MM/YYYY')
  ELSE NULL
END;

ALTER TABLE receipts DROP COLUMN receipt_date;
ALTER TABLE receipts RENAME COLUMN receipt_date_new TO receipt_date;

-- +goose Down
ALTER TABLE receipts ALTER COLUMN receipt_date TYPE TEXT USING (receipt_date::text);

-- Receipt photos are no longer stored: the image is parsed in memory and
-- discarded. Nothing in the product ever rendered the photo back, so keeping
-- it only accumulated other people's receipts — merchant, date, line items,
-- often a card's last four digits.
--
-- The column stays (the writer simply passes NULL now) but the URLs already
-- recorded are cleared, so no stored link to a receipt photo survives this
-- deploy. The objects themselves must be deleted from the bucket separately;
-- clearing the column does not reach into Cloud Storage.

-- +goose Up
UPDATE receipts SET image_url = NULL WHERE image_url IS NOT NULL;

-- +goose Down
-- Irreversible by design: the URLs are not recoverable, and the objects they
-- pointed at are deleted out of band.
SELECT 1;

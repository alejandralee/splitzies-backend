-- +goose Up
-- Adds anonymous device identity, receipt membership (history), share links, and
-- a receipt version counter for cheap conditional polling.
--
-- Every addition here is nullable or defaulted so existing rows and the live
-- frontend (which sends no device token) keep working unchanged.

-- An anonymous, account-less identity held by one browser/app install. user_id is
-- deliberately unused for now: it is the seam where a future users table lets
-- someone claim a device's history on login.
CREATE TABLE devices (
    id VARCHAR(26) PRIMARY KEY,
    token_hash BYTEA NOT NULL UNIQUE,
    user_id VARCHAR(26),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Which devices can see a receipt in their history. The same row shape serves
-- both "the bill I created" (role 'owner') and "the bill a friend joined"
-- (role 'member').
CREATE TABLE receipt_devices (
    receipt_id VARCHAR(26) NOT NULL,
    device_id VARCHAR(26) NOT NULL,
    role TEXT NOT NULL DEFAULT 'member',
    joined_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (receipt_id, device_id),
    FOREIGN KEY (receipt_id) REFERENCES receipts(id) ON DELETE CASCADE,
    FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE CASCADE
);

CREATE INDEX idx_receipt_devices_device ON receipt_devices (device_id, joined_at DESC);

-- Share links are capability tokens: holding one grants full edit access to the
-- receipt. The token is stored in plaintext so the owner can re-open the share
-- sheet and see the same QR code instead of rotating the link on every view.
CREATE TABLE receipt_share_links (
    token TEXT PRIMARY KEY,
    receipt_id VARCHAR(26) NOT NULL,
    created_by_device_id VARCHAR(26),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at TIMESTAMP,
    FOREIGN KEY (receipt_id) REFERENCES receipts(id) ON DELETE CASCADE,
    FOREIGN KEY (created_by_device_id) REFERENCES devices(id) ON DELETE SET NULL
);

-- At most one active link per receipt, so POST /receipts/{id}/share is idempotent.
CREATE UNIQUE INDEX idx_share_links_active_receipt
    ON receipt_share_links (receipt_id) WHERE revoked_at IS NULL;

-- Bumped by every mutation; served as the ETag so unchanged polls cost one row read.
ALTER TABLE receipts ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE receipts ADD COLUMN updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP;

-- Links a participant to the device acting as them, so a client can answer
-- "which of these people am I?" and history can show "your total".
ALTER TABLE receipt_users ADD COLUMN device_id VARCHAR(26)
    REFERENCES devices(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX idx_receipt_users_device
    ON receipt_users (receipt_id, device_id) WHERE device_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_receipt_users_device;
ALTER TABLE receipt_users DROP COLUMN IF EXISTS device_id;
ALTER TABLE receipts DROP COLUMN IF EXISTS updated_at;
ALTER TABLE receipts DROP COLUMN IF EXISTS version;
DROP TABLE IF EXISTS receipt_share_links;
DROP TABLE IF EXISTS receipt_devices;
DROP TABLE IF EXISTS devices;

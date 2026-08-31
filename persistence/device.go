package persistence

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
)

// deviceTokenBytes is the entropy of a device token before base64url encoding.
const deviceTokenBytes = 32

// lastSeenThrottle is how stale last_seen_at must be before a request writes to
// it. Clients poll every few seconds, so an unthrottled update would mean a
// write per request.
const lastSeenThrottle = time.Hour

// Device is an anonymous, account-less identity belonging to one browser or app
// install. UserID is always nil today — it exists so a future users table can
// claim a device's history on login.
type Device struct {
	ID         string
	UserID     *string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// ErrDeviceNotFound is returned when a device token does not resolve to a device.
var ErrDeviceNotFound = errors.New("device not found")

// generateDeviceToken returns a new opaque token with deviceTokenBytes of entropy.
func generateDeviceToken() (string, error) {
	b := make([]byte, deviceTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate device token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken returns the SHA-256 of a token. Only the hash is stored, so a
// database leak does not hand over usable device tokens.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// CreateDevice mints a new device and returns it along with the plaintext token.
// The token is returned exactly once — only its hash is persisted.
func (c *Client) CreateDevice(ctx context.Context) (*Device, string, error) {
	token, err := generateDeviceToken()
	if err != nil {
		return nil, "", err
	}

	deviceID := ulid.Make().String()
	var device Device
	err = c.db.QueryRow(ctx, `
		INSERT INTO devices (id, token_hash, created_at, last_seen_at)
		VALUES ($1, $2, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id, user_id, created_at, last_seen_at
	`, deviceID, hashToken(token)).Scan(&device.ID, &device.UserID, &device.CreatedAt, &device.LastSeenAt)
	if err != nil {
		return nil, "", fmt.Errorf("failed to insert device: %w", err)
	}

	return &device, token, nil
}

// DeviceByToken resolves a plaintext device token to its device, refreshing
// last_seen_at at most once per lastSeenThrottle.
// Returns ErrDeviceNotFound if the token is unknown.
func (c *Client) DeviceByToken(ctx context.Context, token string) (*Device, error) {
	if token == "" {
		return nil, ErrDeviceNotFound
	}

	tokenHash := hashToken(token)

	var device Device
	err := c.db.QueryRow(ctx, `
		SELECT id, user_id, created_at, last_seen_at FROM devices WHERE token_hash = $1
	`, tokenHash).Scan(&device.ID, &device.UserID, &device.CreatedAt, &device.LastSeenAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDeviceNotFound
		}
		return nil, fmt.Errorf("failed to look up device: %w", err)
	}

	// Refresh last_seen_at only when it is actually stale — a conditional UPDATE
	// here would still rewrite the row on every poll.
	if time.Since(device.LastSeenAt) > lastSeenThrottle {
		if _, err := c.db.Exec(ctx,
			"UPDATE devices SET last_seen_at = CURRENT_TIMESTAMP WHERE id = $1", device.ID,
		); err != nil {
			return nil, fmt.Errorf("failed to refresh device last_seen_at: %w", err)
		}
	}

	return &device, nil
}

package persistence

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// shareTokenBytes is the entropy of a share link token before base64url
// encoding. 16 bytes keeps the resulting URL short enough to scan comfortably
// as a QR code while staying unguessable.
const shareTokenBytes = 16

// Receipt membership roles.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// ErrShareLinkNotFound is returned for unknown or revoked share tokens.
var ErrShareLinkNotFound = errors.New("share link not found")

// ShareLink is a capability token granting full edit access to one receipt.
type ShareLink struct {
	Token             string
	ReceiptID         string
	CreatedByDeviceID *string
	CreatedAt         time.Time
}

// ReceiptSummary is one row of a device's bill history. It deliberately omits
// items and assignments — the history list only needs headline numbers.
type ReceiptSummary struct {
	ReceiptID        string
	Title            *string
	Currency         *string
	ImageURL         *string
	ReceiptDate      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
	Role             string
	JoinedAt         time.Time
	ParticipantCount int
	ItemCount        int
	ItemTotal        float64
	Tax              *float64
	Tip              *float64
}

func generateShareToken() (string, error) {
	b := make([]byte, shareTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate share token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// AddDeviceToReceipt records that a device can see a receipt in its history.
// It is idempotent, and never downgrades an existing owner to a member.
func (c *Client) AddDeviceToReceipt(ctx context.Context, receiptID, deviceID, role string) error {
	_, err := c.db.Exec(ctx, `
		INSERT INTO receipt_devices (receipt_id, device_id, role, joined_at)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
		ON CONFLICT (receipt_id, device_id) DO NOTHING
	`, receiptID, deviceID, role)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return fmt.Errorf("receipt %q not found", receiptID)
		}
		return fmt.Errorf("failed to add device to receipt: %w", err)
	}
	return nil
}

// RemoveDeviceFromReceipt hides a receipt from one device's history. The receipt
// itself is untouched, so other participants keep it.
func (c *Client) RemoveDeviceFromReceipt(ctx context.Context, receiptID, deviceID string) error {
	result, err := c.db.Exec(ctx,
		"DELETE FROM receipt_devices WHERE receipt_id = $1 AND device_id = $2",
		receiptID, deviceID,
	)
	if err != nil {
		return fmt.Errorf("failed to remove device from receipt: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("receipt %q not found in history", receiptID)
	}
	return nil
}

// DeviceOnReceipt reports whether a device is a member of a receipt.
func (c *Client) DeviceOnReceipt(ctx context.Context, receiptID, deviceID string) (bool, error) {
	var exists bool
	err := c.db.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM receipt_devices WHERE receipt_id = $1 AND device_id = $2)",
		receiptID, deviceID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check receipt membership: %w", err)
	}
	return exists, nil
}

// CreateOrGetShareLink returns the receipt's active share link, creating one if
// there isn't one. It is idempotent so the share sheet shows a stable QR code.
func (c *Client) CreateOrGetShareLink(ctx context.Context, receiptID string, createdByDeviceID *string) (*ShareLink, error) {
	var link ShareLink
	err := c.db.QueryRow(ctx, `
		SELECT token, receipt_id, created_by_device_id, created_at
		FROM receipt_share_links
		WHERE receipt_id = $1 AND revoked_at IS NULL
	`, receiptID).Scan(&link.Token, &link.ReceiptID, &link.CreatedByDeviceID, &link.CreatedAt)
	if err == nil {
		return &link, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to look up share link: %w", err)
	}

	token, err := generateShareToken()
	if err != nil {
		return nil, err
	}

	err = c.db.QueryRow(ctx, `
		INSERT INTO receipt_share_links (token, receipt_id, created_by_device_id, created_at)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
		RETURNING token, receipt_id, created_by_device_id, created_at
	`, token, receiptID, createdByDeviceID).Scan(
		&link.Token, &link.ReceiptID, &link.CreatedByDeviceID, &link.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			// 23503: the receipt doesn't exist. 23505: another request created the
			// active link between our SELECT and INSERT — re-read theirs.
			if pgErr.Code == "23503" {
				return nil, fmt.Errorf("receipt %q not found", receiptID)
			}
			if pgErr.Code == "23505" {
				return c.CreateOrGetShareLink(ctx, receiptID, createdByDeviceID)
			}
		}
		return nil, fmt.Errorf("failed to create share link: %w", err)
	}

	return &link, nil
}

// ShareLinkByToken resolves an active share token.
// Returns ErrShareLinkNotFound if it is unknown or revoked.
func (c *Client) ShareLinkByToken(ctx context.Context, token string) (*ShareLink, error) {
	if token == "" {
		return nil, ErrShareLinkNotFound
	}

	var link ShareLink
	err := c.db.QueryRow(ctx, `
		SELECT token, receipt_id, created_by_device_id, created_at
		FROM receipt_share_links
		WHERE token = $1 AND revoked_at IS NULL
	`, token).Scan(&link.Token, &link.ReceiptID, &link.CreatedByDeviceID, &link.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrShareLinkNotFound
		}
		return nil, fmt.Errorf("failed to look up share link: %w", err)
	}
	return &link, nil
}

// RevokeShareLink deactivates a receipt's active share link. Devices that
// already joined keep their access; only the link stops working.
// Returns ErrShareLinkNotFound if there was no active link.
func (c *Client) RevokeShareLink(ctx context.Context, receiptID string) error {
	result, err := c.db.Exec(ctx, `
		UPDATE receipt_share_links SET revoked_at = CURRENT_TIMESTAMP
		WHERE receipt_id = $1 AND revoked_at IS NULL
	`, receiptID)
	if err != nil {
		return fmt.Errorf("failed to revoke share link: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrShareLinkNotFound
	}
	return nil
}

// ClaimReceiptUser links a participant to the calling device, so the client can
// tell which of the people on a bill is them. Claiming a participant already
// claimed by another device, or claiming a second participant on the same
// receipt, is rejected by the partial unique index.
// Returns ErrNotFound-shaped errors for unknown or already-claimed participants.
func (c *Client) ClaimReceiptUser(ctx context.Context, receiptID, receiptUserID, deviceID string) error {
	tx, err := c.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	result, err := tx.Exec(ctx, `
		UPDATE receipt_users SET device_id = $1
		WHERE id = $2 AND receipt_id = $3 AND (device_id IS NULL OR device_id = $1)
	`, deviceID, receiptUserID, receiptID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("device already claims another participant on receipt %q", receiptID)
		}
		return fmt.Errorf("failed to claim receipt user: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("participant %q not found on receipt %q, or already claimed by another device", receiptUserID, receiptID)
	}

	if err := touchReceiptTx(ctx, tx, receiptID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// ListReceiptsForDevice returns a device's bill history, newest first, keyset
// paginated on joined_at. Pass a zero cursor for the first page.
func (c *Client) ListReceiptsForDevice(ctx context.Context, deviceID string, limit int, cursor time.Time) ([]ReceiptSummary, error) {
	rows, err := c.db.Query(ctx, `
		SELECT
			r.id, r.title, r.currency, r.image_url, r.receipt_date, r.created_at, r.updated_at,
			r.tax, r.tip, rd.role, rd.joined_at,
			(SELECT COUNT(*) FROM receipt_users ru WHERE ru.receipt_id = r.id),
			(SELECT COUNT(*) FROM receipt_items ri WHERE ri.receipt_id = r.id),
			COALESCE((SELECT SUM(ri.amount) FROM receipt_items ri WHERE ri.receipt_id = r.id), 0)
		FROM receipt_devices rd
		JOIN receipts r ON r.id = rd.receipt_id
		WHERE rd.device_id = $1 AND ($2::timestamp IS NULL OR rd.joined_at < $2)
		ORDER BY rd.joined_at DESC
		LIMIT $3
	`, deviceID, nullableTime(cursor), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query device receipts: %w", err)
	}
	defer rows.Close()

	var summaries []ReceiptSummary
	for rows.Next() {
		var s ReceiptSummary
		if err := rows.Scan(
			&s.ReceiptID, &s.Title, &s.Currency, &s.ImageURL, &s.ReceiptDate, &s.CreatedAt, &s.UpdatedAt,
			&s.Tax, &s.Tip, &s.Role, &s.JoinedAt,
			&s.ParticipantCount, &s.ItemCount, &s.ItemTotal,
		); err != nil {
			return nil, fmt.Errorf("failed to scan receipt summary: %w", err)
		}
		summaries = append(summaries, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating device receipts: %w", err)
	}
	return summaries, nil
}

// DeviceReceiptUser returns the participant a device has claimed on a receipt,
// or nil if it has not claimed one.
func (c *Client) DeviceReceiptUser(ctx context.Context, receiptID, deviceID string) (*ReceiptUser, error) {
	var u ReceiptUser
	err := c.db.QueryRow(ctx, `
		SELECT id, receipt_id, name, created_at
		FROM receipt_users
		WHERE receipt_id = $1 AND device_id = $2
	`, receiptID, deviceID).Scan(&u.ID, &u.ReceiptID, &u.Name, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to look up claimed participant: %w", err)
	}
	return &u, nil
}

// nullableTime maps the zero time to nil so it can be used as a SQL NULL cursor.
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

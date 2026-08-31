package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oklog/ulid/v2"
)

// ReceiptUser represents a user associated with a receipt.
type ReceiptUser struct {
	ID        string
	ReceiptID string
	Name      string
	// DeviceID is the device acting as this participant, if one has claimed
	// them. Nil for participants typed in by someone else.
	DeviceID  *string
	CreatedAt time.Time
}

// ReceiptUserItem represents the assignment of an individual item unit to a
// user. There is no stored amount — the split is always the item's amount
// divided evenly among however many users are assigned to it, computed at
// read time by ComputeBillSplit.
type ReceiptUserItem struct {
	ReceiptUserID string
	ReceiptItemID string
	CreatedAt     time.Time
}

// ReceiptTaxTip holds optional tax and tip amounts for a receipt.
type ReceiptTaxTip struct {
	Tax *float64
	Tip *float64
}

// AddUserToReceipt creates a new user entry on a receipt. When deviceID is
// non-nil the new participant is claimed by that device, so its client can tell
// which of the people on the bill is them.
// Returns a not-found error if the receipt does not exist.
func (c *Client) AddUserToReceipt(ctx context.Context, receiptID, name string, deviceID *string) (*ReceiptUser, error) {
	userID := ulid.Make().String()

	tx, err := c.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		"INSERT INTO receipt_users (id, receipt_id, name, device_id, created_at) VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)",
		userID, receiptID, name, deviceID,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23503" {
				return nil, fmt.Errorf("receipt %q not found", receiptID)
			}
			// The partial unique index allows one claimed participant per device
			// per receipt.
			if pgErr.Code == "23505" {
				return nil, fmt.Errorf("device already claims a participant on receipt %q", receiptID)
			}
		}
		return nil, fmt.Errorf("failed to insert receipt user: %w", err)
	}

	if err := touchReceiptTx(ctx, tx, receiptID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &ReceiptUser{
		ID:        userID,
		ReceiptID: receiptID,
		Name:      name,
		DeviceID:  deviceID,
	}, nil
}

// RemoveUserFromReceipt deletes a participant from a receipt. Their item
// assignments go with them (receipt_item_users cascades on receipt_user_id), so
// anything they alone had claimed falls back to unclaimed.
// Returns a not-found error if the participant isn't on that receipt.
func (c *Client) RemoveUserFromReceipt(ctx context.Context, receiptID, userID string) error {
	tx, err := c.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	result, err := tx.Exec(ctx,
		"DELETE FROM receipt_users WHERE id = $1 AND receipt_id = $2",
		userID, receiptID,
	)
	if err != nil {
		return fmt.Errorf("failed to delete receipt user: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("user %q not found on receipt %q", userID, receiptID)
	}

	if err := touchReceiptTx(ctx, tx, receiptID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// AssignUserToItem assigns an item unit to a user (idempotent — assigning the
// same user/item pair twice is a no-op, not an error).
func (c *Client) AssignUserToItem(ctx context.Context, receiptUserID, receiptItemID string) (*ReceiptUserItem, error) {
	var userReceiptID, itemReceiptID string
	err := c.db.QueryRow(ctx, `
		SELECT
			COALESCE((SELECT receipt_id FROM receipt_users WHERE id = $1), ''),
			COALESCE((SELECT receipt_id FROM receipt_items WHERE id = $2), '')
	`, receiptUserID, receiptItemID).Scan(&userReceiptID, &itemReceiptID)
	if err != nil {
		return nil, fmt.Errorf("failed to verify user and item: %w", err)
	}
	if userReceiptID == "" || itemReceiptID == "" {
		return nil, fmt.Errorf("receipt user or item not found")
	}
	if userReceiptID != itemReceiptID {
		return nil, fmt.Errorf("user and item do not belong to the same receipt")
	}

	tx, err := c.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO receipt_item_users (receipt_item_id, receipt_user_id, created_at)
		VALUES ($1, $2, CURRENT_TIMESTAMP)
		ON CONFLICT (receipt_item_id, receipt_user_id) DO NOTHING
	`, receiptItemID, receiptUserID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return nil, fmt.Errorf("receipt user or item not found")
		}
		return nil, fmt.Errorf("failed to assign item to user: %w", err)
	}

	var a ReceiptUserItem
	err = tx.QueryRow(ctx, `
		SELECT receipt_user_id, receipt_item_id, created_at
		FROM receipt_item_users
		WHERE receipt_user_id = $1 AND receipt_item_id = $2
	`, receiptUserID, receiptItemID).Scan(
		&a.ReceiptUserID, &a.ReceiptItemID, &a.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to read assignment: %w", err)
	}

	if err := touchReceiptTx(ctx, tx, itemReceiptID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &a, nil
}

// UnassignUserFromItem removes an item unit's assignment to a user, if present.
func (c *Client) UnassignUserFromItem(ctx context.Context, receiptUserID, receiptItemID string) error {
	tx, err := c.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	result, err := tx.Exec(ctx,
		"DELETE FROM receipt_item_users WHERE receipt_user_id = $1 AND receipt_item_id = $2",
		receiptUserID, receiptItemID,
	)
	if err != nil {
		return fmt.Errorf("failed to unassign item from user: %w", err)
	}

	// Nothing was assigned, so nothing changed — leave the version alone so
	// polling clients aren't woken by a no-op delete.
	if result.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}

	var receiptID string
	if err := tx.QueryRow(ctx,
		"SELECT receipt_id FROM receipt_items WHERE id = $1", receiptItemID,
	).Scan(&receiptID); err != nil {
		return fmt.Errorf("failed to look up item's receipt: %w", err)
	}
	if err := touchReceiptTx(ctx, tx, receiptID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// GetReceiptUsers returns all users on a receipt ordered by creation time.
func (c *Client) GetReceiptUsers(ctx context.Context, receiptID string) ([]ReceiptUser, error) {
	rows, err := c.db.Query(ctx,
		"SELECT id, receipt_id, name, device_id, created_at FROM receipt_users WHERE receipt_id = $1 ORDER BY created_at ASC",
		receiptID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query receipt users: %w", err)
	}
	defer rows.Close()

	var users []ReceiptUser
	for rows.Next() {
		var u ReceiptUser
		if err := rows.Scan(&u.ID, &u.ReceiptID, &u.Name, &u.DeviceID, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan receipt user: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating receipt users: %w", err)
	}
	return users, nil
}

// GetReceiptItems returns all individually-assignable units on a receipt,
// ordered by their group's display order then their own display order.
func (c *Client) GetReceiptItems(ctx context.Context, receiptID string) ([]ReceiptItem, error) {
	rows, err := c.db.Query(ctx, `
		SELECT ri.id, ri.receipt_id, ri.group_id, rig.name, ri.name, ri.amount, ri.display_order
		FROM receipt_items ri
		JOIN receipt_item_groups rig ON rig.id = ri.group_id
		WHERE ri.receipt_id = $1
		ORDER BY rig.display_order ASC, ri.display_order ASC
	`, receiptID)
	if err != nil {
		return nil, fmt.Errorf("failed to query receipt items: %w", err)
	}
	defer rows.Close()

	var items []ReceiptItem
	for rows.Next() {
		var item ReceiptItem
		if err := rows.Scan(&item.ID, &item.ReceiptID, &item.GroupID, &item.GroupName, &item.Name, &item.Amount, &item.DisplayOrder); err != nil {
			return nil, fmt.Errorf("failed to scan receipt item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating receipt items: %w", err)
	}
	return items, nil
}

// GetReceiptAssignments returns all user-item assignments for a receipt.
func (c *Client) GetReceiptAssignments(ctx context.Context, receiptID string) ([]ReceiptUserItem, error) {
	rows, err := c.db.Query(ctx, `
		SELECT rui.receipt_user_id, rui.receipt_item_id, rui.created_at
		FROM receipt_item_users rui
		JOIN receipt_users ru ON ru.id = rui.receipt_user_id
		WHERE ru.receipt_id = $1
		ORDER BY rui.created_at ASC
	`, receiptID)
	if err != nil {
		return nil, fmt.Errorf("failed to query receipt assignments: %w", err)
	}
	defer rows.Close()

	var assignments []ReceiptUserItem
	for rows.Next() {
		var a ReceiptUserItem
		if err := rows.Scan(&a.ReceiptUserID, &a.ReceiptItemID, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan receipt assignment: %w", err)
		}
		assignments = append(assignments, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating receipt assignments: %w", err)
	}
	return assignments, nil
}

// ReceiptExists reports whether a receipt with the given ID exists.
func (c *Client) ReceiptExists(ctx context.Context, receiptID string) (bool, error) {
	var exists bool
	err := c.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM receipts WHERE id = $1)", receiptID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check receipt existence: %w", err)
	}
	return exists, nil
}

// GetReceiptCurrency returns the currency code for a receipt, or nil if not set.
func (c *Client) GetReceiptCurrency(ctx context.Context, receiptID string) (*string, error) {
	var currency *string
	err := c.db.QueryRow(ctx, "SELECT currency FROM receipts WHERE id = $1", receiptID).Scan(&currency)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receipt %q not found", receiptID)
		}
		return nil, fmt.Errorf("failed to get receipt currency: %w", err)
	}
	return currency, nil
}

// GetReceiptTaxTip returns the tax and tip amounts for a receipt.
func (c *Client) GetReceiptTaxTip(ctx context.Context, receiptID string) (*ReceiptTaxTip, error) {
	var tax, tip *float64
	err := c.db.QueryRow(ctx, "SELECT tax, tip FROM receipts WHERE id = $1", receiptID).Scan(&tax, &tip)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("receipt %q not found", receiptID)
		}
		return nil, fmt.Errorf("failed to get receipt tax/tip: %w", err)
	}
	return &ReceiptTaxTip{Tax: tax, Tip: tip}, nil
}

// UpdateReceiptTaxTip updates the tax and/or tip on a receipt. At least one must be non-nil.
func (c *Client) UpdateReceiptTaxTip(ctx context.Context, receiptID string, tax, tip *float64) error {
	var setClauses []string
	var args []interface{}
	n := 1
	if tax != nil {
		setClauses = append(setClauses, fmt.Sprintf("tax = $%d", n))
		args = append(args, *tax)
		n++
	}
	if tip != nil {
		setClauses = append(setClauses, fmt.Sprintf("tip = $%d", n))
		args = append(args, *tip)
		n++
	}
	if len(setClauses) == 0 {
		return fmt.Errorf("at least one of tax or tip must be provided")
	}
	args = append(args, receiptID)

	// Bump the version in the same statement so pollers see the tax/tip change.
	query := fmt.Sprintf(
		"UPDATE receipts SET %s, version = version + 1, updated_at = CURRENT_TIMESTAMP WHERE id = $%d",
		strings.Join(setClauses, ", "), n,
	)
	result, err := c.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update receipt: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("receipt %q not found", receiptID)
	}
	return nil
}

// GetUserItems returns all items assigned to a specific receipt user.
func (c *Client) GetUserItems(ctx context.Context, receiptUserID string) ([]ReceiptUserItem, error) {
	rows, err := c.db.Query(ctx,
		"SELECT receipt_user_id, receipt_item_id, created_at FROM receipt_item_users WHERE receipt_user_id = $1 ORDER BY created_at ASC",
		receiptUserID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query user items: %w", err)
	}
	defer rows.Close()

	var items []ReceiptUserItem
	for rows.Next() {
		var item ReceiptUserItem
		if err := rows.Scan(&item.ReceiptUserID, &item.ReceiptItemID, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan user item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating user items: %w", err)
	}
	return items, nil
}

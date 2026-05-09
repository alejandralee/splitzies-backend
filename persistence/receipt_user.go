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
	CreatedAt time.Time
}

// ReceiptUserItem represents the assignment of a line-item to a user.
type ReceiptUserItem struct {
	ID            string
	ReceiptUserID string
	ReceiptItemID string
	AmountOwed    *float64
	CreatedAt     time.Time
}

// ReceiptTaxTip holds optional tax and tip amounts for a receipt.
type ReceiptTaxTip struct {
	Tax *float64
	Tip *float64
}

// AddUserToReceipt creates a new user entry on a receipt.
// Returns ErrNotFound if the receipt does not exist.
func (c *Client) AddUserToReceipt(ctx context.Context, receiptID, name string) (*ReceiptUser, error) {
	userID := ulid.Make().String()

	_, err := c.db.Exec(ctx,
		"INSERT INTO receipt_users (id, receipt_id, name, created_at) VALUES ($1, $2, $3, CURRENT_TIMESTAMP)",
		userID, receiptID, name,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return nil, fmt.Errorf("receipt %q not found", receiptID)
		}
		return nil, fmt.Errorf("failed to insert receipt user: %w", err)
	}

	return &ReceiptUser{
		ID:        userID,
		ReceiptID: receiptID,
		Name:      name,
	}, nil
}

// AssignItemToUser upserts an assignment of a line-item to a user.
// On conflict the amount_owed is updated and the existing row is returned.
func (c *Client) AssignItemToUser(ctx context.Context, receiptUserID, receiptItemID string, amountOwed *float64) (*ReceiptUserItem, error) {
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

	_, err = c.db.Exec(ctx, `
		INSERT INTO receipt_user_items (id, receipt_user_id, receipt_item_id, amount_owed, created_at)
		VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)
		ON CONFLICT (receipt_user_id, receipt_item_id)
		DO UPDATE SET amount_owed = EXCLUDED.amount_owed
	`, ulid.Make().String(), receiptUserID, receiptItemID, amountOwed)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return nil, fmt.Errorf("receipt user or item not found")
		}
		return nil, fmt.Errorf("failed to assign item to user: %w", err)
	}

	// Query by the unique business key because ON CONFLICT preserves the original row ID.
	var a ReceiptUserItem
	err = c.db.QueryRow(ctx, `
		SELECT id, receipt_user_id, receipt_item_id, amount_owed, created_at
		FROM receipt_user_items
		WHERE receipt_user_id = $1 AND receipt_item_id = $2
	`, receiptUserID, receiptItemID).Scan(
		&a.ID, &a.ReceiptUserID, &a.ReceiptItemID, &a.AmountOwed, &a.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to read assignment: %w", err)
	}

	return &a, nil
}

// GetReceiptUsers returns all users on a receipt ordered by creation time.
func (c *Client) GetReceiptUsers(ctx context.Context, receiptID string) ([]ReceiptUser, error) {
	rows, err := c.db.Query(ctx,
		"SELECT id, receipt_id, name, created_at FROM receipt_users WHERE receipt_id = $1 ORDER BY created_at ASC",
		receiptID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query receipt users: %w", err)
	}
	defer rows.Close()

	var users []ReceiptUser
	for rows.Next() {
		var u ReceiptUser
		if err := rows.Scan(&u.ID, &u.ReceiptID, &u.Name, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan receipt user: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating receipt users: %w", err)
	}
	return users, nil
}

// GetReceiptItems returns all line-items on a receipt ordered by ID.
func (c *Client) GetReceiptItems(ctx context.Context, receiptID string) ([]ReceiptItem, error) {
	rows, err := c.db.Query(ctx,
		"SELECT id, receipt_id, name, quantity, total_price, price_per_item FROM receipt_items WHERE receipt_id = $1 ORDER BY id ASC",
		receiptID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query receipt items: %w", err)
	}
	defer rows.Close()

	var items []ReceiptItem
	for rows.Next() {
		var item ReceiptItem
		if err := rows.Scan(&item.ID, &item.ReceiptID, &item.Name, &item.Quantity, &item.TotalPrice, &item.PricePerItem); err != nil {
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
		SELECT rui.id, rui.receipt_user_id, rui.receipt_item_id, rui.amount_owed, rui.created_at
		FROM receipt_user_items rui
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
		if err := rows.Scan(&a.ID, &a.ReceiptUserID, &a.ReceiptItemID, &a.AmountOwed, &a.CreatedAt); err != nil {
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

	query := fmt.Sprintf("UPDATE receipts SET %s WHERE id = $%d", strings.Join(setClauses, ", "), n)
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
		"SELECT id, receipt_user_id, receipt_item_id, amount_owed, created_at FROM receipt_user_items WHERE receipt_user_id = $1 ORDER BY created_at ASC",
		receiptUserID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query user items: %w", err)
	}
	defer rows.Close()

	var items []ReceiptUserItem
	for rows.Next() {
		var item ReceiptUserItem
		if err := rows.Scan(&item.ID, &item.ReceiptUserID, &item.ReceiptItemID, &item.AmountOwed, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan user item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating user items: %w", err)
	}
	return items, nil
}

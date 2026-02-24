package persistence

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// ReceiptUser represents a user associated with a receipt
type ReceiptUser struct {
	ID        string
	ReceiptID string
	Name      string
	CreatedAt time.Time
}

// ReceiptUserItem represents the assignment of an item to a user
type ReceiptUserItem struct {
	ID            string
	ReceiptUserID string
	ReceiptItemID string
	AmountOwed    *float64 // NULL means equal split, non-NULL means custom amount
	CreatedAt     time.Time
}

// AddUserToReceipt adds a user to a receipt
func (c *Client) AddUserToReceipt(ctx context.Context, receiptID, name string) (*ReceiptUser, error) {
	// Generate ULID for user
	userID := ulid.Make().String()

	// Insert user (foreign key constraint will fail if receipt doesn't exist)
	_, err := c.db.Exec(ctx, `
		INSERT INTO receipt_users (id, receipt_id, name, created_at)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
	`, userID, receiptID, name)
	if err != nil {
		// Check if it's a foreign key violation (receipt doesn't exist)
		if strings.Contains(err.Error(), "foreign key") || strings.Contains(err.Error(), "violates foreign key") {
			return nil, fmt.Errorf("receipt not found")
		}
		return nil, fmt.Errorf("failed to insert receipt user: %w", err)
	}

	user := &ReceiptUser{
		ID:        userID,
		ReceiptID: receiptID,
		Name:      name,
		// CreatedAt is kept in DB but not surfaced in responses
	}

	return user, nil
}

// AssignItemToUser assigns an item to a user
// If amountPaid is nil, it means equal split (will be calculated when needed)
// If amountPaid is set, it's a custom amount
func (c *Client) AssignItemToUser(ctx context.Context, receiptUserID, receiptItemID string, amountPaid *float64) (*ReceiptUserItem, error) {
	// Verify user and item belong to the same receipt (this also verifies they exist)
	var userReceiptID, itemReceiptID string
	err := c.db.QueryRow(ctx, `
		SELECT 
			(SELECT receipt_id FROM receipt_users WHERE id = $1),
			(SELECT receipt_id FROM receipt_items WHERE id = $2)
	`, receiptUserID, receiptItemID).Scan(&userReceiptID, &itemReceiptID)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, fmt.Errorf("receipt user or item not found")
		}
		return nil, fmt.Errorf("failed to verify user and item: %w", err)
	}
	if userReceiptID != itemReceiptID {
		return nil, fmt.Errorf("user and item must belong to the same receipt")
	}

	// Generate ULID for assignment
	assignmentID := ulid.Make().String()

	// Insert assignment (or update if exists due to unique constraint)
	// Foreign key constraints will fail if user or item doesn't exist
	_, err = c.db.Exec(ctx, `
		INSERT INTO receipt_user_items (id, receipt_user_id, receipt_item_id, amount_owed, created_at)
		VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)
		ON CONFLICT (receipt_user_id, receipt_item_id) 
		DO UPDATE SET amount_owed = EXCLUDED.amount_owed
	`, assignmentID, receiptUserID, receiptItemID, amountPaid)
	if err != nil {
		// Check if it's a foreign key violation
		if strings.Contains(err.Error(), "foreign key") || strings.Contains(err.Error(), "violates foreign key") {
			return nil, fmt.Errorf("receipt user or item not found")
		}
		return nil, fmt.Errorf("failed to assign item to user: %w", err)
	}

	// Get amount_owed (for conflict case where it might have been updated)
	var dbAmountOwed *float64
	err = c.db.QueryRow(ctx, "SELECT amount_owed FROM receipt_user_items WHERE id = $1", assignmentID).Scan(&dbAmountOwed)
	if err != nil {
		return nil, fmt.Errorf("failed to get receipt user item data: %w", err)
	}

	assignment := &ReceiptUserItem{
		ID:            assignmentID,
		ReceiptUserID: receiptUserID,
		ReceiptItemID: receiptItemID,
		AmountOwed:    dbAmountOwed,
		// CreatedAt is kept in DB but not surfaced in responses
	}

	return assignment, nil
}

// GetReceiptUsers gets all users for a receipt
func (c *Client) GetReceiptUsers(ctx context.Context, receiptID string) ([]ReceiptUser, error) {
	rows, err := c.db.Query(ctx, `
		SELECT id, receipt_id, name, created_at
		FROM receipt_users
		WHERE receipt_id = $1
		ORDER BY created_at ASC
	`, receiptID)
	if err != nil {
		return nil, fmt.Errorf("failed to query receipt users: %w", err)
	}
	defer rows.Close()

	users := make([]ReceiptUser, 0)
	for rows.Next() {
		var user ReceiptUser
		err := rows.Scan(&user.ID, &user.ReceiptID, &user.Name, &user.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan receipt user: %w", err)
		}
		users = append(users, user)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating receipt users: %w", err)
	}

	return users, nil
}

// ReceiptTaxTip holds tax and tip for a receipt
type ReceiptTaxTip struct {
	Tax *float64
	Tip *float64
}

// GetReceiptCurrency gets the currency code for a receipt (nil if not set).
func (c *Client) GetReceiptCurrency(ctx context.Context, receiptID string) (*string, error) {
	var currency *string
	err := c.db.QueryRow(ctx, "SELECT currency FROM receipts WHERE id = $1", receiptID).Scan(&currency)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, fmt.Errorf("receipt not found")
		}
		return nil, fmt.Errorf("failed to get receipt currency: %w", err)
	}
	return currency, nil
}

// GetReceiptTaxTip gets tax and tip for a receipt
func (c *Client) GetReceiptTaxTip(ctx context.Context, receiptID string) (*ReceiptTaxTip, error) {
	var tax, tip *float64
	err := c.db.QueryRow(ctx, "SELECT tax, tip FROM receipts WHERE id = $1", receiptID).Scan(&tax, &tip)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, fmt.Errorf("receipt not found")
		}
		return nil, fmt.Errorf("failed to get receipt tax/tip: %w", err)
	}
	return &ReceiptTaxTip{Tax: tax, Tip: tip}, nil
}

// UpdateReceiptTaxTip sets tax and/or tip for a receipt. Pass nil for fields to leave unchanged.
func (c *Client) UpdateReceiptTaxTip(ctx context.Context, receiptID string, tax, tip *float64) error {
	var setClauses []string
	var args []interface{}
	argNum := 1
	if tax != nil {
		setClauses = append(setClauses, fmt.Sprintf("tax = $%d", argNum))
		args = append(args, *tax)
		argNum++
	}
	if tip != nil {
		setClauses = append(setClauses, fmt.Sprintf("tip = $%d", argNum))
		args = append(args, *tip)
		argNum++
	}
	if len(setClauses) == 0 {
		return fmt.Errorf("at least one of tax or tip must be provided")
	}
	args = append(args, receiptID)
	query := fmt.Sprintf("UPDATE receipts SET %s WHERE id = $%d", strings.Join(setClauses, ", "), argNum)
	result, err := c.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update receipt tax/tip: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("receipt not found")
	}
	return nil
}

// ReceiptExists checks if a receipt exists
func (c *Client) ReceiptExists(ctx context.Context, receiptID string) (bool, error) {
	var exists bool
	err := c.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM receipts WHERE id = $1)", receiptID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check receipt existence: %w", err)
	}
	return exists, nil
}

// GetReceiptItems gets all items for a receipt
func (c *Client) GetReceiptItems(ctx context.Context, receiptID string) ([]ReceiptItem, error) {
	rows, err := c.db.Query(ctx, `
		SELECT id, receipt_id, name, quantity, total_price, price_per_item
		FROM receipt_items
		WHERE receipt_id = $1
		ORDER BY id ASC
	`, receiptID)
	if err != nil {
		return nil, fmt.Errorf("failed to query receipt items: %w", err)
	}
	defer rows.Close()

	items := make([]ReceiptItem, 0)
	for rows.Next() {
		var item ReceiptItem
		err := rows.Scan(&item.ID, &item.ReceiptID, &item.Name, &item.Quantity, &item.TotalPrice, &item.PricePerItem)
		if err != nil {
			return nil, fmt.Errorf("failed to scan receipt item: %w", err)
		}
		items = append(items, item)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating receipt items: %w", err)
	}

	return items, nil
}

// GetReceiptAssignments gets all user-item assignments for a receipt
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

	assignments := make([]ReceiptUserItem, 0)
	for rows.Next() {
		var a ReceiptUserItem
		err := rows.Scan(&a.ID, &a.ReceiptUserID, &a.ReceiptItemID, &a.AmountOwed, &a.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan receipt assignment: %w", err)
		}
		assignments = append(assignments, a)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating receipt assignments: %w", err)
	}

	return assignments, nil
}

// GetUserItems gets all items assigned to a user
func (c *Client) GetUserItems(ctx context.Context, receiptUserID string) ([]ReceiptUserItem, error) {
	rows, err := c.db.Query(ctx, `
		SELECT id, receipt_user_id, receipt_item_id, amount_owed, created_at
		FROM receipt_user_items
		WHERE receipt_user_id = $1
		ORDER BY created_at ASC
	`, receiptUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to query user items: %w", err)
	}
	defer rows.Close()

	items := make([]ReceiptUserItem, 0)
	for rows.Next() {
		var item ReceiptUserItem
		err := rows.Scan(&item.ID, &item.ReceiptUserID, &item.ReceiptItemID, &item.AmountOwed, &item.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan user item: %w", err)
		}
		items = append(items, item)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating user items: %w", err)
	}

	return items, nil
}

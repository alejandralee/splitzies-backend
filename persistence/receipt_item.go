package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
)

// CreateReceiptItem adds one individually-assignable unit to a receipt.
//
// When groupID is nil, a new group is created (named name) holding this single
// unit at amount. When groupID is non-nil, the new unit joins that existing
// group instead — it adopts the group's name and copies the amount of an
// existing unit in the group, since every unit in a group is kept at the same
// price (edited together via PatchReceiptItemGroup). name/amount are ignored
// in that case.
func (c *Client) CreateReceiptItem(ctx context.Context, receiptID string, groupID *string, name string, amount float64) (*ReceiptItem, error) {
	tx, err := c.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var resolvedGroupID, resolvedGroupName string
	var displayOrder int

	if groupID != nil {
		var existingReceiptID string
		err := tx.QueryRow(ctx,
			"SELECT receipt_id, name FROM receipt_item_groups WHERE id = $1",
			*groupID,
		).Scan(&existingReceiptID, &resolvedGroupName)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("item group %q not found", *groupID)
			}
			return nil, fmt.Errorf("failed to look up item group: %w", err)
		}
		if existingReceiptID != receiptID {
			return nil, fmt.Errorf("item group %q not found", *groupID)
		}
		resolvedGroupID = *groupID

		if err := tx.QueryRow(ctx,
			"SELECT amount FROM receipt_items WHERE group_id = $1 ORDER BY display_order DESC LIMIT 1",
			resolvedGroupID,
		).Scan(&amount); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("failed to look up existing unit amount: %w", err)
		}

		if err := tx.QueryRow(ctx,
			"SELECT COUNT(*) FROM receipt_items WHERE group_id = $1",
			resolvedGroupID,
		).Scan(&displayOrder); err != nil {
			return nil, fmt.Errorf("failed to count existing units: %w", err)
		}
		name = resolvedGroupName
	} else {
		resolvedGroupID = ulid.Make().String()
		resolvedGroupName = name
		displayOrder = 0

		var groupCount int
		if err := tx.QueryRow(ctx,
			"SELECT COUNT(*) FROM receipt_item_groups WHERE receipt_id = $1",
			receiptID,
		).Scan(&groupCount); err != nil {
			return nil, fmt.Errorf("failed to count existing groups: %w", err)
		}

		_, err = tx.Exec(ctx,
			"INSERT INTO receipt_item_groups (id, receipt_id, name, display_order) VALUES ($1, $2, $3, $4)",
			resolvedGroupID, receiptID, resolvedGroupName, groupCount,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to insert item group: %w", err)
		}
	}

	itemID := ulid.Make().String()
	_, err = tx.Exec(ctx,
		"INSERT INTO receipt_items (id, receipt_id, group_id, name, amount, display_order) VALUES ($1, $2, $3, $4, $5, $6)",
		itemID, receiptID, resolvedGroupID, name, amount, displayOrder,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert receipt item: %w", err)
	}

	if err := touchReceiptTx(ctx, tx, receiptID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &ReceiptItem{
		ID:           itemID,
		ReceiptID:    receiptID,
		GroupID:      resolvedGroupID,
		GroupName:    resolvedGroupName,
		Name:         name,
		Amount:       amount,
		DisplayOrder: displayOrder,
	}, nil
}

// PatchReceiptItemGroup updates a group's name and/or the amount of every unit
// in it. At least one of name or amount must be non-nil.
func (c *Client) PatchReceiptItemGroup(ctx context.Context, receiptID, groupID string, name *string, amount *float64) error {
	if name == nil && amount == nil {
		return fmt.Errorf("at least one of name or amount must be provided")
	}

	tx, err := c.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var existingReceiptID string
	err = tx.QueryRow(ctx,
		"SELECT receipt_id FROM receipt_item_groups WHERE id = $1",
		groupID,
	).Scan(&existingReceiptID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("item group %q not found", groupID)
		}
		return fmt.Errorf("failed to look up item group: %w", err)
	}
	if existingReceiptID != receiptID {
		return fmt.Errorf("item group %q not found", groupID)
	}

	if name != nil {
		if _, err := tx.Exec(ctx,
			"UPDATE receipt_item_groups SET name = $1 WHERE id = $2",
			*name, groupID,
		); err != nil {
			return fmt.Errorf("failed to update item group name: %w", err)
		}
		if _, err := tx.Exec(ctx,
			"UPDATE receipt_items SET name = $1 WHERE group_id = $2",
			*name, groupID,
		); err != nil {
			return fmt.Errorf("failed to update item unit names: %w", err)
		}
	}
	if amount != nil {
		if _, err := tx.Exec(ctx,
			"UPDATE receipt_items SET amount = $1 WHERE group_id = $2",
			*amount, groupID,
		); err != nil {
			return fmt.Errorf("failed to update item unit amounts: %w", err)
		}
	}

	if err := touchReceiptTx(ctx, tx, receiptID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// DeleteReceiptItem removes a single unit from a receipt. Its assignments
// cascade via the receipt_item_users foreign key. If it was the last unit in
// its group, the now-empty group is removed too.
func (c *Client) DeleteReceiptItem(ctx context.Context, receiptID, itemID string) error {
	tx, err := c.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var groupID string
	err = tx.QueryRow(ctx,
		"SELECT group_id FROM receipt_items WHERE id = $1 AND receipt_id = $2",
		itemID, receiptID,
	).Scan(&groupID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("item %q not found on receipt %q", itemID, receiptID)
		}
		return fmt.Errorf("failed to look up item: %w", err)
	}

	if _, err := tx.Exec(ctx, "DELETE FROM receipt_items WHERE id = $1", itemID); err != nil {
		return fmt.Errorf("failed to delete item: %w", err)
	}

	var remaining int
	if err := tx.QueryRow(ctx,
		"SELECT COUNT(*) FROM receipt_items WHERE group_id = $1",
		groupID,
	).Scan(&remaining); err != nil {
		return fmt.Errorf("failed to count remaining units: %w", err)
	}
	if remaining == 0 {
		if _, err := tx.Exec(ctx, "DELETE FROM receipt_item_groups WHERE id = $1", groupID); err != nil {
			return fmt.Errorf("failed to delete empty item group: %w", err)
		}
	}

	if err := touchReceiptTx(ctx, tx, receiptID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

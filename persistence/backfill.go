package persistence

import (
	"context"
	"fmt"

	"github.com/oklog/ulid/v2"
)

// BackfillItemGroups explodes every legacy receipt_items row (one row per
// parsed line, with quantity/total_price/price_per_item) into a
// receipt_item_groups row plus one receipt_items_new row per unit. Intended
// to run once, after the "add_item_groups_and_units" migration and before the
// "drop_legacy_item_tables" migration. Safe to re-run: it only reads from the
// legacy receipt_items table, which the follow-up migration drops once this
// has succeeded, and does not touch receipt_user_items (unused in practice,
// so its assignments are not carried forward).
func (c *Client) BackfillItemGroups(ctx context.Context) error {
	rows, err := c.db.Query(ctx,
		"SELECT id, receipt_id, name, quantity, total_price, price_per_item FROM receipt_items ORDER BY receipt_id ASC, id ASC",
	)
	if err != nil {
		return fmt.Errorf("failed to query legacy receipt items: %w", err)
	}
	type legacyItem struct {
		id, receiptID, name      string
		quantity                 int
		totalPrice, pricePerItem float64
	}
	var legacy []legacyItem
	for rows.Next() {
		var li legacyItem
		if err := rows.Scan(&li.id, &li.receiptID, &li.name, &li.quantity, &li.totalPrice, &li.pricePerItem); err != nil {
			rows.Close()
			return fmt.Errorf("failed to scan legacy receipt item: %w", err)
		}
		legacy = append(legacy, li)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("error iterating legacy receipt items: %w", err)
	}
	rows.Close()

	groupOrder := make(map[string]int) // receiptID -> next display_order
	for _, li := range legacy {
		quantity := max(li.quantity, 1)
		amount := li.pricePerItem
		if amount == 0 {
			amount = li.totalPrice / float64(quantity)
		}

		groupID := ulid.Make().String()
		order := groupOrder[li.receiptID]
		groupOrder[li.receiptID] = order + 1

		if _, err := c.db.Exec(ctx,
			"INSERT INTO receipt_item_groups (id, receipt_id, name, display_order) VALUES ($1, $2, $3, $4)",
			groupID, li.receiptID, li.name, order,
		); err != nil {
			return fmt.Errorf("failed to insert backfilled group for legacy item %q: %w", li.id, err)
		}

		for unit := 0; unit < quantity; unit++ {
			itemID := ulid.Make().String()
			if _, err := c.db.Exec(ctx,
				"INSERT INTO receipt_items_new (id, receipt_id, group_id, name, amount, display_order) VALUES ($1, $2, $3, $4, $5, $6)",
				itemID, li.receiptID, groupID, li.name, amount, unit,
			); err != nil {
				return fmt.Errorf("failed to insert backfilled unit %d for legacy item %q: %w", unit, li.id, err)
			}
		}
	}

	return nil
}

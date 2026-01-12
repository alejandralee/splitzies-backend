package persistence

import (
	"context"
	"fmt"

	"github.com/oklog/ulid/v2"
)

// Receipt represents a receipt in the database
type Receipt struct {
	ID        string
	CreatedAt string
	ImageURL  *string
	Items     []ReceiptItem
}

// ReceiptItem represents a receipt item in the database
type ReceiptItem struct {
	ID           string
	ReceiptID    string
	Name         string
	Quantity     int
	TotalPrice   float64
	PricePerItem float64
}

// SaveReceipt saves a receipt with its items to the database
// imageURL is optional - pass nil if no image is provided
func SaveReceipt(items []ReceiptItemDB, imageURL *string) (*Receipt, error) {
	ctx := context.Background()
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	// Generate ULID for receipt
	receiptID := ulid.Make().String()

	// Start a transaction
	tx, err := DB.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Insert receipt with generated ULID and optional image URL
	_, err = tx.Exec(ctx, "INSERT INTO receipts (id, created_at, image_url) VALUES ($1, CURRENT_TIMESTAMP, $2)", receiptID, imageURL)
	if err != nil {
		return nil, fmt.Errorf("failed to insert receipt: %w", err)
	}

	dbItems := make([]ReceiptItem, 0, len(items))
	for _, item := range items {
		// Generate ULID for each item
		itemID := ulid.Make().String()

		_, err := tx.Exec(ctx, `
			INSERT INTO receipt_items (id, receipt_id, name, quantity, total_price, price_per_item)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, itemID, receiptID, item.Name, item.Quantity, item.TotalPrice, item.PricePerItem)
		if err != nil {
			return nil, fmt.Errorf("failed to insert receipt item: %w", err)
		}

		dbItems = append(dbItems, ReceiptItem{
			ID:           itemID,
			ReceiptID:    receiptID,
			Name:         item.Name,
			Quantity:     item.Quantity,
			TotalPrice:   item.TotalPrice,
			PricePerItem: item.PricePerItem,
		})
	}

	// Commit transaction
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Get receipt with created_at timestamp and image_url
	var createdAt string
	var dbImageURL *string
	err = DB.QueryRow(ctx, "SELECT created_at, image_url FROM receipts WHERE id = $1", receiptID).Scan(&createdAt, &dbImageURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get receipt timestamp: %w", err)
	}

	receipt := &Receipt{
		ID:        receiptID,
		CreatedAt: createdAt,
		ImageURL:  dbImageURL,
		Items:     dbItems,
	}

	return receipt, nil
}

// ReceiptItemDB is used for saving items to the database (with non-nullable float64)
type ReceiptItemDB struct {
	Name         string
	Quantity     int
	TotalPrice   float64
	PricePerItem float64
}

// GenerateReceiptID generates a new ULID for a receipt
func GenerateReceiptID() string {
	return ulid.Make().String()
}

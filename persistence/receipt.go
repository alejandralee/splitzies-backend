package persistence

import (
	"fmt"

	"github.com/oklog/ulid/v2"
)

// Receipt represents a receipt in the database
type Receipt struct {
	ID        string
	CreatedAt string
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
func SaveReceipt(items []ReceiptItemDB) (*Receipt, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	// Generate ULID for receipt
	receiptID := ulid.Make().String()

	// Start a transaction
	tx, err := DB.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert receipt with generated ULID
	_, err = tx.Exec("INSERT INTO receipts (id, created_at) VALUES ($1, CURRENT_TIMESTAMP)", receiptID)
	if err != nil {
		return nil, fmt.Errorf("failed to insert receipt: %w", err)
	}

	// Insert receipt items
	stmt, err := tx.Prepare(`
		INSERT INTO receipt_items (id, receipt_id, name, quantity, total_price, price_per_item)
		VALUES ($1, $2, $3, $4, $5, $6)
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	dbItems := make([]ReceiptItem, 0, len(items))
	for _, item := range items {
		// Generate ULID for each item
		itemID := ulid.Make().String()
		
		_, err := stmt.Exec(itemID, receiptID, item.Name, item.Quantity, item.TotalPrice, item.PricePerItem)
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
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Get receipt with created_at timestamp
	var createdAt string
	err = DB.QueryRow("SELECT created_at FROM receipts WHERE id = $1", receiptID).Scan(&createdAt)
	if err != nil {
		return nil, fmt.Errorf("failed to get receipt timestamp: %w", err)
	}

	receipt := &Receipt{
		ID:        receiptID,
		CreatedAt: createdAt,
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

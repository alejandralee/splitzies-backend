package persistence

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// Receipt represents a receipt in the database
type Receipt struct {
	ID          string
	CreatedAt   time.Time
	ImageURL    *string
	OCRText     *OCRTextData
	Currency    *string
	ReceiptDate *string
	Title       *string
	Items       []ReceiptItem
}

// OCRTextData represents the OCR text data stored as JSONB
type OCRTextData struct {
	Text string `json:"text"`
}

// Value implements driver.Valuer for JSONB storage
func (o *OCRTextData) Value() (driver.Value, error) {
	if o == nil {
		return nil, nil
	}
	return json.Marshal(o)
}

// Scan implements sql.Scanner for JSONB retrieval
func (o *OCRTextData) Scan(value interface{}) error {
	if value == nil {
		*o = OCRTextData{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("cannot scan %T into OCRTextData", value)
	}
	if len(bytes) == 0 {
		*o = OCRTextData{}
		return nil
	}
	return json.Unmarshal(bytes, o)
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
// ocrText is optional - pass nil if no OCR text is provided
// tax and tip are optional - parsed from receipt or can be set via PATCH later
func SaveReceipt(items []ReceiptItemDB, imageURL *string, ocrText *OCRTextData, currency *string, receiptDate *string, title *string, tax *float64, tip *float64) (*Receipt, error) {
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

	// Convert OCRTextData to JSONB
	var ocrTextJSON []byte
	if ocrText != nil {
		ocrTextJSON, err = json.Marshal(ocrText)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal OCR text: %w", err)
		}
	}

	// Insert receipt with generated ULID, optional image URL, optional OCR text, Gemini metadata, and tax/tip if parsed
	_, err = tx.Exec(ctx, "INSERT INTO receipts (id, created_at, image_url, ocr_text, currency, receipt_date, title, tax, tip) VALUES ($1, CURRENT_TIMESTAMP, $2, $3, $4, $5, $6, $7, $8)", receiptID, imageURL, ocrTextJSON, currency, receiptDate, title, tax, tip)
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

	// Get receipt with created_at timestamp, image_url, ocr_text, and metadata
	var createdAt time.Time
	var dbImageURL *string
	var dbOCRTextJSON []byte
	var dbCurrency *string
	var dbReceiptDate *string
	var dbTitle *string
	err = DB.QueryRow(ctx, "SELECT created_at, image_url, ocr_text, currency, receipt_date, title FROM receipts WHERE id = $1", receiptID).Scan(&createdAt, &dbImageURL, &dbOCRTextJSON, &dbCurrency, &dbReceiptDate, &dbTitle)
	if err != nil {
		return nil, fmt.Errorf("failed to get receipt data: %w", err)
	}

	var dbOCRText *OCRTextData
	if len(dbOCRTextJSON) > 0 {
		dbOCRText = &OCRTextData{}
		if err := json.Unmarshal(dbOCRTextJSON, dbOCRText); err != nil {
			return nil, fmt.Errorf("failed to unmarshal OCR text: %w", err)
		}
	}

	receipt := &Receipt{
		ID:          receiptID,
		CreatedAt:   createdAt,
		ImageURL:    dbImageURL,
		OCRText:     dbOCRText,
		Currency:    dbCurrency,
		ReceiptDate: dbReceiptDate,
		Title:       dbTitle,
		Items:       dbItems,
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

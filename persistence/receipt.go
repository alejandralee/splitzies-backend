package persistence

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// Receipt represents a receipt returned after saving.
type Receipt struct {
	ID          string
	CreatedAt   time.Time
	ImageURL    *string
	OCRText     *OCRTextData
	Currency    *string
	ReceiptDate *time.Time
	Title       *string
	Items       []ReceiptItem
}

// OCRTextData is the JSONB-stored OCR result.
type OCRTextData struct {
	Text string `json:"text"`
}

func (o *OCRTextData) Value() (driver.Value, error) {
	if o == nil {
		return nil, nil
	}
	return json.Marshal(o)
}

func (o *OCRTextData) Scan(value interface{}) error {
	if value == nil {
		*o = OCRTextData{}
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("cannot scan %T into OCRTextData", value)
	}
	if len(b) == 0 {
		*o = OCRTextData{}
		return nil
	}
	return json.Unmarshal(b, o)
}

// ReceiptItem represents a receipt line-item from the database.
type ReceiptItem struct {
	ID           string
	ReceiptID    string
	Name         string
	Quantity     int
	TotalPrice   float64
	PricePerItem float64
}

// ReceiptItemDB is used when inserting items (no ID yet).
type ReceiptItemDB struct {
	Name         string
	Quantity     int
	TotalPrice   float64
	PricePerItem float64
}

// GenerateReceiptID generates a new ULID string.
func GenerateReceiptID() string {
	return ulid.Make().String()
}

// SaveReceipt inserts a receipt and its items in a single transaction and
// returns the persisted Receipt. All optional fields may be nil.
func (c *Client) SaveReceipt(
	ctx context.Context,
	items []ReceiptItemDB,
	imageURL *string,
	ocrText *OCRTextData,
	currency *string,
	receiptDate *time.Time,
	title *string,
	tax *float64,
	tip *float64,
) (*Receipt, error) {
	receiptID := ulid.Make().String()

	tx, err := c.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var ocrTextJSON []byte
	if ocrText != nil {
		ocrTextJSON, err = json.Marshal(ocrText)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal OCR text: %w", err)
		}
	}

	_, err = tx.Exec(ctx,
		"INSERT INTO receipts (id, created_at, image_url, ocr_text, currency, receipt_date, title, tax, tip) VALUES ($1, CURRENT_TIMESTAMP, $2, $3, $4, $5, $6, $7, $8)",
		receiptID, imageURL, ocrTextJSON, currency, receiptDate, title, tax, tip,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert receipt: %w", err)
	}

	savedItems := make([]ReceiptItem, 0, len(items))
	for _, item := range items {
		itemID := ulid.Make().String()
		_, err := tx.Exec(ctx,
			"INSERT INTO receipt_items (id, receipt_id, name, quantity, total_price, price_per_item) VALUES ($1, $2, $3, $4, $5, $6)",
			itemID, receiptID, item.Name, item.Quantity, item.TotalPrice, item.PricePerItem,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to insert receipt item: %w", err)
		}
		savedItems = append(savedItems, ReceiptItem{
			ID:           itemID,
			ReceiptID:    receiptID,
			Name:         item.Name,
			Quantity:     item.Quantity,
			TotalPrice:   item.TotalPrice,
			PricePerItem: item.PricePerItem,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	var (
		createdAt   time.Time
		dbImageURL  *string
		dbOCRJSON   []byte
		dbCurrency  *string
		dbDate      *time.Time
		dbTitle     *string
	)
	err = c.db.QueryRow(ctx,
		"SELECT created_at, image_url, ocr_text, currency, receipt_date, title FROM receipts WHERE id = $1",
		receiptID,
	).Scan(&createdAt, &dbImageURL, &dbOCRJSON, &dbCurrency, &dbDate, &dbTitle)
	if err != nil {
		return nil, fmt.Errorf("failed to read saved receipt: %w", err)
	}

	var dbOCRText *OCRTextData
	if len(dbOCRJSON) > 0 {
		dbOCRText = &OCRTextData{}
		if err := json.Unmarshal(dbOCRJSON, dbOCRText); err != nil {
			return nil, fmt.Errorf("failed to unmarshal OCR text: %w", err)
		}
	}

	return &Receipt{
		ID:          receiptID,
		CreatedAt:   createdAt,
		ImageURL:    dbImageURL,
		OCRText:     dbOCRText,
		Currency:    dbCurrency,
		ReceiptDate: dbDate,
		Title:       dbTitle,
		Items:       savedItems,
	}, nil
}

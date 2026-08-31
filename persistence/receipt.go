package persistence

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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

// ReceiptItemGroup represents a display-only grouping of receipt_items
// (e.g. "Margarita" grouping 3 individually-assignable units).
type ReceiptItemGroup struct {
	ID           string
	ReceiptID    string
	Name         string
	DisplayOrder int
}

// ReceiptItem represents a single, individually-assignable unit from the database.
// A quantity>1 line item is stored as multiple ReceiptItem rows sharing a GroupID.
type ReceiptItem struct {
	ID           string
	ReceiptID    string
	GroupID      string
	GroupName    string
	Name         string
	Amount       float64
	DisplayOrder int
}

// ReceiptItemDB is the input shape for SaveReceipt — what OCR/parsing produces
// (still "3 Margaritas @ $12"); SaveReceipt explodes this into one group plus
// Quantity individual ReceiptItem rows.
type ReceiptItemDB struct {
	Name         string
	Quantity     int
	TotalPrice   float64
	PricePerItem float64
}

// unitAmount returns the per-unit price for exploding a parsed line item into
// individual units, falling back to TotalPrice/Quantity if PricePerItem is
// missing/zero (some OCR parses only populate one of the two fields).
func (item ReceiptItemDB) unitAmount() float64 {
	if item.PricePerItem != 0 {
		return item.PricePerItem
	}
	if item.Quantity > 0 {
		return item.TotalPrice / float64(item.Quantity)
	}
	return item.TotalPrice
}

// GenerateReceiptID generates a new ULID string.
func GenerateReceiptID() string {
	return ulid.Make().String()
}

// touchReceiptTx bumps a receipt's version, which clients poll via ETag to
// notice edits made by other people on the same bill. Every mutation must call
// this inside its own transaction so the version and the change land together.
func touchReceiptTx(ctx context.Context, tx pgx.Tx, receiptID string) error {
	result, err := tx.Exec(ctx,
		"UPDATE receipts SET version = version + 1, updated_at = CURRENT_TIMESTAMP WHERE id = $1",
		receiptID,
	)
	if err != nil {
		return fmt.Errorf("failed to bump receipt version: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("receipt %q not found", receiptID)
	}
	return nil
}

// GetReceiptVersion returns a receipt's current version. It doubles as an
// existence check: a missing receipt yields ErrNoRows, mapped to a not-found
// error, which is what lets GetReceiptHandler answer a conditional request
// without running the full read.
func (c *Client) GetReceiptVersion(ctx context.Context, receiptID string) (int, error) {
	var version int
	err := c.db.QueryRow(ctx, "SELECT version FROM receipts WHERE id = $1", receiptID).Scan(&version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("receipt %q not found", receiptID)
		}
		return 0, fmt.Errorf("failed to get receipt version: %w", err)
	}
	return version, nil
}

// SaveReceipt inserts a receipt and its items in a single transaction and
// returns the persisted Receipt. All optional fields may be nil. When
// ownerDeviceID is non-nil the receipt is recorded in that device's history.
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
	ownerDeviceID *string,
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

	if ownerDeviceID != nil {
		_, err = tx.Exec(ctx,
			"INSERT INTO receipt_devices (receipt_id, device_id, role, joined_at) VALUES ($1, $2, $3, CURRENT_TIMESTAMP)",
			receiptID, *ownerDeviceID, RoleOwner,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to record receipt owner: %w", err)
		}
	}

	savedItems := make([]ReceiptItem, 0, len(items))
	for groupOrder, item := range items {
		groupID := ulid.Make().String()
		_, err := tx.Exec(ctx,
			"INSERT INTO receipt_item_groups (id, receipt_id, name, display_order) VALUES ($1, $2, $3, $4)",
			groupID, receiptID, item.Name, groupOrder,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to insert receipt item group: %w", err)
		}

		amount := item.unitAmount()
		quantity := max(item.Quantity, 1)
		for unit := 0; unit < quantity; unit++ {
			itemID := ulid.Make().String()
			_, err := tx.Exec(ctx,
				"INSERT INTO receipt_items (id, receipt_id, group_id, name, amount, display_order) VALUES ($1, $2, $3, $4, $5, $6)",
				itemID, receiptID, groupID, item.Name, amount, unit,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to insert receipt item: %w", err)
			}
			savedItems = append(savedItems, ReceiptItem{
				ID:           itemID,
				ReceiptID:    receiptID,
				GroupID:      groupID,
				GroupName:    item.Name,
				Name:         item.Name,
				Amount:       amount,
				DisplayOrder: unit,
			})
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	var (
		createdAt  time.Time
		dbImageURL *string
		dbOCRJSON  []byte
		dbCurrency *string
		dbDate     *time.Time
		dbTitle    *string
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

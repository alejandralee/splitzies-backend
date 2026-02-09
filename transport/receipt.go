package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"splitzies/persistence"
	"splitzies/storage"
)

// ReceiptItem represents a single item in a receipt
type ReceiptItem struct {
	Name         string   `json:"name"`
	Quantity     int      `json:"quantity"`
	TotalPrice   *float64 `json:"total_price,omitempty"`    // Optional, can be calculated
	PricePerItem *float64 `json:"price_per_item,omitempty"` // Optional, can be calculated
}

// AddReceiptRequest represents the request body for adding a receipt
type AddReceiptRequest struct {
	Items []ReceiptItem `json:"items"`
}

// AddReceiptResponse represents the response after processing a receipt
type AddReceiptResponse struct {
	Message  string        `json:"message"`
	Items    []ReceiptItem `json:"items"`
	ImageURL *string       `json:"image_url,omitempty"`
}

// UploadReceiptImageHandler handles receipt image uploads
// Expects multipart/form-data with:
//   - "image": the receipt image file
//
// Returns the uploaded image URL
func (t *Transport) UploadReceiptImageHandler(w http.ResponseWriter, r *http.Request) {
	// Generate receipt ID first (we'll create a receipt record with just the image)
	ctx := context.Background()
	receiptID := persistence.GenerateReceiptID()

	file, contentType, err := t.validateReceiptImageRequest(w, r)
	if err != nil {
		return
	}
	defer file.Close()

	// Read file data for OCR (we need to read it before uploading)
	// Reset file position after reading
	fileData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read image file: %v", err), http.StatusInternalServerError)
		return
	}

	// Upload image to GCS (using the data we just read)
	imageURL, err := t.gcsClient.UploadReceiptImageFromReader(ctx, bytes.NewReader(fileData), receiptID, contentType)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to upload image: %v", err), http.StatusInternalServerError)
		return
	}

	// Perform OCR on the image
	ocrText, err := t.visionClient.PerformOCRFromBytes(ctx, fileData)
	var parsedItems []persistence.ReceiptItemDB
	var ocrTextData *persistence.OCRTextData
	var currency *string
	var receiptDate *string
	var title *string

	if err != nil {
		// OCR failed - log but don't fail the request
		// We'll save the receipt without OCR text
		fmt.Printf("Warning: OCR failed: %v\n", err)
	} else if ocrText != "" {
		// Always save OCR text for reference
		ocrTextData = &persistence.OCRTextData{
			Text: ocrText,
		}

		// Try to parse receipt items from OCR text using Gemini
		parseResult, parseErr := storage.ParseReceiptItemsWithGemini(ctx, ocrText)
		if parseErr != nil {
			fmt.Printf("Warning: Gemini parse failed: %v\n", parseErr)
			parseResult.Items = storage.ExtractReceiptItemsFromText(ocrText)
			parseResult.Currency = nil
			parseResult.ReceiptDate = nil
			parseResult.Title = nil
		}
		currency = parseResult.Currency
		receiptDate = parseResult.ReceiptDate
		title = parseResult.Title

		if len(parseResult.Items) > 0 {
			// Successfully parsed items - convert to ReceiptItemDB and save them
			parsedItems = make([]persistence.ReceiptItemDB, len(parseResult.Items))
			for i, item := range parseResult.Items {
				parsedItems[i] = persistence.ReceiptItemDB{
					Name:         item.Name,
					Quantity:     item.Quantity,
					TotalPrice:   item.TotalPrice,
					PricePerItem: item.PricePerItem,
				}
			}
		}
		// If items couldn't be parsed, we still save the OCR text (already set above)
	}

	// Save receipt with image URL, parsed items (if any), OCR text, and Gemini metadata
	savedReceipt, err := persistence.SaveReceipt(parsedItems, &imageURL, ocrTextData, currency, receiptDate, title)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to save receipt: %v", err), http.StatusInternalServerError)
		return
	}

	// Convert parsed items to response format
	responseItems := make([]ReceiptItem, len(parsedItems))
	for i, item := range parsedItems {
		totalPrice := item.TotalPrice
		pricePerItem := item.PricePerItem
		responseItems[i] = ReceiptItem{
			Name:         item.Name,
			Quantity:     item.Quantity,
			TotalPrice:   &totalPrice,
			PricePerItem: &pricePerItem,
		}
	}

	// Return success response
	response := map[string]interface{}{
		"message":    fmt.Sprintf("Receipt image uploaded successfully with ID: %s", savedReceipt.ID),
		"receipt_id": savedReceipt.ID,
		"image_url":  imageURL,
		"items":      responseItems,
	}

	// Include OCR text in response when available (for reference/debugging)
	if ocrTextData != nil {
		response["ocr_text"] = ocrTextData.Text
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		// Response already written, can't write error
		fmt.Printf("Failed to encode response: %v\n", err)
		return
	}
}

func (t *Transport) validateReceiptImageRequest(w http.ResponseWriter, r *http.Request) (file io.ReadCloser, contentType string, err error) {
	// Only allow POST method
	if r.Method != http.MethodPost {
		err = NewInvalidMethodError(r.Method)
		http.Error(w, err.Error(), http.StatusMethodNotAllowed)
		return nil, "", err
	}

	// Parse multipart form (max 10MB)
	err = r.ParseMultipartForm(10 << 20) // 10MB
	if err != nil {
		validationErr := NewValidationError("form", fmt.Sprintf("failed to parse multipart form: %v", err))
		http.Error(w, validationErr.Error(), http.StatusBadRequest)
		return nil, "", validationErr
	}

	// Get the image file from form
	file, header, err := r.FormFile("image")
	if err != nil {
		validationErr := NewValidationError("image", fmt.Sprintf("failed to get image file: %v", err))
		http.Error(w, validationErr.Error(), http.StatusBadRequest)
		return nil, "", validationErr
	}

	// Validate file size (max 10MB)
	if header.Size > 10<<20 {
		validationErr := NewValidationError("image", "image file too large (max 10MB)")
		http.Error(w, validationErr.Error(), http.StatusBadRequest)
		return nil, "", validationErr
	}

	// Validate content type
	contentType = header.Header.Get("Content-Type")
	if contentType != "" {
		validTypes := map[string]bool{
			"image/jpeg": true,
			"image/jpg":  true,
			"image/png":  true,
			"image/gif":  true,
			"image/webp": true,
		}
		if !validTypes[contentType] {
			validationErr := NewValidationError("image", fmt.Sprintf("invalid image type: %s", contentType))
			http.Error(w, validationErr.Error(), http.StatusBadRequest)
			return nil, "", validationErr
		}
	}
	return file, contentType, nil
}

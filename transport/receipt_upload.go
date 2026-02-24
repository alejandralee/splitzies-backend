package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"splitzies/money"
	"splitzies/persistence"
	"splitzies/storage"
)

// ocrParseResult holds the result of parsing OCR text for a receipt
type ocrParseResult struct {
	items       []persistence.ReceiptItemDB
	ocrTextData *persistence.OCRTextData
	currency    *string
	receiptDate *time.Time
	title       *string
	tax         *float64
	tip         *float64
}

// parseOCRForReceipt performs OCR on image data and parses the result using Gemini.
// Returns nil for ocrTextData and items if OCR fails or text is empty.
func (t *Transport) parseOCRForReceipt(ctx context.Context, fileData []byte) *ocrParseResult {
	ocrText, err := t.visionClient.PerformOCRFromBytes(ctx, fileData)
	if err != nil {
		t.log.Error("OCR failed", "error", err)
		return nil
	}
	if ocrText == "" {
		return nil
	}

	result := &ocrParseResult{
		ocrTextData: &persistence.OCRTextData{Text: ocrText},
	}

	parseResult, parseErr := storage.ParseReceiptItemsWithGemini(ctx, ocrText)
	if parseErr != nil {
		t.log.Error("Gemini parse failed", "error", parseErr)
		parseResult.Items = storage.ExtractReceiptItemsFromText(ocrText)
		parseResult.Currency = nil
		parseResult.ReceiptDate = nil
		parseResult.Title = nil
		parseResult.Tax = nil
		parseResult.Tip = nil
	}

	result.currency = parseResult.Currency
	result.receiptDate = parseResult.ReceiptDate
	result.title = parseResult.Title
	result.tax = parseResult.Tax
	result.tip = parseResult.Tip

	if len(parseResult.Items) > 0 {
		result.items = make([]persistence.ReceiptItemDB, len(parseResult.Items))
		for i, item := range parseResult.Items {
			result.items[i] = persistence.ReceiptItemDB{
				Name:         item.Name,
				Quantity:     item.Quantity,
				TotalPrice:   item.TotalPrice,
				PricePerItem: item.PricePerItem,
			}
		}
	}

	return result
}

// UploadReceiptImageHandler handles receipt image uploads
// Expects multipart/form-data with:
//   - "image": the receipt image file
//
// Returns the uploaded image URL
func (t *Transport) UploadReceiptImageHandler(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	receiptID := persistence.GenerateReceiptID()

	file, contentType, err := t.validateReceiptImageRequest(w, r)
	if err != nil {
		return
	}
	defer file.Close()

	fileData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read image file: %v", err), http.StatusInternalServerError)
		return
	}

	imageURL, err := t.gcsClient.UploadReceiptImageFromReader(ctx, bytes.NewReader(fileData), receiptID, contentType)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to upload image: %v", err), http.StatusInternalServerError)
		return
	}

	var parsedItems []persistence.ReceiptItemDB
	var ocrTextData *persistence.OCRTextData
	var currency, title *string
	var receiptDate *time.Time
	var tax, tip *float64

	if ocr := t.parseOCRForReceipt(ctx, fileData); ocr != nil {
		parsedItems = ocr.items
		ocrTextData = ocr.ocrTextData
		currency = ocr.currency
		receiptDate = ocr.receiptDate
		title = ocr.title
		tax = ocr.tax
		tip = ocr.tip
	}

	savedReceipt, err := persistence.SaveReceipt(parsedItems, &imageURL, ocrTextData, currency, receiptDate, title, tax, tip)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to save receipt: %v", err), http.StatusInternalServerError)
		return
	}

	response := buildUploadReceiptResponse(savedReceipt, imageURL, ocrTextData, currency, tax, tip)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		fmt.Printf("Failed to encode response: %v\n", err)
	}
}

func buildUploadReceiptResponse(savedReceipt *persistence.Receipt, imageURL string, ocrTextData *persistence.OCRTextData, currency *string, tax, tip *float64) UploadReceiptResponse {
	responseItems := make([]ReceiptItem, len(savedReceipt.Items))
	for i, item := range savedReceipt.Items {
		responseItems[i] = ReceiptItem{
			ID:           item.ID,
			Name:         item.Name,
			Quantity:     item.Quantity,
			TotalPrice:   money.Ptr(&item.TotalPrice, currency),
			PricePerItem: money.Ptr(&item.PricePerItem, currency),
		}
	}

	response := UploadReceiptResponse{
		ReceiptID: savedReceipt.ID,
		ImageURL:  imageURL,
		Items:     responseItems,
	}
	if ocrTextData != nil {
		response.OCRText = &ocrTextData.Text
	}
	if tax != nil {
		a := money.NewAmount(*tax, currency)
		response.Tax = &a
	}
	if tip != nil {
		a := money.NewAmount(*tip, currency)
		response.Tip = &a
	}
	return response
}

func (t *Transport) validateReceiptImageRequest(w http.ResponseWriter, r *http.Request) (file io.ReadCloser, contentType string, err error) {
	if r.Method != http.MethodPost {
		err = NewInvalidMethodError(r.Method)
		http.Error(w, err.Error(), http.StatusMethodNotAllowed)
		return nil, "", err
	}

	err = r.ParseMultipartForm(10 << 20) // 10MB
	if err != nil {
		validationErr := NewValidationError("form", fmt.Sprintf("failed to parse multipart form: %v", err))
		http.Error(w, validationErr.Error(), http.StatusBadRequest)
		return nil, "", validationErr
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		validationErr := NewValidationError("image", fmt.Sprintf("failed to get image file: %v", err))
		http.Error(w, validationErr.Error(), http.StatusBadRequest)
		return nil, "", validationErr
	}

	if header.Size > 10<<20 {
		validationErr := NewValidationError("image", "image file too large (max 10MB)")
		http.Error(w, validationErr.Error(), http.StatusBadRequest)
		return nil, "", validationErr
	}

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

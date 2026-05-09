package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"splitzies/money"
	"splitzies/persistence"
	"splitzies/storage"
)

// UploadReceiptImageHandler handles POST /receipts/image
func (t *Transport) UploadReceiptImageHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rid := requestID(r)

	file, contentType, err := t.validateReceiptImageRequest(w, r)
	if err != nil {
		return
	}
	defer file.Close()

	fileData, err := io.ReadAll(file)
	if err != nil {
		t.log.Error("failed to read receipt image", "request_id", rid, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "receipt_image_read_failed", "failed to read receipt image", rid)
		return
	}

	imageURL, err := t.gcsClient.UploadReceiptImageFromReader(ctx, bytes.NewReader(fileData), persistence.GenerateReceiptID(), contentType)
	if err != nil {
		t.log.Error("failed to upload receipt image", "request_id", rid, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "receipt_image_upload_failed", "failed to upload receipt image", rid)
		return
	}

	var (
		parsedItems []persistence.ReceiptItemDB
		ocrTextData *persistence.OCRTextData
		currency    *string
		receiptDate *time.Time
		title       *string
		tax, tip    *float64
	)
	if ocr := t.parseOCRForReceipt(ctx, fileData); ocr != nil {
		parsedItems = ocr.items
		ocrTextData = ocr.ocrTextData
		currency = ocr.currency
		receiptDate = ocr.receiptDate
		title = ocr.title
		tax = ocr.tax
		tip = ocr.tip
	}

	savedReceipt, err := t.persistenceClient.SaveReceipt(ctx, parsedItems, &imageURL, ocrTextData, currency, receiptDate, title, tax, tip)
	if err != nil {
		t.log.Error("failed to save receipt", "request_id", rid, "image_url", imageURL, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "receipt_save_failed", "failed to save receipt", rid)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(buildUploadReceiptResponse(savedReceipt, imageURL, ocrTextData, currency, tax, tip)); err != nil {
		t.log.Error("failed to encode upload receipt response", "request_id", rid, "error", err)
	}
}

// ocrParseResult holds the result of parsing OCR text from a receipt image.
type ocrParseResult struct {
	items       []persistence.ReceiptItemDB
	ocrTextData *persistence.OCRTextData
	currency    *string
	receiptDate *time.Time
	title       *string
	tax         *float64
	tip         *float64
}

// parseOCRForReceipt performs OCR on the image bytes then parses the result with Gemini.
// Returns nil if OCR produces no text; falls back to regex parsing if Gemini fails.
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

	parsed, parseErr := storage.ParseReceiptItemsWithGemini(ctx, ocrText)
	if parseErr != nil {
		t.log.Error("Gemini parse failed, falling back to regex", "error", parseErr)
		parsed.Items = storage.ExtractReceiptItemsFromText(ocrText)
	}

	result.currency = parsed.Currency
	result.receiptDate = parsed.ReceiptDate
	result.title = parsed.Title
	result.tax = parsed.Tax
	result.tip = parsed.Tip

	if len(parsed.Items) > 0 {
		result.items = make([]persistence.ReceiptItemDB, len(parsed.Items))
		for i, item := range parsed.Items {
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

func (t *Transport) validateReceiptImageRequest(w http.ResponseWriter, r *http.Request) (io.ReadCloser, string, error) {
	rid := requestID(r)

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		t.log.Warn("failed to parse multipart form", "request_id", rid, "error", err)
		writeJSONError(w, http.StatusBadRequest, "invalid_multipart_form", "failed to parse multipart form", rid)
		return nil, "", err
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		t.log.Warn("missing or invalid image file", "request_id", rid, "error", err)
		writeJSONError(w, http.StatusBadRequest, "missing_image_file", newValidationError("image", "image file is required").Error(), rid)
		return nil, "", err
	}

	if header.Size > 10<<20 {
		file.Close()
		err = newValidationError("image", "image file too large (max 10MB)")
		t.log.Warn("receipt image too large", "request_id", rid, "size_bytes", header.Size)
		writeJSONError(w, http.StatusBadRequest, "image_too_large", err.Error(), rid)
		return nil, "", err
	}

	ct := header.Header.Get("Content-Type")
	if ct != "" {
		validTypes := map[string]bool{
			"image/jpeg": true,
			"image/jpg":  true,
			"image/png":  true,
			"image/gif":  true,
			"image/webp": true,
		}
		if !validTypes[ct] {
			file.Close()
			err = newValidationError("image", "unsupported image type "+ct+" (accepted: jpeg, png, gif, webp)")
			t.log.Warn("invalid image content type", "request_id", rid, "content_type", ct)
			writeJSONError(w, http.StatusBadRequest, "invalid_image_type", err.Error(), rid)
			return nil, "", err
		}
	}

	return file, ct, nil
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

func requestID(r *http.Request) string {
	for _, h := range []string{"X-Request-Id", "X-Request-ID", "Request-Id"} {
		if v := r.Header.Get(h); v != "" {
			return v
		}
	}
	return ""
}

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

	uploadCtx, cancel := context.WithTimeout(ctx, gcsUploadTimeout)
	imageURL, err := t.gcsClient.UploadReceiptImageFromReader(uploadCtx, bytes.NewReader(fileData), persistence.GenerateReceiptID(), contentType)
	cancel()
	if err != nil {
		t.log.Error("failed to upload receipt image", "request_id", rid, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "receipt_image_upload_failed", "failed to upload receipt image", rid)
		return
	}

	ocr := t.parseReceipt(ctx, rid, fileData, contentType)
	if ocr == nil || len(ocr.items) == 0 {
		t.log.Error("no receipt items extracted, not persisting receipt", "request_id", rid, "image_url", imageURL)
		writeJSONError(w, http.StatusUnprocessableEntity, "receipt_parse_failed", "failed to extract any items from receipt image", rid)
		return
	}

	// When the uploader has a device token, the receipt lands in their history.
	savedReceipt, err := t.persistenceClient.SaveReceipt(ctx, ocr.items, &imageURL, ocr.ocrTextData, ocr.currency, ocr.receiptDate, ocr.title, ocr.tax, ocr.tip, deviceIDFromContext(ctx))
	if err != nil {
		t.log.Error("failed to save receipt", "request_id", rid, "image_url", imageURL, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "receipt_save_failed", "failed to save receipt", rid)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(buildUploadReceiptResponse(savedReceipt, imageURL, ocr.ocrTextData, ocr.currency, ocr.tax, ocr.tip)); err != nil {
		t.log.Error("failed to encode upload receipt response", "request_id", rid, "error", err)
	}
}

// ocrParseResult holds the result of parsing a receipt image into items.
type ocrParseResult struct {
	items       []persistence.ReceiptItemDB
	ocrTextData *persistence.OCRTextData
	currency    *string
	receiptDate *time.Time
	title       *string
	tax         *float64
	tip         *float64
}

// Timeouts bound every external call in the parse pipeline so a slow or hung
// upstream can't tie up a request indefinitely.
const (
	gcsUploadTimeout   = 15 * time.Second
	visionTimeout      = 15 * time.Second
	geminiTextTimeout  = 20 * time.Second
	geminiImageTimeout = 30 * time.Second
)

// parseReceipt turns image bytes into receipt items.
//
// Default path: one multimodal Gemini call on the image — cheaper than Vision
// OCR plus a text parse, one round trip instead of two, and the receipt's
// column layout survives. If it errors or yields no items we fall back to the
// legacy Vision-OCR path automatically, so a bad image-path result degrades
// instead of failing the upload. Setting RECEIPT_PARSE_MODE=ocr skips the image
// path entirely and restores the old behaviour.
//
// Returns nil when no path produced anything usable.
func (t *Transport) parseReceipt(ctx context.Context, rid string, fileData []byte, contentType string) *ocrParseResult {
	if t.receiptParseMode == ReceiptParseModeOCR {
		t.log.Info("parsing receipt via legacy OCR path", "request_id", rid, "mode", t.receiptParseMode, "model", t.geminiClient.Model())
		return t.parseReceiptViaOCR(ctx, rid, fileData)
	}

	geminiCtx, cancel := context.WithTimeout(ctx, geminiImageTimeout)
	defer cancel()

	parsed, err := t.geminiClient.ParseReceiptImage(geminiCtx, fileData, contentType)
	switch {
	case err != nil:
		t.log.Error("Gemini image parse failed, falling back to Vision OCR",
			"request_id", rid, "model", t.geminiClient.Model(), "error", err)
	case len(parsed.Items) == 0:
		t.log.Error("Gemini image parse extracted no items, falling back to Vision OCR",
			"request_id", rid, "model", t.geminiClient.Model())
	default:
		t.log.Info("parsed receipt from image", "request_id", rid,
			"model", t.geminiClient.Model(), "item_count", len(parsed.Items))
		return buildParseResult(parsed, nil)
	}

	return t.parseReceiptViaOCR(ctx, rid, fileData)
}

// parseReceiptViaOCR is the original pipeline: Cloud Vision
// DOCUMENT_TEXT_DETECTION, then Gemini over the flattened text, with the regex
// parser as a last resort.
func (t *Transport) parseReceiptViaOCR(ctx context.Context, rid string, fileData []byte) *ocrParseResult {
	visionCtx, cancel := context.WithTimeout(ctx, visionTimeout)
	defer cancel()

	ocrText, err := t.visionClient.PerformOCRFromBytes(visionCtx, fileData)
	if err != nil {
		t.log.Error("receipt OCR request failed", "request_id", rid, "error", err)
		return nil
	}
	if ocrText == "" {
		t.log.Error("receipt OCR produced no text", "request_id", rid)
		return nil
	}

	geminiCtx, cancelGemini := context.WithTimeout(ctx, geminiTextTimeout)
	defer cancelGemini()

	parsed, parseErr := t.geminiClient.ParseReceiptText(geminiCtx, ocrText)
	if parseErr != nil {
		t.log.Error("Gemini text parse failed, falling back to regex", "request_id", rid, "error", parseErr)
		parsed.Items = storage.ExtractReceiptItemsFromText(ocrText)
		if len(parsed.Items) == 0 {
			t.log.Error("regex fallback also extracted no items from OCR text", "request_id", rid)
		}
	}

	return buildParseResult(parsed, &persistence.OCRTextData{Text: ocrText})
}

func buildParseResult(parsed storage.GeminiReceiptParseResult, ocrTextData *persistence.OCRTextData) *ocrParseResult {
	result := &ocrParseResult{
		ocrTextData: ocrTextData,
		currency:    parsed.Currency,
		receiptDate: parsed.ReceiptDate,
		title:       parsed.Title,
		tax:         parsed.Tax,
		tip:         parsed.Tip,
	}

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

	// Cap the whole request body, not just the file field, so a client can't
	// force us to buffer an oversized multipart body before we ever get to
	// check header.Size.
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)

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
			GroupID:      item.GroupID,
			GroupName:    item.GroupName,
			Name:         item.Name,
			DisplayOrder: item.DisplayOrder,
			Amount:       money.Ptr(&item.Amount, currency),
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

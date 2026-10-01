package transport

import (
	"log/slog"

	"splitzies/persistence"
	"splitzies/storage"
)

// Receipt parsing modes, selected by the RECEIPT_PARSE_MODE env var.
//
// ReceiptParseModeImage (default) sends the uploaded image straight to Gemini.
// ReceiptParseModeOCR is the previous pipeline — Cloud Vision
// DOCUMENT_TEXT_DETECTION, then Gemini over the extracted text — kept intact as
// a one-env-var rollback if the image path misbehaves in production.
const (
	ReceiptParseModeImage = "image"
	ReceiptParseModeOCR   = "ocr"
)

type Transport struct {
	log               *slog.Logger
	persistenceClient *persistence.Client
	gcsClient         *storage.GCSClient
	visionClient      *storage.VisionClient
	geminiClient      *storage.GeminiClient
	receiptParseMode  string
}

func NewTransport(
	log *slog.Logger,
	persistenceClient *persistence.Client,
	gcsClient *storage.GCSClient,
	visionClient *storage.VisionClient,
	geminiClient *storage.GeminiClient,
	receiptParseMode string,
) *Transport {
	if receiptParseMode != ReceiptParseModeOCR {
		receiptParseMode = ReceiptParseModeImage
	}

	return &Transport{
		log:               log,
		persistenceClient: persistenceClient,
		gcsClient:         gcsClient,
		visionClient:      visionClient,
		geminiClient:      geminiClient,
		receiptParseMode:  receiptParseMode,
	}
}

// ReceiptParseMode is the parse pipeline this process is configured to use.
func (t *Transport) ReceiptParseMode() string { return t.receiptParseMode }

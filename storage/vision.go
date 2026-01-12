package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"cloud.google.com/go/storage"
	vision "cloud.google.com/go/vision/apiv1"
	"google.golang.org/api/option"
	pb "google.golang.org/genproto/googleapis/cloud/vision/v1"
)

// ExtractReceiptItemsFromText parses OCR text to extract receipt items
// This is a basic parser - receipt formats vary widely, so this may need refinement
func ExtractReceiptItemsFromText(ocrText string) []ReceiptItemParsed {
	items := []ReceiptItemParsed{}

	// Split text into lines
	lines := strings.Split(ocrText, "\n")

	// Common patterns for receipt items:
	// - Item name followed by quantity and price
	// - Lines with prices at the end
	// - Lines that look like: "Item Name    2    $10.00"

	// Pattern to match lines with prices (e.g., "Item Name    2    $10.00" or "Item Name  $10.00")
	// This regex looks for: optional item name, optional quantity, and a price
	pricePattern := regexp.MustCompile(`(?i)(.+?)\s+(\d+)?\s*\$?([\d,]+\.?\d{0,2})`)

	// Pattern to match just a price at the end of a line
	endPricePattern := regexp.MustCompile(`(.+?)\s+\$?([\d,]+\.?\d{0,2})\s*$`)

	// Skip header/footer lines (common receipt patterns)
	skipPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)^(subtotal|tax|total|amount|change|cash|card|receipt|thank|visit|date|time)`),
		regexp.MustCompile(`(?i)^\s*\$?[\d,]+\.?\d{0,2}\s*$`), // Just a price
		regexp.MustCompile(`^[\s\-=]+$`),                      // Separator lines
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Skip header/footer lines
		shouldSkip := false
		for _, pattern := range skipPatterns {
			if pattern.MatchString(line) {
				shouldSkip = true
				break
			}
		}
		if shouldSkip {
			continue
		}

		var item ReceiptItemParsed
		var found bool

		// Try to match pattern with quantity
		if matches := pricePattern.FindStringSubmatch(line); len(matches) >= 4 {
			item.Name = strings.TrimSpace(matches[1])
			if matches[2] != "" {
				if qty, err := strconv.Atoi(matches[2]); err == nil {
					item.Quantity = qty
				} else {
					item.Quantity = 1
				}
			} else {
				item.Quantity = 1
			}
			priceStr := strings.ReplaceAll(matches[3], ",", "")
			if price, err := strconv.ParseFloat(priceStr, 64); err == nil {
				item.TotalPrice = price
				item.PricePerItem = price / float64(item.Quantity)
				found = true
			}
		} else if matches := endPricePattern.FindStringSubmatch(line); len(matches) >= 3 {
			// Try pattern with just price at end
			item.Name = strings.TrimSpace(matches[1])
			item.Quantity = 1
			priceStr := strings.ReplaceAll(matches[2], ",", "")
			if price, err := strconv.ParseFloat(priceStr, 64); err == nil {
				item.TotalPrice = price
				item.PricePerItem = price
				found = true
			}
		}

		// Only add if we found a valid item (has name and price)
		if found && item.Name != "" && item.TotalPrice > 0 {
			// Clean up item name (remove extra spaces, common prefixes)
			item.Name = strings.TrimSpace(item.Name)
			items = append(items, item)
		}
	}

	return items
}

// ReceiptItemParsed represents a parsed receipt item from OCR
type ReceiptItemParsed struct {
	Name         string
	Quantity     int
	TotalPrice   float64
	PricePerItem float64
}

// PerformOCRFromGCS performs OCR on an image/PDF stored in GCS
// For images (JPG/PNG): uses synchronous DOCUMENT_TEXT_DETECTION
// For PDFs/TIFFs: uses asynchronous batch processing
func PerformOCRFromGCS(ctx context.Context, gcsURI string) (string, error) {
	// Get credentials from environment variable
	credsJSON := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON")
	if credsJSON == "" {
		return "", fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS_JSON environment variable is not set")
	}

	// Create Vision client
	client, err := vision.NewImageAnnotatorClient(ctx, option.WithCredentialsJSON([]byte(credsJSON)))
	if err != nil {
		return "", fmt.Errorf("failed to create Vision client: %w", err)
	}
	defer client.Close()

	// Determine file type from URI
	ext := strings.ToLower(filepath.Ext(gcsURI))

	// For images (JPG, PNG, etc.), use synchronous DOCUMENT_TEXT_DETECTION
	if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" || ext == ".webp" {
		image := &pb.Image{
			Source: &pb.ImageSource{
				ImageUri: gcsURI,
			},
		}

		// Use DOCUMENT_TEXT_DETECTION for receipts (better for dense text)
		response, err := client.DetectDocumentText(ctx, image, nil)
		if err != nil {
			return "", fmt.Errorf("failed to detect document text: %w", err)
		}

		if response == nil {
			return "", fmt.Errorf("no text detected in image")
		}

		text := response.GetText()
		if text == "" {
			return "", fmt.Errorf("no text detected in image")
		}

		return text, nil
	}

	// For PDFs and TIFFs, use async batch processing
	// Note: The v1 API doesn't support async batch operations directly
	// For now, we'll return an error suggesting to use images instead
	// In production, you'd need to use the v2 API or handle this differently
	if ext == ".pdf" || ext == ".tiff" || ext == ".tif" {
		return "", fmt.Errorf("PDF and TIFF files require async processing via Cloud Storage. Please use the v2 API or convert to image format first")
	}

	return "", fmt.Errorf("unsupported file type: %s", ext)
}

// PerformOCRFromBytes performs OCR on image bytes (synchronous, for images only)
func PerformOCRFromBytes(ctx context.Context, imageData []byte) (string, error) {
	// Get credentials from environment variable
	credsJSON := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON")
	if credsJSON == "" {
		return "", fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS_JSON environment variable is not set")
	}

	// Create Vision client
	client, err := vision.NewImageAnnotatorClient(ctx, option.WithCredentialsJSON([]byte(credsJSON)))
	if err != nil {
		return "", fmt.Errorf("failed to create Vision client: %w", err)
	}
	defer client.Close()

	image := &pb.Image{
		Content: imageData,
	}

	// Use DOCUMENT_TEXT_DETECTION for receipts
	response, err := client.DetectDocumentText(ctx, image, nil)
	if err != nil {
		return "", fmt.Errorf("failed to detect document text: %w", err)
	}

	if response == nil {
		return "", fmt.Errorf("no text detected in image")
	}

	text := response.GetText()
	if text == "" {
		return "", fmt.Errorf("no text detected in image")
	}

	return text, nil
}

// Helper function to get MIME type from extension
func getMimeType(ext string) string {
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".tiff", ".tif":
		return "image/tiff"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// Helper function to read OCR result from GCS (for async operations)
func readOCRResultFromGCS(ctx context.Context, gcsURI string) (string, error) {
	// Parse GCS URI: gs://bucket/path
	if !strings.HasPrefix(gcsURI, "gs://") {
		return "", fmt.Errorf("invalid GCS URI: %s", gcsURI)
	}

	uriParts := strings.TrimPrefix(gcsURI, "gs://")
	parts := strings.SplitN(uriParts, "/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid GCS URI format: %s", gcsURI)
	}

	bucketName := parts[0]
	objectName := parts[1]

	// Get credentials
	credsJSON := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON")
	if credsJSON == "" {
		return "", fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS_JSON environment variable is not set")
	}

	// Create GCS client
	client, err := storage.NewClient(ctx, option.WithCredentialsJSON([]byte(credsJSON)))
	if err != nil {
		return "", fmt.Errorf("failed to create GCS client: %w", err)
	}
	defer client.Close()

	// Read the object
	reader, err := client.Bucket(bucketName).Object(objectName).NewReader(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create reader: %w", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read data: %w", err)
	}

	// The OCR result is stored as JSON, we need to parse it
	// For now, return as string - in production you'd parse the JSON structure
	return string(data), nil
}

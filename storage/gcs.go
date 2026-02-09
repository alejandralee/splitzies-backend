package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// UploadReceiptImage uploads a receipt image to Google Cloud Storage
// Returns the public URL of the uploaded image
func UploadReceiptImage(ctx context.Context, imageData []byte, receiptID string) (string, error) {
	// Get credentials from environment variable
	credsJSON := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON")
	if credsJSON == "" {
		return "", fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS_JSON environment variable is not set")
	}

	// Parse the JSON credentials
	var creds map[string]interface{}
	if err := json.Unmarshal([]byte(credsJSON), &creds); err != nil {
		return "", fmt.Errorf("failed to parse credentials JSON: %w", err)
	}

	// Get bucket name from environment variable (defaults to "splitzies")
	bucketName := os.Getenv("GCS_BUCKET_NAME")
	if bucketName == "" {
		bucketName = "splitzies"
	}

	// Create GCS client with credentials
	client, err := storage.NewClient(ctx, option.WithCredentialsJSON([]byte(credsJSON)))
	if err != nil {
		return "", fmt.Errorf("failed to create GCS client: %w", err)
	}
	defer client.Close()

	// Generate object name with receipt ID and timestamp
	// Format: receipts/{receiptID}/{timestamp}.jpg
	timestamp := time.Now().Format("20060102_150405")
	objectName := fmt.Sprintf("receipts/%s/%s.jpg", receiptID, timestamp)

	// Create writer for the object
	wc := client.Bucket(bucketName).Object(objectName).NewWriter(ctx)
	wc.ContentType = "image/jpeg"
	wc.Metadata = map[string]string{
		"receipt_id":  receiptID,
		"uploaded_at": time.Now().Format(time.RFC3339),
	}

	// Write image data
	if _, err := wc.Write(imageData); err != nil {
		wc.Close()
		return "", fmt.Errorf("failed to write image data: %w", err)
	}

	// Close writer to finalize upload
	if err := wc.Close(); err != nil {
		return "", fmt.Errorf("failed to close writer: %w", err)
	}

	// Construct the public URL
	// Format: https://storage.googleapis.com/{bucket}/{object}
	imageURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucketName, objectName)

	return imageURL, nil
}

// UploadReceiptImageFromReader uploads a receipt image from an io.Reader to Google Cloud Storage
// This is useful when reading from multipart form data
func UploadReceiptImageFromReader(ctx context.Context, reader io.Reader, receiptID string, contentType string) (string, error) {
	// Get credentials from environment variable
	credsJSON := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON")
	if credsJSON == "" {
		return "", fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS_JSON environment variable is not set")
	}

	// Get bucket name from environment variable (defaults to "splitzies")
	bucketName := os.Getenv("GCS_BUCKET_NAME")
	if bucketName == "" {
		bucketName = "splitzies"
	}

	// Create GCS client with credentials
	client, err := storage.NewClient(ctx, option.WithCredentialsJSON([]byte(credsJSON)))
	if err != nil {
		return "", fmt.Errorf("failed to create GCS client: %w", err)
	}
	defer client.Close()

	// Determine file extension from content type
	ext := ".jpg"
	if contentType != "" {
		switch contentType {
		case "image/png":
			ext = ".png"
		case "image/jpeg", "image/jpg":
			ext = ".jpg"
		case "image/gif":
			ext = ".gif"
		case "image/webp":
			ext = ".webp"
		default:
			ext = filepath.Ext(contentType)
			if ext == "" {
				ext = ".jpg"
			}
		}
	}

	// Generate object name with receipt ID
	objectName := fmt.Sprintf("receipts/%s%s", receiptID, ext)

	// Create writer for the object
	wc := client.Bucket(bucketName).Object(objectName).NewWriter(ctx)
	wc.ContentType = contentType
	if wc.ContentType == "" {
		wc.ContentType = "image/jpeg"
	}
	wc.Metadata = map[string]string{
		"receipt_id":  receiptID,
		"uploaded_at": time.Now().Format(time.RFC3339),
	}

	// Copy data from reader to writer
	if _, err := io.Copy(wc, reader); err != nil {
		wc.Close()
		return "", fmt.Errorf("failed to copy image data: %w", err)
	}

	// Close writer to finalize upload
	if err := wc.Close(); err != nil {
		return "", fmt.Errorf("failed to close writer: %w", err)
	}

	// Construct the public URL
	imageURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucketName, objectName)

	return imageURL, nil
}

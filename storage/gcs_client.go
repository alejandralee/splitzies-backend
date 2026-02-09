package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

type GCSClient struct {
	client     *storage.Client
	bucketName string
}

func NewGCSClient(ctx context.Context) (*GCSClient, error) {
	credsJSON := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON")
	if credsJSON == "" {
		return nil, fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS_JSON environment variable is not set")
	}

	// Get bucket name from environment variable (defaults to "splitzies")
	bucketName := os.Getenv("GCS_BUCKET_NAME")
	if bucketName == "" {
		bucketName = "splitzies"
	}

	client, err := storage.NewClient(ctx, option.WithCredentialsJSON([]byte(credsJSON)))
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	return &GCSClient{
		client:     client,
		bucketName: bucketName,
	}, nil
}

func (c *GCSClient) UploadReceiptImageFromReader(ctx context.Context, reader io.Reader, receiptID string, contentType string) (string, error) {
	bucket := c.client.Bucket(c.bucketName)
	object := bucket.Object(getObjectName(receiptID, contentType))

	writer := object.NewWriter(ctx)
	writer.ContentType = contentType
	writer.Metadata = map[string]string{
		"receipt_id":  receiptID,
		"uploaded_at": time.Now().Format(time.RFC3339),
	}

	if _, err := io.Copy(writer, reader); err != nil {
		return "", fmt.Errorf("failed to upload receipt image: %w", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close writer: %w", err)
	}

	attrs, err := object.Attrs(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get object attributes: %w", err)
	}
	return attrs.MediaLink, nil
}

func (c *GCSClient) Close() error {
	return c.client.Close()
}

func getObjectName(receiptID string, contentType string) string {
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
	return objectName
}

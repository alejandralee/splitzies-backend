package storage

import (
	"context"
	"fmt"
	"os"

	vision "cloud.google.com/go/vision/apiv1"
	"google.golang.org/api/option"
	pb "google.golang.org/genproto/googleapis/cloud/vision/v1"
)

type VisionClient struct {
	client *vision.ImageAnnotatorClient
}

func NewVisionClient(ctx context.Context) (*VisionClient, error) {
	credsJSON := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON")
	if credsJSON == "" {
		return nil, fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS_JSON environment variable is not set")
	}

	client, err := vision.NewImageAnnotatorClient(ctx, option.WithCredentialsJSON([]byte(credsJSON)))
	if err != nil {
		return nil, fmt.Errorf("failed to create Vision client: %w", err)
	}

	return &VisionClient{
		client: client,
	}, nil
}

func (c *VisionClient) Close() error {
	return c.client.Close()
}

func (c *VisionClient) PerformOCRFromBytes(ctx context.Context, imageData []byte) (string, error) {
	image := &pb.Image{
		Content: imageData,
	}

	// Use DOCUMENT_TEXT_DETECTION for receipts
	response, err := c.client.DetectDocumentText(ctx, image, nil)
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

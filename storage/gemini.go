package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"cloud.google.com/go/auth/credentials"
	"google.golang.org/genai"
)

type geminiReceiptItem struct {
	Name         string   `json:"name"`
	Quantity     int      `json:"quantity"`
	TotalPrice   *float64 `json:"total_price,omitempty"`
	PricePerItem *float64 `json:"price_per_item,omitempty"`
}

type geminiReceiptData struct {
	Items []geminiReceiptItem `json:"items"`
}

// ParseReceiptItemsWithGemini parses OCR text into receipt items using Gemini.
func ParseReceiptItemsWithGemini(ctx context.Context, ocrText string) ([]ReceiptItemParsed, error) {
	if strings.TrimSpace(ocrText) == "" {
		return nil, fmt.Errorf("ocr text is empty")
	}

	credsJSON := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON")
	if credsJSON == "" {
		return nil, fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS_JSON environment variable is not set")
	}

	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		projectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT_ID environment variable is not set")
	}

	location := os.Getenv("VERTEX_AI_LOCATION")
	if location == "" {
		location = "us-central1"
	}

	creds, err := credentials.DetectDefault(&credentials.DetectOptions{
		CredentialsJSON: []byte(credsJSON),
		// Scopes:          []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load Google credentials: %w", err)
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:     projectID,
		Location:    location,
		Backend:     genai.BackendVertexAI,
		Credentials: creds,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create GenAI client: %w", err)
	}

	prompt := fmt.Sprintf(`You are parsing OCR text from a receipt.
Return ONLY valid JSON with this schema:
{
  "items": [
    {"name": "string", "quantity": 1, "total_price": 1.23, "price_per_item": 1.23}
  ]
}
Rules:
- Include only line items (exclude tax, totals, payment, change, headers, footers).
- If quantity is missing, use 1.
- If total_price or price_per_item is missing, set it to null.

Receipt OCR text:
---
%s
---`, ocrText)

	config := &genai.GenerateContentConfig{
		Temperature:     genai.Ptr(float32(0.1)),
		TopP:            genai.Ptr(float32(0.95)),
		TopK:            genai.Ptr(float32(40)),
		MaxOutputTokens: 1024,
	}
	resp, err := client.Models.GenerateContent(ctx, "gemini-1.0-pro-002", genai.Text(prompt), config)
	if err != nil {
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}

	fmt.Println("Gemini response:", resp)

	responseText := extractGeminiText(resp)
	if responseText == "" {
		return nil, fmt.Errorf("empty response from Gemini")
	}

	fmt.Println("Gemini response text:", responseText)
	cleaned := cleanGeminiJSON(responseText)
	fmt.Println("Cleaned Gemini JSON:", cleaned)
	var parsed geminiReceiptData
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini JSON: %w", err)
	}

	items := make([]ReceiptItemParsed, 0, len(parsed.Items))
	for _, item := range parsed.Items {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}

		qty := item.Quantity
		if qty <= 0 {
			qty = 1
		}

		if item.TotalPrice == nil && item.PricePerItem == nil {
			continue
		}

		var totalPrice float64
		var pricePerItem float64
		if item.TotalPrice == nil && item.PricePerItem != nil {
			pricePerItem = *item.PricePerItem
			totalPrice = pricePerItem * float64(qty)
		} else if item.PricePerItem == nil && item.TotalPrice != nil {
			totalPrice = *item.TotalPrice
			pricePerItem = totalPrice / float64(qty)
		} else if item.TotalPrice != nil && item.PricePerItem != nil {
			totalPrice = *item.TotalPrice
			pricePerItem = *item.PricePerItem
		}

		if totalPrice <= 0 || pricePerItem <= 0 {
			continue
		}

		items = append(items, ReceiptItemParsed{
			Name:         strings.TrimSpace(item.Name),
			Quantity:     qty,
			TotalPrice:   totalPrice,
			PricePerItem: pricePerItem,
		})
	}

	return items, nil
}

func extractGeminiText(resp *genai.GenerateContentResponse) string {
	if resp == nil {
		return ""
	}

	return strings.TrimSpace(resp.Text())
}

func cleanGeminiJSON(input string) string {
	cleaned := strings.TrimSpace(input)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	start := strings.Index(cleaned, "{")
	end := strings.LastIndex(cleaned, "}")
	if start >= 0 && end >= start {
		return cleaned[start : end+1]
	}

	return cleaned
}

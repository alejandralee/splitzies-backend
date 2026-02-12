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
	Items       []geminiReceiptItem `json:"items"`
	Currency    *string            `json:"currency"`
	Date        *string            `json:"date"`
	ReceiptDate *string            `json:"receipt_date"`
	Title       *string            `json:"title"`
	Tax         *float64           `json:"tax"`
	Tip         *float64           `json:"tip"`
}

type GeminiReceiptParseResult struct {
	Items       []ReceiptItemParsed
	Currency    *string
	ReceiptDate *string
	Title       *string
	Tax         *float64
	Tip         *float64
}

// ParseReceiptItemsWithGemini parses OCR text into receipt items using Gemini.
func ParseReceiptItemsWithGemini(ctx context.Context, ocrText string) (GeminiReceiptParseResult, error) {
	var empty GeminiReceiptParseResult
	if strings.TrimSpace(ocrText) == "" {
		return empty, fmt.Errorf("ocr text is empty")
	}

	credsJSON := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON")
	if credsJSON == "" {
		return empty, fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS_JSON environment variable is not set")
	}

	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		projectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if projectID == "" {
		return empty, fmt.Errorf("GCP_PROJECT_ID environment variable is not set")
	}

	location := os.Getenv("VERTEX_AI_LOCATION")
	if location == "" {
		location = "global"
	}

	creds, err := credentials.DetectDefault(&credentials.DetectOptions{
		CredentialsJSON: []byte(credsJSON),
		Scopes:          []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		return empty, fmt.Errorf("failed to load Google credentials: %w", err)
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:     projectID,
		Location:    location,
		Backend:     genai.BackendVertexAI,
		Credentials: creds,
	})
	if err != nil {
		return empty, fmt.Errorf("failed to create GenAI client: %w", err)
	}

	prompt := fmt.Sprintf(`You are parsing OCR text from a receipt.
Return ONLY valid JSON with this schema:
{
  "items": [
    {"name": "string", "quantity": 1, "total_price": 1.23, "price_per_item": 1.23}
  ],
  "currency": "string",
  "receipt_date": "string",
  "title": "string",
  "tax": 1.23,
  "tip": 2.50
}
Rules:
- Include only line items in items (exclude tax, totals, payment, change, headers, footers).
- If quantity is missing, use 1.
- If total_price or price_per_item is missing, set it to null.
- Try to convert the name into a human-readable format (e.g., "Coca-Cola" instead of "COLA").
- Title should be the restaurant name or where the receipt is from.
- If currency is not explicit, try to infer it from the context (e.g., "USD" for US-based receipts). If no currency is found, leave it null.
- tax: Parse the sales tax amount if present (e.g., "Tax: $1.50"). Null if not found.
- tip: Parse the tip/gratuity amount if present (e.g., "Tip: $5.00"). Null if not found.

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
	resp, err := client.Models.GenerateContent(ctx, "gemini-2.0-flash-001", genai.Text(prompt), config)
	if err != nil {
		return empty, fmt.Errorf("failed to generate content: %w", err)
	}

	fmt.Println("Gemini response:", resp)

	responseText := extractGeminiText(resp)
	if responseText == "" {
		return empty, fmt.Errorf("empty response from Gemini")
	}

	fmt.Println("Gemini response text:", responseText)
	cleaned := cleanGeminiJSON(responseText)
	fmt.Println("Cleaned Gemini JSON:", cleaned)
	var parsed geminiReceiptData
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return empty, fmt.Errorf("failed to parse Gemini JSON: %w", err)
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

	receiptDate := normalizeOptionalString(parsed.ReceiptDate)
	if receiptDate == nil {
		receiptDate = normalizeOptionalString(parsed.Date)
	}

	return GeminiReceiptParseResult{
		Items:       items,
		Currency:    normalizeOptionalString(parsed.Currency),
		ReceiptDate: receiptDate,
		Title:       normalizeOptionalString(parsed.Title),
		Tax:         parsed.Tax,
		Tip:         parsed.Tip,
	}, nil
}

func extractGeminiText(resp *genai.GenerateContentResponse) string {
	if resp == nil {
		return ""
	}

	return strings.TrimSpace(resp.Text())
}

func normalizeOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
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

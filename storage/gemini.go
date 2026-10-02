package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/auth/credentials"
	"google.golang.org/genai"
)

const (
	// DefaultGeminiModel reads receipt images directly. Overridable with
	// GEMINI_MODEL so the model can be changed (or rolled back to
	// LegacyGeminiModel) by restarting with a different env var, no deploy.
	DefaultGeminiModel = "gemini-3.1-flash-lite"

	// LegacyGeminiModel is what the Vision-OCR-then-parse pipeline used.
	LegacyGeminiModel = "gemini-2.5-flash"
)

type geminiReceiptItem struct {
	Name         string   `json:"name"`
	Quantity     int      `json:"quantity"`
	TotalPrice   *float64 `json:"total_price,omitempty"`
	PricePerItem *float64 `json:"price_per_item,omitempty"`
}

type geminiReceiptData struct {
	Items       []geminiReceiptItem `json:"items"`
	Currency    *string             `json:"currency"`
	Date        *string             `json:"date"`
	ReceiptDate *string             `json:"receipt_date"`
	Title       *string             `json:"title"`
	Tax         *float64            `json:"tax"`
	Tip         *float64            `json:"tip"`
}

type GeminiReceiptParseResult struct {
	Items       []ReceiptItemParsed
	Currency    *string
	ReceiptDate *time.Time
	Title       *string
	Tax         *float64
	Tip         *float64
}

// GeminiClient holds a Vertex AI client. Build it once at startup: credential
// detection and client construction are far too expensive to repeat on every
// upload, which is what the old package-level parse function did.
type GeminiClient struct {
	client *genai.Client
	model  string
}

func NewGeminiClient(ctx context.Context) (*GeminiClient, error) {
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
		location = "global"
	}

	model := strings.TrimSpace(os.Getenv("GEMINI_MODEL"))
	if model == "" {
		model = DefaultGeminiModel
	}

	creds, err := credentials.DetectDefault(&credentials.DetectOptions{
		CredentialsJSON: []byte(credsJSON),
		Scopes:          []string{"https://www.googleapis.com/auth/cloud-platform"},
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

	return &GeminiClient{client: client, model: model}, nil
}

// Model is the Gemini model this client sends requests to.
func (c *GeminiClient) Model() string { return c.model }

const receiptRules = `Rules:
- Include only line items in items (exclude tax, totals, subtotals, payment, change, headers, footers).
- If quantity is missing, use 1.
- If total_price or price_per_item is missing, set it to null. Never invent a price.
- Convert the name into a human-readable format (e.g., "Coca-Cola" instead of "COLA").
- Output one entry per printed line; do not combine lines. When the same item is printed on several lines, give every one of those lines the identical name.
- title is the restaurant or store the receipt is from.
- If currency is not explicit, infer it from context (e.g., "USD" for US receipts). Null if unknown.
- receipt_date: ISO 8601 (YYYY-MM-DD) preferred. Null if not present.
- tax: the sales tax amount if present. Null if not found.
- tip: the tip/gratuity amount if present. Null if not found.`

const receiptImagePrompt = `You are reading a photo of a receipt and extracting its line items.
Use the column layout of the receipt: prices are usually right-aligned on the same row as the item they belong to, and a leading number is usually a quantity.
` + receiptRules

const receiptTextPromptHeader = `You are parsing OCR text from a receipt.
` + receiptRules + `

Receipt OCR text:
---
`

// ParseReceiptImage extracts receipt data straight from the image bytes. This
// is the primary path: it is cheaper than Vision OCR plus a text parse, and it
// keeps the receipt's spatial layout, which OCR flattening destroys.
func (c *GeminiClient) ParseReceiptImage(ctx context.Context, imageData []byte, mimeType string) (GeminiReceiptParseResult, error) {
	var empty GeminiReceiptParseResult
	if len(imageData) == 0 {
		return empty, fmt.Errorf("image data is empty")
	}

	mimeType = strings.TrimSpace(mimeType)
	if mimeType == "" || !strings.HasPrefix(mimeType, "image/") {
		mimeType = http.DetectContentType(imageData)
	}
	if mimeType == "image/jpg" {
		mimeType = "image/jpeg"
	}

	parts := []*genai.Part{
		genai.NewPartFromText(receiptImagePrompt),
		genai.NewPartFromBytes(imageData, mimeType),
	}
	return c.generate(ctx, parts)
}

// ParseReceiptText parses already-extracted OCR text. Retained for the
// RECEIPT_PARSE_MODE=ocr rollback path and as the automatic fallback when the
// image path yields nothing.
func (c *GeminiClient) ParseReceiptText(ctx context.Context, ocrText string) (GeminiReceiptParseResult, error) {
	var empty GeminiReceiptParseResult
	if strings.TrimSpace(ocrText) == "" {
		return empty, fmt.Errorf("ocr text is empty")
	}

	parts := []*genai.Part{genai.NewPartFromText(receiptTextPromptHeader + ocrText + "\n---")}
	return c.generate(ctx, parts)
}

func (c *GeminiClient) generate(ctx context.Context, parts []*genai.Part) (GeminiReceiptParseResult, error) {
	var empty GeminiReceiptParseResult

	contents := []*genai.Content{{Role: genai.RoleUser, Parts: parts}}
	resp, err := c.client.Models.GenerateContent(ctx, c.model, contents, c.config())
	if err != nil {
		return empty, fmt.Errorf("failed to generate content: %w", err)
	}

	responseText := extractGeminiText(resp)
	if responseText == "" {
		return empty, fmt.Errorf("empty response from Gemini")
	}

	var parsed geminiReceiptData
	if err := json.Unmarshal([]byte(cleanGeminiJSON(responseText)), &parsed); err != nil {
		return empty, fmt.Errorf("failed to parse Gemini JSON: %w", err)
	}

	receiptDate := parseReceiptDate(parsed.ReceiptDate)
	if receiptDate == nil {
		receiptDate = parseReceiptDate(parsed.Date)
	}

	return GeminiReceiptParseResult{
		Items:       normalizeParsedItems(parsed.Items),
		Currency:    normalizeOptionalString(parsed.Currency),
		ReceiptDate: receiptDate,
		Title:       normalizeOptionalString(parsed.Title),
		Tax:         parsed.Tax,
		Tip:         parsed.Tip,
	}, nil
}

// config asks for schema-constrained JSON so the response needs no coaxing,
// and keeps reasoning off — receipt extraction does not benefit from it and it
// is billed as output tokens. Gemini 3 uses thinkingLevel and mediaResolution;
// 2.5 uses thinkingBudget and rejects mediaResolution.
func (c *GeminiClient) config() *genai.GenerateContentConfig {
	config := &genai.GenerateContentConfig{
		Temperature:      genai.Ptr(float32(0.1)),
		TopP:             genai.Ptr(float32(0.95)),
		MaxOutputTokens:  4096,
		ResponseMIMEType: "application/json",
		ResponseSchema:   receiptResponseSchema(),
	}

	if strings.HasPrefix(c.model, "gemini-3") {
		config.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelMinimal}
		// Receipts are dense small text; don't let the model downsample them.
		config.MediaResolution = genai.MediaResolutionHigh
	} else {
		config.ThinkingConfig = &genai.ThinkingConfig{ThinkingBudget: genai.Ptr(int32(0))}
		config.TopK = genai.Ptr(float32(40))
	}

	return config
}

func receiptResponseSchema() *genai.Schema {
	nullableNumber := &genai.Schema{Type: genai.TypeNumber, Nullable: genai.Ptr(true)}
	nullableString := &genai.Schema{Type: genai.TypeString, Nullable: genai.Ptr(true)}

	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"items": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"name":           {Type: genai.TypeString},
						"quantity":       {Type: genai.TypeInteger},
						"total_price":    nullableNumber,
						"price_per_item": nullableNumber,
					},
					PropertyOrdering: []string{"name", "quantity", "total_price", "price_per_item"},
					Required:         []string{"name", "quantity"},
				},
			},
			"currency":     nullableString,
			"receipt_date": nullableString,
			"title":        nullableString,
			"tax":          nullableNumber,
			"tip":          nullableNumber,
		},
		PropertyOrdering: []string{"items", "currency", "receipt_date", "title", "tax", "tip"},
		Required:         []string{"items"},
	}
}

// normalizeParsedItems drops unusable rows and derives whichever of
// total/per-item price the model left null.
func normalizeParsedItems(raw []geminiReceiptItem) []ReceiptItemParsed {
	items := make([]ReceiptItemParsed, 0, len(raw))
	for _, item := range raw {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}

		qty := item.Quantity
		if qty <= 0 {
			qty = 1
		}

		if item.TotalPrice == nil && item.PricePerItem == nil {
			continue
		}

		var totalPrice, pricePerItem float64
		switch {
		case item.TotalPrice == nil:
			pricePerItem = *item.PricePerItem
			totalPrice = pricePerItem * float64(qty)
		case item.PricePerItem == nil:
			totalPrice = *item.TotalPrice
			pricePerItem = totalPrice / float64(qty)
		default:
			totalPrice = *item.TotalPrice
			pricePerItem = *item.PricePerItem
		}

		if totalPrice <= 0 || pricePerItem <= 0 {
			continue
		}

		items = append(items, ReceiptItemParsed{
			Name:         name,
			Quantity:     qty,
			TotalPrice:   totalPrice,
			PricePerItem: pricePerItem,
		})
	}
	return items
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

// parseReceiptDate parses a date string from a receipt into *time.Time.
// Tries common receipt date formats; returns nil if parsing fails.
func parseReceiptDate(value *string) *time.Time {
	if value == nil {
		return nil
	}
	s := strings.TrimSpace(*value)
	if s == "" {
		return nil
	}
	layouts := []string{
		"2006-01-02",          // ISO 8601
		"2006-01-02T15:04:05", // ISO 8601 with time
		"01/02/2006",          // US
		"02/01/2006",          // EU
		"2006/01/02",
		"Jan 2, 2006",
		"January 2, 2006",
		"2 Jan 2006",
		"02-Jan-2006",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

// cleanGeminiJSON is belt-and-braces now that the request carries a response
// schema: it strips code fences if a model ever ignores the JSON mime type.
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

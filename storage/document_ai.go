package storage

import (
	"context"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"

	documentai "cloud.google.com/go/documentai/apiv1"
	documentaipb "cloud.google.com/go/documentai/apiv1/documentaipb"
	"google.golang.org/api/option"
)

// DocumentAIReceipt captures the structured result from Document AI.
type DocumentAIReceipt struct {
	Text         string
	MerchantName string
	TotalAmount  *float64
	TaxAmount    *float64
	Items        []ReceiptItemParsed
}

var moneyPattern = regexp.MustCompile(`[-+]?\d[\d,]*\.?\d{0,2}`)
var quantityPattern = regexp.MustCompile(`\d+(\.\d+)?`)

// ProcessReceiptWithDocumentAI sends the document bytes to the Document AI receipt processor.
func ProcessReceiptWithDocumentAI(ctx context.Context, documentData []byte, mimeType string) (*DocumentAIReceipt, error) {
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

	location := os.Getenv("DOCUMENT_AI_LOCATION")
	if location == "" {
		location = os.Getenv("DOCUMENT_AI_PROCESSOR_LOCATION")
	}
	if location == "" {
		return nil, fmt.Errorf("DOCUMENT_AI_LOCATION environment variable is not set")
	}

	processorID := os.Getenv("DOCUMENT_AI_PROCESSOR_ID")
	if processorID == "" {
		return nil, fmt.Errorf("DOCUMENT_AI_PROCESSOR_ID environment variable is not set")
	}

	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	client, err := documentai.NewDocumentProcessorClient(ctx, option.WithCredentialsJSON([]byte(credsJSON)))
	if err != nil {
		return nil, fmt.Errorf("failed to create Document AI client: %w", err)
	}
	defer client.Close()

	processorName := fmt.Sprintf("projects/%s/locations/%s/processors/%s", projectID, location, processorID)
	req := &documentaipb.ProcessRequest{
		Name: processorName,
		RawDocument: &documentaipb.RawDocument{
			Content:  documentData,
			MimeType: mimeType,
		},
	}

	resp, err := client.ProcessDocument(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to process document: %w", err)
	}

	doc := resp.GetDocument()
	if doc == nil {
		return nil, fmt.Errorf("no document returned from Document AI")
	}

	result := &DocumentAIReceipt{
		Text: doc.GetText(),
	}

	for _, entity := range doc.GetEntities() {
		switch entity.GetType() {
		case "merchant_name", "supplier_name", "vendor_name":
			if result.MerchantName == "" {
				result.MerchantName = strings.TrimSpace(entity.GetMentionText())
			}
		case "total_amount":
			if amount, ok := moneyFromEntity(entity); ok {
				result.TotalAmount = &amount
			}
		case "tax_amount":
			if amount, ok := moneyFromEntity(entity); ok {
				result.TaxAmount = &amount
			}
		case "line_item":
			item := parseLineItemEntity(entity)
			if item.Name != "" && item.TotalPrice > 0 {
				result.Items = append(result.Items, item)
			}
		}
	}

	return result, nil
}

func parseLineItemEntity(entity *documentaipb.Document_Entity) ReceiptItemParsed {
	item := ReceiptItemParsed{Quantity: 1}

	for _, prop := range entity.GetProperties() {
		switch prop.GetType() {
		case "description":
			item.Name = strings.TrimSpace(prop.GetMentionText())
		case "quantity":
			item.Quantity = parseQuantity(prop.GetMentionText())
		case "unit_price":
			if amount, ok := moneyFromEntity(prop); ok {
				item.PricePerItem = amount
			}
		case "amount":
			if amount, ok := moneyFromEntity(prop); ok {
				item.TotalPrice = amount
			}
		}
	}

	if item.TotalPrice == 0 && item.PricePerItem > 0 {
		item.TotalPrice = item.PricePerItem * float64(item.Quantity)
	}
	if item.PricePerItem == 0 && item.TotalPrice > 0 {
		item.PricePerItem = item.TotalPrice / float64(item.Quantity)
	}

	return item
}

func moneyFromEntity(entity *documentaipb.Document_Entity) (float64, bool) {
	if entity == nil {
		return 0, false
	}

	if normalized := entity.GetNormalizedValue(); normalized != nil {
		if money := normalized.GetMoneyValue(); money != nil {
			return moneyToFloat(money), true
		}
	}

	return moneyFromText(entity.GetMentionText())
}

func moneyToFloat(money *documentaipb.Money) float64 {
	if money == nil {
		return 0
	}
	return float64(money.Units) + float64(money.Nanos)/1e9
}

func moneyFromText(text string) (float64, bool) {
	match := moneyPattern.FindString(text)
	if match == "" {
		return 0, false
	}
	match = strings.ReplaceAll(match, ",", "")
	amount, err := strconv.ParseFloat(match, 64)
	if err != nil {
		return 0, false
	}
	return amount, true
}

func parseQuantity(text string) int {
	match := quantityPattern.FindString(text)
	if match == "" {
		return 1
	}
	value, err := strconv.ParseFloat(match, 64)
	if err != nil {
		return 1
	}
	if value < 1 {
		return 1
	}
	return int(math.Round(value))
}

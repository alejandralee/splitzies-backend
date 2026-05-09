package storage

import (
	"regexp"
	"strconv"
	"strings"
)

// ReceiptItemParsed represents a parsed receipt item from OCR text.
type ReceiptItemParsed struct {
	Name         string
	Quantity     int
	TotalPrice   float64
	PricePerItem float64
}

// ExtractReceiptItemsFromText is a fallback regex parser used when Gemini fails.
func ExtractReceiptItemsFromText(ocrText string) []ReceiptItemParsed {
	pricePattern := regexp.MustCompile(`(?i)(.+?)\s+(\d+)?\s*\$?([\d,]+\.?\d{0,2})`)
	endPricePattern := regexp.MustCompile(`(.+?)\s+\$?([\d,]+\.?\d{0,2})\s*$`)
	skipPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)^(subtotal|tax|total|amount|change|cash|card|receipt|thank|visit|date|time)`),
		regexp.MustCompile(`(?i)^\s*\$?[\d,]+\.?\d{0,2}\s*$`),
		regexp.MustCompile(`^[\s\-=]+$`),
	}

	var items []ReceiptItemParsed
	for _, line := range strings.Split(ocrText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		skip := false
		for _, p := range skipPatterns {
			if p.MatchString(line) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		var item ReceiptItemParsed
		var found bool

		if m := pricePattern.FindStringSubmatch(line); len(m) >= 4 {
			item.Name = strings.TrimSpace(m[1])
			item.Quantity = 1
			if m[2] != "" {
				if q, err := strconv.Atoi(m[2]); err == nil {
					item.Quantity = q
				}
			}
			if price, err := strconv.ParseFloat(strings.ReplaceAll(m[3], ",", ""), 64); err == nil {
				item.TotalPrice = price
				item.PricePerItem = price / float64(item.Quantity)
				found = true
			}
		} else if m := endPricePattern.FindStringSubmatch(line); len(m) >= 3 {
			item.Name = strings.TrimSpace(m[1])
			item.Quantity = 1
			if price, err := strconv.ParseFloat(strings.ReplaceAll(m[2], ",", ""), 64); err == nil {
				item.TotalPrice = price
				item.PricePerItem = price
				found = true
			}
		}

		if found && item.Name != "" && item.TotalPrice > 0 {
			items = append(items, item)
		}
	}
	return items
}

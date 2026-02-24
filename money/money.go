package money

import (
	"fmt"
	"strings"

	"github.com/Rhymond/go-money"
)

// Amount represents a monetary value with currency-aware decimal precision for JSON marshaling.
// Uses go-money for ISO 4217 currency support (e.g. USD=2, KWD=3, JPY=0 decimal places).
type Amount struct {
	Value    float64
	Currency *string
}

// MarshalJSON implements json.Marshaler to output clean decimal format (e.g. 12.95 not 12.950000762939453).
func (a Amount) MarshalJSON() ([]byte, error) {
	decimals := DecimalPlaces(a.Currency)
	format := fmt.Sprintf("%%.%df", decimals)
	return []byte(fmt.Sprintf(format, a.Value)), nil
}

// DecimalPlaces returns the number of decimal places for the currency per ISO 4217.
// Defaults to 2 for nil or unknown currencies.
func DecimalPlaces(currency *string) int {
	code := money.USD
	if currency != nil && strings.TrimSpace(*currency) != "" {
		code = strings.ToUpper(*currency)
	}
	c := money.GetCurrency(code)
	if c == nil {
		return 2
	}
	return c.Fraction
}

// Round rounds a value to the currency's decimal places using go-money.
func Round(value float64, currency *string) float64 {
	code := money.USD
	if currency != nil && strings.TrimSpace(*currency) != "" {
		code = strings.ToUpper(*currency)
	}
	m := money.NewFromFloat(value, code)
	rounded := m.Round()
	return rounded.AsMajorUnits()
}

// NewAmount creates an Amount for JSON marshaling with currency-aware precision.
func NewAmount(value float64, currency *string) Amount {
	return Amount{
		Value:    Round(value, currency),
		Currency: currency,
	}
}

// Ptr returns a pointer to an Amount, or nil if value is nil.
func Ptr(value *float64, currency *string) *Amount {
	if value == nil {
		return nil
	}
	a := NewAmount(*value, currency)
	return &a
}

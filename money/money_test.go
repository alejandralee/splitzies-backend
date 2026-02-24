package money

import (
	"encoding/json"
	"testing"
)

func TestRound(t *testing.T) {
	usd := "USD"
	tests := []struct {
		value    float64
		currency *string
		want     float64
	}{
		{21.95, &usd, 21.95},
		{22.0, &usd, 22.0},
		{18.00, &usd, 18.0},
		{12.950000762939453, &usd, 12.95},
		{21.95, nil, 21.95},
	}
	for _, tt := range tests {
		got := Round(tt.value, tt.currency)
		if got != tt.want {
			t.Errorf("Round(%v, %v) = %v, want %v", tt.value, tt.currency, got, tt.want)
		}
	}
}

func TestAmountMarshalJSON(t *testing.T) {
	usd := "USD"
	tests := []struct {
		value float64
		want  string
	}{
		{21.95, "21.95"},
		{22.0, "22.00"},
		{18.0, "18.00"},
	}
	for _, tt := range tests {
		a := Amount{Value: tt.value, Currency: &usd}
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if got := string(b); got != tt.want {
			t.Errorf("Marshal(%v) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestNewAmountPreservesDecimals(t *testing.T) {
	usd := "USD"
	// NewAmount uses Round - ensure 21.95 is preserved (was incorrectly rounded to 22.00 before fix)
	a := NewAmount(21.95, &usd)
	if a.Value != 21.95 {
		t.Errorf("NewAmount(21.95) = %v, want 21.95", a.Value)
	}
	b, _ := json.Marshal(a)
	if string(b) != "21.95" {
		t.Errorf("NewAmount(21.95) marshaled as %q, want \"21.95\"", string(b))
	}
}

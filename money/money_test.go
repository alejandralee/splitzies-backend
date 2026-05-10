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
	jpy := "JPY"
	tests := []struct {
		value    float64
		currency *string
		want     string
	}{
		{21.95, &usd, `{"amount":21.95,"currency":"USD"}`},
		{22.0, &usd, `{"amount":22.00,"currency":"USD"}`},
		{18.0, &usd, `{"amount":18.00,"currency":"USD"}`},
		{1500.0, &jpy, `{"amount":1500,"currency":"JPY"}`},
		{21.95, nil, `{"amount":21.95,"currency":"USD"}`},
	}
	for _, tt := range tests {
		a := Amount{Value: tt.value, Currency: tt.currency}
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
	a := NewAmount(21.95, &usd)
	if a.Value != 21.95 {
		t.Errorf("NewAmount(21.95) = %v, want 21.95", a.Value)
	}
	b, _ := json.Marshal(a)
	if string(b) != `{"amount":21.95,"currency":"USD"}` {
		t.Errorf("NewAmount(21.95) marshaled as %q", string(b))
	}
}

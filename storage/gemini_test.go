package storage

import "testing"

func ptr[T any](v T) *T { return &v }

func TestNormalizeParsedItems(t *testing.T) {
	tests := []struct {
		name string
		in   geminiReceiptItem
		want *ReceiptItemParsed
	}{
		{
			name: "derives per-item price from total",
			in:   geminiReceiptItem{Name: " Fries ", Quantity: 2, TotalPrice: ptr(7.00)},
			want: &ReceiptItemParsed{Name: "Fries", Quantity: 2, TotalPrice: 7.00, PricePerItem: 3.50},
		},
		{
			name: "derives total from per-item price",
			in:   geminiReceiptItem{Name: "Beer", Quantity: 3, PricePerItem: ptr(6.00)},
			want: &ReceiptItemParsed{Name: "Beer", Quantity: 3, TotalPrice: 18.00, PricePerItem: 6.00},
		},
		{
			name: "missing quantity defaults to one",
			in:   geminiReceiptItem{Name: "Taco", TotalPrice: ptr(4.25)},
			want: &ReceiptItemParsed{Name: "Taco", Quantity: 1, TotalPrice: 4.25, PricePerItem: 4.25},
		},
		{
			name: "drops item with no price",
			in:   geminiReceiptItem{Name: "Water", Quantity: 1},
			want: nil,
		},
		{
			name: "drops unnamed item",
			in:   geminiReceiptItem{Name: "  ", Quantity: 1, TotalPrice: ptr(3.00)},
			want: nil,
		},
		{
			name: "drops non-positive price",
			in:   geminiReceiptItem{Name: "Discount", Quantity: 1, TotalPrice: ptr(-2.00)},
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeParsedItems([]geminiReceiptItem{tc.in})
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("expected item to be dropped, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("expected 1 item, got %d", len(got))
			}
			if got[0] != *tc.want {
				t.Fatalf("got %+v, want %+v", got[0], *tc.want)
			}
		})
	}
}

func TestParseReceiptDate(t *testing.T) {
	for _, in := range []string{"2026-03-04", "03/04/2026", "Mar 4, 2026", "2026-03-04 12:30:00"} {
		if got := parseReceiptDate(&in); got == nil {
			t.Errorf("parseReceiptDate(%q) = nil, want a date", in)
		}
	}
	for _, in := range []string{"", "   ", "not a date"} {
		if got := parseReceiptDate(&in); got != nil {
			t.Errorf("parseReceiptDate(%q) = %v, want nil", in, got)
		}
	}
	if got := parseReceiptDate(nil); got != nil {
		t.Errorf("parseReceiptDate(nil) = %v, want nil", got)
	}
}

func TestCleanGeminiJSON(t *testing.T) {
	cases := map[string]string{
		"```json\n{\"items\":[]}\n```": `{"items":[]}`,
		`{"items":[]}`:                 `{"items":[]}`,
		"here you go: {\"a\":1} done":  `{"a":1}`,
	}
	for in, want := range cases {
		if got := cleanGeminiJSON(in); got != want {
			t.Errorf("cleanGeminiJSON(%q) = %q, want %q", in, got, want)
		}
	}
}

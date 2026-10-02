package transport

import (
	"reflect"
	"testing"

	"splitzies/storage"
)

func TestMergeDuplicateItems(t *testing.T) {
	usd := "USD"
	in := []storage.ReceiptItemParsed{
		{Name: "Beer", Quantity: 1, TotalPrice: 6, PricePerItem: 6},
		{Name: "Fries", Quantity: 1, TotalPrice: 5, PricePerItem: 5},
		{Name: " beer ", Quantity: 1, TotalPrice: 6, PricePerItem: 6},
		{Name: "Beer", Quantity: 2, TotalPrice: 12, PricePerItem: 6.0000001},
		{Name: "Beer", Quantity: 1, TotalPrice: 4, PricePerItem: 4},
		{Name: "Draft Beer", Quantity: 1, TotalPrice: 6, PricePerItem: 6},
	}
	want := []storage.ReceiptItemParsed{
		{Name: "Beer", Quantity: 4, TotalPrice: 24, PricePerItem: 6},
		{Name: "Fries", Quantity: 1, TotalPrice: 5, PricePerItem: 5},
		{Name: "Beer", Quantity: 1, TotalPrice: 4, PricePerItem: 4},
		{Name: "Draft Beer", Quantity: 1, TotalPrice: 6, PricePerItem: 6},
	}

	got := mergeDuplicateItems(in, &usd)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if got := mergeDuplicateItems(nil, nil); len(got) != 0 {
		t.Fatalf("expected no items, got %+v", got)
	}
}

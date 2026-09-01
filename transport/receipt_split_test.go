package transport

import (
	"testing"

	"splitzies/persistence"
)

func TestComputeBillSplit_EvenSplitAmongAssignees(t *testing.T) {
	items := []persistence.ReceiptItem{
		{ID: "margarita-1", Name: "Margarita", Amount: 12},
		{ID: "margarita-2", Name: "Margarita", Amount: 12},
		{ID: "margarita-3", Name: "Margarita", Amount: 12},
		{ID: "nachos-1", Name: "Nachos", Amount: 10},
		{ID: "nachos-2", Name: "Nachos", Amount: 10},
	}
	assignments := []persistence.ReceiptUserItem{
		{ReceiptUserID: "alice", ReceiptItemID: "margarita-1"},
		{ReceiptUserID: "alice", ReceiptItemID: "margarita-2"},
		{ReceiptUserID: "bob", ReceiptItemID: "margarita-3"},
		{ReceiptUserID: "alice", ReceiptItemID: "nachos-1"},
		{ReceiptUserID: "bob", ReceiptItemID: "nachos-1"},
		{ReceiptUserID: "charlie", ReceiptItemID: "nachos-2"},
	}

	result := ComputeBillSplit(items, assignments)

	want := map[string]float64{
		"alice":   29,
		"bob":     17,
		"charlie": 10,
	}
	for user, expected := range want {
		if got := result.UserTotal[user]; got != expected {
			t.Errorf("UserTotal[%q] = %v, want %v", user, got, expected)
		}
	}

	// Same group ("Nachos"), independently assigned items: group membership
	// must never influence the split.
	if got := result.AmountByUserItem["alice:nachos-1"]; got != 5 {
		t.Errorf("alice:nachos-1 = %v, want 5 (shared with bob)", got)
	}
	if got := result.AmountByUserItem["charlie:nachos-2"]; got != 10 {
		t.Errorf("charlie:nachos-2 = %v, want 10 (sole assignee)", got)
	}
}

func TestToGetReceiptResponse_ExtrasDefaultsToProportionalWhenNil(t *testing.T) {
	response := ToGetReceiptResponse("receipt-1", nil, nil, nil, BillSplitResult{}, nil, nil)

	if response.ExtrasMode != "proportional" {
		t.Errorf("ExtrasMode = %q, want %q", response.ExtrasMode, "proportional")
	}
	if response.Tax != nil || response.Tip != nil {
		t.Errorf("Tax/Tip = %v/%v, want nil/nil when extras is nil", response.Tax, response.Tip)
	}
}

func TestToGetReceiptResponse_CarriesExtrasThrough(t *testing.T) {
	tax, tip := 1.5, 5.0
	extras := &persistence.ReceiptExtras{Tax: &tax, Tip: &tip, ExtrasMode: "even"}

	response := ToGetReceiptResponse("receipt-1", nil, nil, nil, BillSplitResult{}, nil, extras)

	if response.ExtrasMode != "even" {
		t.Errorf("ExtrasMode = %q, want %q", response.ExtrasMode, "even")
	}
	if response.Tax == nil || response.Tax.Value != 1.5 {
		t.Errorf("Tax = %v, want 1.5", response.Tax)
	}
	if response.Tip == nil || response.Tip.Value != 5.0 {
		t.Errorf("Tip = %v, want 5.0", response.Tip)
	}
}

func TestComputeBillSplit_UnassignedItemExcluded(t *testing.T) {
	items := []persistence.ReceiptItem{
		{ID: "item-1", Name: "Soda", Amount: 3},
	}
	var assignments []persistence.ReceiptUserItem

	result := ComputeBillSplit(items, assignments)

	if len(result.UserTotal) != 0 {
		t.Errorf("expected no user totals for an unassigned item, got %v", result.UserTotal)
	}
	if len(result.AmountByUserItem) != 0 {
		t.Errorf("expected no per-item amounts for an unassigned item, got %v", result.AmountByUserItem)
	}
}

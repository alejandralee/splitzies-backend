package transport

import (
	"encoding/json"
	"fmt"
	"net/http"

	"splitzies/money"
)

// CreateReceiptItemHandler handles POST /receipts/{receipt_id}/items.
//
// When group_id is set, the new unit joins that existing group (adopting its
// name and price) — this is the quantity stepper's "+". When group_id is
// omitted, name and amount are required and a new group is created holding
// this single unit.
func (t *Transport) CreateReceiptItemHandler(w http.ResponseWriter, r *http.Request) {
	receiptID := r.PathValue("receipt_id")
	ctx := r.Context()

	var req CreateReceiptItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body", "")
		return
	}

	var amount float64
	if req.GroupID == nil {
		if req.Name == "" {
			writeJSONError(w, http.StatusBadRequest, "missing_field", newValidationError("name", "name is required when group_id is not set").Error(), "")
			return
		}
		if req.Amount == nil {
			writeJSONError(w, http.StatusBadRequest, "missing_field", newValidationError("amount", "amount is required when group_id is not set").Error(), "")
			return
		}
		amount = *req.Amount
	}

	exists, err := t.persistenceClient.ReceiptExists(ctx, receiptID)
	if err != nil {
		t.log.Error("failed to check receipt existence", "receipt_id", receiptID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to check receipt", "")
		return
	}
	if !exists {
		writeJSONError(w, http.StatusNotFound, "receipt_not_found", fmt.Sprintf("receipt %q not found", receiptID), "")
		return
	}

	item, err := t.persistenceClient.CreateReceiptItem(ctx, receiptID, req.GroupID, req.Name, amount)
	if err != nil {
		t.log.Error("failed to create receipt item", "receipt_id", receiptID, "error", err)
		if isNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "group_not_found", err.Error(), "")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "create_item_failed", "failed to create receipt item", "")
		return
	}

	currency, err := t.persistenceClient.GetReceiptCurrency(ctx, receiptID)
	if err != nil {
		t.log.Error("failed to get receipt currency, defaulting to USD", "receipt_id", receiptID, "error", err)
		currency = &defaultUSD
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(ReceiptItem{
		ID:           item.ID,
		GroupID:      item.GroupID,
		GroupName:    item.GroupName,
		Name:         item.Name,
		DisplayOrder: item.DisplayOrder,
		Amount:       money.Ptr(&item.Amount, currency),
	}); err != nil {
		t.log.Error("failed to encode create item response", "error", err)
	}
}

// PatchReceiptItemGroupHandler handles PATCH /receipts/{receipt_id}/item-groups/{group_id}.
// Updates apply to every unit in the group, since the review screen edits name
// and price per group, not per unit.
func (t *Transport) PatchReceiptItemGroupHandler(w http.ResponseWriter, r *http.Request) {
	receiptID := r.PathValue("receipt_id")
	groupID := r.PathValue("group_id")

	var req PatchReceiptItemGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body", "")
		return
	}
	if req.Name == nil && req.Amount == nil {
		writeJSONError(w, http.StatusBadRequest, "missing_field", "at least one of name or amount is required", "")
		return
	}

	if err := t.persistenceClient.PatchReceiptItemGroup(r.Context(), receiptID, groupID, req.Name, req.Amount); err != nil {
		t.log.Error("failed to patch item group", "receipt_id", receiptID, "group_id", groupID, "error", err)
		if isNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "group_not_found", err.Error(), "")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "patch_group_failed", "failed to update item group", "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"message": "item group updated successfully"}); err != nil {
		t.log.Error("failed to encode patch item group response", "error", err)
	}
}

// DeleteReceiptItemHandler handles DELETE /receipts/{receipt_id}/items/{item_id}.
// Assignments cascade automatically; if this was the group's last unit, the
// now-empty group is removed too.
func (t *Transport) DeleteReceiptItemHandler(w http.ResponseWriter, r *http.Request) {
	receiptID := r.PathValue("receipt_id")
	itemID := r.PathValue("item_id")

	if err := t.persistenceClient.DeleteReceiptItem(r.Context(), receiptID, itemID); err != nil {
		t.log.Error("failed to delete receipt item", "receipt_id", receiptID, "item_id", itemID, "error", err)
		if isNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "item_not_found", err.Error(), "")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "delete_item_failed", "failed to delete receipt item", "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"message": "item deleted successfully"}); err != nil {
		t.log.Error("failed to encode delete item response", "error", err)
	}
}

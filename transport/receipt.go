package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"splitzies/money"
	"splitzies/persistence"
)

// AddUserToReceiptHandler handles adding a user to a receipt
// Expects POST /receipts/{receipt_id}/users
// Request body: {"name": "John Doe"}
func (t *Transport) AddUserToReceiptHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, NewInvalidMethodError(r.Method).Error(), http.StatusMethodNotAllowed)
		return
	}
	receiptID, ok := parseReceiptUsersPath(r.URL.Path)
	if !ok {
		http.Error(w, NewValidationError("path", "invalid URL path format").Error(), http.StatusBadRequest)
		return
	}

	var req AddUserToReceiptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, NewValidationError("body", fmt.Sprintf("failed to parse request body: %v", err)).Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, NewValidationError("name", "name is required").Error(), http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	user, err := t.persistenceClient.AddUserToReceipt(ctx, receiptID, req.Name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("Failed to add user to receipt: %v", err), http.StatusInternalServerError)
		return
	}

	response := AddUserToReceiptResponse{
		Message: "User added to receipt successfully",
	}
	response.User.ID = user.ID
	response.User.ReceiptID = user.ReceiptID
	response.User.Name = user.Name

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		fmt.Printf("Failed to encode response: %v\n", err)
	}
}

// PatchReceiptHandler handles updating tax and tip on a receipt (when not parsed from OCR)
// Expects PATCH /receipts/{receipt_id}
// Request body: {"tax": 1.50, "tip": 5.00} - both optional
func (t *Transport) PatchReceiptHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, NewInvalidMethodError(r.Method).Error(), http.StatusMethodNotAllowed)
		return
	}
	receiptID, ok := parseReceiptIDPath(r.URL.Path)
	if !ok {
		http.Error(w, NewValidationError("path", "invalid URL path format").Error(), http.StatusBadRequest)
		return
	}

	var req PatchReceiptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, NewValidationError("body", fmt.Sprintf("failed to parse request body: %v", err)).Error(), http.StatusBadRequest)
		return
	}
	if req.Tax == nil && req.Tip == nil {
		http.Error(w, NewValidationError("body", "at least one of tax or tip is required").Error(), http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	err := t.persistenceClient.UpdateReceiptTaxTip(ctx, receiptID, req.Tax, req.Tip)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("Failed to update receipt: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{"message": "Receipt updated successfully"}); err != nil {
		fmt.Printf("Failed to encode response: %v\n", err)
	}
}

// GetReceiptUsersHandler handles getting users for a receipt
// Expects GET /receipts/{receipt_id}/users
func (t *Transport) GetReceiptUsersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, NewInvalidMethodError(r.Method).Error(), http.StatusMethodNotAllowed)
		return
	}
	receiptID, ok := parseReceiptUsersPath(r.URL.Path)
	if !ok {
		http.Error(w, NewValidationError("path", "invalid URL path format").Error(), http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	exists, err := t.persistenceClient.ReceiptExists(ctx, receiptID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to check receipt: %v", err), http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "receipt not found", http.StatusNotFound)
		return
	}

	users, err := t.persistenceClient.GetReceiptUsers(ctx, receiptID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get receipt users: %v", err), http.StatusInternalServerError)
		return
	}

	responseUsers := make([]GetReceiptUserResponse, len(users))
	for i, u := range users {
		responseUsers[i] = GetReceiptUserResponse{
			ID:        u.ID,
			ReceiptID: u.ReceiptID,
			Name:      u.Name,
			UserTotal: nil,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(GetReceiptUsersResponse{Users: responseUsers}); err != nil {
		fmt.Printf("Failed to encode response: %v\n", err)
	}
}

// GetReceiptItemsHandler handles getting items for a receipt
// Expects GET /receipts/{receipt_id}/items
func (t *Transport) GetReceiptItemsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, NewInvalidMethodError(r.Method).Error(), http.StatusMethodNotAllowed)
		return
	}
	receiptID, ok := parseReceiptItemsPath(r.URL.Path)
	if !ok {
		http.Error(w, NewValidationError("path", "invalid URL path format").Error(), http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	exists, err := t.persistenceClient.ReceiptExists(ctx, receiptID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to check receipt: %v", err), http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "receipt not found", http.StatusNotFound)
		return
	}

	items, err := t.persistenceClient.GetReceiptItems(ctx, receiptID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get receipt items: %v", err), http.StatusInternalServerError)
		return
	}

	currency, err := t.persistenceClient.GetReceiptCurrency(ctx, receiptID)
	if err != nil {
		t.log.Error("Failed to get receipt currency, using USD", "receipt_id", receiptID, "error", err)
		currency = &defaultUSD
	}
	responseItems := itemsToReceiptItems(items, currency)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{"items": responseItems}); err != nil {
		fmt.Printf("Failed to encode response: %v\n", err)
	}
}

// GetReceiptHandler handles getting the full receipt with users, items, and assignments (bill split data)
// Expects GET /receipts/{receipt_id}
// Returns users, items, and assignments (user-item correlation) for easy frontend bill split UI
func (t *Transport) GetReceiptHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, NewInvalidMethodError(r.Method).Error(), http.StatusMethodNotAllowed)
		return
	}
	receiptID, ok := parseReceiptIDPath(r.URL.Path)
	if !ok {
		http.Error(w, NewValidationError("path", "invalid URL path format").Error(), http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	exists, err := t.persistenceClient.ReceiptExists(ctx, receiptID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to check receipt: %v", err), http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "receipt not found", http.StatusNotFound)
		return
	}

	users, err := t.persistenceClient.GetReceiptUsers(ctx, receiptID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get receipt users: %v", err), http.StatusInternalServerError)
		return
	}
	items, err := t.persistenceClient.GetReceiptItems(ctx, receiptID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get receipt items: %v", err), http.StatusInternalServerError)
		return
	}
	assignments, err := t.persistenceClient.GetReceiptAssignments(ctx, receiptID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get receipt assignments: %v", err), http.StatusInternalServerError)
		return
	}

	currency, err := t.persistenceClient.GetReceiptCurrency(ctx, receiptID)
	if err != nil {
		t.log.Error("Failed to get receipt currency, using USD", "receipt_id", receiptID, "error", err)
		currency = &defaultUSD
	}

	split := ComputeBillSplit(items, assignments)
	response := ToGetReceiptResponse(receiptID, users, items, assignments, split, currency)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		fmt.Printf("Failed to encode response: %v\n", err)
	}
}

// AssignItemsToUserHandler handles assigning items to a user
// Expects POST /receipts/{receipt_id}/users/{user_id}/items
func (t *Transport) AssignItemsToUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, NewInvalidMethodError(r.Method).Error(), http.StatusMethodNotAllowed)
		return
	}
	userID, ok := parseReceiptUserItemsPath(r.URL.Path)
	if !ok {
		http.Error(w, NewValidationError("path", "invalid URL path format").Error(), http.StatusBadRequest)
		return
	}

	var req AssignItemsToUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, NewValidationError("body", fmt.Sprintf("failed to parse request body: %v", err)).Error(), http.StatusBadRequest)
		return
	}
	if len(req.ItemIDs) == 0 {
		http.Error(w, NewValidationError("item_ids", "at least one item_id is required").Error(), http.StatusBadRequest)
		return
	}

	assignedItems := make([]AssignItemsToUserItem, 0, len(req.ItemIDs))

	ctx := context.Background()
	for _, itemID := range req.ItemIDs {
		assignment, err := t.persistenceClient.AssignItemToUser(ctx, userID, itemID, nil)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, fmt.Sprintf("Failed to assign item %s to user: %v", itemID, err), http.StatusInternalServerError)
			return
		}
		assignedItems = append(assignedItems, AssignItemsToUserItem{
			ID:            assignment.ID,
			ReceiptUserID: assignment.ReceiptUserID,
			ReceiptItemID: assignment.ReceiptItemID,
		})
	}

	response := AssignItemsToUserResponse{
		Message: fmt.Sprintf("Successfully assigned %d item(s) to user", len(assignedItems)),
		Items:   assignedItems,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		fmt.Printf("Failed to encode response: %v\n", err)
	}
}

func itemsToReceiptItems(items []persistence.ReceiptItem, currency *string) []ReceiptItem {
	result := make([]ReceiptItem, len(items))
	for i, item := range items {
		result[i] = ReceiptItem{
			ID:           item.ID,
			Name:         item.Name,
			Quantity:     item.Quantity,
			TotalPrice:   money.Ptr(&item.TotalPrice, currency),
			PricePerItem: money.Ptr(&item.PricePerItem, currency),
		}
	}
	return result
}

package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"splitzies/money"
	"splitzies/persistence"
)

// AddUserToReceiptHandler handles POST /receipts/{receipt_id}/users
func (t *Transport) AddUserToReceiptHandler(w http.ResponseWriter, r *http.Request) {
	receiptID := r.PathValue("receipt_id")

	var req AddUserToReceiptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body", "")
		return
	}
	if req.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_field", newValidationError("name", "name is required").Error(), "")
		return
	}

	// Only claim the new participant for this device when explicitly asked, so
	// an organiser typing in everyone's names doesn't claim the first one.
	var deviceID *string
	if req.Claim {
		deviceID = deviceIDFromContext(r.Context())
		if deviceID == nil {
			writeJSONError(w, http.StatusUnauthorized, "device_required",
				"claim requires a valid "+DeviceTokenHeader+" header; create one with POST /devices", "")
			return
		}
	}

	user, err := t.persistenceClient.AddUserToReceipt(r.Context(), receiptID, req.Name, deviceID)
	if err != nil {
		t.log.Error("failed to add user to receipt", "receipt_id", receiptID, "error", err)
		if isNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "receipt_not_found", fmt.Sprintf("receipt %q not found", receiptID), "")
			return
		}
		if strings.Contains(err.Error(), "already claims") {
			writeJSONError(w, http.StatusConflict, "already_claimed", err.Error(), "")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "add_user_failed", "failed to add user to receipt", "")
		return
	}

	// A device that adds itself to a bill should find that bill in its history.
	if deviceID != nil {
		if err := t.persistenceClient.AddDeviceToReceipt(r.Context(), receiptID, *deviceID, persistence.RoleMember); err != nil {
			t.log.Error("failed to add device to receipt", "receipt_id", receiptID, "error", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(AddUserToReceiptResponse{
		Message: "user added to receipt successfully",
		User: struct {
			ID        string `json:"id"`
			ReceiptID string `json:"receipt_id"`
			Name      string `json:"name"`
		}{ID: user.ID, ReceiptID: user.ReceiptID, Name: user.Name},
	}); err != nil {
		t.log.Error("failed to encode add user response", "error", err)
	}
}

// RemoveUserFromReceiptHandler handles DELETE /receipts/{receipt_id}/users/{user_id}
func (t *Transport) RemoveUserFromReceiptHandler(w http.ResponseWriter, r *http.Request) {
	receiptID := r.PathValue("receipt_id")
	userID := r.PathValue("user_id")

	if err := t.persistenceClient.RemoveUserFromReceipt(r.Context(), receiptID, userID); err != nil {
		if isNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "user_not_found", err.Error(), "")
			return
		}
		t.log.Error("failed to remove user from receipt", "receipt_id", receiptID, "user_id", userID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "remove_user_failed", "failed to remove user from receipt", "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"message": "user removed from receipt successfully"}); err != nil {
		t.log.Error("failed to encode remove user response", "error", err)
	}
}

// PatchReceiptHandler handles PATCH /receipts/{receipt_id}
func (t *Transport) PatchReceiptHandler(w http.ResponseWriter, r *http.Request) {
	receiptID := r.PathValue("receipt_id")

	var req PatchReceiptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body", "")
		return
	}
	if req.Tax == nil && req.Tip == nil && req.ExtrasMode == nil {
		writeJSONError(w, http.StatusBadRequest, "missing_field", "at least one of tax, tip, or extras_mode is required", "")
		return
	}
	if req.ExtrasMode != nil && *req.ExtrasMode != "proportional" && *req.ExtrasMode != "even" {
		writeJSONError(w, http.StatusBadRequest, "invalid_field",
			newValidationError("extras_mode", `must be "proportional" or "even"`).Error(), "")
		return
	}

	if err := t.persistenceClient.UpdateReceiptExtras(r.Context(), receiptID, req.Tax, req.Tip, req.ExtrasMode); err != nil {
		t.log.Error("failed to update receipt extras", "receipt_id", receiptID, "error", err)
		if isNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "receipt_not_found", fmt.Sprintf("receipt %q not found", receiptID), "")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "update_receipt_failed", "failed to update receipt", "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"message": "receipt updated successfully"}); err != nil {
		t.log.Error("failed to encode patch receipt response", "error", err)
	}
}

// GetReceiptUsersHandler handles GET /receipts/{receipt_id}/users
func (t *Transport) GetReceiptUsersHandler(w http.ResponseWriter, r *http.Request) {
	receiptID := r.PathValue("receipt_id")
	ctx := r.Context()

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

	users, err := t.persistenceClient.GetReceiptUsers(ctx, receiptID)
	if err != nil {
		t.log.Error("failed to get receipt users", "receipt_id", receiptID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to get receipt users", "")
		return
	}

	responseUsers := make([]GetReceiptUserResponse, len(users))
	for i, u := range users {
		responseUsers[i] = GetReceiptUserResponse{
			ID:        u.ID,
			ReceiptID: u.ReceiptID,
			Name:      u.Name,
			DeviceID:  u.DeviceID,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(GetReceiptUsersResponse{Users: responseUsers}); err != nil {
		t.log.Error("failed to encode receipt users response", "error", err)
	}
}

// GetReceiptItemsHandler handles GET /receipts/{receipt_id}/items
func (t *Transport) GetReceiptItemsHandler(w http.ResponseWriter, r *http.Request) {
	receiptID := r.PathValue("receipt_id")
	ctx := r.Context()

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

	items, err := t.persistenceClient.GetReceiptItems(ctx, receiptID)
	if err != nil {
		t.log.Error("failed to get receipt items", "receipt_id", receiptID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to get receipt items", "")
		return
	}

	currency, err := t.persistenceClient.GetReceiptCurrency(ctx, receiptID)
	if err != nil {
		t.log.Error("failed to get receipt currency, defaulting to USD", "receipt_id", receiptID, "error", err)
		currency = &defaultUSD
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{"items": itemsToReceiptItems(items, currency)}); err != nil {
		t.log.Error("failed to encode receipt items response", "error", err)
	}
}

// GetReceiptHandler handles GET /receipts/{receipt_id}.
//
// This is the endpoint collaborators poll to pick up each other's edits, so it
// answers conditionally: the receipt's version is served as an ETag, and a
// matching If-None-Match returns 304 after a single row read, skipping the four
// queries and the split computation below.
func (t *Transport) GetReceiptHandler(w http.ResponseWriter, r *http.Request) {
	receiptID := r.PathValue("receipt_id")
	ctx := r.Context()

	version, err := t.persistenceClient.GetReceiptVersion(ctx, receiptID)
	if err != nil {
		if isNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "receipt_not_found", fmt.Sprintf("receipt %q not found", receiptID), "")
			return
		}
		t.log.Error("failed to read receipt version", "receipt_id", receiptID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to check receipt", "")
		return
	}

	etag := receiptETag(version)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	users, err := t.persistenceClient.GetReceiptUsers(ctx, receiptID)
	if err != nil {
		t.log.Error("failed to get receipt users", "receipt_id", receiptID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to get receipt users", "")
		return
	}
	items, err := t.persistenceClient.GetReceiptItems(ctx, receiptID)
	if err != nil {
		t.log.Error("failed to get receipt items", "receipt_id", receiptID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to get receipt items", "")
		return
	}
	assignments, err := t.persistenceClient.GetReceiptAssignments(ctx, receiptID)
	if err != nil {
		t.log.Error("failed to get receipt assignments", "receipt_id", receiptID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to get receipt assignments", "")
		return
	}

	currency, err := t.persistenceClient.GetReceiptCurrency(ctx, receiptID)
	if err != nil {
		t.log.Error("failed to get receipt currency, defaulting to USD", "receipt_id", receiptID, "error", err)
		currency = &defaultUSD
	}

	extras, err := t.persistenceClient.GetReceiptExtras(ctx, receiptID)
	if err != nil {
		t.log.Error("failed to get receipt extras", "receipt_id", receiptID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "failed to get receipt", "")
		return
	}

	split := ComputeBillSplit(items, assignments)
	response := ToGetReceiptResponse(receiptID, users, items, assignments, split, currency, extras)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.log.Error("failed to encode get receipt response", "error", err)
	}
}

// AssignItemsToUserHandler handles POST /receipts/{receipt_id}/users/{user_id}/items
func (t *Transport) AssignItemsToUserHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")

	var req AssignItemsToUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "failed to parse request body", "")
		return
	}
	if len(req.ItemIDs) == 0 {
		writeJSONError(w, http.StatusBadRequest, "missing_field", newValidationError("item_ids", "at least one item_id is required").Error(), "")
		return
	}

	assignedItems := make([]AssignItemsToUserItem, 0, len(req.ItemIDs))
	for _, itemID := range req.ItemIDs {
		assignment, err := t.persistenceClient.AssignUserToItem(r.Context(), userID, itemID)
		if err != nil {
			t.log.Error("failed to assign item to user", "user_id", userID, "item_id", itemID, "error", err)
			if isNotFound(err) {
				writeJSONError(w, http.StatusNotFound, "not_found", err.Error(), "")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "assign_failed", fmt.Sprintf("failed to assign item %q to user", itemID), "")
			return
		}
		assignedItems = append(assignedItems, AssignItemsToUserItem{
			ReceiptUserID: assignment.ReceiptUserID,
			ReceiptItemID: assignment.ReceiptItemID,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(AssignItemsToUserResponse{
		Message: fmt.Sprintf("successfully assigned %d item(s) to user", len(assignedItems)),
		Items:   assignedItems,
	}); err != nil {
		t.log.Error("failed to encode assign items response", "error", err)
	}
}

// UnassignItemFromUserHandler handles DELETE /receipts/{receipt_id}/users/{user_id}/items/{item_id}
func (t *Transport) UnassignItemFromUserHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	itemID := r.PathValue("item_id")

	if err := t.persistenceClient.UnassignUserFromItem(r.Context(), userID, itemID); err != nil {
		t.log.Error("failed to unassign item from user", "user_id", userID, "item_id", itemID, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "unassign_failed", fmt.Sprintf("failed to unassign item %q from user", itemID), "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"message": "item unassigned from user successfully"}); err != nil {
		t.log.Error("failed to encode unassign item response", "error", err)
	}
}

func itemsToReceiptItems(items []persistence.ReceiptItem, currency *string) []ReceiptItem {
	result := make([]ReceiptItem, len(items))
	for i, item := range items {
		result[i] = ReceiptItem{
			ID:           item.ID,
			GroupID:      item.GroupID,
			GroupName:    item.GroupName,
			Name:         item.Name,
			DisplayOrder: item.DisplayOrder,
			Amount:       money.Ptr(&item.Amount, currency),
		}
	}
	return result
}

// isNotFound returns true when an error message indicates a "not found" condition.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}

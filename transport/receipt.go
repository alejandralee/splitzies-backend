package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"splitzies/persistence"
	"splitzies/storage"
)

// ReceiptItem represents a single item in a receipt
type ReceiptItem struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Quantity     int      `json:"quantity"`
	TotalPrice   *float64 `json:"total_price,omitempty"`    // Optional, can be calculated
	PricePerItem *float64 `json:"price_per_item,omitempty"` // Optional, can be calculated
}

// AddReceiptRequest represents the request body for adding a receipt
type AddReceiptRequest struct {
	Items []ReceiptItem `json:"items"`
}

// AddReceiptResponse represents the response after processing a receipt
type AddReceiptResponse struct {
	Message  string        `json:"message"`
	Items    []ReceiptItem `json:"items"`
	ImageURL *string       `json:"image_url,omitempty"`
}

// UploadReceiptImageHandler handles receipt image uploads
// Expects multipart/form-data with:
//   - "image": the receipt image file
//
// Returns the uploaded image URL
func (t *Transport) UploadReceiptImageHandler(w http.ResponseWriter, r *http.Request) {
	// Generate receipt ID first (we'll create a receipt record with just the image)
	ctx := context.Background()
	receiptID := persistence.GenerateReceiptID()

	file, contentType, err := t.validateReceiptImageRequest(w, r)
	if err != nil {
		return
	}
	defer file.Close()

	// Read file data for OCR (we need to read it before uploading)
	// Reset file position after reading
	fileData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read image file: %v", err), http.StatusInternalServerError)
		return
	}

	// Upload image to GCS (using the data we just read)
	imageURL, err := t.gcsClient.UploadReceiptImageFromReader(ctx, bytes.NewReader(fileData), receiptID, contentType)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to upload image: %v", err), http.StatusInternalServerError)
		return
	}

	// Perform OCR on the image
	ocrText, err := t.visionClient.PerformOCRFromBytes(ctx, fileData)
	var parsedItems []persistence.ReceiptItemDB
	var ocrTextData *persistence.OCRTextData
	var currency *string
	var receiptDate *time.Time
	var title *string
	var tax *float64
	var tip *float64

	if err != nil {
		// OCR failed - log but don't fail the request
		// We'll save the receipt without OCR text
		fmt.Printf("Warning: OCR failed: %v\n", err)
	} else if ocrText != "" {
		// Always save OCR text for reference
		ocrTextData = &persistence.OCRTextData{
			Text: ocrText,
		}

		// Try to parse receipt items from OCR text using Gemini
		parseResult, parseErr := storage.ParseReceiptItemsWithGemini(ctx, ocrText)
		if parseErr != nil {
			fmt.Printf("Warning: Gemini parse failed: %v\n", parseErr)
			parseResult.Items = storage.ExtractReceiptItemsFromText(ocrText)
			parseResult.Currency = nil
			parseResult.ReceiptDate = nil
			parseResult.Title = nil
			parseResult.Tax = nil
			parseResult.Tip = nil
		}
		currency = parseResult.Currency
		receiptDate = parseResult.ReceiptDate
		title = parseResult.Title
		tax = parseResult.Tax
		tip = parseResult.Tip

		if len(parseResult.Items) > 0 {
			// Successfully parsed items - convert to ReceiptItemDB and save them
			parsedItems = make([]persistence.ReceiptItemDB, len(parseResult.Items))
			for i, item := range parseResult.Items {
				parsedItems[i] = persistence.ReceiptItemDB{
					Name:         item.Name,
					Quantity:     item.Quantity,
					TotalPrice:   item.TotalPrice,
					PricePerItem: item.PricePerItem,
				}
			}
		}
		// If items couldn't be parsed, we still save the OCR text (already set above)
	}

	// Save receipt with image URL, parsed items (if any), OCR text, Gemini metadata, and tax/tip if parsed
	savedReceipt, err := persistence.SaveReceipt(parsedItems, &imageURL, ocrTextData, currency, receiptDate, title, tax, tip)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to save receipt: %v", err), http.StatusInternalServerError)
		return
	}

	// Convert saved receipt items to response format (use savedReceipt.Items which have IDs)
	responseItems := make([]ReceiptItem, len(savedReceipt.Items))
	for i, item := range savedReceipt.Items {
		totalPrice := item.TotalPrice
		pricePerItem := item.PricePerItem
		responseItems[i] = ReceiptItem{
			ID:           item.ID,
			Name:         item.Name,
			Quantity:     item.Quantity,
			TotalPrice:   &totalPrice,
			PricePerItem: &pricePerItem,
		}
	}

	// Return success response
	response := map[string]interface{}{
		"message":    fmt.Sprintf("Receipt image uploaded successfully with ID: %s", savedReceipt.ID),
		"receipt_id": savedReceipt.ID,
		"image_url":  imageURL,
		"items":      responseItems,
	}

	// Include OCR text in response when available (for reference/debugging)
	if ocrTextData != nil {
		response["ocr_text"] = ocrTextData.Text
	}

	// Include tax/tip when parsed from receipt
	if tax != nil {
		response["tax"] = *tax
	}
	if tip != nil {
		response["tip"] = *tip
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		// Response already written, can't write error
		fmt.Printf("Failed to encode response: %v\n", err)
		return
	}
}

func (t *Transport) validateReceiptImageRequest(w http.ResponseWriter, r *http.Request) (file io.ReadCloser, contentType string, err error) {
	// Only allow POST method
	if r.Method != http.MethodPost {
		err = NewInvalidMethodError(r.Method)
		http.Error(w, err.Error(), http.StatusMethodNotAllowed)
		return nil, "", err
	}

	// Parse multipart form (max 10MB)
	err = r.ParseMultipartForm(10 << 20) // 10MB
	if err != nil {
		validationErr := NewValidationError("form", fmt.Sprintf("failed to parse multipart form: %v", err))
		http.Error(w, validationErr.Error(), http.StatusBadRequest)
		return nil, "", validationErr
	}

	// Get the image file from form
	file, header, err := r.FormFile("image")
	if err != nil {
		validationErr := NewValidationError("image", fmt.Sprintf("failed to get image file: %v", err))
		http.Error(w, validationErr.Error(), http.StatusBadRequest)
		return nil, "", validationErr
	}

	// Validate file size (max 10MB)
	if header.Size > 10<<20 {
		validationErr := NewValidationError("image", "image file too large (max 10MB)")
		http.Error(w, validationErr.Error(), http.StatusBadRequest)
		return nil, "", validationErr
	}

	// Validate content type
	contentType = header.Header.Get("Content-Type")
	if contentType != "" {
		validTypes := map[string]bool{
			"image/jpeg": true,
			"image/jpg":  true,
			"image/png":  true,
			"image/gif":  true,
			"image/webp": true,
		}
		if !validTypes[contentType] {
			validationErr := NewValidationError("image", fmt.Sprintf("invalid image type: %s", contentType))
			http.Error(w, validationErr.Error(), http.StatusBadRequest)
			return nil, "", validationErr
		}
	}
	return file, contentType, nil
}

// AddUserToReceiptRequest represents the request body for adding a user to a receipt
type AddUserToReceiptRequest struct {
	Name string `json:"name"`
}

// AddUserToReceiptResponse represents the response after adding a user to a receipt
type AddUserToReceiptResponse struct {
	Message string `json:"message"`
	User    struct {
		ID        string `json:"id"`
		ReceiptID string `json:"receipt_id"`
		Name      string `json:"name"`
	} `json:"user"`
}

// AddUserToReceiptHandler handles adding a user to a receipt
// Expects POST /receipts/{receipt_id}/users
// Request body: {"name": "John Doe"}
func (t *Transport) AddUserToReceiptHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, NewInvalidMethodError(r.Method).Error(), http.StatusMethodNotAllowed)
		return
	}

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) != 3 || pathParts[0] != "receipts" || pathParts[2] != "users" {
		http.Error(w, NewValidationError("path", "invalid URL path format").Error(), http.StatusBadRequest)
		return
	}
	receiptID := pathParts[1]

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

// AssignItemsToUserRequest represents the request body for assigning items to a user
type AssignItemsToUserRequest struct {
	ItemIDs []string `json:"item_ids"`
}

// AssignItemsToUserResponse represents the response after assigning items to a user
type AssignItemsToUserResponse struct {
	Message string `json:"message"`
	Items   []struct {
		ID            string `json:"id"`
		ReceiptUserID string `json:"receipt_user_id"`
		ReceiptItemID string `json:"receipt_item_id"`
	} `json:"items"`
}

// PatchReceiptRequest represents the request body for updating receipt tax/tip
type PatchReceiptRequest struct {
	Tax *float64 `json:"tax"`
	Tip *float64 `json:"tip"`
}

// PatchReceiptHandler handles updating tax and tip on a receipt (when not parsed from OCR)
// Expects PATCH /receipts/{receipt_id}
// Request body: {"tax": 1.50, "tip": 5.00} - both optional
func (t *Transport) PatchReceiptHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, NewInvalidMethodError(r.Method).Error(), http.StatusMethodNotAllowed)
		return
	}

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) != 2 || pathParts[0] != "receipts" {
		http.Error(w, NewValidationError("path", "invalid URL path format").Error(), http.StatusBadRequest)
		return
	}
	receiptID := pathParts[1]

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

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) != 3 || pathParts[0] != "receipts" || pathParts[2] != "users" {
		http.Error(w, NewValidationError("path", "invalid URL path format").Error(), http.StatusBadRequest)
		return
	}
	receiptID := pathParts[1]

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

	responseUsers := make([]map[string]interface{}, len(users))
	for i, u := range users {
		responseUsers[i] = map[string]interface{}{
			"id":         u.ID,
			"receipt_id": u.ReceiptID,
			"name":       u.Name,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{"users": responseUsers}); err != nil {
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

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) != 3 || pathParts[0] != "receipts" || pathParts[2] != "items" {
		http.Error(w, NewValidationError("path", "invalid URL path format").Error(), http.StatusBadRequest)
		return
	}
	receiptID := pathParts[1]

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

	responseItems := make([]ReceiptItem, len(items))
	for i, item := range items {
		totalPrice := item.TotalPrice
		pricePerItem := item.PricePerItem
		responseItems[i] = ReceiptItem{
			ID:           item.ID,
			Name:         item.Name,
			Quantity:     item.Quantity,
			TotalPrice:   &totalPrice,
			PricePerItem: &pricePerItem,
		}
	}

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

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) != 2 || pathParts[0] != "receipts" {
		http.Error(w, NewValidationError("path", "invalid URL path format").Error(), http.StatusBadRequest)
		return
	}
	receiptID := pathParts[1]

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

	// Build item ID -> total price map
	itemPrice := make(map[string]float64)
	for _, item := range items {
		itemPrice[item.ID] = item.TotalPrice
	}

	// Build item ID -> ordered list of (user_id, assignment) for equal split
	// Each user assigned to an item gets 1/n of the total, rounded to cents
	itemUserOrder := make(map[string][]string)
	for _, a := range assignments {
		itemUserOrder[a.ReceiptItemID] = append(itemUserOrder[a.ReceiptItemID], a.ReceiptUserID)
	}

	// Compute amount for each (user_id, item_id) pair
	amountByUserItem := make(map[string]float64)
	for itemID, userIDs := range itemUserOrder {
		totalPrice := itemPrice[itemID]
		n := len(userIDs)
		if n == 0 {
			continue
		}
		totalCents := int(math.Round(totalPrice * 100))
		baseCents := totalCents / n
		remainder := totalCents - baseCents*n
		for i, userID := range userIDs {
			cents := baseCents
			if i < remainder {
				cents++
			}
			key := userID + ":" + itemID
			amountByUserItem[key] = float64(cents) / 100
		}
	}

	// Build response - assignments provide the user-item correlation for bill split
	responseUsers := make([]map[string]interface{}, len(users))
	for i, u := range users {
		responseUsers[i] = map[string]interface{}{
			"id":         u.ID,
			"receipt_id": u.ReceiptID,
			"name":       u.Name,
		}
	}

	responseItems := make([]ReceiptItem, len(items))
	for i, item := range items {
		totalPrice := item.TotalPrice
		pricePerItem := item.PricePerItem
		responseItems[i] = ReceiptItem{
			ID:           item.ID,
			Name:         item.Name,
			Quantity:     item.Quantity,
			TotalPrice:   &totalPrice,
			PricePerItem: &pricePerItem,
		}
	}

	responseAssignments := make([]map[string]interface{}, len(assignments))
	for i, a := range assignments {
		key := a.ReceiptUserID + ":" + a.ReceiptItemID
		amount := amountByUserItem[key]
		responseAssignments[i] = map[string]interface{}{
			"id":             a.ID,
			"user_id":        a.ReceiptUserID,
			"item_id":        a.ReceiptItemID,
			"amount_paid":    amount,
		}
	}

	response := map[string]interface{}{
		"receipt_id":  receiptID,
		"users":       responseUsers,
		"items":       responseItems,
		"assignments": responseAssignments,
	}

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

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) != 5 || pathParts[0] != "receipts" || pathParts[2] != "users" || pathParts[4] != "items" {
		http.Error(w, NewValidationError("path", "invalid URL path format").Error(), http.StatusBadRequest)
		return
	}
	userID := pathParts[3]

	var req AssignItemsToUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, NewValidationError("body", fmt.Sprintf("failed to parse request body: %v", err)).Error(), http.StatusBadRequest)
		return
	}

	if len(req.ItemIDs) == 0 {
		http.Error(w, NewValidationError("item_ids", "at least one item_id is required").Error(), http.StatusBadRequest)
		return
	}

	assignedItems := make([]struct {
		ID            string `json:"id"`
		ReceiptUserID string `json:"receipt_user_id"`
		ReceiptItemID string `json:"receipt_item_id"`
	}, 0, len(req.ItemIDs))

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

		assignedItems = append(assignedItems, struct {
			ID            string `json:"id"`
			ReceiptUserID string `json:"receipt_user_id"`
			ReceiptItemID string `json:"receipt_item_id"`
		}{
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

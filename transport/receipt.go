package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"splitzies/persistence"
	"splitzies/storage"
)

// ReceiptItem represents a single item in a receipt
type ReceiptItem struct {
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

// convertReceiptItems converts request receipt items to persistence receipt items.
// It validates and calculates missing price fields.
// Returns the converted items and an error if validation fails.
func convertReceiptItems(items []ReceiptItem) ([]persistence.ReceiptItemDB, error) {
	itemsToSave := make([]persistence.ReceiptItemDB, 0, len(items))

	for i := range items {
		item := &items[i]

		// Validate name
		if item.Name == "" {
			return nil, fmt.Errorf("item %d: name is required", i+1)
		}

		// Validate quantity
		if item.Quantity <= 0 {
			return nil, fmt.Errorf("item %d: quantity must be greater than 0", i+1)
		}

		// Calculate missing fields
		if item.TotalPrice == nil && item.PricePerItem == nil {
			return nil, fmt.Errorf("item %d: either total_price or price_per_item must be provided", i+1)
		}

		var totalPrice, pricePerItem float64

		if item.TotalPrice == nil && item.PricePerItem != nil {
			// Calculate total price from price per item and quantity
			pricePerItem = *item.PricePerItem
			totalPrice = pricePerItem * float64(item.Quantity)
			item.TotalPrice = &totalPrice
		} else if item.PricePerItem == nil && item.TotalPrice != nil {
			// Calculate price per item from total price and quantity
			totalPrice = *item.TotalPrice
			pricePerItem = totalPrice / float64(item.Quantity)
			item.PricePerItem = &pricePerItem
		} else {
			// Both are provided
			totalPrice = *item.TotalPrice
			pricePerItem = *item.PricePerItem
		}

		// Add to items to save
		itemsToSave = append(itemsToSave, persistence.ReceiptItemDB{
			Name:         item.Name,
			Quantity:     item.Quantity,
			TotalPrice:   totalPrice,
			PricePerItem: pricePerItem,
		})
	}

	return itemsToSave, nil
}

func AddReceiptHandler(w http.ResponseWriter, r *http.Request) {
	// Only allow POST method
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req AddReceiptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Convert request items to persistence items
	itemsToSave, err := convertReceiptItems(req.Items)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Save receipt to database (no image for manual entry)
	savedReceipt, err := persistence.SaveReceipt(itemsToSave, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to save receipt: %v", err), http.StatusInternalServerError)
		return
	}

	// Return success response with processed items and receipt ID
	response := AddReceiptResponse{
		Message: fmt.Sprintf("Receipt added successfully with ID: %s", savedReceipt.ID),
		Items:   req.Items,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// UploadReceiptImageHandler handles receipt image uploads
// Expects multipart/form-data with:
//   - "image": the receipt image file
// Returns the uploaded image URL
func UploadReceiptImageHandler(w http.ResponseWriter, r *http.Request) {
	// Only allow POST method
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form (max 10MB)
	err := r.ParseMultipartForm(10 << 20) // 10MB
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse multipart form: %v", err), http.StatusBadRequest)
		return
	}

	// Get the image file from form
	file, header, err := r.FormFile("image")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get image file: %v", err), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Validate file size (max 10MB)
	if header.Size > 10<<20 {
		http.Error(w, "Image file too large (max 10MB)", http.StatusBadRequest)
		return
	}

	// Validate content type
	contentType := header.Header.Get("Content-Type")
	if contentType != "" {
		validTypes := map[string]bool{
			"image/jpeg": true,
			"image/jpg":  true,
			"image/png":  true,
			"image/gif":  true,
			"image/webp": true,
		}
		if !validTypes[contentType] {
			http.Error(w, fmt.Sprintf("Invalid image type: %s. Supported types: jpeg, jpg, png, gif, webp", contentType), http.StatusBadRequest)
			return
		}
	}

	// Generate receipt ID first (we'll create a receipt record with just the image)
	ctx := context.Background()
	receiptID := persistence.GenerateReceiptID()

	// Upload image to GCS
	imageURL, err := storage.UploadReceiptImageFromReader(ctx, file, receiptID, contentType)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to upload image: %v", err), http.StatusInternalServerError)
		return
	}

	// Create a receipt record with the image URL (no items yet)
	// We'll save an empty receipt with just the image
	itemsToSave := []persistence.ReceiptItemDB{}
	savedReceipt, err := persistence.SaveReceipt(itemsToSave, &imageURL)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to save receipt: %v", err), http.StatusInternalServerError)
		return
	}

	// Return success response
	response := map[string]interface{}{
		"message":   fmt.Sprintf("Receipt image uploaded successfully with ID: %s", savedReceipt.ID),
		"receipt_id": savedReceipt.ID,
		"image_url": imageURL,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

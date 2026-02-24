package transport

import "splitzies/money"

// defaultUSD is used when GetReceiptCurrency fails or returns nil
var defaultUSD = "USD"

// ReceiptItem represents a single item in a receipt
type ReceiptItem struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Quantity     int           `json:"quantity"`
	TotalPrice   *money.Amount `json:"total_price,omitempty"`    // Optional, can be calculated
	PricePerItem *money.Amount `json:"price_per_item,omitempty"` // Optional, can be calculated
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

// UploadReceiptResponse represents the response for receipt image upload
type UploadReceiptResponse struct {
	ReceiptID string        `json:"receipt_id"`
	ImageURL  string        `json:"image_url"`
	Items     []ReceiptItem `json:"items"`
	OCRText   *string       `json:"ocr_text,omitempty"`
	Tax       *money.Amount `json:"tax,omitempty"`
	Tip       *money.Amount `json:"tip,omitempty"`
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

// GetReceiptUserResponse represents a user in the get receipt response
type GetReceiptUserResponse struct {
	ID        string        `json:"id"`
	ReceiptID string        `json:"receipt_id"`
	Name      string        `json:"name"`
	UserTotal *money.Amount `json:"user_total,omitempty"`
}

// GetReceiptUsersResponse represents the response for GET receipt users
type GetReceiptUsersResponse struct {
	Users []GetReceiptUserResponse `json:"users"`
}

// GetReceiptAssignmentResponse represents an assignment in the get receipt response
type GetReceiptAssignmentResponse struct {
	ID         string       `json:"id"`
	UserID     string       `json:"user_id"`
	ItemID     string       `json:"item_id"`
	AmountOwed money.Amount `json:"amount_owed"`
}

// GetReceiptResponse represents the full get receipt response
type GetReceiptResponse struct {
	ReceiptID   string                         `json:"receipt_id"`
	Users       []GetReceiptUserResponse       `json:"users"`
	Items       []ReceiptItem                  `json:"items"`
	Assignments []GetReceiptAssignmentResponse `json:"assignments"`
}

// AssignItemsToUserRequest represents the request body for assigning items to a user
type AssignItemsToUserRequest struct {
	ItemIDs []string `json:"item_ids"`
}

// AssignItemsToUserItem represents an assigned item in the response
type AssignItemsToUserItem struct {
	ID            string `json:"id"`
	ReceiptUserID string `json:"receipt_user_id"`
	ReceiptItemID string `json:"receipt_item_id"`
}

// AssignItemsToUserResponse represents the response after assigning items to a user
type AssignItemsToUserResponse struct {
	Message string                 `json:"message"`
	Items   []AssignItemsToUserItem `json:"items"`
}

// PatchReceiptRequest represents the request body for updating receipt tax/tip
type PatchReceiptRequest struct {
	Tax *float64 `json:"tax"`
	Tip *float64 `json:"tip"`
}

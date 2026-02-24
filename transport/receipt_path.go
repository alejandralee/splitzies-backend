package transport

import "strings"

// pathParts returns the URL path split by "/" with leading/trailing slashes trimmed
func pathParts(path string) []string {
	return strings.Split(strings.Trim(path, "/"), "/")
}

// parseReceiptIDPath expects path like /receipts/{receipt_id}
// Returns receiptID and true if valid, empty string and false otherwise
func parseReceiptIDPath(path string) (receiptID string, ok bool) {
	parts := pathParts(path)
	if len(parts) != 2 || parts[0] != "receipts" {
		return "", false
	}
	return parts[1], true
}

// parseReceiptUsersPath expects path like /receipts/{receipt_id}/users
// Returns receiptID and true if valid
func parseReceiptUsersPath(path string) (receiptID string, ok bool) {
	parts := pathParts(path)
	if len(parts) != 3 || parts[0] != "receipts" || parts[2] != "users" {
		return "", false
	}
	return parts[1], true
}

// parseReceiptItemsPath expects path like /receipts/{receipt_id}/items
// Returns receiptID and true if valid
func parseReceiptItemsPath(path string) (receiptID string, ok bool) {
	parts := pathParts(path)
	if len(parts) != 3 || parts[0] != "receipts" || parts[2] != "items" {
		return "", false
	}
	return parts[1], true
}

// parseReceiptUserItemsPath expects path like /receipts/{receipt_id}/users/{user_id}/items
// Returns userID and true if valid
func parseReceiptUserItemsPath(path string) (userID string, ok bool) {
	parts := pathParts(path)
	if len(parts) != 5 || parts[0] != "receipts" || parts[2] != "users" || parts[4] != "items" {
		return "", false
	}
	return parts[3], true
}

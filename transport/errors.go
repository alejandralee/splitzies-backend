package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// errorResponse is the JSON body returned for all error responses.
type errorResponse struct {
	Error     string `json:"error"`
	Code      string `json:"code,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// writeJSONError writes a JSON error response with the given HTTP status code.
func writeJSONError(w http.ResponseWriter, statusCode int, code, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(errorResponse{
		Error:     message,
		Code:      code,
		RequestID: requestID,
	})
}

// ValidationError represents an invalid field in a request.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("field %q: %s", e.Field, e.Message)
}

func newValidationError(field, message string) *ValidationError {
	return &ValidationError{Field: field, Message: message}
}

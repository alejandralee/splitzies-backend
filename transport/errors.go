package transport

import "fmt"

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("Validation error on field '%s': %s", e.Field, e.Message)
}

func NewValidationError(field, message string) *ValidationError {
	return &ValidationError{
		Field:   field,
		Message: message,
	}
}

type InvalidMethodError struct {
	Method string `json:"method"`
}

func (e *InvalidMethodError) Error() string {
	return fmt.Sprintf("Invalid method: %s", e.Method)
}

func NewInvalidMethodError(method string) *InvalidMethodError {
	return &InvalidMethodError{
		Method: method,
	}
}

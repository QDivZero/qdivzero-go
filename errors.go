package qdivzero

import (
	"encoding/json"
	"fmt"
)

// ParseErrorResponse decodes an ErrorResponse from a raw response body, e.g.
// the Body field of a generated response type.
func ParseErrorResponse(body []byte) (*ErrorResponse, error) {
	var r ErrorResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("qdivzero: decode error response: %w", err)
	}
	return &r, nil
}

// ErrorString returns the message of an ErrorResponse, or "" when the response
// is nil or carries no message.
func ErrorString(r *ErrorResponse) string {
	if r == nil || r.Error == nil {
		return ""
	}
	return *r.Error
}

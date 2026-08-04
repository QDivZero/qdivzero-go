package qdivzero

import "testing"

func TestParseErrorResponse(t *testing.T) {
	r, err := ParseErrorResponse([]byte(`{"error":"boom"}`))
	if err != nil {
		t.Fatalf("ParseErrorResponse: %v", err)
	}
	if got := ErrorString(r); got != "boom" {
		t.Fatalf("ErrorString = %q, want %q", got, "boom")
	}
}

func TestParseErrorResponseInvalidJSON(t *testing.T) {
	if _, err := ParseErrorResponse([]byte(`not json`)); err == nil {
		t.Fatal("ParseErrorResponse: expected error for invalid JSON")
	}
}

func TestErrorStringNilSafe(t *testing.T) {
	if got := ErrorString(nil); got != "" {
		t.Fatalf("ErrorString(nil) = %q, want empty", got)
	}
	if got := ErrorString(&ErrorResponse{}); got != "" {
		t.Fatalf("ErrorString(empty) = %q, want empty", got)
	}
}

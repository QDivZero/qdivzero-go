package qdivzero

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func writeCredentials(t *testing.T, content string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".qdivzero")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialsFileTokens(t *testing.T) {
	writeCredentials(t, `{"access_token":"file-access","refresh_token":"file-refresh"}`)
	api, err := NewAPI(WithServerURL("http://example.invalid"))
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	if got := api.AccessToken(); got != "file-access" {
		t.Fatalf("AccessToken = %q, want %q", got, "file-access")
	}
}

func TestCredentialsFileLoginOnDemand(t *testing.T) {
	var loginCalls int32
	validToken := "file-login-token"
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&loginCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(LoginResponse{AccessToken: strPtr(validToken), RefreshToken: strPtr("file-refresh")})
	})
	mux.HandleFunc("/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+validToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	writeCredentials(t, `{"email":"file@example.com","password":"secret"}`)
	api, err := NewAPI(WithServerURL(srv.URL))
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	resp, err := api.GetAuthMeWithResponse(context.Background())
	if err != nil {
		t.Fatalf("GetAuthMeWithResponse: %v", err)
	}
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	if got := atomic.LoadInt32(&loginCalls); got != 1 {
		t.Fatalf("login calls = %d, want 1", got)
	}
}

func TestCredentialsFileMissingIsNoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	api, err := NewAPI(WithServerURL("http://example.invalid"))
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	if got := api.AccessToken(); got != "" {
		t.Fatalf("AccessToken = %q, want empty", got)
	}
}

func TestCredentialsFileMalformedFails(t *testing.T) {
	writeCredentials(t, `not json`)
	if _, err := NewAPI(WithServerURL("http://example.invalid")); err == nil {
		t.Fatal("NewAPI: expected error for malformed credentials file")
	}
}

func TestOptionsOverrideCredentialsFile(t *testing.T) {
	writeCredentials(t, `{"access_token":"file-access"}`)
	api, err := NewAPI(WithServerURL("http://example.invalid"), WithAccessToken("option-access"))
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	if got := api.AccessToken(); got != "option-access" {
		t.Fatalf("AccessToken = %q, want %q", got, "option-access")
	}
}

func TestCredentialsFileAPIKey(t *testing.T) {
	writeCredentials(t, `{"api_key":"tk_abc"}`)
	api, err := NewAPI(WithServerURL("http://example.invalid"))
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	if got := api.APIKey(); got != "tk_abc" {
		t.Fatalf("APIKey = %q, want tk_abc", got)
	}
}

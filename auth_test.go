package qdivzero

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func strPtr(s string) *string { return &s }

func TestLoginAndAutoRefresh(t *testing.T) {
	var refreshCalls int32
	validToken := "refreshed-token"
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(LoginResponse{
			AccessToken:  strPtr("first-token"),
			RefreshToken: strPtr("refresh-1"),
		})
	})
	mux.HandleFunc("/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RefreshResponse{AccessToken: strPtr(validToken)})
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

	api, err := NewAPI(WithServerURL(srv.URL))
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	if err := api.Login(context.Background(), "user@example.com", "secret"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := api.AccessToken(); got != "first-token" {
		t.Fatalf("AccessToken after login = %q, want %q", got, "first-token")
	}

	resp, err := api.GetAuthMeWithResponse(context.Background())
	if err != nil {
		t.Fatalf("GetAuthMeWithResponse: %v", err)
	}
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
}

func TestConcurrent401RefreshesOnlyOnce(t *testing.T) {
	var refreshCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RefreshResponse{AccessToken: strPtr("new-token")})
	})
	mux.HandleFunc("/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	api, err := NewAPI(WithServerURL(srv.URL), WithAccessToken("stale-token"), WithRefreshToken("refresh-1"))
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := api.GetAuthMeWithResponse(context.Background())
			if err != nil {
				t.Errorf("GetAuthMeWithResponse: %v", err)
				return
			}
			if resp.StatusCode() != http.StatusOK {
				t.Errorf("status = %d, want 200", resp.StatusCode())
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
}

func TestLoginFailureDoesNotTriggerRefresh(t *testing.T) {
	var refreshCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(ErrorResponse{Error: strPtr("bad credentials")})
	})
	mux.HandleFunc("/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	api, err := NewAPI(WithServerURL(srv.URL))
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	if err := api.Login(context.Background(), "user@example.com", "wrong"); err == nil {
		t.Fatal("Login: expected error for 401 response")
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 0 {
		t.Fatalf("refresh calls = %d, want 0", got)
	}
}

func TestAPIKeyAndRawAuthorization(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/me", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Auth-Echo", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	api, err := NewAPI(WithServerURL(srv.URL), WithAPIKey("k-123"))
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	resp, err := api.GetAuthMeWithResponse(context.Background())
	if err != nil {
		t.Fatalf("GetAuthMeWithResponse: %v", err)
	}
	if got := resp.HTTPResponse.Header.Get("X-Auth-Echo"); got != "Bearer k-123" {
		t.Fatalf("Authorization = %q, want %q", got, "Bearer k-123")
	}

	api.SetAuthorization("ApiKey k-999")
	resp, err = api.GetAuthMeWithResponse(context.Background())
	if err != nil {
		t.Fatalf("GetAuthMeWithResponse: %v", err)
	}
	if got := resp.HTTPResponse.Header.Get("X-Auth-Echo"); got != "ApiKey k-999" {
		t.Fatalf("Authorization = %q, want %q", got, "ApiKey k-999")
	}
}

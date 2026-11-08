package qdivzero

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func strPtr(s string) *string { return &s }

// noCredentialsFile isolates a test from the real ~/.qdivzero/credentials.
func noCredentialsFile(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func setSessionCookies(w http.ResponseWriter, access, refresh string) {
	if access != "" {
		http.SetCookie(w, &http.Cookie{Name: "qdiv0_access", Value: access, Path: "/", HttpOnly: true})
	}
	if refresh != "" {
		http.SetCookie(w, &http.Cookie{Name: "qdiv0_refresh", Value: refresh, Path: "/v1/auth/refresh", HttpOnly: true})
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{}`)
}

func hasCookie(r *http.Request, name, value string) bool {
	cookie, err := r.Cookie(name)
	return err == nil && cookie.Value == value
}

func TestAccessTokenUsesSessionCookie(t *testing.T) {
	noCredentialsFile(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want no bearer session token", got)
		}
		if !hasCookie(r, "qdiv0_access", "session-token") {
			t.Error("missing access session cookie")
		}
		if _, err := r.Cookie("qdiv0_refresh"); err == nil {
			t.Error("refresh cookie was sent to a non-refresh endpoint")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	api, err := NewAPI(WithServerURL(srv.URL), WithAccessToken("session-token"), WithRefreshToken("refresh-token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.GetAuthMeWithResponse(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLoginAndAutoRefresh(t *testing.T) {
	noCredentialsFile(t)
	var refreshCalls int32
	validToken := "refreshed-token"
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		setSessionCookies(w, "first-token", "refresh-1")
	})
	mux.HandleFunc("/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt32(&refreshCalls, 1)
		if r.ContentLength != 0 {
			t.Errorf("refresh request has a body of length %d", r.ContentLength)
		}
		if call == 1 {
			if !hasCookie(r, "qdiv0_refresh", "refresh-1") {
				t.Error("first refresh did not receive the login refresh cookie")
			}
			setSessionCookies(w, validToken, "refresh-2")
			return
		}
		if !hasCookie(r, "qdiv0_refresh", "refresh-2") {
			t.Error("second refresh did not receive the rotated refresh cookie")
		}
		setSessionCookies(w, "refreshed-again", "refresh-3")
	})
	mux.HandleFunc("/v1/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("session token must not be sent as a bearer credential")
		}
		if !hasCookie(r, "qdiv0_access", validToken) {
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
	if err := api.Refresh(context.Background()); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	if got := api.AccessToken(); got != "refreshed-again" {
		t.Fatalf("AccessToken after second refresh = %q, want refreshed-again", got)
	}
}

func TestConcurrent401RefreshesOnlyOnce(t *testing.T) {
	noCredentialsFile(t)
	var refreshCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
		time.Sleep(50 * time.Millisecond)
		if !hasCookie(r, "qdiv0_refresh", "refresh-1") {
			t.Error("missing refresh cookie")
		}
		setSessionCookies(w, "new-token", "refresh-2")
	})
	mux.HandleFunc("/v1/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if !hasCookie(r, "qdiv0_access", "new-token") {
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

func TestLateUnauthorizedResponseReusesRefreshedSession(t *testing.T) {
	noCredentialsFile(t)
	var staleRequests, refreshCalls int32
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseSecond := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if hasCookie(r, "qdiv0_access", "new-token") {
			w.WriteHeader(http.StatusOK)
			return
		}
		switch atomic.AddInt32(&staleRequests, 1) {
		case 1:
			close(firstStarted)
			<-secondStarted
		case 2:
			close(secondStarted)
			<-releaseSecond
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
		setSessionCookies(w, "new-token", "rotated-refresh")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	api, err := NewAPI(WithServerURL(srv.URL), WithAccessToken("stale-token"), WithRefreshToken("refresh-1"))
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	request := func() error {
		resp, err := api.GetAuthMeWithResponse(context.Background())
		if err != nil {
			return err
		}
		if resp.StatusCode() != http.StatusOK {
			return fmt.Errorf("status = %d, want 200", resp.StatusCode())
		}
		return nil
	}
	go func() {
		err := request()
		close(releaseSecond)
		results <- err
	}()
	<-firstStarted
	go func() { results <- request() }()
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
}

func TestLoginFailureDoesNotTriggerRefresh(t *testing.T) {
	noCredentialsFile(t)
	var refreshCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(ErrorResponse{Error: strPtr("bad credentials")})
	})
	mux.HandleFunc("/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
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

func TestAutoRefreshRetriesWithBody(t *testing.T) {
	noCredentialsFile(t)
	var refreshCalls int32
	validToken := "refreshed-token"
	var receivedBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
		setSessionCookies(w, validToken, "refresh-2")
	})
	mux.HandleFunc("/v1/accounts", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		receivedBody = string(body)
		if !hasCookie(r, "custom", "preserved") {
			t.Error("unrelated cookie was not preserved")
		}
		if !hasCookie(r, "qdiv0_access", validToken) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	api, err := NewAPI(WithServerURL(srv.URL), WithAccessToken("stale-token"), WithRefreshToken("refresh-1"))
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	payload := `{"name":"acme"}`
	resp, err := api.PostAccountsWithBody(context.Background(), "application/json", strings.NewReader(payload), func(_ context.Context, req *http.Request) error {
		req.AddCookie(&http.Cookie{Name: "custom", Value: "preserved"})
		return nil
	})
	if err != nil {
		t.Fatalf("PostAccountsWithBody: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	if receivedBody != payload {
		t.Fatalf("server received body %q, want %q", receivedBody, payload)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
}

func TestAPIKeyAndRawAuthorization(t *testing.T) {
	noCredentialsFile(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/me", func(w http.ResponseWriter, r *http.Request) {
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

func TestWithHeaderSentOnEveryRequest(t *testing.T) {
	noCredentialsFile(t)
	var got string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/me", func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Private-Beta-Token")
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	api, err := NewAPI(WithServerURL(srv.URL), WithAccessToken("t"), WithHeader("X-Private-Beta-Token", "beta-1"))
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	if _, err := api.GetAuthMeWithResponse(context.Background()); err != nil {
		t.Fatalf("GetAuthMeWithResponse: %v", err)
	}
	if got != "beta-1" {
		t.Fatalf("X-Private-Beta-Token = %q, want %q", got, "beta-1")
	}
}

func TestWithHeaderIsSentOnLoginAndRefresh(t *testing.T) {
	noCredentialsFile(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Header.Get("X-Private-Beta-Token") != "beta-1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		setSessionCookies(w, "access", "refresh")
	}))
	defer srv.Close()
	api, err := NewAPI(WithServerURL(srv.URL), WithHeader("X-Private-Beta-Token", "beta-1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := api.Login(context.Background(), "user@example.com", "secret"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := api.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("auth requests = %d, want 2", got)
	}
}

func TestLoginRequiresCompletedSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "verification required", body: `{"next_step":"verify_email"}`, want: "verify_email"},
		{name: "missing cookies", body: `{}`, want: "access"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			noCredentialsFile(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			api, err := NewAPI(WithServerURL(srv.URL))
			if err != nil {
				t.Fatal(err)
			}
			if err := api.Login(context.Background(), "user@example.com", "secret"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Login error = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestLoginUsesNewCredentialsDespiteExistingSession(t *testing.T) {
	noCredentialsFile(t)
	var loginCalls, refreshCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&loginCalls, 1)
		var body LoginRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email != "new@example.com" || body.Password != "new-password" {
			t.Errorf("unexpected login credentials or decode error: email=%q error=%v", body.Email, err)
		}
		setSessionCookies(w, "new-access", "new-refresh")
	})
	mux.HandleFunc("/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
		setSessionCookies(w, "old-session-refreshed", "old-refresh-rotated")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	api, err := NewAPI(WithServerURL(srv.URL), WithAccessToken("old-access"), WithRefreshToken("old-refresh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := api.Login(context.Background(), "new@example.com", "new-password"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := api.AccessToken(); got != "new-access" {
		t.Fatalf("AccessToken = %q, want new-access", got)
	}
	if loginCalls != 1 || refreshCalls != 0 {
		t.Fatalf("login calls = %d, refresh calls = %d; want 1 and 0", loginCalls, refreshCalls)
	}
}

func TestAuthenticationEndpointsDoNotTriggerRefresh(t *testing.T) {
	noCredentialsFile(t)
	var refreshCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
		if !hasCookie(r, "qdiv0_refresh", "refresh-token") {
			t.Error("explicit refresh request is missing the session refresh cookie")
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	api, err := NewAPI(WithServerURL(srv.URL), WithRefreshToken("refresh-token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.PostAuthLoginWithResponse(context.Background(), LoginRequest{Email: "bad", Password: "bad"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.PostAuthRefreshWithResponse(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh calls = %d, want only the explicit refresh", got)
	}
}

func TestGeneratedRefreshStoresRotatedSession(t *testing.T) {
	noCredentialsFile(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt32(&calls, 1)
		if call == 1 {
			if !hasCookie(r, "qdiv0_refresh", "refresh-1") {
				t.Error("generated refresh did not receive the initial cookie")
			}
			setSessionCookies(w, "access-2", "refresh-2")
			return
		}
		if !hasCookie(r, "qdiv0_refresh", "refresh-2") {
			t.Error("subsequent refresh did not receive the rotated cookie")
		}
		setSessionCookies(w, "access-3", "refresh-3")
	}))
	defer srv.Close()
	api, err := NewAPI(WithServerURL(srv.URL), WithAccessToken("access-1"), WithRefreshToken("refresh-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.PostAuthRefreshWithResponse(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := api.AccessToken(); got != "access-2" {
		t.Fatalf("AccessToken after generated refresh = %q, want access-2", got)
	}
	if err := api.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
}

func TestRefreshOverridesStaleCustomJarCookies(t *testing.T) {
	noCredentialsFile(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		setSessionCookies(w, "old-access", "old-refresh")
	})
	mux.HandleFunc("/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		if !hasCookie(r, "qdiv0_refresh", "old-refresh") {
			t.Error("refresh request did not receive the original cookie")
		}
		setSessionCookies(w, "new-access", "new-refresh")
	})
	mux.HandleFunc("/v1/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if !hasCookie(r, "qdiv0_access", "new-access") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	api, err := NewAPI(WithServerURL(srv.URL), WithCustomHTTPClient(&http.Client{Jar: jar}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.PostAuthLoginWithResponse(context.Background(), LoginRequest{Email: "user", Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := api.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	resp, err := api.GetAuthMeWithResponse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200 with the rotated access cookie", resp.StatusCode())
	}
}

func TestRedirectDoesNotForwardManagedCredentialsToAnotherOrigin(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []Option
	}{
		{name: "session", opts: []Option{WithAccessToken("session-token"), WithRefreshToken("refresh-token")}},
		{name: "api key", opts: []Option{WithAPIKey("tk_secret")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			noCredentialsFile(t)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, name := range []string{"Authorization", "Cookie", "X-Private-Beta-Token"} {
					if got := r.Header.Get(name); got != "" {
						t.Errorf("redirected request contains %s", name)
					}
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer target.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+"/v1/auth/refresh", http.StatusFound)
			}))
			defer source.Close()
			opts := append(tc.opts, WithServerURL(source.URL), WithHeader("X-Private-Beta-Token", "beta-secret"))
			api, err := NewAPI(opts...)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := api.GetAuthMeWithResponse(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSessionAuthenticationDoesNotFollowCrossOriginRedirects(t *testing.T) {
	for _, tc := range []struct {
		name    string
		refresh bool
	}{
		{name: "login"},
		{name: "refresh", refresh: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			noCredentialsFile(t)
			var targetCalls int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&targetCalls, 1)
				setSessionCookies(w, "access", "refresh")
			}))
			defer target.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
			}))
			defer source.Close()
			api, err := NewAPI(WithServerURL(source.URL), WithRefreshToken("secret-refresh"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.refresh {
				err = api.Refresh(context.Background())
			} else {
				err = api.Login(context.Background(), "user@example.com", "secret-password")
			}
			if err == nil {
				t.Error("expected cross-origin redirect to stop authentication")
			}
			if got := atomic.LoadInt32(&targetCalls); got != 0 {
				t.Fatalf("cross-origin target received %d authentication requests", got)
			}
		})
	}
}

func TestRefreshRejectsMissingAccessCookie(t *testing.T) {
	noCredentialsFile(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSessionCookies(w, "", "rotated-refresh")
	}))
	defer srv.Close()
	api, err := NewAPI(WithServerURL(srv.URL), WithAccessToken("old-access"), WithRefreshToken("old-refresh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := api.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh: expected an error for a missing access cookie")
	}
	if got := api.AccessToken(); got != "old-access" {
		t.Fatalf("AccessToken = %q, want old-access", got)
	}
}

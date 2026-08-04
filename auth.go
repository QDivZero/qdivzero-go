package qdivzero

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
)

// authManager holds the authentication state for an API client.
type authManager struct {
	gen          *ClientWithResponses
	mu           sync.Mutex
	accessToken  string
	refreshToken string
	apiKey       string
	rawAuth      string
	email        string
	password     string
	refreshing   bool
	refreshDone  chan struct{}
	refreshErr   error
}

// applyAuth sets the Authorization header unless one is already present.
// Precedence: raw value, access token, API key.
func (m *authManager) applyAuth(req *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if req.Header.Get("Authorization") != "" {
		return
	}
	switch {
	case m.rawAuth != "":
		req.Header.Set("Authorization", m.rawAuth)
	case m.accessToken != "":
		req.Header.Set("Authorization", "Bearer "+m.accessToken)
	case m.apiKey != "":
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
}

// canRefresh reports whether a 401 on the given path may trigger a refresh.
// Login and refresh endpoints are excluded to avoid recursion.
func (m *authManager) canRefresh(path string) bool {
	if path == "/auth/login" || path == "/auth/refresh" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refreshToken != "" || (m.email != "" && m.password != "")
}

// refreshNow refreshes the access token, coalescing concurrent attempts so
// only one refresh request is issued at a time.
func (m *authManager) refreshNow(ctx context.Context) error {
	m.mu.Lock()
	if m.refreshing {
		done := m.refreshDone
		m.mu.Unlock()
		select {
		case <-done:
			return m.refreshErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.refreshing = true
	m.refreshErr = nil
	m.refreshDone = make(chan struct{})
	m.mu.Unlock()

	err := m.doRefresh(ctx)

	m.mu.Lock()
	m.refreshErr = err
	m.refreshing = false
	close(m.refreshDone)
	m.mu.Unlock()
	return err
}

// doRefresh performs the actual token refresh or credential login using a
// plain client that bypasses the auth transport.
func (m *authManager) doRefresh(ctx context.Context) error {
	m.mu.Lock()
	refreshToken := m.refreshToken
	email, password := m.email, m.password
	m.mu.Unlock()

	if refreshToken != "" {
		body := RefreshRequest{RefreshToken: refreshToken}
		resp, err := m.gen.PostAuthRefreshWithResponse(ctx, body)
		if err != nil {
			return fmt.Errorf("refresh token: %w", err)
		}
		if resp.JSON200 == nil {
			return fmt.Errorf("refresh token: unexpected status %d", resp.StatusCode())
		}
		access := resp.JSON200.AccessToken
		if access == nil || *access == "" {
			return errors.New("refresh token: empty access token in response")
		}
		m.mu.Lock()
		m.accessToken = *access
		m.mu.Unlock()
		return nil
	}

	if email != "" {
		body := LoginRequest{Email: email, Password: password}
		resp, err := m.gen.PostAuthLoginWithResponse(ctx, body)
		if err != nil {
			return fmt.Errorf("login: %w", err)
		}
		if resp.JSON200 == nil {
			return fmt.Errorf("login: unexpected status %d", resp.StatusCode())
		}
		access := resp.JSON200.AccessToken
		if access == nil || *access == "" {
			return errors.New("login: empty access token in response")
		}
		m.mu.Lock()
		m.accessToken = *access
		if resp.JSON200.RefreshToken != nil {
			m.refreshToken = *resp.JSON200.RefreshToken
		}
		m.mu.Unlock()
		return nil
	}

	return errors.New("no refresh token or credentials available")
}

// authTransport injects authentication headers and transparently refreshes
// expired access tokens on 401 responses, retrying the request once.
type authTransport struct {
	base http.RoundTripper
	auth *authManager
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.auth.applyAuth(req)
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && t.auth.canRefresh(req.URL.Path) {
		if refreshErr := t.auth.refreshNow(req.Context()); refreshErr == nil {
			resp.Body.Close()
			retried := req.Clone(req.Context())
			retried.Header.Del("Authorization")
			t.auth.applyAuth(retried)
			return t.base.RoundTrip(retried)
		}
	}
	return resp, nil
}

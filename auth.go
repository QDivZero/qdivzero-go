package qdivzero

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const (
	accessCookieName  = "qdiv0_access"
	refreshCookieName = "qdiv0_refresh"
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
	headers      map[string]string
	activeAuth   *authAttempt
}

type authAttempt struct {
	done chan struct{}
	err  error
}

// applyAuth sets the Authorization header or access session cookie unless an
// Authorization header is already present.
// Precedence: raw value, access session, API key. Extra headers configured via
// WithHeader are applied unless already present on the request.
func (m *authManager) applyAuth(req *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if req.Header.Get("Authorization") == "" {
		switch {
		case m.rawAuth != "":
			req.Header.Set("Authorization", m.rawAuth)
		case m.accessToken != "":
			setRequestCookie(req, accessCookieName, m.accessToken)
		case m.apiKey != "":
			req.Header.Set("Authorization", "Bearer "+m.apiKey)
		}
	}
	if strings.HasSuffix(req.URL.Path, "/auth/refresh") && m.refreshToken != "" {
		setRequestCookie(req, refreshCookieName, m.refreshToken)
	}
	for name, value := range m.headers {
		if req.Header.Get(name) == "" {
			req.Header.Set(name, value)
		}
	}
}

func setRequestCookie(req *http.Request, name, value string) {
	cookies := req.Cookies()
	req.Header.Del("Cookie")
	for _, cookie := range cookies {
		if cookie.Name != name {
			req.AddCookie(cookie)
		}
	}
	req.AddCookie(&http.Cookie{Name: name, Value: value})
}

// canRefresh reports whether a 401 on the given path may trigger a refresh.
// Login and refresh endpoints are excluded to avoid recursion.
func (m *authManager) canRefresh(path string) bool {
	if strings.HasSuffix(path, "/auth/login") || strings.HasSuffix(path, "/auth/refresh") {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refreshToken != "" || (m.email != "" && m.password != "")
}

// refreshNow refreshes the access token, coalescing concurrent attempts so
// only one refresh request is issued at a time.
func (m *authManager) refreshNow(ctx context.Context) error {
	return m.runAuth(ctx, nil, nil)
}

func (m *authManager) refreshAfterUnauthorized(ctx context.Context, sentAccess string) error {
	return m.runAuth(ctx, nil, &sentAccess)
}

// loginNow waits for any ongoing refresh, then starts a login using the supplied
// credentials even if there is already a refreshable session.
func (m *authManager) loginNow(ctx context.Context, email, password string) error {
	return m.runAuth(ctx, func() error {
		m.mu.Lock()
		m.email, m.password = email, password
		m.mu.Unlock()
		return m.doLogin(ctx, email, password)
	}, nil)
}

func (m *authManager) runAuth(ctx context.Context, login func() error, sentAccess *string) error {
	for {
		m.mu.Lock()
		if sentAccess != nil && m.accessToken != "" && m.accessToken != *sentAccess {
			m.mu.Unlock()
			return nil
		}
		if active := m.activeAuth; active != nil {
			m.mu.Unlock()
			select {
			case <-active.done:
				if login != nil {
					continue
				}
				return active.err
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		attempt := &authAttempt{done: make(chan struct{})}
		m.activeAuth = attempt
		m.mu.Unlock()

		if login != nil {
			attempt.err = login()
		} else {
			attempt.err = m.doRefresh(ctx)
		}

		m.mu.Lock()
		m.activeAuth = nil
		close(attempt.done)
		m.mu.Unlock()
		return attempt.err
	}
}

// doRefresh performs the actual token refresh or credential login using a
// plain client that bypasses the auth transport.
func (m *authManager) doRefresh(ctx context.Context) error {
	m.mu.Lock()
	refreshToken := m.refreshToken
	email, password := m.email, m.password
	m.mu.Unlock()

	if refreshToken != "" {
		resp, err := m.gen.PostAuthRefreshWithResponse(ctx, func(_ context.Context, req *http.Request) error {
			req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: refreshToken})
			m.applyExtraHeaders(req)
			return nil
		})
		if err != nil {
			return fmt.Errorf("refresh token: %w", err)
		}
		if resp.StatusCode() != http.StatusOK {
			return fmt.Errorf("refresh token: unexpected status %d", resp.StatusCode())
		}
		if err := m.storeSession(resp.HTTPResponse); err != nil {
			return fmt.Errorf("refresh token: %w", err)
		}
		return nil
	}

	if email != "" {
		return m.doLogin(ctx, email, password)
	}

	return errors.New("no refresh token or credentials available")
}

func (m *authManager) doLogin(ctx context.Context, email, password string) error {
	body := LoginRequest{Email: email, Password: password}
	resp, err := m.gen.PostAuthLoginWithResponse(ctx, body, func(_ context.Context, req *http.Request) error {
		m.applyExtraHeaders(req)
		return nil
	})
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("login: unexpected status %d", resp.StatusCode())
	}
	if err := m.storeSession(resp.HTTPResponse); err != nil {
		if resp.JSON200 != nil && resp.JSON200.NextStep != nil && *resp.JSON200.NextStep != "" {
			return fmt.Errorf("login: verification required: %s", *resp.JSON200.NextStep)
		}
		return fmt.Errorf("login: %w", err)
	}
	return nil
}

// storeSession updates both tokens only after a complete session is received.
func (m *authManager) storeSession(resp *http.Response) error {
	var access, refresh string
	for _, cookie := range resp.Cookies() {
		if cookie.MaxAge < 0 {
			continue
		}
		switch cookie.Name {
		case accessCookieName:
			access = cookie.Value
		case refreshCookieName:
			refresh = cookie.Value
		}
	}
	if access == "" {
		return errors.New("missing access session cookie")
	}
	if refresh == "" {
		return errors.New("missing refresh session cookie")
	}
	m.mu.Lock()
	m.accessToken = access
	m.refreshToken = refresh
	m.mu.Unlock()
	return nil
}

func (m *authManager) applyExtraHeaders(req *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, value := range m.headers {
		if req.Header.Get(name) == "" {
			req.Header.Set(name, value)
		}
	}
}

// authTransport injects authentication and transparently refreshes
// expired access tokens on 401 responses, retrying the request once.
type authTransport struct {
	base   http.RoundTripper
	auth   *authManager
	origin *url.URL
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !sameOrigin(req.URL, t.origin) {
		return t.base.RoundTrip(req)
	}
	original := req
	req = req.Clone(req.Context())
	t.auth.applyAuth(req)
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK && (strings.HasSuffix(req.URL.Path, "/auth/login") || strings.HasSuffix(req.URL.Path, "/auth/refresh")) {
		// Generated auth methods also rotate the session; incomplete responses
		// leave the current session unchanged.
		_ = t.auth.storeSession(resp)
	}
	if resp.StatusCode == http.StatusUnauthorized && t.auth.canRefresh(req.URL.Path) {
		sentAccess := ""
		if cookie, err := req.Cookie(accessCookieName); err == nil {
			sentAccess = cookie.Value
		}
		if refreshErr := t.auth.refreshAfterUnauthorized(req.Context(), sentAccess); refreshErr == nil {
			resp.Body.Close()
			retried := original.Clone(original.Context())
			retried.Header.Del("Authorization")
			cookies := retried.Cookies()
			retried.Header.Del("Cookie")
			for _, cookie := range cookies {
				if cookie.Name != accessCookieName {
					retried.AddCookie(cookie)
				}
			}
			if retried.GetBody != nil {
				if body, err := retried.GetBody(); err == nil {
					retried.Body = body
				}
			}
			t.auth.applyAuth(retried)
			return t.base.RoundTrip(retried)
		}
	}
	return resp, nil
}

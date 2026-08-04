// Package qdivzero is a client for the QDivZero API (https://api.qdiv0.com).
// Typed operations and models are generated from the OpenAPI specification
// snapshot in api/openapi.json; files named *.gen.go are regenerated
// automatically and must not be edited by hand.
package qdivzero

import (
	"context"
	"fmt"
	"net/http"
)

// DefaultBaseURL is the production API endpoint.
const DefaultBaseURL = "https://api.qdiv0.com"

// API is the client facade. It embeds the generated ClientWithResponses so
// every typed operation (e.g. GetAccountsWithResponse) is available directly,
// and adds automatic authentication: tokens set via Login, Refresh or options
// are attached to requests and refreshed transparently when the API answers
// 401 Unauthorized.
type API struct {
	*ClientWithResponses
	auth *authManager
}

type options struct {
	baseURL      string
	httpClient   *http.Client
	accessToken  string
	refreshToken string
	apiKey       string
	rawAuth      string
	headers      map[string]string
	email        string
	password     string
}

// Option customizes an API client.
type Option func(*options)

// WithServerURL overrides the default base URL (https://api.qdiv0.com).
func WithServerURL(u string) Option {
	return func(o *options) { o.baseURL = u }
}

// WithCustomHTTPClient uses the given HTTP client instead of a default one.
func WithCustomHTTPClient(c *http.Client) Option {
	return func(o *options) { o.httpClient = c }
}

// WithAccessToken pre-authenticates the client with a bearer access token.
func WithAccessToken(token string) Option {
	return func(o *options) { o.accessToken = token }
}

// WithRefreshToken sets a refresh token used for automatic token refresh.
func WithRefreshToken(token string) Option {
	return func(o *options) { o.refreshToken = token }
}

// WithAPIKey authenticates with a QDivZero API key, sent as
// "Authorization: Bearer <key>". Use WithAuthorization for other formats.
func WithAPIKey(key string) Option {
	return func(o *options) { o.apiKey = key }
}

// WithAuthorization sends the given value verbatim as the Authorization header.
func WithAuthorization(value string) Option {
	return func(o *options) { o.rawAuth = value }
}

// WithHeader sends the given header on every request, unless the request
// already carries it. Useful for temporary API headers.
func WithHeader(name, value string) Option {
	return func(o *options) {
		if o.headers == nil {
			o.headers = make(map[string]string)
		}
		o.headers[name] = value
	}
}

// WithCredentials stores credentials for on-demand login when a request
// receives a 401 response.
func WithCredentials(email, password string) Option {
	return func(o *options) { o.email = email; o.password = password }
}

// NewAPI creates a client for the QDivZero API.
func NewAPI(opts ...Option) (*API, error) {
	cfg := options{baseURL: DefaultBaseURL, httpClient: &http.Client{}}
	for _, o := range opts {
		o(&cfg)
	}

	cf, err := loadCredentialsFile()
	if err != nil {
		return nil, err
	}
	if cfg.accessToken == "" {
		cfg.accessToken = cf.AccessToken
	}
	if cfg.refreshToken == "" {
		cfg.refreshToken = cf.RefreshToken
	}
	if cfg.email == "" {
		cfg.email = cf.Email
	}
	if cfg.password == "" {
		cfg.password = cf.Password
	}

	plain, err := NewClientWithResponses(cfg.baseURL, WithHTTPClient(&http.Client{
		Transport: cfg.httpClient.Transport,
	}))
	if err != nil {
		return nil, fmt.Errorf("qdivzero: %w", err)
	}

	auth := &authManager{
		gen:          plain,
		accessToken:  cfg.accessToken,
		refreshToken: cfg.refreshToken,
		apiKey:       cfg.apiKey,
		rawAuth:      cfg.rawAuth,
		email:        cfg.email,
		password:     cfg.password,
		headers:      cfg.headers,
	}

	base := cfg.httpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client := &http.Client{
		Transport:     &authTransport{base: base, auth: auth},
		Timeout:       cfg.httpClient.Timeout,
		Jar:           cfg.httpClient.Jar,
		CheckRedirect: cfg.httpClient.CheckRedirect,
	}
	gen, err := NewClientWithResponses(cfg.baseURL, WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("qdivzero: %w", err)
	}
	return &API{ClientWithResponses: gen, auth: auth}, nil
}

// Login authenticates with email and password, storing the returned access
// and refresh tokens for automatic use by subsequent requests. Subsequent 401
// responses re-authenticate with the stored credentials.
func (a *API) Login(ctx context.Context, email, password string) error {
	a.auth.mu.Lock()
	a.auth.email = email
	a.auth.password = password
	a.auth.mu.Unlock()
	return a.auth.refreshNow(ctx)
}

// Refresh exchanges the stored refresh token for a new access token.
func (a *API) Refresh(ctx context.Context) error {
	return a.auth.refreshNow(ctx)
}

// SetAccessToken replaces the bearer access token used for requests.
func (a *API) SetAccessToken(token string) {
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	a.auth.accessToken = token
}

// SetRefreshToken replaces the refresh token used for automatic refresh.
func (a *API) SetRefreshToken(token string) {
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	a.auth.refreshToken = token
}

// SetAPIKey authenticates with a QDivZero API key.
func (a *API) SetAPIKey(key string) {
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	a.auth.apiKey = key
}

// SetAuthorization sets a raw Authorization header value, overriding the
// bearer token and API key.
func (a *API) SetAuthorization(value string) {
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	a.auth.rawAuth = value
}

// AccessToken returns the access token currently in use.
func (a *API) AccessToken() string {
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	return a.auth.accessToken
}

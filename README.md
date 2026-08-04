<p align="center">
  <img src="assets/qdiv0-mark.png" alt="QDivZero" width="256">
</p>

# qdivzero-go

Go client for the [Tokenfactory API Gateway](https://api.qdiv0.com), generated
from its [OpenAPI specification](https://api.qdiv0.com/openapi).

[![CI](https://github.com/QDivZero/qdivzero-go/actions/workflows/ci.yml/badge.svg)](https://github.com/QDivZero/qdivzero-go/actions/workflows/ci.yml)

## Install

````sh
go get github.com/QDivZero/qdivzero-go@latest
````

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/QDivZero/qdivzero-go"
)

func main() {
	ctx := context.Background()

	client, err := qdivzero.NewAPI(qdivzero.WithCredentials("you@example.com", "your-password"))
	if err != nil {
		log.Fatal(err)
	}

	accounts, err := client.GetAccountsWithResponse(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if accounts.StatusCode() != 200 {
		detail, _ := qdivzero.ParseErrorResponse(accounts.Body)
		log.Fatalf("get accounts: status %d: %s", accounts.StatusCode(), qdivzero.ErrorString(detail))
	}
	for _, m := range *accounts.JSON200.Memberships {
		fmt.Printf("account=%s role=%s\n", deref(m.AccountId), deref(m.Role))
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
```

## Authentication

The client manages authentication for you:

- `client.Login(ctx, email, password)` authenticates and stores the access and
  refresh tokens.
- When a request fails with `401`, the client transparently refreshes the
  access token (using the refresh token, or re-logging in with stored
  credentials) and retries the request once. Concurrent 401s trigger a single
  refresh.
- `WithAPIKey(key)` authenticates with a Tokenfactory API key
  (`Authorization: Bearer <key>`); `WithAuthorization(value)` sends a raw
  Authorization header for other formats.
- `WithAccessToken(token)` / `WithRefreshToken(token)` pre-configure tokens
  without calling Login.
- `client.Refresh(ctx)` forces a token refresh.

## Credentials file

If `~/.qdivzero/credentials` exists, the client loads it automatically on
`NewAPI`. All fields are optional:

````json
{
  "email": "you@example.com",
  "password": "your-password",
  "access_token": "...",
  "refresh_token": "..."
}
````

`email`/`password` enable on-demand login; `access_token`/`refresh_token`
authenticate directly. Explicit `With*` options override file values. Keep the
file private (`chmod 600 ~/.qdivzero/credentials`) and never commit it.

## Error handling

Generated operations return a typed response with a `StatusCode()` method; the
response body is available on `resp.Body` and decoded error bodies via
`qdivzero.ParseErrorResponse`. `error` returns from operations indicate
transport-level failures only.

## Regeneration

This library is regenerated automatically from
`https://api.qdiv0.com/openapi` by the
[update workflow](.github/workflows/update.yml) (daily at 06:00 UTC, or
manually via *Actions → update → Run workflow*). The upstream spec snapshot is
committed at `api/openapi.json`; the generation pipeline strips the upstream
`x-go-name` extension, which corrupts generated Go field names.

**Files `types.gen.go` and `client.gen.go` are generated — do not edit them by
hand.** A new release tag (`v1.0.<n>`) is published on every change.

## Development

Regenerate locally:

```sh
go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0
scripts/update.sh
```

Checks: `gofmt -l .` (empty), `go vet ./...`, `go test ./...`.

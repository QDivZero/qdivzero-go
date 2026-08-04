// Command example demonstrates the qdivzero client: login with credentials and
// list the user's accounts with error handling.
//
// Usage: QDIV0_EMAIL=you@example.com QDIV0_PASSWORD=secret go run ./examples
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/QDivZero/qdivzero-go"
)

func main() {
	email := os.Getenv("QDIV0_EMAIL")
	password := os.Getenv("QDIV0_PASSWORD")
	if email == "" || password == "" {
		fmt.Fprintln(os.Stderr, "set QDIV0_EMAIL and QDIV0_PASSWORD")
		os.Exit(2)
	}

	client, err := qdivzero.NewAPI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "client: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	if err := client.Login(ctx, email, password); err != nil {
		fmt.Fprintf(os.Stderr, "login: %v\n", err)
		os.Exit(1)
	}

	accounts, err := client.GetAccountsWithResponse(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "get accounts: %v\n", err)
		os.Exit(1)
	}
	if accounts.StatusCode() != 200 {
		detail, _ := qdivzero.ParseErrorResponse(accounts.Body)
		fmt.Fprintf(os.Stderr, "get accounts: status %d: %s\n", accounts.StatusCode(), qdivzero.ErrorString(detail))
		os.Exit(1)
	}
	if accounts.JSON200 != nil {
		for _, m := range *accounts.JSON200.Memberships {
			fmt.Printf("account=%s role=%s\n", deref(m.AccountId), deref(m.Role))
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

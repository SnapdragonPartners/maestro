//go:build integration

package migrations

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"testing"

	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/secret"
)

// testDSN builds a connection string for the running local data plane.
//
// It assembles the DSN here rather than calling stack.Config.DSN because
// internal/dataplane/stack imports THIS package, and a test helper is not a
// good enough reason to invert that dependency. Adding a `dataplanectl dsn`
// subcommand was the other option and is worse: it would print a live
// credential to stdout, where shell history and CI logs collect it.
//
// The small duplication below is therefore deliberate. It is coupled to the
// stack package's defaults, and if those change this helper fails loudly by
// not connecting rather than silently testing the wrong database.
func testDSN(t *testing.T) string {
	t.Helper()

	roots, err := paths.Resolve()
	if err != nil {
		t.Skipf("cannot resolve storage roots: %v", err)
	}
	rootKey, err := paths.EnsureKey(roots.Config)
	if err != nil {
		t.Skipf("cannot read the root-of-trust key: %v", err)
	}
	password, err := secret.Derive(rootKey, secret.ContextPostgresPassword)
	if err != nil {
		t.Fatalf("derive password: %v", err)
	}

	port := 55432 // stack.DefaultPGPort
	if raw := os.Getenv("MAESTRO_PG_PORT"); raw != "" {
		parsed, convErr := strconv.Atoi(raw)
		if convErr != nil {
			t.Fatalf("MAESTRO_PG_PORT=%q: %v", raw, convErr)
		}
		port = parsed
	}

	dsn := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword("maestro", password),
		Host:     net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Path:     "/maestro",
		RawQuery: "sslmode=disable",
	}
	return fmt.Sprint(dsn.String())
}

package migrations

import (
	"net/url"
	"strings"
	"testing"
)

// A migration that hangs holds the data-plane lifecycle lock, so nothing
// else can proceed either. The statement timeout is what bounds a single
// blocked DDL statement; GracefulStop only bounds the sequence between
// migrations. This asserts the timeout actually reaches the server, which
// is easy to believe and easy to get wrong: setting it with a SET statement
// would apply to one pooled connection rather than all of them.
func TestStatementTimeoutIsAppliedToTheConnection(t *testing.T) {
	bounded, err := withStatementTimeout("postgres://u:p@127.0.0.1:5432/db?sslmode=disable")
	if err != nil {
		t.Fatalf("withStatementTimeout: %v", err)
	}

	u, err := url.Parse(bounded)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	options := u.Query().Get("options")
	if !strings.Contains(options, "statement_timeout=") {
		t.Fatalf("connection options carry no statement timeout: %q", options)
	}
	if !strings.Contains(options, "300000") {
		t.Errorf("statement timeout is not the configured %s: %q", statementTimeout, options)
	}

	// Existing parameters must survive: dropping sslmode would change how
	// the connection is made, not just how long a statement may run.
	if u.Query().Get("sslmode") != "disable" {
		t.Error("existing connection parameters were lost")
	}
	if u.Path != "/db" || u.Host != "127.0.0.1:5432" {
		t.Errorf("connection target changed: %s", bounded)
	}
}

func TestStatementTimeoutRejectsAMalformedDSN(t *testing.T) {
	if _, err := withStatementTimeout("://not a dsn"); err == nil {
		t.Error("a malformed DSN was accepted")
	}
}

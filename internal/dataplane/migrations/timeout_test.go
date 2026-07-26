package migrations

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
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

// Cancellation choreography, tested without a database because that is
// where its correctness lives: channels, not SQL. A hung migration holds
// the data-plane lifecycle lock, so nothing else can proceed either, and no
// integration test would reliably reproduce it.

func TestRunOpReturnsWhenTheOperationCompletes(t *testing.T) {
	stopped := false
	err := runOp(context.Background(), func() { stopped = true }, func() error { return nil }, "test")
	if err != nil {
		t.Fatalf("runOp: %v", err)
	}
	if stopped {
		t.Error("the stopper was invoked for an operation that completed normally")
	}
}

func TestRunOpStopsAndWaitsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	release := make(chan struct{})
	finished := make(chan struct{})
	stopped := make(chan struct{})

	op := func() error {
		<-release // Block until the stopper says to finish.
		close(finished)
		return nil
	}
	stop := func() {
		close(stopped)
		close(release)
	}

	go cancel()

	err := runOp(ctx, stop, op, "test")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runOp = %v; want context.Canceled", err)
	}

	select {
	case <-stopped:
	default:
		t.Error("the operation was not asked to stop")
	}
	// The wait is the point: returning before the operation finishes would
	// report the database as unmigrated while a migration still runs.
	select {
	case <-finished:
	default:
		t.Error("runOp returned before the operation finished")
	}
}

// runOp accepts an arbitrary stopper, so it must not hang when one cannot
// deliver its signal.
//
// This is a property of runOp's own contract, not a reproduction of
// production behaviour: the pinned golang-migrate gives GracefulStop a
// capacity of 1, so the real stopper never blocks. An earlier version of
// this test claimed to reproduce a shipped deadlock, which was wrong on the
// facts -- the value here is that runOp stays correct for any stopper,
// including one bounded against a future upstream change.
func TestRunOpDoesNotHangWhenTheStopperCannotDeliver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// A stopper that behaves like a send to a channel nobody reads, bounded
	// the way gracefulStopper bounds it.
	unheard := make(chan bool)
	stop := func() {
		select {
		case unheard <- true:
		case <-time.After(50 * time.Millisecond):
		}
	}

	done := make(chan error, 1)
	go func() { done <- runOp(ctx, stop, func() error { return nil }, "test") }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runOp hung waiting for a stopper that could not deliver")
	}
}

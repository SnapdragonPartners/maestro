//go:build integration

package plane_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/harness"
	"orchestrator/internal/dataplane/plane"
	"orchestrator/internal/dataplane/planetest"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store/postgres"
	"orchestrator/internal/prompt"
)

// cutoverQuery is the runbook's migration-cutover check, verbatim
// (docs/v2/process_runbook.md, "Schema migration cutover"), with the columns
// these tests read. It is deliberately NOT filtered on application_name.
const cutoverQuery = `
SELECT pid, application_name
  FROM pg_stat_activity
 WHERE backend_type = 'client backend'
   AND datname = current_database()
   AND pid <> pg_backend_pid()`

// cutoverSessions runs the cutover check from a connection of its own and returns
// application_name by backend pid.
func cutoverSessions(t *testing.T, dsn string) map[uint32]string {
	t.Helper()
	ctx := context.Background()
	observer, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect the observer: %v", err)
	}
	defer observer.Close(ctx)

	rows, err := observer.Query(ctx, cutoverQuery)
	if err != nil {
		t.Fatalf("run the cutover check: %v", err)
	}
	defer rows.Close()
	found := make(map[uint32]string)
	for rows.Next() {
		var pid uint32
		var name string
		if err := rows.Scan(&pid, &name); err != nil {
			t.Fatal(err)
		}
		found[pid] = name
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return found
}

func composeWith(t *testing.T, dsn, label string, running harness.Version) plane.Composition {
	t.Helper()
	types, err := registry.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := planetest.Blob(t, label)
	return plane.Composition{
		DSN: dsn, Objects: blob, RootKey: planetest.RootKey(t),
		Caller: plane.Caller{Types: types, Keys: configkeys.MustNew(nil), Prompts: prompt.MustNew(nil), Harness: running},
	}
}

// TestSeamReportsTheVersionItWasComposedWith: store.Store.Harness is the one
// authority for the running version (item 4 design, D3), so it has to be the
// composition's and not a default. Two seams under two versions, so a Harness
// that returned a constant -- including the right constant for one of them --
// fails.
func TestSeamReportsTheVersionItWasComposedWith(t *testing.T) {
	ctx := context.Background()
	dsn := planetest.DSN(t, "harness")
	for _, raw := range []string{planetest.HarnessVersion, "v2.0.0-phase.3.4.1+build.9", harness.Development} {
		running, err := harness.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		composition := composeWith(t, dsn, "harness", running)
		seam, err := plane.Open(ctx, composition)
		if err != nil {
			t.Fatalf("open under %s: %v", raw, err)
		}
		if got := seam.Harness(); got != running || got.String() != raw {
			t.Errorf("composed with %q, Harness() = %q", raw, got)
		}
		seam.Close()
	}
}

// TestOpenRefusesAZeroHarnessBeforeThePlane: the refusal is validate's, so it
// must arrive with the database unreachable.
func TestOpenRefusesAZeroHarnessBeforeThePlane(t *testing.T) {
	composition := composeWith(t, "postgres://127.0.0.1:1/unreachable", "zero", harness.Version{})
	_, err := plane.Open(context.Background(), composition)
	if err == nil || !strings.Contains(err.Error(), "no harness version") {
		t.Fatalf("Open() = %v, want the harness-version refusal, not a connection failure", err)
	}
}

// TestSeamSessionsCarryTheLabelAndTheCutoverCheckNeedsNone covers both halves
// of the runbook's claim.
//
// The seam's sessions are labelled, THROUGH plane.Open -- the route every
// composer takes -- and not merely by a pool helper nobody is shown to call.
//
// And the unfiltered check lists a session that carries no label at all,
// which is the one the first cutover exists to find: a connection opened by
// code that predates the label. A check filtered on the label would return
// empty with that session still open.
func TestSeamSessionsCarryTheLabelAndTheCutoverCheckNeedsNone(t *testing.T) {
	ctx := context.Background()
	dsn := planetest.DSN(t, "label")

	// A DSN that names the session something else must not win: the label
	// is what the narrowed check reads to decide no seam is open.
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	composition := composeWith(t, dsn+separator+"application_name=renamed-by-dsn", "label", planetest.Harness(t))
	seam, err := plane.Open(ctx, composition)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(seam.Close)

	unlabelled, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer unlabelled.Close(ctx)
	var unlabelledPID uint32
	if err := unlabelled.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&unlabelledPID); err != nil {
		t.Fatal(err)
	}

	found := cutoverSessions(t, dsn)

	name, listed := found[unlabelledPID]
	if !listed {
		t.Fatalf("the cutover check did not list an unlabelled session (pid %d): %v", unlabelledPID, found)
	}
	if name != "" {
		t.Fatalf("the control session is labelled %q, so it proves nothing about unlabelled ones", name)
	}

	var labelled int
	for pid, name := range found {
		if pid == unlabelledPID {
			continue
		}
		if name != postgres.ApplicationName {
			t.Errorf("session %d on the seam's database is labelled %q, want %q", pid, name, postgres.ApplicationName)
		}
		labelled++
	}
	if labelled == 0 {
		t.Fatal("no seam session was listed, so the label assertion above ran over nothing")
	}
	if !strings.HasPrefix(postgres.ApplicationName, "maestro") {
		t.Fatalf("the runbook narrows with LIKE 'maestro%%'; %q would not match", postgres.ApplicationName)
	}
}

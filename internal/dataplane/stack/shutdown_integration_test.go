//go:build integration

package stack

import (
	"strings"
	"testing"
)

// Crash-recovery markers the Postgres log carries when a cluster was NOT
// shut down cleanly. Either of them means the backup captured a torn
// cluster.
//
// Matched as substrings of the server log rather than parsed: these strings
// are stable across the major versions this project pins, and a structured
// reader would be more machinery than the assertion is worth.
var crashRecoveryMarkers = []string{
	"was not properly shut down",
	"automatic recovery in progress",
	"database system was interrupted",
}

// readyMarker is what a completed startup logs. Counting it is what stops
// the assertion below from passing against a cluster that never restarted.
const readyMarker = "database system is ready to accept connections"

// TestBackupShutsDownCleanlyWithAConnectionHeld is test-plan item 2, and the
// held connection is the entire point.
//
// D3 chose `compose stop` with the pinned image's STOPSIGNAL of SIGINT --
// Postgres's FAST shutdown, which terminates sessions, checkpoints, and
// exits. The plausible alternative, SIGTERM, is Postgres's SMART shutdown:
// it waits for every client to disconnect first. With a client holding a
// connection open, smart shutdown never completes, Compose's timeout
// escalates to SIGKILL, and the backup copies a cluster that was killed
// mid-write -- which then performs crash recovery on restore, silently, and
// looks like a successful backup right up until it isn't.
//
// A backup with no clients connected cannot tell those two designs apart.
// This one holds an open transaction across the whole operation, and reads
// the server's own log afterwards rather than assuming.
func TestBackupShutsDownCleanlyWithAConnectionHeld(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	seed := seedCrossStore(t, cfg)

	// An OPEN TRANSACTION, not merely an open pool. A pool may have no
	// live session at the moment of the stop; a transaction guarantees a
	// backend is attached and busy, which is what smart shutdown waits for.
	database := openPlane(t, cfg)
	transaction, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin the held transaction: %v", err)
	}
	var one int
	if err := transaction.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("the held transaction has no backend attached: %v", err)
	}
	// Deliberately never committed. It is torn down with the cluster, and
	// rolling it back here would release the very session under test.
	defer func() { _ = transaction.Rollback() }()

	archive := t.TempDir() + "/archive"
	if err := Backup(t.Context(), cfg, testComposeFile(), archive); err != nil {
		t.Fatalf("Backup with a connection held: %v", err)
	}

	// The plane is back up, and the log now covers the whole life of the
	// container: the first start, the stop the backup performed, and the
	// restart. A crash marker anywhere in it is a shutdown that was not
	// clean.
	logs := postgresLogs(t, cfg)
	for _, marker := range crashRecoveryMarkers {
		if strings.Contains(logs, marker) {
			t.Errorf("the Postgres log contains %q: the backup did not quiesce the cluster cleanly, "+
				"so the archive holds a cluster captured mid-write", marker)
		}
	}

	// And it really did stop and start again, so the absence of crash
	// markers is not the absence of a restart.
	if restarts := strings.Count(logs, readyMarker); restarts < 2 {
		t.Errorf("the Postgres log reports %d completed startups, want at least 2 (the original and "+
			"the backup's restart): this assertion would pass for a backup that never stopped the "+
			"cluster at all", restarts)
	}

	// The plane still holds what it held, across both stores.
	//
	// The wait is not incidental. `Backup` restarts the project with
	// `compose start` and returns as soon as the containers are started --
	// it does not wait for them to be USABLE, the way `up` does. So a
	// backup can return with MinIO still refusing connections for a second
	// or two, which is what this test hit on its first run.
	//
	// Whether that is a defect is a real question and not this test's to
	// settle: D9 says backup "returns the project to the state it found",
	// and a project that was ready before and is merely started after is
	// arguably not that state. It is raised for review rather than changed
	// here, because making the restart wait lengthens every backup and is a
	// behaviour change, not a test fix.
	env, err := cfg.composeEnv(placeholderKey())
	if err != nil {
		t.Fatalf("compose env: %v", err)
	}
	if err := waitReady(t.Context(), cfg, testComposeFile(), env); err != nil {
		t.Fatalf("the plane never became usable after the backup restarted it: %v", err)
	}
	assertCrossStoreIntact(t, cfg, seed)
}

// postgresLogs returns the whole server log for the project's Postgres
// container.
func postgresLogs(t *testing.T, cfg *Config) string {
	t.Helper()
	env, err := cfg.composeEnv(placeholderKey())
	if err != nil {
		t.Fatalf("compose env: %v", err)
	}
	out, err := composeOutput(t.Context(), cfg.ProjectName, testComposeFile(), env,
		"logs", "--no-color", "postgres")
	if err != nil {
		t.Fatalf("read the postgres log: %v\n%s", err, out)
	}
	if len(out) == 0 {
		t.Fatal("the postgres log is empty: this test would report a clean shutdown for a cluster " +
			"whose log it could not read")
	}
	return string(out)
}

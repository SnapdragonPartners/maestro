//go:build integration

package stack

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/dataplane/paths"
)

// teardownTimeout bounds the best-effort cleanup each isolated plane
// registers.
const teardownTimeout = 2 * time.Minute

// isolatedPlane builds a Config for a data plane of its very own:
// throwaway roots, a unique Compose project, and ports nothing else holds.
//
// It exists because the destructive verbs cannot be tested any other way.
// The rest of this repository's integration tests isolate at the DATABASE
// level — a disposable database on the developer's cluster, a disposable
// bucket on their MinIO — which is exactly right for query and object
// tests and useless here: `restore` clears the whole data root, `reset`
// deletes it, and `backup` stops the whole Compose project.
//
// THE TWO HALVES OF ISOLATION, both asserted below, because either alone is
// a half-isolation that reads as complete:
//
//   - Compose selects containers by PROJECT IDENTITY. A config pointed at
//     temporary roots still reaches the developer's containers if it
//     carries the default project name, so a `down` would remove the plane
//     they are running and a later `up` would recreate that same project
//     against the temporary roots.
//   - The bind mounts come from the ROOTS. A config with a unique project
//     name but the developer's real roots would mount their live Postgres
//     directory into fresh containers, and `reset` would delete the data
//     while the project-name assertion sat there passing.
//
// The assertions are hard failures rather than skips: a harness that
// quietly degraded to the developer's plane is precisely the outcome worth
// crashing over.
func isolatedPlane(t *testing.T) *Config {
	t.Helper()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generate a project suffix: %v", err)
	}
	project := "maestro-it-" + hex.EncodeToString(suffix)

	// EVERY part of the isolation goes through the environment, and the
	// roots are resolved rather than constructed.
	//
	// An earlier version set the project and ports through the environment
	// — which subprocesses inherit — and built the roots as a struct
	// literal, which they do not. A child `dataplanectl` therefore
	// inherited the isolated project and ports and then called
	// paths.Resolve(), landing on the DEVELOPER's roots: a child `reset`
	// would have deleted their data, and a child `up` would have mounted
	// their live PGDATA into a second Postgres container. The comment
	// beside it explained why struct assignment leaves subprocesses behind,
	// two lines above the struct assignment that did.
	//
	// Deriving the roots through paths.Resolve() under MAESTRO_HOME is what
	// makes parent and child agree by construction rather than by two
	// copies of the same intent.
	// The developer's roots are captured BEFORE the override, or the
	// comparison below resolves under MAESTRO_HOME and compares the
	// isolated root against itself — an assertion that can only ever fire
	// on the safe case and never on the dangerous one.
	developer, developerErr := paths.Resolve()

	home := t.TempDir()
	t.Setenv(paths.HomeEnv, home)
	t.Setenv(EnvProjectName, project)
	t.Setenv(EnvPGPort, freePort(t))
	t.Setenv(EnvMinIOPort, freePort(t))
	t.Setenv(EnvMinIOConsolePort, freePort(t))

	roots, err := paths.Resolve()
	if err != nil {
		t.Fatalf("resolve isolated roots: %v", err)
	}
	cfg, err := NewConfig(roots)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}

	if !strings.HasPrefix(cfg.Roots.Data, home) {
		t.Fatalf("harness data root %s is outside its MAESTRO_HOME %s: a subprocess would resolve "+
			"somewhere this test does not control", cfg.Roots.Data, home)
	}

	if cfg.ProjectName == DefaultProjectName {
		t.Fatalf("harness resolved the DEFAULT compose project (%s): every operation would act on the "+
			"developer's running containers", DefaultProjectName)
	}
	if developerErr == nil && cfg.Roots.Data == developer.Data {
		t.Fatalf("harness resolved the developer's real data root (%s): a unique project name does not "+
			"stop a reset from deleting what is bind-mounted there", developer.Data)
	}

	// Best-effort teardown on a context of its own, so a cancelled or
	// timed-out test still removes the containers it created. Without it a
	// failing run leaks a project per test.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), teardownTimeout)
		defer cancel()
		// Errorf, not Logf. A test that passes while leaving a destructive
		// stack running has not finished, and the next run would meet
		// containers it did not create — on ports and a project it believes
		// are its own.
		if err := Down(ctx, cfg, testComposeFile()); err != nil {
			t.Errorf("teardown of %s did not complete, leaving an orphaned stack: %v", cfg.ProjectName, err)
		}
	})

	return cfg
}

// testComposeFile locates the shipped Compose file from the package
// directory, which is where `go test` runs.
func testComposeFile() string {
	return filepath.Join("..", "..", "..", DefaultComposeFile)
}

// freePort asks the kernel for a port nothing currently holds.
//
// Bind-then-release rather than a fixed offset from the defaults: parallel
// packages and a CI bootstrap stack can hold anything, and a collision
// would surface as a container that fails to start rather than as a clear
// message about ports.
func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	defer func() { _ = listener.Close() }()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("read the chosen port: %v", err)
	}
	return port
}

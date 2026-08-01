//go:build integration

package stack

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"path/filepath"
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

	home := t.TempDir()
	roots := paths.Roots{
		Config: filepath.Join(home, "config"),
		Cache:  filepath.Join(home, "cache"),
		State:  filepath.Join(home, "state"),
		Data:   filepath.Join(home, "data"),
	}

	// Set through the environment rather than assigned to the struct: the
	// killed-process cases run `dataplanectl` as a child, and only an
	// environment override reaches it. Assigning the field would isolate
	// this process and quietly leave subprocesses on the developer's plane.
	t.Setenv(EnvProjectName, project)
	t.Setenv(EnvPGPort, freePort(t))
	t.Setenv(EnvMinIOPort, freePort(t))
	t.Setenv(EnvMinIOConsolePort, freePort(t))

	cfg, err := NewConfig(roots)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}

	if cfg.ProjectName == DefaultProjectName {
		t.Fatalf("harness resolved the DEFAULT compose project (%s): every operation would act on the "+
			"developer's running containers", DefaultProjectName)
	}
	if real, resolveErr := paths.Resolve(); resolveErr == nil && cfg.Roots.Data == real.Data {
		t.Fatalf("harness resolved the developer's real data root (%s): a unique project name does not "+
			"stop a reset from deleting what is bind-mounted there", real.Data)
	}

	// Best-effort teardown on a context of its own, so a cancelled or
	// timed-out test still removes the containers it created. Without it a
	// failing run leaks a project per test.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), teardownTimeout)
		defer cancel()
		if err := Down(ctx, cfg, testComposeFile()); err != nil {
			t.Logf("teardown of %s did not complete: %v", cfg.ProjectName, err)
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

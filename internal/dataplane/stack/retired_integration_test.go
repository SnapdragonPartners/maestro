//go:build integration

package stack

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/paths"
)

// TestDownRemovesARetiredServicesContainer is the upgrade transition #350
// creates: a plane started under the previous compose file has a container
// for a service the current file no longer declares (MinIO). Compose calls
// that an orphan and a plain `down` leaves it running -- holding its port,
// so the replacement cannot bind it, and holding its bind mount open, so
// `reset` would empty a directory a live process is still writing into.
//
// The orphan is planted with the labels Compose itself writes, because
// orphan detection keys on them: a container carrying only the project and
// service labels is NOT recognised (measured), and a real legacy container
// carries the full set.
//
// THE MUTANT: drop --remove-orphans from down(). The planted container
// survives and the assertion below finds it.
func TestDownRemovesARetiredServicesContainer(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	composeFile, err := filepath.Abs(testComposeFile())
	if err != nil {
		t.Fatal(err)
	}
	name := cfg.ProjectName + "-minio-1"
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	run := exec.CommandContext(t.Context(), "docker", "run", "-d", "--name", name,
		"--label", "com.docker.compose.project="+cfg.ProjectName,
		"--label", "com.docker.compose.service=minio",
		"--label", "com.docker.compose.oneoff=False",
		"--label", "com.docker.compose.container-number=1",
		"--label", "com.docker.compose.config-hash=retired",
		"--label", "com.docker.compose.project.config_files="+composeFile,
		"--label", "com.docker.compose.project.working_dir="+filepath.Dir(composeFile),
		"--label", "com.docker.compose.version=2",
		"--label", "com.maestro.component=dataplane",
		"alpine", "sleep", "600")
	if out, runErr := run.CombinedOutput(); runErr != nil {
		t.Fatalf("plant the retired container: %v\n%s", runErr, out)
	}
	if !containerExists(t, name) {
		t.Fatal("the retired container was not planted; the assertion below would be vacuous")
	}
	// The retired service's bind-mount source, as an upgraded plane has it.
	retiredDir := filepath.Join(cfg.Roots.Data, "minio")
	if err := os.MkdirAll(retiredDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(retiredDir, ".minio.sys"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Down(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if containerExists(t, name) {
		t.Fatalf("%s survived down: the retired service keeps its port and its bind mount", name)
	}

	// And reset, which goes through the same down, returns the whole root
	// to fresh -- the retired directory included, emptied in place.
	if err := Reset(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	entries, err := os.ReadDir(retiredDir)
	if err != nil {
		t.Fatalf("the retired directory was removed rather than emptied: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("reset left %d entries in the retired directory", len(entries))
	}
}

func containerExists(t *testing.T, name string) bool {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "docker", "ps", "-a", "--filter", "name=^/"+name+"$",
		"--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	return strings.TrimSpace(string(out)) == name
}

// TestBackupRestartProvesTheStoreUsableWithoutTheKey is design D3a's
// promise under the condition review named: the plane is usable when
// Backup returns, and that must not depend on the host key file, because
// a backup renders no credential and runs keyless by design. The proof
// therefore uses the credentials the restarted container itself holds.
//
// The key is removed BEFORE the backup, so a restart proof that derived
// credentials from it would have nothing to derive from; the read after
// Backup returns uses credentials derived while the key still existed,
// which is what a caller holding an open seam has.
//
// THE MUTANT: make composeStart return after liveness alone (skip
// objectsFromRunningContainer / waitObjectsServe). The read below then
// lands in the window between /healthz and the volume path serving, and
// fails with InternalError -- not deterministically, which is why the
// round-trip exists; the deterministic half is that this test still
// PASSES with the key gone, which a key-derived proof cannot.
func TestBackupRestartProvesTheStoreUsableWithoutTheKey(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	rootKey, err := paths.LoadKey(cfg.Roots.Config)
	if err != nil {
		t.Fatalf("load the key while it exists: %v", err)
	}
	blob, err := ensureBucket(t.Context(), cfg, rootKey)
	if err != nil {
		t.Fatalf("adapter: %v", err)
	}
	body := []byte("written before the keyless backup")
	const key = "org/aa/bb/keyless-backup"
	if _, err := blob.PutStaged(t.Context(), key, int64(len(body)), bytes.NewReader(body)); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := os.Remove(cfg.Roots.KeyPath()); err != nil {
		t.Fatalf("remove the key: %v", err)
	}
	if err := Backup(t.Context(), cfg, testComposeFile(), filepath.Join(t.TempDir(), "archive")); err != nil {
		t.Fatalf("Backup without the key: %v", err)
	}

	// Immediately, with no wait of its own: Backup promised usability.
	reader, err := blob.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("read straight after a keyless backup: %v", err)
	}
	got, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("read back %q (%v), want the bytes written before the backup", got, err)
	}
}

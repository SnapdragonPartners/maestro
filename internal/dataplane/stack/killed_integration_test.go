//go:build integration

package stack

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildDataplanectl compiles the CLI once and returns the binary's path.
//
// A built binary rather than `go run`, and the distinction is load-bearing
// for every killed-process case: `go run` compiles and then executes the
// program as a CHILD, so signalling the `go run` process kills the wrapper
// and leaves the real one running. A test that did that would report a
// killed backup while the backup carried on and completed behind it.
func buildDataplanectl(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "dataplanectl")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/dataplanectl")
	build.Dir = filepath.Join("..", "..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dataplanectl: %v\n%s", err, out)
	}
	return binary
}

// padDataRoot makes the copy phase long enough to be interrupted.
//
// Legitimate rather than a trick: D1's whole point is that the data root is
// copied wholesale, whatever is in it. Without padding, a freshly
// initialised plane copies in well under a second and every kill lands after
// the copy has finished.
func padDataRoot(t *testing.T, cfg *Config) {
	t.Helper()
	block := make([]byte, 8<<20)
	for i := range block {
		block[i] = byte(i)
	}
	for chunk := range 24 {
		name := filepath.Join(cfg.Roots.Data, "padding-"+fmt.Sprint(chunk))
		if err := os.WriteFile(name, block, 0o600); err != nil {
			t.Fatalf("pad the data root: %v", err)
		}
	}
}

// TestAKilledBackupLeavesNothingRestorable is test-plan item 11.
//
// The archive's completion protocol exists because CLEANUP CANNOT BE RELIED
// ON. A killed process runs no deferred functions, so the staging tree it
// was building survives at a temporary path -- and restore accepts a source
// by shape, so a partial tree containing the service directories would pass
// a structural check. The operator reaching for it is by definition someone
// whose live plane is already in trouble.
//
// Which is why the manifest is written and fsynced LAST, and why validity is
// a property of an archive's CONTENTS rather than of its path. "It is called
// temporary" is not a safety boundary.
//
// The process is genuinely killed, not handed an injected error: an error
// return unwinds through the defers that remove the staging tree, so it
// exercises the cleanup path and leaves precisely none of the residue this
// test is about.
func TestAKilledBackupLeavesNothingRestorable(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	seed := seedCrossStore(t, cfg)
	padDataRoot(t, cfg)

	// The destination's PARENT is where backup builds its staging sibling,
	// so it is where the residue will be.
	parent := t.TempDir()
	destination := filepath.Join(parent, "archive")

	// Flags BEFORE the verb: the standard flag package stops parsing at the
	// first non-flag argument, so `backup -to X` leaves -to unparsed and the
	// CLI prints its usage and exits.
	command := exec.Command(buildDataplanectl(t), "-to", destination, "backup") //nolint:noctx // killed deliberately
	command.Dir = filepath.Join("..", "..", "..")
	// Inherited, which is what puts the child on the isolated plane:
	// MAESTRO_HOME, the project name and the ports are all in this
	// process's environment.
	command.Env = os.Environ()
	// Captured so a child that dies on its own says why. Without this a
	// failed child is indistinguishable from a slow one, and the test times
	// out reporting that no staging tree appeared.
	var childOutput strings.Builder
	command.Stdout = &childOutput
	command.Stderr = &childOutput
	if err := command.Start(); err != nil {
		t.Fatalf("start the child backup: %v", err)
	}
	childExit := make(chan error, 1)
	go func() { childExit <- command.Wait() }()

	// Kill once the copy is demonstrably under way -- the staging tree
	// exists and has begun taking content. Killing earlier would leave no
	// residue and prove nothing; killing on a timer would do either
	// depending on the machine.
	staging := waitForStagingContent(t, parent, childExit, &childOutput)
	if err := command.Process.Kill(); err != nil {
		t.Fatalf("kill the child backup: %v", err)
	}
	<-childExit

	// The residue is still there, because nothing ran to remove it. If it
	// is not, this test proves nothing about what restore does with one.
	if _, err := os.Stat(staging); err != nil {
		t.Fatalf("no residue survives at %s (%v): the kill landed outside the copy, so the case "+
			"this test exists for was never produced", staging, err)
	}
	// And no archive was ever published.
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a killed backup published an archive at %s (%v): the rename must happen only after "+
			"the manifest is written", destination, err)
	}

	// The residue has no manifest, which is the only thing that makes a
	// directory an archive.
	if _, err := ReadManifest(staging); !errors.Is(err, ErrArchiveIncomplete) {
		t.Errorf("ReadManifest(residue) = %v, want a refusal: the copy never finished, so the tree "+
			"is not an archive whatever it looks like", err)
	}

	// The assertion that matters to an operator: restore REFUSES it, and
	// refuses it before touching the plane.
	restoreErr := Restore(t.Context(), cfg, testComposeFile(), staging, true)
	if !errors.Is(restoreErr, ErrArchiveIncomplete) {
		t.Fatalf("Restore(residue) = %v, want a refusal", restoreErr)
	}

	// "Before taking any destructive action" is the load-bearing half, and
	// it is asserted on the plane rather than inferred from the error. A
	// refusal that arrived after the data root had been cleared would
	// satisfy the error check above and have destroyed the plane.
	if _, err := os.Stat(markerPath(cfg)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the refused restore left an incomplete marker (%v): it began deleting before it "+
			"validated the source", err)
	}
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up after the refused restore: %v", err)
	}
	assertCrossStoreIntact(t, cfg, seed)
}

// waitForStagingContent blocks until a backup staging tree exists under
// parent AND has begun receiving content, then returns its path.
//
// Content, not mere existence: the directory is created before the copy
// starts, so killing on existence alone would often land before a single
// byte was written -- a residue that is trivially not an archive, and a
// weaker test than one whose residue looks convincingly like a plane.
func waitForStagingContent(
	t *testing.T, parent string, childExit <-chan error, childOutput *strings.Builder,
) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case err := <-childExit:
			t.Fatalf("the child backup exited on its own (%v) before any staging tree appeared:\n%s",
				err, childOutput.String())
		default:
		}
		entries, err := os.ReadDir(parent)
		if err != nil {
			t.Fatalf("read %s: %v", parent, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".maestro-backup-") {
				continue
			}
			staging := filepath.Join(parent, entry.Name())
			copied, statErr := os.ReadDir(filepath.Join(staging, ArchiveDataDir))
			if statErr == nil && len(copied) > 0 {
				return staging
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no backup staging tree ever appeared with content in it")
	return ""
}

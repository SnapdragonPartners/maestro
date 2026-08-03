//go:build integration

package stack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A child dataplanectl must land on the SAME plane as its parent — same
// Compose project and same roots.
//
// This is the assertion the harness was missing, and its absence was not
// theoretical. Setting the project and ports through the environment while
// building the roots as a struct literal isolated this process and left
// every subprocess resolving the developer's roots: a child `reset` would
// have deleted their cluster, and a child `up` would have mounted their
// live PGDATA into a second Postgres container while the parent's
// assertions all passed.
//
// The killed-process cases in the test plan all run dataplanectl as a
// child, so nothing else in the suite is safe until this holds.
//
// It asserts EFFECTS rather than reading the child's configuration back:
// what matters is where the child actually wrote and which containers it
// actually created, not what it claimed it would do.
func TestSubprocessInheritsTheIsolatedPlane(t *testing.T) {
	cfg := isolatedPlane(t)

	// Nothing has provisioned this root yet, which is what makes the
	// evidence below unambiguous.
	if entries, err := os.ReadDir(filepath.Join(cfg.Roots.Data, "postgres")); err == nil && len(entries) > 0 {
		t.Fatalf("isolated data root is already populated; this test cannot tell who wrote it")
	}

	// No -compose flag: the child runs from the repository root, where
	// DefaultComposeFile already resolves. Passing the parent's path would
	// hand the child a path relative to the PACKAGE directory instead.
	command := exec.CommandContext(t.Context(), "go", "run", "./cmd/dataplanectl", "up")
	command.Dir = filepath.Join("..", "..", "..")
	// The child inherits this process's environment, which is exactly the
	// mechanism under test: t.Setenv has put MAESTRO_HOME, the project name
	// and the ports there.
	command.Env = os.Environ()

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("child dataplanectl up: %v\n%s", err, output)
	}

	// The child provisioned the ISOLATED root. If it had resolved the
	// developer's roots this directory would still be empty, and their
	// cluster would have been mounted into the container instead.
	entries, err := os.ReadDir(filepath.Join(cfg.Roots.Data, "postgres"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("child did not provision the isolated data root %s (err %v): it resolved somewhere else",
			cfg.Roots.Data, err)
	}

	// And it created containers in the ISOLATED project, not the default.
	names := dockerContainerNames(t, "com.docker.compose.project="+cfg.ProjectName)
	if len(names) == 0 {
		t.Fatalf("child created no containers in project %s: it used a different project", cfg.ProjectName)
	}
	for _, name := range names {
		if strings.Contains(name, DefaultProjectName) {
			t.Errorf("child created %s, which belongs to the default project", name)
		}
	}
}

// dockerContainerNames lists containers carrying a label.
func dockerContainerNames(t *testing.T, label string) []string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "docker", "ps",
		"--filter", "label="+label, "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names
}

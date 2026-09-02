package orchestrator_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// seamPackage is the package whose closure this test pins.
const seamPackage = "orchestrator/internal/orchestrator"

// allowedClosure is every in-module package the Orchestrator may reach,
// directly or transitively (Phase 3 item 3, design D2): the seam, the two
// vocabularies it declares, the readiness causes, the work types, the
// secrets seam the store returns values through, two neutral helpers, and
// itself. Nothing local, nothing v1.
//
// An exact set, not a deny-list: a package added to the data plane later is
// forbidden until somebody adds it here deliberately. `stack`, `paths`,
// `plane`, `cloud`, `migrations`, `objects` and `store/postgres` are the
// ones this rule exists to keep out, and `pkg/config` -- v1's file-based
// configuration -- is forbidden by name, since an Orchestrator that can reach
// it has a second source to drift toward.
var allowedClosure = []string{
	"orchestrator/internal/dataplane/canonical",
	"orchestrator/internal/dataplane/configkeys",
	"orchestrator/internal/dataplane/nilcheck",
	"orchestrator/internal/dataplane/readiness",
	"orchestrator/internal/dataplane/registry",
	"orchestrator/internal/dataplane/secret",
	"orchestrator/internal/dataplane/store",
	"orchestrator/internal/dataplane/work",
	seamPackage,
}

// forbiddenByName are v1 packages outside internal/dataplane that the closure
// must not reach either; the exact set above only governs the data plane.
var forbiddenByName = []string{
	"orchestrator/pkg/persistence",
	"orchestrator/pkg/state",
	"orchestrator/pkg/config",
}

// TestOrchestratorClosureReachesNothingLocalOrV1 is the exit criterion
// "paths.Bootstrap is not imported from above the seam", checked at the
// caller; the seam's own closure is pinned by the sibling test in store.
//
// The closure is taken under EVERY applicable build configuration, one at a
// time, per the Reachability Claims rule: a single default listing is one
// configuration, and a file guarded by a tag is invisible to it. The tag set
// is re-derived from the tree rather than listed, since hand-maintained
// enumerations have failed three times in this repository.
func TestOrchestratorClosureReachesNothingLocalOrV1(t *testing.T) {
	root := moduleRoot(t)
	for _, tags := range buildConfigurations(t, root) {
		t.Run("tags="+strings.Join(tags, ","), func(t *testing.T) {
			deps := listDeps(t, root, tags)
			var offending []string
			for _, dep := range deps {
				local := strings.HasPrefix(dep, "orchestrator/internal/dataplane/") && !slices.Contains(allowedClosure, dep)
				if local || slices.Contains(forbiddenByName, dep) {
					offending = append(offending, dep)
				}
			}
			if len(offending) != 0 {
				t.Fatalf("the Orchestrator reaches %v.\n"+
					"Its data-plane closure must be exactly %v and it must not reach %v: it sees store.Store "+
					"and nothing below it, and is handed an Opener rather than a composer (design D2, D3).",
					offending, allowedClosure, forbiddenByName)
			}
			for _, want := range allowedClosure {
				if !slices.Contains(deps, want) {
					t.Fatalf("the Orchestrator no longer reaches %s; update allowedClosure deliberately rather "+
						"than leaving a package on the list that nothing pins", want)
				}
			}
		})
	}
}

// buildConfigurations returns the tag sets to evaluate: none, then each bare
// tag the tree declares, one at a time. At the time of writing every
// //go:build expression in the repository is a single bare tag; a negated or
// compound expression would need the crossing item 1 describes, and this
// function fails loudly rather than evaluating it wrong.
func buildConfigurations(t *testing.T, root string) [][]string {
	t.Helper()
	out, err := exec.Command("grep", "-rh", "--include=*.go", "^//go:build", root).Output()
	if err != nil {
		var exit *exec.ExitError
		// grep exits 1 on no matches, which would mean no tags at all.
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatalf("enumerate build constraints: %v", err)
		}
	}
	bare := regexp.MustCompile(`^//go:build ([A-Za-z0-9_]+)$`)
	seen := map[string]bool{}
	configs := [][]string{nil}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		m := bare.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("build constraint %q is not a single bare tag; this test evaluates one tag at a "+
				"time and must be extended before it can claim to cover it", line)
		}
		if !seen[m[1]] {
			seen[m[1]] = true
			configs = append(configs, []string{m[1]})
		}
	}
	sort.Slice(configs[1:], func(i, j int) bool { return configs[i+1][0] < configs[j+1][0] })
	return configs
}

// listDeps is `go list -deps` for the seam under one tag set.
func listDeps(t *testing.T, root string, tags []string) []string {
	t.Helper()
	args := []string{"list", "-deps"}
	if len(tags) != 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	args = append(args, seamPackage)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// moduleRoot walks up from the test's directory to go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

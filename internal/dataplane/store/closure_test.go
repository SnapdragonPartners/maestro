package store_test

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
const seamPackage = "orchestrator/internal/dataplane/store"

// allowedClosure is every in-module package the seam may reach, directly or
// transitively (Phase 3 item 3, design D2). Six packages, none local, none
// v1: the seam, the two vocabularies a caller declares, the secrets seam it
// returns values through, and two neutral helpers.
//
// It is an exact set rather than a deny-list, because a deny-list permits
// by omission: a package added to the data plane later would be reachable
// until somebody remembered to forbid it, and the package this rule exists
// to keep out — `paths`, with its data roots, key files and flocks — was
// reachable for exactly that reason before item 3 cut the edge.
var allowedClosure = []string{
	"orchestrator/internal/dataplane/canonical",
	"orchestrator/internal/dataplane/configkeys",
	"orchestrator/internal/dataplane/nilcheck",
	"orchestrator/internal/dataplane/registry",
	"orchestrator/internal/dataplane/secret",
	seamPackage,
}

// TestSeamClosureReachesNothingLocalOrV1 is the exit criterion "paths.Bootstrap
// is not imported from above the seam", checked at the seam itself — because
// before item 3 the seam's own closure ran store → secret → paths →
// pkg/utils → pkg/config, and a guard placed above the seam would have
// failed on the seam.
//
// The closure is taken under EVERY applicable build configuration, one at a
// time, per the Reachability Claims rule: a single default listing is one
// configuration, and a file guarded by a tag is invisible to it. The tag set
// is re-derived from the tree rather than listed, since hand-maintained
// enumerations have failed three times in this repository.
func TestSeamClosureReachesNothingLocalOrV1(t *testing.T) {
	root := moduleRoot(t)
	for _, tags := range buildConfigurations(t, root) {
		t.Run("tags="+strings.Join(tags, ","), func(t *testing.T) {
			deps := listDeps(t, root, tags)
			var offending []string
			for _, dep := range deps {
				if strings.HasPrefix(dep, "orchestrator/") && !slices.Contains(allowedClosure, dep) {
					offending = append(offending, dep)
				}
			}
			if len(offending) != 0 {
				t.Fatalf("the persistence seam reaches %v.\n"+
					"Its closure must be exactly %v: the Orchestrator sees store.Store and nothing below it, "+
					"and every package the seam drags in is one the Orchestrator drags in too (design D2).",
					offending, allowedClosure)
			}
			for _, want := range allowedClosure {
				if !slices.Contains(deps, want) {
					t.Fatalf("the seam no longer reaches %s; update allowedClosure deliberately rather than "+
						"leaving a package on the list that nothing pins", want)
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

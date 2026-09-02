package store_test

import (
	"bytes"
	"errors"
	"fmt"
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
	configs := buildConfigurations(t, root)
	if len(configs) < 20 {
		t.Fatalf("%d configurations; the crossed matrix over four tags is 20", len(configs))
	}
	for _, c := range configs {
		t.Run(c.String(), func(t *testing.T) {
			deps := listDeps(t, root, c)
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

// configuration is one point in the crossed matrix process_build.md's
// Reachability Claims rule requires: an explicit tag selection, a supported
// target (ADR 0026's linux/amd64 and linux/arm64), and a cgo setting. The
// axes are CROSSED, not evaluated separately, because a file can carry both
// a tag and a platform suffix.
type configuration struct {
	tags   []string
	goos   string
	goarch string
	cgo    string
}

func (c configuration) String() string {
	return fmt.Sprintf("tags=%s/%s-%s/cgo=%s", strings.Join(c.tags, ","), c.goos, c.goarch, c.cgo)
}

// buildConfigurations enumerates the matrix. The tag axis is re-derived from
// the tree rather than listed, since hand-maintained enumerations have failed
// three times in this repository: no tags, then each bare tag the tree
// declares, one at a time. At the time of writing every //go:build
// expression is a single bare tag; a negated or compound expression would
// need the crossing item 1 describes, and this fails loudly rather than
// evaluating it wrong.
func buildConfigurations(t *testing.T, root string) []configuration {
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
	tagSets := [][]string{nil}
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
			tagSets = append(tagSets, []string{m[1]})
		}
	}
	sort.Slice(tagSets[1:], func(i, j int) bool { return tagSets[i+1][0] < tagSets[j+1][0] })

	var configs []configuration
	for _, tags := range tagSets {
		for _, target := range [][2]string{{"linux", "amd64"}, {"linux", "arm64"}} {
			for _, cgo := range []string{"0", "1"} {
				configs = append(configs, configuration{tags: tags, goos: target[0], goarch: target[1], cgo: cgo})
			}
		}
	}
	return configs
}

// listDeps is `go list -deps` for the package under one configuration.
// Cross-listing needs no toolchain for the target: `go list` resolves file
// selection and imports without compiling.
func listDeps(t *testing.T, root string, c configuration) []string {
	t.Helper()
	args := []string{"list", "-deps"}
	if len(c.tags) != 0 {
		args = append(args, "-tags", strings.Join(c.tags, ","))
	}
	args = append(args, seamPackage)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS="+c.goos, "GOARCH="+c.goarch, "CGO_ENABLED="+c.cgo)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go %s under %s: %v\n%s", strings.Join(args, " "), c, err, stderr.String())
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

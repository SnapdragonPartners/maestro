package orchestrator_test

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

// Named because the design names them: pkg/persistence, pkg/state and
// pkg/config are the v1 packages the closure rule exists to keep out, and
// they are refused by the exact set above like every other in-module
// package not on it -- not by a deny-list that would let pkg/agent through.

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
	configs := buildConfigurations(t, root)
	if len(configs) < 20 {
		t.Fatalf("%d configurations; the crossed matrix over four tags is 20", len(configs))
	}
	for _, c := range configs {
		t.Run(c.String(), func(t *testing.T) {
			assertMatrixReachesTheToolchain(t, root, c)
			deps := listDeps(t, root, c, seamPackage)
			var offending []string
			for _, dep := range deps {
				// Every in-module dependency must be on the list: an exact
				// set, not a deny-list, so pkg/agent or internal/supervisor
				// is refused the same way stack is.
				if strings.HasPrefix(dep, "orchestrator/") && !slices.Contains(allowedClosure, dep) {
					offending = append(offending, dep)
				}
			}
			if len(offending) != 0 {
				t.Fatalf("the Orchestrator reaches %v.\n"+
					"Its in-module closure must be exactly %v: it sees store.Store and nothing below it, "+
					"nothing of v1, and is handed an Opener rather than a composer (design D2, D3).",
					offending, allowedClosure)
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
	bare := regexp.MustCompile(`^//go:build (!?)([A-Za-z0-9_]+)$`)
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
		// `cgo` is the toolchain's constraint, not a repository tag: the
		// matrix varies it through CGO_ENABLED, and passing it as -tags
		// would force it on regardless. The positive-control fixture is
		// what declares it.
		if m[2] == "cgo" {
			continue
		}
		if m[1] == "!" {
			t.Fatalf("build constraint %q is negated; only the toolchain's cgo constraint may be", line)
		}
		if !seen[m[2]] {
			seen[m[2]] = true
			tagSets = append(tagSets, []string{m[2]})
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
func listDeps(t *testing.T, root string, c configuration, pkg string) []string {
	t.Helper()
	args := []string{"list", "-deps"}
	if len(c.tags) != 0 {
		args = append(args, "-tags", strings.Join(c.tags, ","))
	}
	args = append(args, pkg)
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

// fixturePackage has a different import set per matrix cell, selected by
// filename suffix and by the cgo constraint. Listing it proves the cell's
// environment reached the toolchain: without it every cell resolves to the
// host, and a guard that dropped the environment would stay green because
// the guarded package's closure happens to be identical across cells today.
const fixturePackage = "orchestrator/internal/dataplane/closurefixture"

// assertMatrixReachesTheToolchain is the positive control for one cell.
// THE MUTANT: drop the GOOS/GOARCH/CGO_ENABLED assignment in listDeps; every
// cell then reports the host's selection and the wrong cells fail here.
func assertMatrixReachesTheToolchain(t *testing.T, root string, c configuration) {
	t.Helper()
	deps := listDeps(t, root, c, fixturePackage)
	want := map[string]bool{
		"encoding/base32":  c.goarch == "amd64",
		"encoding/ascii85": c.goarch == "arm64",
		"hash/adler32":     c.cgo == "1",
		"hash/fnv":         c.cgo == "0",
	}
	for pkg, present := range want {
		if slices.Contains(deps, pkg) != present {
			t.Fatalf("under %s the fixture %s reach %s; the configuration did not reach the toolchain, "+
				"so this cell's closure is the host's, not the one it claims", c, map[bool]string{true: "should", false: "should not"}[present], pkg)
		}
	}
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

package stack

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"gopkg.in/yaml.v3"

	"orchestrator/internal/dataplane/paths"
)

// composeStatefulServices reads the shipped Compose file and returns the
// services that bind-mount a directory under the data root.
//
// It keys off the mount SOURCE naming a MAESTRO_*_DATA_DIR variable rather
// than off the service name, because the question the registry answers is
// "which services own state under the data root", not "which services
// exist". A service added with no such mount is correctly absent from both
// sides of the comparison.
func composeStatefulServices(t *testing.T) []string {
	t.Helper()

	var file struct {
		Services map[string]struct {
			Volumes []string `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(composeSource(t)), &file); err != nil {
		t.Fatalf("parse the compose file: %v", err)
	}

	var stateful []string
	for name, service := range file.Services {
		for _, volume := range service.Volumes {
			source, _, ok := strings.Cut(volume, ":")
			if ok && strings.Contains(source, "_DATA_DIR}") {
				stateful = append(stateful, name)
				break
			}
		}
	}
	sort.Strings(stateful)
	return stateful
}

// The registry and the Compose file must agree, in BOTH directions.
//
// One direction catches a service added to Compose without being
// registered — which would leave its bind-mount source uncreated, so Docker
// would create it as root and the container would fail to write it. The
// other catches a registered service with no Compose presence, which would
// have `up` creating a directory nothing ever mounts.
//
// This test is what lets the registry stay a Go constant rather than being
// derived from resolved Compose configuration at runtime (Phase 2 plan,
// amended 2026-08-01): drift is loud, and the derivation's cost — one
// subprocess per lifecycle operation, and a malformed Compose file becoming
// a lifecycle failure — is not paid.
func TestServicesRegistryMatchesCompose(t *testing.T) {
	registered := make([]string, 0, len(paths.Services()))
	for _, service := range paths.Services() {
		registered = append(registered, string(service))
	}
	sort.Strings(registered)

	composed := composeStatefulServices(t)

	if strings.Join(registered, ",") != strings.Join(composed, ",") {
		t.Errorf("registry and compose disagree about stateful services:\n\tpaths.Services() = %v\n\tcompose.yaml     = %v\n"+
			"add the service to both, or to neither", registered, composed)
	}
}

// planeEvidence decides whether `up` may mint a root key over what is
// already in the data root, so what it counts as evidence is the whole
// safety property. Each case below is a state that has to be classified
// correctly for a different reason.
func TestPlaneEvidence(t *testing.T) {
	lockPath := func(root string) string { return filepath.Join(root, LifecycleLockFile) }

	tests := []struct {
		name    string
		setup   func(t *testing.T, root string)
		want    bool // fresh
		wantErr bool
	}{
		{
			name:  "empty root is fresh",
			setup: func(*testing.T, string) {},
			want:  true,
		},
		{
			// The first-run case: `up` creates the service directories
			// BEFORE it asks whether the root is fresh, so counting empty
			// directories would refuse to provision a clean checkout.
			name: "empty service directories are fresh",
			setup: func(t *testing.T, root string) {
				for _, service := range paths.Services() {
					mustMkdir(t, filepath.Join(root, string(service)))
				}
			},
			want: true,
		},
		{
			// `up` takes the lifecycle lock before judging freshness, so the
			// lock file is present on every first run. It is also never
			// unlinked by design (ADR 0027).
			name: "the lifecycle lock alone is fresh",
			setup: func(t *testing.T, root string) {
				mustWrite(t, lockPath(root), nil)
			},
			want: true,
		},
		{
			name: "a regular file is evidence",
			setup: func(t *testing.T, root string) {
				mustMkdir(t, filepath.Join(root, "postgres"))
				mustWrite(t, filepath.Join(root, "postgres", "PG_VERSION"), []byte("18\n"))
			},
			want: false,
		},
		{
			// A stray file nobody's service put there still counts. Refusing
			// is the safe direction; the error names the path so the case is
			// diagnosable.
			name: "an incidental file at the root is evidence",
			setup: func(t *testing.T, root string) {
				mustWrite(t, filepath.Join(root, ".DS_Store"), []byte("x"))
			},
			want: false,
		},
		{
			name: "a symlink is evidence",
			setup: func(t *testing.T, root string) {
				if err := os.Symlink("/etc/hosts", filepath.Join(root, "link")); err != nil {
					t.Fatalf("symlink: %v", err)
				}
			},
			want: false,
		},
		{
			// The case a regular-files-only rule would have called fresh.
			// Nothing we ship creates a FIFO here, which is exactly why an
			// unrecognised entry must not read as emptiness.
			name: "a FIFO is evidence",
			setup: func(t *testing.T, root string) {
				if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
					t.Skipf("mkfifo unsupported here: %v", err)
				}
			},
			want: false,
		},
		{
			// Nothing-known is not emptiness. Answering "fresh" here would
			// authorise minting a key over a root we could not read.
			name: "an unreadable subdirectory is an error, not fresh",
			setup: func(t *testing.T, root string) {
				if os.Geteuid() == 0 {
					t.Skip("root traverses unreadable directories")
				}
				dir := filepath.Join(root, "postgres")
				mustMkdir(t, dir)
				mustWrite(t, filepath.Join(dir, "PG_VERSION"), []byte("18\n"))
				if err := os.Chmod(dir, 0o000); err != nil {
					t.Fatalf("chmod: %v", err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := planeAt(t)
			test.setup(t, cfg.Roots.Data)

			fresh, err := dataRootIsEmpty(cfg)
			switch {
			case test.wantErr && err == nil:
				t.Fatalf("want an error, got fresh=%v", fresh)
			case test.wantErr:
				return
			case err != nil:
				t.Fatalf("planeEvidence: %v", err)
			case fresh != test.want:
				t.Errorf("fresh = %v, want %v", fresh, test.want)
			}
		})
	}
}

// The refusal must name what it found, so an incidental file is
// distinguishable from a real plane without the operator going to look.
func TestPlaneEvidenceNamesThePaths(t *testing.T) {
	cfg := planeAt(t)
	mustWrite(t, filepath.Join(cfg.Roots.Data, ".DS_Store"), []byte("x"))

	evidence, err := planeEvidence(cfg)
	if err != nil {
		t.Fatalf("planeEvidence: %v", err)
	}
	if len(evidence) != 1 || !strings.HasSuffix(evidence[0], ".DS_Store") {
		t.Fatalf("evidence = %v, want the offending path named", evidence)
	}
}

// A provisioned cluster holds thousands of files; the error names a
// handful. Unbounded, the message would be unreadable in the one place it
// has to be read.
func TestPlaneEvidenceIsBounded(t *testing.T) {
	cfg := planeAt(t)
	dir := filepath.Join(cfg.Roots.Data, "postgres")
	mustMkdir(t, dir)
	for i := range maxEvidencePaths * 3 {
		mustWrite(t, filepath.Join(dir, string(rune('a'+i))), []byte("x"))
	}

	evidence, err := planeEvidence(cfg)
	if err != nil {
		t.Fatalf("planeEvidence: %v", err)
	}
	if len(evidence) > maxEvidencePaths {
		t.Errorf("named %d paths, want at most %d", len(evidence), maxEvidencePaths)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

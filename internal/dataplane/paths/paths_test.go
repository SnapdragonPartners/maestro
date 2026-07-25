package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envOf builds a getenv func from a map, so cases stay declarative.
func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolve(t *testing.T) {
	const home = "/home/u"

	tests := []struct {
		name string
		goos string
		env  map[string]string
		want Roots
	}{
		{
			name: "linux defaults",
			goos: "linux",
			want: Roots{
				Config: "/home/u/.config/maestro/config",
				Cache:  "/home/u/.cache/maestro/cache",
				State:  "/home/u/.local/state/maestro/state",
				Data:   "/home/u/.local/share/maestro/data",
			},
		},
		{
			name: "linux honours XDG overrides",
			goos: "linux",
			env: map[string]string{
				"XDG_CONFIG_HOME": "/x/cfg",
				"XDG_CACHE_HOME":  "/x/cache",
				"XDG_STATE_HOME":  "/x/state",
				"XDG_DATA_HOME":   "/x/data",
			},
			want: Roots{
				Config: "/x/cfg/maestro/config",
				Cache:  "/x/cache/maestro/cache",
				State:  "/x/state/maestro/state",
				Data:   "/x/data/maestro/data",
			},
		},
		{
			// Per the XDG spec a relative base is invalid and must be
			// ignored, not joined onto the working directory.
			name: "linux ignores relative XDG values",
			goos: "linux",
			env:  map[string]string{"XDG_DATA_HOME": "relative/share"},
			want: Roots{
				Config: "/home/u/.config/maestro/config",
				Cache:  "/home/u/.cache/maestro/cache",
				State:  "/home/u/.local/state/maestro/state",
				Data:   "/home/u/.local/share/maestro/data",
			},
		},
		{
			name: "darwin separates the three roots sharing a base",
			goos: "darwin",
			want: Roots{
				Config: "/home/u/Library/Application Support/maestro/config",
				Cache:  "/home/u/Library/Caches/maestro/cache",
				State:  "/home/u/Library/Application Support/maestro/state",
				Data:   "/home/u/Library/Application Support/maestro/data",
			},
		},
		{
			// The override IS the base — no "maestro" component. The user
			// already named the directory, and ~/.maestro/maestro/config
			// is not the documented contract.
			name: "MAESTRO_HOME collapses all four as direct subdirectories",
			goos: "linux",
			env:  map[string]string{HomeEnv: "/opt/maestro"},
			want: Roots{
				Config: "/opt/maestro/config",
				Cache:  "/opt/maestro/cache",
				State:  "/opt/maestro/state",
				Data:   "/opt/maestro/data",
			},
		},
		{
			name: "MAESTRO_HOME overrides XDG and platform alike",
			goos: "darwin",
			env:  map[string]string{HomeEnv: "/opt/m", "XDG_DATA_HOME": "/x/data"},
			want: Roots{
				Config: "/opt/m/config",
				Cache:  "/opt/m/cache",
				State:  "/opt/m/state",
				Data:   "/opt/m/data",
			},
		},
		{
			name: "MAESTRO_HOME is cleaned",
			goos: "linux",
			env:  map[string]string{HomeEnv: "/opt/./m/sub/.."},
			want: Roots{
				Config: "/opt/m/config",
				Cache:  "/opt/m/cache",
				State:  "/opt/m/state",
				Data:   "/opt/m/data",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolve(tc.goos, envOf(tc.env), home)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got != tc.want {
				t.Errorf("roots mismatch\n got: %+v\nwant: %+v", got, tc.want)
			}
		})
	}
}

// The macOS base collision is the reason this package exists, so assert the
// property directly rather than trusting the golden paths above to encode
// it. Runs on any host: the platform is injected.
func TestRootsAreDistinctAndNonNested(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		for _, env := range []map[string]string{nil, {HomeEnv: "/opt/m"}} {
			got, err := resolve(goos, envOf(env), "/home/u")
			if err != nil {
				t.Fatalf("resolve(%s): %v", goos, err)
			}
			roots := got.All()
			for i := range roots {
				for j := range roots {
					if i == j {
						continue
					}
					if roots[i] == roots[j] {
						t.Errorf("%s env=%v: roots %d and %d are the same path %q", goos, env, i, j, roots[i])
					}
					// Containment matters specifically for Config and Data:
					// backup copies Data and must exclude the key in Config.
					if isAncestor(roots[i], roots[j]) {
						t.Errorf("%s env=%v: root %q contains root %q", goos, env, roots[i], roots[j])
					}
				}
			}
		}
	}
}

func isAncestor(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, "..")
}

func TestResolveRejects(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		env     map[string]string
		wantSub string
	}{
		{
			name:    "relative MAESTRO_HOME",
			goos:    "linux",
			env:     map[string]string{HomeEnv: "relative/dir"},
			wantSub: "absolute path",
		},
		{
			name:    "unsupported platform",
			goos:    "windows",
			wantSub: "unsupported platform",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolve(tc.goos, envOf(tc.env), "/home/u")
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not mention %q", err, tc.wantSub)
			}
		})
	}
}

func TestEnsureCreatesRootsWithTightPermissions(t *testing.T) {
	base := t.TempDir()
	roots, err := resolve("linux", envOf(map[string]string{HomeEnv: base}), "/home/u")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := roots.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, dir := range roots.All() {
		info, statErr := os.Stat(dir)
		if statErr != nil {
			t.Fatalf("stat %s: %v", dir, statErr)
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
		if perm := info.Mode().Perm(); perm != rootPerm {
			t.Errorf("%s has mode %#o, want %#o", dir, perm, rootPerm)
		}
	}
	// Ensure is the everyday path, not just first-run setup.
	if err := roots.Ensure(); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
}

func TestEnsureRefusesWidenedRoot(t *testing.T) {
	base := t.TempDir()
	roots, err := resolve("linux", envOf(map[string]string{HomeEnv: base}), "/home/u")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := roots.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	if chmodErr := os.Chmod(roots.Data, 0o755); chmodErr != nil {
		t.Fatalf("chmod: %v", chmodErr)
	}
	if err := roots.Ensure(); !errors.Is(err, ErrRootPermissions) {
		t.Fatalf("got %v, want ErrRootPermissions", err)
	}

	// As with the key file, the widened directory is left exactly as
	// found — repairing it would erase the evidence.
	info, statErr := os.Stat(roots.Data)
	if statErr != nil {
		t.Fatalf("stat: %v", statErr)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("permissions changed to %#o; Ensure must not repair them", perm)
	}
}

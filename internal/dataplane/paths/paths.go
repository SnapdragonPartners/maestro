// Package paths resolves Maestro's four local storage roots and manages the
// root-of-trust key file that anchors the secrets vault.
//
// The four-root split (config, cache, state, data) comes from the Phase 0
// project-folder spike: v2 has no "project directory", and what remains on
// disk is split by function into OS-standard locations. ADR 0022's local
// durability invariant makes the data root the single durable location, and
// the cold-backup operation copies exactly that root while deliberately
// excluding the unlock key under the config root.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	// HomeEnv overrides all four platform bases with a single directory,
	// yielding the classic ~/.maestro layout. The four roots remain named
	// subdirectories of it — never flattened, so the split's semantics
	// travel with the override.
	HomeEnv = "MAESTRO_HOME"

	// appDir is Maestro's directory under each platform base.
	appDir = "maestro"

	// Every root is a named subdirectory of its platform base, on every
	// platform. macOS maps config, state, and data to the same base
	// (~/Library/Application Support), so without these names three roots
	// would collide — and the only non-colliding alternative nests the data
	// root inside the config root, which is precisely wrong for a backup
	// boundary that copies the former and must exclude a key in the latter.
	dirConfig = "config"
	dirCache  = "cache"
	dirState  = "state"
	dirData   = "data"

	// rootPerm is deliberately tight: these roots hold the Postgres cluster,
	// the object store, and the secrets unlock key. None of it is other
	// users' business on a shared machine.
	rootPerm = 0o700
)

// Roots holds Maestro's four local storage roots. They are always distinct
// paths, and Data is never an ancestor or descendant of Config.
type Roots struct {
	// Config holds the data-plane bootstrap pointer and the root-of-trust
	// key file, and nothing else. Excluded from backup by design.
	Config string
	// Cache holds mirrors and reconstructible workspaces only. The OS may
	// purge it, so nothing whose only copy is local may live here.
	Cache string
	// State holds active workspaces, which until pushed hold the only copy
	// of real work. Non-purgeable.
	State string
	// Data is the single durable root: the bind-mounted Postgres and object
	// store, and the airplane-mode forge's data. This is what backup copies.
	Data string
}

// All returns the four roots in a stable order, for callers that need to
// create or inspect every one of them.
func (r Roots) All() []string {
	return []string{r.Config, r.Cache, r.State, r.Data}
}

// Ensure creates every root that does not yet exist, with restrictive
// permissions. It does not repair the permissions of directories that
// already exist: silently widening or tightening a directory a user or an
// earlier version created would hide a real change rather than surface it.
func (r Roots) Ensure() error {
	for _, dir := range r.All() {
		if err := os.MkdirAll(dir, rootPerm); err != nil {
			return fmt.Errorf("create maestro root %s: %w", dir, err)
		}
	}
	return nil
}

// Resolve returns the four roots for the current user and platform.
func Resolve() (Roots, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Roots{}, fmt.Errorf("locate home directory: %w", err)
	}
	return resolve(runtime.GOOS, os.Getenv, home)
}

// resolve is the testable core of Resolve. Injecting the platform, the
// environment, and the home directory lets one platform's CI assert
// another's layout — which matters here, because the collision this
// package exists to avoid only occurs on macOS.
func resolve(goos string, getenv func(string) string, home string) (Roots, error) {
	if override := getenv(HomeEnv); override != "" {
		if !filepath.IsAbs(override) {
			return Roots{}, fmt.Errorf("%s must be an absolute path, got %q", HomeEnv, override)
		}
		return rootsUnder(filepath.Clean(override), filepath.Clean(override), filepath.Clean(override), filepath.Clean(override)), nil
	}

	switch goos {
	case "linux":
		return rootsUnder(
			xdg(getenv, "XDG_CONFIG_HOME", filepath.Join(home, ".config")),
			xdg(getenv, "XDG_CACHE_HOME", filepath.Join(home, ".cache")),
			xdg(getenv, "XDG_STATE_HOME", filepath.Join(home, ".local", "state")),
			xdg(getenv, "XDG_DATA_HOME", filepath.Join(home, ".local", "share")),
		), nil
	case "darwin":
		appSupport := filepath.Join(home, "Library", "Application Support")
		caches := filepath.Join(home, "Library", "Caches")
		return rootsUnder(appSupport, caches, appSupport, appSupport), nil
	default:
		// Docker is already load-bearing for every mode (ADR 0022) and WSL
		// is the documented path on Windows. A half-working %AppData% guess
		// would be worse than refusing.
		return Roots{}, fmt.Errorf("unsupported platform %q: use Linux or macOS (on Windows, run Maestro under WSL, and set %s if you want an explicit location)", goos, HomeEnv)
	}
}

// rootsUnder assembles the four roots from their platform bases. Each root
// is <base>/maestro/<name>, so roots sharing a base stay distinct siblings.
func rootsUnder(configBase, cacheBase, stateBase, dataBase string) Roots {
	return Roots{
		Config: filepath.Join(configBase, appDir, dirConfig),
		Cache:  filepath.Join(cacheBase, appDir, dirCache),
		State:  filepath.Join(stateBase, appDir, dirState),
		Data:   filepath.Join(dataBase, appDir, dirData),
	}
}

// xdg returns the value of an XDG base-directory variable, falling back to
// the spec's default. Per the spec a relative value is ignored rather than
// honoured, since XDG base directories must be absolute.
func xdg(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); filepath.IsAbs(v) {
		return filepath.Clean(v)
	}
	return fallback
}

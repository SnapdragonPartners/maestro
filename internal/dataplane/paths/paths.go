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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"orchestrator/pkg/utils"
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

// ErrRootPermissions reports a storage root that exists with permissions
// other than 0700. Like ErrKeyPermissions it is distinct because the
// correct response is to investigate, not to retry.
var ErrRootPermissions = errors.New("maestro storage root has unexpected permissions")

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

// Service names a data-plane service that owns a child of the data root.
//
// It is a named type over a closed set rather than a bare string because
// the value becomes a filesystem path: an unvalidated "../config" would
// escape the data root entirely, and the caller would be pointing a
// container's bind mount at the directory holding the unlock key.
type Service string

// The services with bind-mounted state under the data root. This set is
// closed: adding a service means adding a constant here and to
// knownServices, which is a reviewed change rather than a caller's choice.
const (
	ServicePostgres Service = "postgres"
	ServiceMinIO    Service = "minio"
)

// knownServices is the membership test behind Service.validate.
//
//nolint:gochecknoglobals // Immutable lookup table for a closed constant set.
var knownServices = map[Service]bool{
	ServicePostgres: true,
	ServiceMinIO:    true,
}

// ErrInvalidService reports a service name that is not one of the known
// services, or whose value is not a safe single path segment.
var ErrInvalidService = errors.New("invalid data-plane service name")

// ErrServiceDataDir reports a service data directory that exists but
// cannot be used by the identity the containers run as.
var ErrServiceDataDir = errors.New("unusable service data directory")

// validate enforces membership in the known set, then re-checks that the
// value is a safe path segment.
//
// The second check is not redundant with the first: it guards against a
// future constant being defined with a traversing value, which membership
// alone would happily accept. Both matter because this value becomes a
// directory under the data root.
func (s Service) validate() error {
	if !knownServices[s] {
		return fmt.Errorf("%w: %q is not a known data-plane service", ErrInvalidService, string(s))
	}
	name := string(s)
	switch {
	case name == "" || name == "." || name == "..":
		return fmt.Errorf("%w: %q is not a usable directory name", ErrInvalidService, name)
	case strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator):
		return fmt.Errorf("%w: %q contains a path separator", ErrInvalidService, name)
	case name != filepath.Clean(name):
		return fmt.Errorf("%w: %q is not a clean path segment", ErrInvalidService, name)
	}
	return nil
}

// ownershipError describes a wrongly-owned service directory and what to
// do about it.
//
// Two things it deliberately does not do. It never suggests deleting the
// directory: this error surfaces on existing installations, where that
// directory holds the authoritative Postgres cluster or object store, so
// the remedy would destroy what the check exists to protect. And it prints
// no copy-pasteable shell command: the default macOS data root contains a
// space (`~/Library/Application Support/...`) and MAESTRO_HOME may contain
// shell metacharacters, so a suggested command would break at best and
// expand into something unintended at worst. It states the action and the
// target values, and lets the operator use their own tooling. The path is
// quoted so its bounds are unambiguous, not so it can be pasted.
func ownershipError(dir string, ownerUID, wantUID, wantGID int) error {
	return fmt.Errorf(
		"%w: %q is owned by uid %d, but Maestro runs as uid %d — containers run as the invoking user, so this directory is unusable. "+
			"It was most likely created by Docker as root. Stop the data-plane stack, inspect the directory, and recursively change "+
			"its ownership to uid %d and gid %d using an administrative tool. Do not delete it: it may hold the authoritative data",
		ErrServiceDataDir, dir, ownerUID, wantUID, wantUID, wantGID)
}

// KeyPath returns the absolute path of the root-of-trust key file.
func (r Roots) KeyPath() string {
	return filepath.Join(r.Config, KeyFileName)
}

// ServiceDataDir returns the bind-mount source for one data-plane service.
//
// Each service gets its own child of the data root rather than sharing it,
// so a container mounts its own directory as its mount root and never has
// to traverse the host parent. That is what lets the data root itself stay
// 0700 while the containers still write freely.
func (r Roots) ServiceDataDir(service Service) (string, error) {
	if err := service.validate(); err != nil {
		return "", err
	}
	return filepath.Join(r.Data, string(service)), nil
}

// EnsureServiceDataDirs creates the per-service bind-mount sources, owned
// by the invoking user with the same tight mode as the roots.
//
// They are pre-created deliberately: left to Compose, Docker creates
// missing bind-mount sources as root, which then cannot be written by a
// container running as the invoking user — and cannot be cleaned up
// without sudo.
//
// Host ownership is only correct because the containers are run as the
// invoking user (`user: "${MAESTRO_UID}:${MAESTRO_GID}"`, design D2a).
// Neither image's default uid — 999 for Postgres, 1000 for MinIO — can
// write a directory owned by someone else, so if that Compose setting is
// ever dropped, native Linux will fail here at container start while macOS
// keeps working, because Docker Desktop virtualises ownership. The two
// halves must change together.
func (r Roots) EnsureServiceDataDirs(services ...Service) error {
	for _, service := range services {
		dir, err := r.ServiceDataDir(service)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, rootPerm); err != nil {
			return fmt.Errorf("create service data directory %s: %w", dir, err)
		}
		if err := verifyOwnedAndWritable(dir); err != nil {
			return err
		}
	}
	return nil
}

// verifyOwnedAndWritable checks that an existing service directory is
// usable by the identity the containers will run as.
//
// MkdirAll succeeds on a directory that already exists no matter who owns
// it, which is the dangerous case: a directory Docker created as root on
// an earlier run passes creation and then fails at container start, far
// from the cause. That is precisely the failure D2a exists to prevent, so
// it is detected here with an actionable message rather than repaired —
// chowning someone else's directory is not ours to do, and would need root
// anyway.
func verifyOwnedAndWritable(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat service data directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrServiceDataDir, dir)
	}
	if perm := info.Mode().Perm(); perm != rootPerm {
		return fmt.Errorf("%w: %s has mode %#o, want %#o", ErrServiceDataDir, dir, perm, rootPerm)
	}

	stat, ok := utils.SafeAssert[*syscall.Stat_t](info.Sys())
	if !ok {
		return fmt.Errorf("%w: cannot read ownership of %s on this platform", ErrServiceDataDir, dir)
	}
	if uid := os.Getuid(); int(stat.Uid) != uid {
		return ownershipError(dir, int(stat.Uid), uid, os.Getgid())
	}

	// Ownership and mode imply writability for the owner on an ordinary
	// filesystem, but not on a read-only mount — where MkdirAll above is a
	// silent no-op. Probe rather than infer.
	probe, err := os.CreateTemp(dir, ".maestro-write-probe-*")
	if err != nil {
		return fmt.Errorf("%w: %s is not writable: %w", ErrServiceDataDir, dir, err)
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		return fmt.Errorf("close write probe in %s: %w", dir, err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("remove write probe %s: %w", name, err)
	}
	return nil
}

// All returns the four roots in a stable order, for callers that need to
// create or inspect every one of them.
func (r Roots) All() []string {
	return []string{r.Config, r.Cache, r.State, r.Data}
}

// Ensure creates every root that does not yet exist, with restrictive
// permissions, and refuses any that already exists with different ones.
//
// Refusing rather than repairing matches the key-file policy for the same
// reason: these roots hold the Postgres cluster, the object store, and the
// secrets unlock key, so a widened directory is a possible exposure and a
// silent chmod destroys the only evidence that it happened. Refusing rather
// than ignoring is the other half — a rule nothing enforces is not a rule.
func (r Roots) Ensure() error {
	for _, dir := range r.All() {
		if err := os.MkdirAll(dir, rootPerm); err != nil {
			return fmt.Errorf("create maestro root %s: %w", dir, err)
		}
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("stat maestro root %s: %w", dir, err)
		}
		if perm := info.Mode().Perm(); perm != rootPerm {
			return fmt.Errorf("%w: %s is %#o, want %#o", ErrRootPermissions, dir, perm, rootPerm)
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
		// The override IS the base: <dir>/{config,cache,state,data}, with
		// no "maestro" component. The user already named the directory.
		dir := filepath.Clean(override)
		return Roots{
			Config: filepath.Join(dir, dirConfig),
			Cache:  filepath.Join(dir, dirCache),
			State:  filepath.Join(dir, dirState),
			Data:   filepath.Join(dir, dirData),
		}, nil
	}

	switch goos {
	case "linux":
		return platformRoots(
			xdg(getenv, "XDG_CONFIG_HOME", filepath.Join(home, ".config")),
			xdg(getenv, "XDG_CACHE_HOME", filepath.Join(home, ".cache")),
			xdg(getenv, "XDG_STATE_HOME", filepath.Join(home, ".local", "state")),
			xdg(getenv, "XDG_DATA_HOME", filepath.Join(home, ".local", "share")),
		), nil
	case "darwin":
		appSupport := filepath.Join(home, "Library", "Application Support")
		caches := filepath.Join(home, "Library", "Caches")
		return platformRoots(appSupport, caches, appSupport, appSupport), nil
	default:
		// Docker is already load-bearing for every mode (ADR 0022) and WSL
		// is the documented path on Windows. A half-working %AppData% guess
		// would be worse than refusing.
		return Roots{}, fmt.Errorf("unsupported platform %q: use Linux or macOS (on Windows, run Maestro under WSL, and set %s if you want an explicit location)", goos, HomeEnv)
	}
}

// platformRoots assembles the four roots from OS base directories. Each is
// <base>/maestro/<name>: the "maestro" component scopes us inside shared
// OS directories, and the trailing name keeps roots that share a base
// (macOS) distinct siblings. Under MAESTRO_HOME neither problem exists, so
// that path does not go through here.
func platformRoots(configBase, cacheBase, stateBase, dataBase string) Roots {
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

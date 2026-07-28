package stack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/paths"
)

// DefaultComposeFile is the Compose file's path relative to the repository
// root, which is where make targets run.
const DefaultComposeFile = "deploy/dataplane/compose.yaml"

// readyTimeout bounds the wait for both services. The first start runs
// initdb, which dominates it; steady-state startup is a few seconds.
const readyTimeout = 3 * time.Minute

// probeTimeout bounds a single readiness probe, so one hung call cannot
// consume the whole budget.
const probeTimeout = 20 * time.Second

// ErrNotReady reports a stack that did not become healthy in time.
var ErrNotReady = errors.New("data plane did not become ready")

// LifecycleLockFile serializes up/down/reset against one data plane. It
// lives at the data root — the resource being protected — and is never
// unlinked (ADR 0027: unlinking a held lock file lets a second caller lock
// a fresh inode at the same path, producing two "exclusive" holders).
const LifecycleLockFile = ".maestro-dataplane.lock"

// lockLifecycle serializes a whole lifecycle operation across processes.
//
// Without it, a `reset` can delete service data while an `up` in another
// terminal is midway through initdb — the destructive-recovery hazard ADR
// 0027 names, where recovery runs concurrently with a live writer. The
// lock is held across the ENTIRE operation, not just its first step,
// because the window that matters spans the whole of initdb.
func lockLifecycle(c *Config) (func() error, error) {
	if err := os.MkdirAll(c.Roots.Data, 0o700); err != nil {
		return nil, fmt.Errorf("create data root %s: %w", c.Roots.Data, err)
	}
	release, err := paths.AcquireLock(filepath.Join(c.Roots.Data, LifecycleLockFile))
	if err != nil {
		return nil, fmt.Errorf("acquire data-plane lifecycle lock: %w", err)
	}
	return release, nil
}

// Up brings the stack up and waits until both services are usable.
//
// It is idempotent: the "one command from a clean checkout" criterion and
// the everyday inner-loop command are the same command, so re-running it
// on a healthy stack must be a no-op rather than a restart.
func Up(ctx context.Context, c *Config, composeFile string) (err error) {
	release, lockErr := lockLifecycle(c)
	if lockErr != nil {
		return lockErr
	}
	defer func() {
		if relErr := release(); relErr != nil && err == nil {
			err = relErr
		}
	}()
	return up(ctx, c, composeFile)
}

func up(ctx context.Context, c *Config, composeFile string) error {
	if err := c.Roots.Ensure(); err != nil {
		return fmt.Errorf("prepare storage roots: %w", err)
	}
	if err := c.Roots.EnsureServiceDataDirs(paths.ServicePostgres, paths.ServiceMinIO); err != nil {
		return fmt.Errorf("prepare service data directories: %w", err)
	}

	rootKey, keyErr := paths.EnsureKey(c.Roots.Config)
	if keyErr != nil {
		return fmt.Errorf("ensure root-of-trust key: %w", keyErr)
	}
	if bootErr := paths.WriteBootstrap(c.Roots.Config, c.Bootstrap()); bootErr != nil {
		return fmt.Errorf("write bootstrap pointer: %w", bootErr)
	}

	env, envErr := c.composeEnv(rootKey)
	if envErr != nil {
		return envErr
	}
	if err := compose(ctx, composeFile, env, "up", "-d", "--wait=false"); err != nil {
		return err
	}
	if err := waitReady(ctx, c, composeFile, env); err != nil {
		return err
	}
	return migrateLocked(ctx, c, rootKey)
}

// Migrate applies pending migrations to an already-running stack.
//
// It takes the lifecycle lock like every other operation: it mutates the
// same data plane, and a migration running against a plane that `reset` is
// concurrently emptying is exactly the interleaving the lock exists to
// prevent.
func Migrate(ctx context.Context, c *Config) (err error) {
	release, lockErr := lockLifecycle(c)
	if lockErr != nil {
		return lockErr
	}
	defer func() {
		if relErr := release(); relErr != nil && err == nil {
			err = relErr
		}
	}()

	rootKey, keyErr := paths.EnsureKey(c.Roots.Config)
	if keyErr != nil {
		return fmt.Errorf("ensure root-of-trust key: %w", keyErr)
	}
	return migrateLocked(ctx, c, rootKey)
}

// migrateLocked applies migrations, assuming the caller holds the
// lifecycle lock.
func migrateLocked(ctx context.Context, c *Config, rootKey []byte) error {
	dsn, err := c.DSN(rootKey)
	if err != nil {
		return err
	}
	if err := migrations.Up(ctx, dsn); err != nil {
		return fmt.Errorf("migrate data plane schema: %w", err)
	}
	return nil
}

// ForceVersion repairs a dirty schema version WITHOUT running migrations.
//
// A failed migration leaves the recorded version marked dirty --
// golang-migrate marks BEFORE executing -- and every later migration
// refuses until that is cleared. Fixing whatever caused the failure is not
// enough on its own; the metadata still claims a migration is half-applied.
//
// This exists because a migration's own recovery instructions must name an
// operation an operator can actually perform. It is deliberately narrow: it
// changes metadata only, and the caller is asserting the schema really is
// at the version being forced. A wrong assertion leaves the schema and its
// recorded version disagreeing, which no later migration can detect.
//
// Serialized on the lifecycle lock like every other stack operation, so it
// cannot race a concurrent migrate.
//
// Takes no context, unlike its neighbours: golang-migrate's Force is not
// context-aware, and accepting one this operation cannot honour would
// promise cancellation that never happens. It is a single metadata write.
func ForceVersion(c *Config, version int) (err error) {
	release, lockErr := lockLifecycle(c)
	if lockErr != nil {
		return lockErr
	}
	defer func() {
		if relErr := release(); relErr != nil && err == nil {
			err = relErr
		}
	}()

	rootKey, keyErr := paths.EnsureKey(c.Roots.Config)
	if keyErr != nil {
		return fmt.Errorf("ensure root-of-trust key: %w", keyErr)
	}
	dsn, dsnErr := c.DSN(rootKey)
	if dsnErr != nil {
		return dsnErr
	}
	if err := migrations.Force(dsn, version); err != nil {
		return fmt.Errorf("force data plane schema version: %w", err)
	}
	return nil
}

// Down stops the stack and leaves the data root untouched.
func Down(ctx context.Context, c *Config, composeFile string) (err error) {
	release, lockErr := lockLifecycle(c)
	if lockErr != nil {
		return lockErr
	}
	defer func() {
		if relErr := release(); relErr != nil && err == nil {
			err = relErr
		}
	}()
	return down(ctx, c, composeFile)
}

func down(ctx context.Context, c *Config, composeFile string) error {
	env, err := c.composeEnv(placeholderKey())
	if err != nil {
		return err
	}
	return compose(ctx, composeFile, env, "down")
}

// Reset stops the stack and deletes the contents of every service data
// directory. This is the only destructive operation here, and it is the
// caller's job to have confirmed it.
//
// The directories themselves are preserved rather than removed and
// recreated: they are bind-mount sources, and on macOS a recreated
// directory has a new inode, which leaves any existing mount pointing at
// the old one (the same hazard CLAUDE.md records for workspaces).
func Reset(ctx context.Context, c *Config, composeFile string) (err error) {
	release, lockErr := lockLifecycle(c)
	if lockErr != nil {
		return lockErr
	}
	defer func() {
		if relErr := release(); relErr != nil && err == nil {
			err = relErr
		}
	}()

	// down, not Down: the lock is already held and flock is not
	// re-entrant, so calling the exported form would deadlock against
	// this very goroutine.
	if err := down(ctx, c, composeFile); err != nil {
		return err
	}
	for _, service := range []paths.Service{paths.ServicePostgres, paths.ServiceMinIO} {
		dir, err := c.Roots.ServiceDataDir(service)
		if err != nil {
			return fmt.Errorf("locate %s data directory: %w", service, err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read %s: %w", dir, err)
		}
		for _, entry := range entries {
			target := filepath.Join(dir, entry.Name())
			if err := os.RemoveAll(target); err != nil {
				return fmt.Errorf("remove %s: %w", target, err)
			}
		}
	}
	return nil
}

// ImagePinsFile is the name of the digest pin file, which lives beside the
// Compose file so the two travel together.
const ImagePinsFile = "images.env"

// loadImagePins reads the digest pins beside the Compose file.
//
// They are loaded here rather than passed to Compose as an --env-file so
// that a missing or malformed pin is our error with our message. Compose's
// own failure mode is to substitute a blank string and then complain that
// a service "has neither an image nor a build context", which names
// neither the file nor the variable.
func loadImagePins(composeFile string) ([]string, error) {
	path := filepath.Join(filepath.Dir(composeFile), ImagePinsFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read image pins %s: %w", path, err)
	}

	lines := strings.Split(string(raw), "\n")
	pins := make([]string, 0, len(lines))
	required := map[string]bool{"MAESTRO_PG_IMAGE": false, "MAESTRO_MINIO_IMAGE": false}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, found := strings.Cut(trimmed, "=")
		if !found {
			return nil, fmt.Errorf("%s: malformed line %q", path, trimmed)
		}
		if !strings.Contains(value, "@sha256:") {
			return nil, fmt.Errorf("%s: %s is not pinned by digest (ADR 0026)", path, key)
		}
		if _, known := required[key]; known {
			required[key] = true
		}
		pins = append(pins, trimmed)
	}
	for key, present := range required {
		if !present {
			return nil, fmt.Errorf("%s does not define %s", path, key)
		}
	}
	return pins, nil
}

// composeOutput runs a docker compose subcommand and returns its combined
// output. Combined, because Compose reports the real cause (a port clash,
// an unwritable mount) on stderr, and losing it turns a diagnosable
// failure into "exit status 1".
func composeOutput(ctx context.Context, composeFile string, env []string, args ...string) ([]byte, error) {
	pins, err := loadImagePins(composeFile)
	if err != nil {
		return nil, err
	}
	full := append([]string{"compose", "--project-name", ProjectName, "--file", composeFile}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Env = append(append(os.Environ(), env...), pins...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("docker compose %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return out, nil
}

// compose runs a docker compose subcommand against the data-plane project.
func compose(ctx context.Context, composeFile string, env []string, args ...string) error {
	_, err := composeOutput(ctx, composeFile, env, args...)
	return err
}

// waitReady blocks until Postgres reports healthy and MinIO answers its
// liveness endpoint, or the deadline passes.
//
// readyTimeout is enforced by a context, not by a loop condition. A loop
// that only checks elapsed time between iterations cannot bound an
// iteration that never returns — a wedged `docker compose ps` or an
// accepted-but-silent HTTP connection would both hang indefinitely on the
// caller's context while the deadline passed unnoticed. Every probe gets
// its own bounded context derived from that one.
func waitReady(ctx context.Context, c *Config, composeFile string, env []string) error {
	waitCtx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()

	var lastErr error
	for {
		pgErr := postgresHealthy(waitCtx, composeFile, env)
		minioErr := minioLive(waitCtx, c)
		if pgErr == nil && minioErr == nil {
			return nil
		}
		lastErr = errors.Join(pgErr, minioErr)

		select {
		case <-waitCtx.Done():
			// Compose logs are the difference between "did not become
			// ready" and a diagnosis: initdb failures, permission errors on
			// the bind mount, and image problems all appear there.
			return fmt.Errorf("%w within %s: %w\n%s",
				ErrNotReady, readyTimeout, lastErr, recentLogs(ctx, composeFile, env))
		case <-time.After(time.Second):
		}
	}
}

// recentLogs returns the tail of the stack's logs for a failure message,
// on a fresh short-lived context so it still works when the caller's has
// already expired — which, at the point this is called, it has.
func recentLogs(ctx context.Context, composeFile string, env []string) string {
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()

	out, err := composeOutput(logCtx, composeFile, env, "logs", "--tail", "40")
	if err != nil {
		return fmt.Sprintf("(could not collect compose logs: %v)", err)
	}
	return string(out)
}

// composePS is the subset of `docker compose ps --format json` this needs.
type composePS struct {
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

// postgresHealthy reads the container's own healthcheck verdict.
//
// The verdict comes from the container's healthcheck, which runs an
// AUTHENTICATED query (compose.yaml, pinned by a test) — deliberately not
// pg_isready, which succeeds with the wrong user, database or password and
// would report a plane ready that nobody can open.
//
// The check lives in the container because that is where a client that
// speaks the protocol ships. A host-side TCP dial would report success as
// soon as the port is bound, which during a cold initdb is long before the
// database can answer.
func postgresHealthy(ctx context.Context, composeFile string, env []string) error {
	// Per-probe bound: one wedged docker invocation must not consume the
	// whole readiness budget.
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	out, err := composeOutput(probeCtx, composeFile, env, "ps", "--format", "json")
	if err != nil {
		return fmt.Errorf("docker compose ps: %w", err)
	}

	// Compose emits one JSON object per line, not an array.
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var entry composePS
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return fmt.Errorf("parse compose ps output: %w", err)
		}
		if entry.Service != "postgres" {
			continue
		}
		if entry.Health == "healthy" {
			return nil
		}
		return fmt.Errorf("postgres is %s (health: %q)", entry.State, entry.Health)
	}
	return errors.New("postgres container not found")
}

// minioLive probes the published port from the host.
//
// Host-side rather than a container healthcheck: this image's minimal base
// has changed its available tooling across releases, so a healthcheck
// shelling out to curl or mc is one pin-bump from silently breaking. This
// also tests the path callers actually use — the published port.
func minioLive(ctx context.Context, c *Config) error {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	url := fmt.Sprintf("http://127.0.0.1:%d/minio/health/live", c.MinIOPort)
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("build minio health request: %w", err)
	}
	// Not http.DefaultClient: it has no timeout, so a connection that is
	// accepted and then never answered would hang on the context alone.
	client := &http.Client{Timeout: probeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("minio not answering: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("minio health returned %s", resp.Status)
	}
	return nil
}

// placeholderKey supplies non-empty key material for operations that must
// render the Compose environment but never touch credentials.
//
// Down and Reset must work even when the key file is missing or
// unreadable — that is exactly when an operator needs to tear the stack
// down. Compose still requires every variable to be set, so the values are
// filled with a constant that is never used to authenticate: the
// containers are being stopped, not started.
func placeholderKey() []byte {
	return []byte("teardown-placeholder-not-a-credential")
}

package stack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/internal/dataplane/migrations"
	"orchestrator/internal/dataplane/objects"
	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
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

	rootKey, keyErr := rootKeyFor(c, lifecycleUp)
	if keyErr != nil {
		return keyErr
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
	blob, err := ensureBucket(ctx, c, rootKey)
	if err != nil {
		return err
	}
	if err := migrateLocked(ctx, c, rootKey); err != nil {
		return err
	}
	// AFTER the migrations, because the claims table is part of the schema
	// they apply, and before `up` reports a ready plane, because a surviving
	// claim is unfinished destructive work.
	return reconcileClaims(ctx, c, rootKey, blob)
}

// ErrPlaneLocked reports a data root that already holds a plane whose
// root-of-trust key is absent.
//
// It is the observable restore state item 8 builds on: refuse, supply the
// original key, open. A bare "file not found" would make that sequence
// untestable as a sequence, because nothing would distinguish it from a
// first run.
var ErrPlaneLocked = errors.New("data plane is locked: its root-of-trust key is not present")

// ErrNoPlane reports an operation that needs a provisioned plane against a
// data root where none exists.
//
// Distinct from ErrPlaneLocked because the two states need opposite actions.
// A locked plane has data and is missing its key: the answer is to restore
// the key, or run new-key recovery. An empty root has neither, and telling
// its operator to "restore the original key" sends them looking for
// something that was never created — the answer is to run `dataplane-up`.
var ErrNoPlane = errors.New("no data plane has been provisioned")

// lifecycle names the operation asking for key material.
//
// It is part of the decision rather than derived from the filesystem, because
// emptiness alone answers the wrong question. An empty data root means "no
// plane has been provisioned"; it does not mean "this operation is the one
// that provisions it". `migrate` against an empty root is not setup — it is
// a migration of a plane that does not exist yet, and minting a key for it
// leaves a key file that the eventual `up` will silently adopt.
type lifecycle int

const (
	// lifecycleUp is the only operation that may create key material, and
	// only then against an empty data root.
	lifecycleUp lifecycle = iota
	lifecycleMigrate
	lifecycleForceVersion
)

func (l lifecycle) String() string {
	switch l {
	case lifecycleUp:
		return "up"
	case lifecycleMigrate:
		return "migrate"
	case lifecycleForceVersion:
		return "force-version"
	default:
		return "unknown"
	}
}

// rootKeyFor is the ONE place that decides whether a lifecycle operation may
// create key material (item 7, D4).
//
// The root key derives the Postgres password and the object-store
// credentials as well as the vault's key material, so minting a new one over
// an existing data root produces a password the cluster does not know. The
// authenticated healthcheck then fails, waitReady times out, and `up`
// reports "data plane did not become ready" three minutes later — a correct
// refusal reached by accident, naming nothing an operator can act on.
//
// TWO conditions, and both are necessary. The operation must be `up`, which
// is the only one that provisions a plane; and the data root must be empty,
// so `up` on an existing plane loads rather than replaces. Emptiness alone
// would let `migrate` or `force-version` mint against a fresh root, which the
// accepted design forbids for a reason worth stating: neither creates a
// plane, so a key either of them generated would belong to nothing until an
// `up` adopted it silently.
//
// Emptiness is judged across every service data directory, not just Postgres:
// the object store's credentials derive from the same key, so a plane holding
// objects and no cluster is still a plane some earlier key provisioned.
func rootKeyFor(c *Config, operation lifecycle) ([]byte, error) {
	fresh, err := dataRootIsEmpty(c)
	if err != nil {
		return nil, err
	}

	access := secret.LoadOnly
	if operation == lifecycleUp && fresh {
		access = secret.MayCreate
	}

	key, keyErr := secret.KeyFile(c.Roots.Config, access).RootKey()
	if keyErr == nil {
		return key, nil
	}
	wrapped := fmt.Errorf("read root-of-trust key for %s: %w", operation, keyErr)
	if !errors.Is(wrapped, paths.ErrNoKey) {
		return nil, wrapped
	}

	// Which refusal depends on what the data root holds, because the two
	// states need opposite actions and the wrong advice sends an operator
	// looking for a key that never existed.
	if fresh {
		return nil, fmt.Errorf("%w in %s, so %s has nothing to act on. Run `dataplane-up` first, "+
			"which is the only operation that provisions one: %w",
			ErrNoPlane, c.Roots.Data, operation, wrapped)
	}
	return nil, fmt.Errorf("%w (%s). Its Postgres password and object-store credentials are "+
		"derived from the original key, so a new one would open neither. Restore the key file "+
		"beside the backup, or run the new-key recovery path: %w",
		ErrPlaneLocked, operation, wrapped)
}

// dataRootIsEmpty reports whether NO service has been provisioned yet.
//
// initdb populates the Postgres directory and the object store populates its
// own, so their contents are the honest signal that some earlier key already
// provisioned this plane. A directory that does not exist yet counts as
// empty, which is the first-run case.
func dataRootIsEmpty(c *Config) (bool, error) {
	for _, service := range []paths.Service{paths.ServicePostgres, paths.ServiceMinIO} {
		dir, err := c.Roots.ServiceDataDir(service)
		if err != nil {
			return false, fmt.Errorf("resolve %s data directory: %w", service, err)
		}
		entries, readErr := os.ReadDir(dir)
		switch {
		case os.IsNotExist(readErr):
			continue
		case readErr != nil:
			return false, fmt.Errorf("inspect %s data directory: %w", service, readErr)
		case len(entries) > 0:
			return false, nil
		}
	}
	return true, nil
}

// ensureBucket provisions the object store the way migrateLocked
// provisions the schema: a service answering its health probe is not the
// same as a service ready to be used.
//
// Nothing created this bucket before. The config named it and the bootstrap
// pointer published it, so `up` reported a ready plane whose first write
// would have failed on a bucket that did not exist. Design D3.
//
// The endpoint comes from the bootstrap pointer rather than being formatted
// again here, so what `up` provisions is by construction the endpoint every
// caller is told to use.
//
// It returns the adapter it built. Claim reconciliation needs one over the
// same bucket, and building a second from the same inputs would be two places
// that have to agree about which bucket the plane uses.
func ensureBucket(ctx context.Context, c *Config, rootKey []byte) (*objects.Blob, error) {
	accessKey, err := secret.Derive(rootKey, secret.ContextObjectAccessKey)
	if err != nil {
		return nil, fmt.Errorf("derive object access key: %w", err)
	}
	secretKey, err := secret.Derive(rootKey, secret.ContextObjectSecretKey)
	if err != nil {
		return nil, fmt.Errorf("derive object secret key: %w", err)
	}

	blob, err := objects.New(objects.Config{
		Endpoint:  c.Bootstrap().Objects.Endpoint,
		Bucket:    c.Bucket,
		AccessKey: accessKey,
		SecretKey: secretKey,
	})
	if err != nil {
		return nil, fmt.Errorf("build object store client: %w", err)
	}
	if err := blob.EnsureBucket(ctx); err != nil {
		return nil, fmt.Errorf("provision object storage: %w", err)
	}
	return blob, nil
}

// reconcileClaims finishes object deletes an earlier actor could not.
//
// It belongs to `up` for the same reason migration does: both make the plane
// CONSISTENT rather than merely running, and a caller must not be handed a
// plane that is migrated but still carrying unfinished destructive work. A
// surviving deletion claim is exactly that -- storage condemned and possibly
// still there, on a digest whose writers cannot take the existing-object
// shortcut until the claim clears.
//
// Safe to run every time, including on a plane with nothing to recover:
// re-issuing a version-specific delete is a no-op by construction, and the
// claims table is empty whenever nothing is mid-delete.
//
// The registry is empty on purpose. Reconciliation reads no payload and
// validates no artifact type; a registry populated for this caller's benefit
// would be a second, drifting copy of the one the Orchestrator will own.
func reconcileClaims(ctx context.Context, c *Config, rootKey []byte, blob *objects.Blob) error {
	dsn, err := c.DSN(rootKey)
	if err != nil {
		return err
	}
	types, err := registry.New(nil)
	if err != nil {
		return fmt.Errorf("build an empty artifact registry: %w", err)
	}
	seam, err := postgres.Open(ctx, dsn, types, blob)
	if err != nil {
		return fmt.Errorf("open the persistence seam: %w", err)
	}
	defer seam.Close()

	recovered, err := seam.ReconcileDeletionClaims(ctx)
	if err != nil {
		return fmt.Errorf("reconcile deletion claims: %w", err)
	}
	// Silent when there was nothing to do, which is every ordinary `up`. A
	// claim exists only because something went wrong earlier, so having
	// finished one is worth an operator's attention rather than a line in the
	// noise.
	if recovered != (store.ClaimReconciliation{}) {
		slog.Default().InfoContext(ctx, "finished object deletions left behind by an earlier run",
			"claims_cleared", recovered.ClaimsCleared,
			"versions_deleted", recovered.VersionsDeleted,
			"uploads_aborted", recovered.UploadsAborted)
	}
	return nil
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

	rootKey, keyErr := rootKeyFor(c, lifecycleMigrate)
	if keyErr != nil {
		return keyErr
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

	rootKey, keyErr := rootKeyFor(c, lifecycleForceVersion)
	if keyErr != nil {
		return keyErr
	}
	dsn, dsnErr := c.DSN(rootKey)
	if dsnErr != nil {
		return dsnErr
	}

	// The clean-database refusal lives in migrations.Force, so a direct
	// caller cannot skip it. This wrapper adds the lifecycle lock, which is
	// what makes the read-then-act inside it safe against a concurrent
	// migrate.
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

package stack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/secret"
)

// New-key recovery: ADR 0022's second restore branch (design D8).
//
// ADR 0022 promises restore from the backup PLUS the key, or re-entry of
// secrets. Item 7 delivered the first and measured the second against the
// pinned images; this implements it.
//
// The shape is forced by what item 7 measured, not chosen:
//
//   - Single-user mode does not work. `postgres --single` calls getpwuid and
//     refuses under the arbitrary uid Compose runs as ("could not look up
//     effective user ID 501"). Running as the image's own user is not an
//     escape either — on native Linux that uid cannot write the 0700
//     host-owned bind mount.
//   - So recovery runs the ORDINARY server with an overridden hba_file, and
//     it must never be reachable over the network while it does. Trust
//     authentication means anyone who can open a connection owns the
//     database, during the one operation whose purpose is restoring data
//     somebody cares about. The absence of a listener is the security
//     boundary.
//   - The object store needs no step at all: its credentials are
//     environment, not baked into the data directory, so it follows the new
//     key. Item 7 measured that too.

// RecoveryMarkerFile records an in-flight recovery at the data root.
//
// It is what authorizes a RESUME, and it names the recovery container,
// because the container can outlive the process that started it: killing
// dataplanectl does not stop Docker, so the isolated postmaster keeps
// running and keeps owning PGDATA while the dead process's flock is
// released. Without a recorded identity, the next attempt would acquire the
// lifecycle lock and start working against a data directory another
// postmaster still holds.
const RecoveryMarkerFile = ".maestro-recovery-in-progress"

// StagedKeyFile is where the new root key waits until the very last step.
//
// It lives beside the real key in the CONFIG root, not the data root: the
// data root is what backup copies and what freshness judges, and a staged
// key there would travel into archives and make a fresh plane look
// provisioned.
const StagedKeyFile = "root.key.staged"

// ErrRecoveryNotAuthorized reports a recovery attempt against a plane that
// is neither locked nor mid-recovery.
//
// It exists to keep this from becoming a general-purpose key rotation
// nobody designed. ADR 0022 describes recovery for a plane whose key is
// GONE; rotating a working plane's key is a different operation with
// different hazards, and it is not this one.
var ErrRecoveryNotAuthorized = errors.New("new-key recovery is not authorized for this plane")

// ErrRecoveryIncoherent reports a marker whose staged key is missing.
//
// A staged key with no marker is ordinary debris and is cleaned up: the
// marker is written before the isolated server ever starts, so nothing
// downstream can have run. A marker WITHOUT its staged key is the reverse
// and cannot be reasoned about — the recovery may have installed a key this
// process cannot reproduce — so it refuses rather than guesses.
var ErrRecoveryIncoherent = errors.New("recovery marker names a staged key that is not present")

// recoveryMarker is the marker's content.
// Field order is chosen for struct alignment, not reading order.
type recoveryMarker struct {
	// StartedAt is operator-facing: a marker hours old means something was
	// abandoned rather than merely interrupted.
	StartedAt time.Time `json:"started_at"`
	// Container is the recovery container's deterministic name, so a
	// survivor can be found and removed by an attempt that did not start it.
	Container string `json:"container"`
	// StagedKey is the absolute path of the key waiting to be installed.
	StagedKey string `json:"staged_key"`
}

// recoveryTimeouts. Recovery restarts a Postgres server twice and issues one
// transaction, so it is bounded generously; a recovery that times out is
// resumable by design.
const (
	recoveryServerTimeout = 90 * time.Second
	recoveryStepTimeout   = 60 * time.Second
)

// recoveryMarkerPath is where the marker lives.
func recoveryMarkerPath(c *Config) string {
	return filepath.Join(c.Roots.Data, RecoveryMarkerFile)
}

// stagedKeyPath is where a minted-but-uninstalled key waits.
func stagedKeyPath(c *Config) string {
	return filepath.Join(c.Roots.Config, StagedKeyFile)
}

// recoveryContainerName is the deterministic identity the marker records.
//
// Derived from the project name so two isolated planes never collide, and
// deliberately OUTSIDE the Compose project: ordinary `compose down` must not
// touch it, and it must not appear in `compose ps`. Nothing else would ever
// clean it up, which is why every attempt removes survivors itself.
func recoveryContainerName(c *Config) string {
	return c.ProjectName + "-keyrecovery"
}

// readRecoveryMarker returns the marker, or nil when none exists.
func readRecoveryMarker(c *Config) (*recoveryMarker, error) {
	body, err := os.ReadFile(recoveryMarkerPath(c))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil //nolint:nilnil // absence is the ordinary case, not an error
		}
		return nil, fmt.Errorf("read %s: %w", RecoveryMarkerFile, err)
	}
	var marker recoveryMarker
	if err := json.Unmarshal(body, &marker); err != nil {
		return nil, fmt.Errorf("parse %s: %w", RecoveryMarkerFile, err)
	}
	return &marker, nil
}

// writeRecoveryMarker records an in-flight recovery durably.
//
// Written AFTER the staged key is fsynced, which is the order resume
// depends on: a staged key with no marker means nothing downstream ran, so
// it is safe to delete, while a marker without its key is incoherent.
func writeRecoveryMarker(c *Config, marker recoveryMarker) error {
	body, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", RecoveryMarkerFile, err)
	}
	if err := writeFileSynced(recoveryMarkerPath(c), append(body, '\n')); err != nil {
		return err
	}
	return syncDir(c.Roots.Data)
}

// writeFileSynced writes a file and fsyncs it before returning.
func writeFileSynced(path string, body []byte) (err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, closeErr)
		}
	}()
	if _, err := file.Write(body); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	return nil
}

// RecoverKey re-keys a plane whose root-of-trust key is gone (design D8).
//
// It is DESTRUCTIVE: every secret is deleted, because a new key cannot
// decrypt ciphertext written under the old one, and the database credential
// is rewritten. The operator re-enters the secrets afterwards. Nothing else
// is touched — item 7 built the vault so it drops wholesale.
//
// The staging order is what makes it resumable, and resumability is not a
// nicety here: this runs on a plane somebody is already anxious about, and
// it is the operation most likely to be interrupted. The real key is
// installed LAST, so an interrupted recovery leaves a plane that is still
// honestly locked rather than one holding a key that opens nothing.
func RecoverKey(ctx context.Context, c *Config, composeFile string, force bool) (err error) {
	if !force {
		return errors.New("new-key recovery deletes every stored secret and rewrites the database " +
			"credential; re-run with -force to confirm")
	}

	release, lockErr := lockLifecycle(c)
	if lockErr != nil {
		return lockErr
	}
	defer func() {
		if relErr := release(); relErr != nil && err == nil {
			err = relErr
		}
	}()

	if guardErr := guardRestoreState(c, lifecycleRecoverKey); guardErr != nil {
		return guardErr
	}

	marker, markerErr := readRecoveryMarker(c)
	if markerErr != nil {
		return markerErr
	}
	if authErr := authorizeRecovery(c, marker); authErr != nil {
		return authErr
	}

	// Exclusive control of the data directory before anything else. The
	// isolated server below opens the same PGDATA as the normal container,
	// so starting it against a running plane is not merely untidy.
	env, envErr := c.composeEnv(placeholderKey())
	if envErr != nil {
		return envErr
	}
	if stopErr := composeStop(ctx, c.ProjectName, composeFile, env); stopErr != nil {
		return stopErr
	}

	staged, marker, stageErr := stageRecovery(c, marker)
	if stageErr != nil {
		return stageErr
	}

	// A survivor from a killed attempt owns PGDATA and must go before the
	// probe, or the probe talks to a server this process did not start and
	// cannot bound.
	if sweepErr := removeRecoveryContainer(ctx, marker.Container); sweepErr != nil {
		return sweepErr
	}

	if applyErr := applyRecovery(ctx, c, composeFile, staged, marker); applyErr != nil {
		return applyErr
	}
	return finishRecovery(ctx, c, composeFile, marker)
}

// finishRecovery is steps 7 and 8: install the key, open the plane, prove
// the new credential works, and only then clear the marker.
//
// Split from RecoverKey so the sequence above reads as the sequence D8
// describes rather than as one long function.
func finishRecovery(ctx context.Context, c *Config, composeFile string, marker *recoveryMarker) error {
	// The staged key becomes the real one only now, atomically. Everything
	// before this point leaves a plane that is still honestly locked.
	if installErr := installStagedKey(c, marker); installErr != nil {
		return installErr
	}

	if upErr := up(ctx, c, composeFile); upErr != nil {
		return fmt.Errorf("bring the plane up on its new key: %w", upErr)
	}
	// Verified over the NETWORK by service name, never from inside the
	// container: the image's own pg_hba trusts in-container connections, so
	// an in-container check accepts any password and proves nothing. This is
	// item 7's recorded trap, at the other end of the sequence.
	if verifyErr := verifyNewCredential(ctx, c, composeFile); verifyErr != nil {
		return verifyErr
	}

	// LAST. A marker cleared before the verification would leave a resume
	// unauthorized for a plane that had not been proven recovered.
	if rmErr := os.Remove(recoveryMarkerPath(c)); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return fmt.Errorf("clear %s: %w", RecoveryMarkerFile, rmErr)
	}
	return syncDir(c.Roots.Data)
}

// authorizeRecovery decides whether this plane may be recovered at all.
//
// Entry and resume are DIFFERENT conditions, and conflating them strands the
// window that most needs finishing. Initial entry requires the plane to
// report locked, which is the situation ADR 0022 describes. A resume is
// authorized by the marker instead, and must proceed even when the final key
// is already installed and the plane therefore no longer reports locked —
// requiring ErrPlaneLocked on resume would refuse exactly the crash window
// between installing the key and clearing the marker.
func authorizeRecovery(c *Config, marker *recoveryMarker) error {
	if marker != nil {
		return nil
	}
	_, keyErr := rootKeyFor(c, lifecycleRecoverKey)
	switch {
	case errors.Is(keyErr, ErrPlaneLocked):
		return nil
	case keyErr != nil:
		return keyErr
	default:
		return fmt.Errorf("%w: its root-of-trust key is present and opens it, so there is nothing to "+
			"recover. This operation exists for a plane whose key is GONE; rotating a working "+
			"plane's key is a different operation", ErrRecoveryNotAuthorized)
	}
}

// stageRecovery mints and records the new key, or adopts what an earlier
// attempt staged.
//
// Returns the staged key material and the marker describing it.
func stageRecovery(c *Config, existing *recoveryMarker) ([]byte, *recoveryMarker, error) {
	if existing != nil {
		staged, err := paths.LoadKeyFile(existing.StagedKey)
		if err != nil {
			// The incoherent case: a marker whose key is gone. Refusing is
			// the only safe answer, because the recovery may already have
			// applied a key this process cannot reproduce.
			return nil, nil, fmt.Errorf("%w (%s): %w. The plane's credential may already have been "+
				"changed to a key that no longer exists; restore %s from wherever it was kept, or "+
				"restore the plane from a backup",
				ErrRecoveryIncoherent, existing.StagedKey, err, existing.StagedKey)
		}
		return staged, existing, nil
	}

	// A staged key with NO marker is debris from an attempt that died
	// between the two writes. Nothing downstream can have run, so it is
	// removed rather than adopted — adopting it would silently reuse key
	// material whose provenance this process cannot establish.
	if err := os.Remove(stagedKeyPath(c)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("remove an orphaned staged key: %w", err)
	}

	key, err := paths.NewKeyMaterial()
	if err != nil {
		return nil, nil, fmt.Errorf("mint the recovery key: %w", err)
	}
	// The key is fsynced FIRST, then the marker that names it. The reverse
	// order leaves a marker pointing at nothing, which is the incoherent
	// state above.
	if err := writeFileSynced(stagedKeyPath(c), paths.EncodeKey(key)); err != nil {
		return nil, nil, err
	}
	if err := syncDir(c.Roots.Config); err != nil {
		return nil, nil, err
	}

	marker := &recoveryMarker{
		Container: recoveryContainerName(c),
		StagedKey: stagedKeyPath(c),
		StartedAt: time.Now().UTC(),
	}
	if err := writeRecoveryMarker(c, *marker); err != nil {
		return nil, nil, err
	}
	return key, marker, nil
}

// installStagedKey moves the staged key into place atomically.
func installStagedKey(c *Config, marker *recoveryMarker) error {
	if err := os.Rename(marker.StagedKey, c.Roots.KeyPath()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Already installed by an earlier attempt that died before
			// clearing the marker. This is D8's third crash window, and the
			// resume converges rather than failing.
			if _, statErr := os.Stat(c.Roots.KeyPath()); statErr == nil {
				return nil
			}
		}
		return fmt.Errorf("install the recovered key: %w", err)
	}
	return syncDir(c.Roots.Config)
}

// applyRecovery performs the credential change, or establishes that an
// earlier attempt already did.
//
// The branch is decided by a PROBE against the cluster rather than by a flag
// this code would have to keep honest: durable evidence living in the
// database is the only thing a resume can trust after a kill.
func applyRecovery(
	ctx context.Context, c *Config, composeFile string, staged []byte, marker *recoveryMarker,
) error {
	newPassword, deriveErr := secret.Derive(staged, secret.ContextPostgresPassword)
	if deriveErr != nil {
		return fmt.Errorf("derive the new database password: %w", deriveErr)
	}

	// Step 5: has the transaction already committed? The probe runs against
	// a server whose HBA demands scram-sha-256 over the socket, NOT the
	// trust HBA the change itself needs. Probing through trust would
	// authenticate whether or not ALTER USER ever ran -- item 7's
	// in-container authentication trap, in a new place -- and every branch
	// of the resume table would then take the same path regardless of what
	// actually happened.
	alreadyApplied, probeErr := probeRecoveredPassword(ctx, c, composeFile, marker, newPassword)
	if probeErr != nil {
		return probeErr
	}
	if alreadyApplied {
		return nil
	}

	// Step 6: one transaction, before any network exposure.
	return changeCredentialAndDropSecrets(ctx, c, composeFile, marker, newPassword)
}

// probeRecoveredPassword reports whether the new password already
// authenticates, using a socket-only server with REAL authentication.
func probeRecoveredPassword(
	ctx context.Context, c *Config, composeFile string, marker *recoveryMarker, password string,
) (bool, error) {
	if err := startRecoveryServer(ctx, c, composeFile, marker, hbaScram); err != nil {
		return false, err
	}
	defer func() { _ = removeRecoveryContainer(context.WithoutCancel(ctx), marker.Container) }()

	stepCtx, cancel := context.WithTimeout(ctx, recoveryStepTimeout)
	defer cancel()

	// Over the container's Unix socket, which needs no listener. A failure
	// here is the ordinary "not yet applied" answer, so it is not wrapped as
	// an error -- but a failure to REACH the server at all would look the
	// same, which is why startRecoveryServer waits for readiness first.
	out, err := dockerExec(stepCtx, marker.Container, []string{"PGPASSWORD=" + password},
		"psql", "-U", c.User, "-d", c.Database, "-tAc", "select 1")
	if err == nil {
		return true, nil
	}
	// Distinguish "the password is wrong" -- the expected pre-commit answer
	// -- from a server that is not answering at all, which would make this
	// probe silently report "not applied" for every plane.
	if strings.Contains(string(out), "authentication failed") ||
		strings.Contains(string(out), "password authentication") {
		return false, nil
	}
	return false, fmt.Errorf("probe the recovered credential: %w\n%s", err, out)
}

// changeCredentialAndDropSecrets is step 6: ALTER USER and the secrets wipe,
// in ONE transaction, through a trust-authenticated socket-only server.
//
// Both in one transaction because Postgres can make them so, and the
// alternative is a plane whose credential has moved while undecryptable
// ciphertext is still readable -- a state that is strictly worse than either
// end of it.
func changeCredentialAndDropSecrets(
	ctx context.Context, c *Config, composeFile string, marker *recoveryMarker, password string,
) error {
	if err := startRecoveryServer(ctx, c, composeFile, marker, hbaTrust); err != nil {
		return err
	}
	defer func() { _ = removeRecoveryContainer(context.WithoutCancel(ctx), marker.Container) }()

	stepCtx, cancel := context.WithTimeout(ctx, recoveryStepTimeout)
	defer cancel()

	// Quoted with dollar-quoting so the derived password -- hex, but that is
	// the deriver's business rather than a property to rely on -- cannot
	// terminate the literal.
	statement := fmt.Sprintf(
		"BEGIN; ALTER USER %s PASSWORD $maestro$%s$maestro$; DELETE FROM secrets; COMMIT;",
		quoteIdentifier(c.User), password)

	out, err := dockerExec(stepCtx, marker.Container, nil,
		"psql", "-v", "ON_ERROR_STOP=1", "-U", c.User, "-d", c.Database, "-c", statement)
	if err != nil {
		return fmt.Errorf("rewrite the database credential and drop every secret: %w\n%s", err, out)
	}
	return nil
}

// The two HBA configurations recovery uses, and the difference between them
// is the whole reason the probe means anything.
const (
	// hbaTrust accepts any password over the socket. It is what the change
	// itself needs, and it is why the server must have no listener at all:
	// trust means whoever can connect owns the database.
	hbaTrust = "local all all trust\n"
	// hbaScram demands real authentication over the same socket, so a
	// successful probe proves the password actually changed.
	hbaScram = "local all all scram-sha-256\n"
)

// startRecoveryServer runs the isolated postmaster and waits for it to
// accept socket connections.
//
// Socket-only, by three independent means, because this server trusts (or,
// for the probe, is one config line away from trusting) whoever reaches it:
// no TCP listener at all, no published ports, and no network attachment.
// Item 7 measured that the recovery container reports no listener on 5432.
func startRecoveryServer(
	ctx context.Context, c *Config, composeFile string, marker *recoveryMarker, hba string,
) error {
	// A survivor from the previous step or a previous process owns PGDATA.
	if err := removeRecoveryContainer(ctx, marker.Container); err != nil {
		return err
	}

	hbaDir, err := os.MkdirTemp("", "maestro-recovery-hba-")
	if err != nil {
		return fmt.Errorf("create the recovery HBA directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(hbaDir) }()
	// World-readable: the container runs as our uid, but Docker Desktop's
	// virtualisation makes the mount's effective identity awkward to reason
	// about, and this file is a three-word policy statement with no secret
	// in it.
	//nolint:gosec // A three-word policy statement with no secret in it; the container's uid must read it.
	if hbaErr := os.WriteFile(filepath.Join(hbaDir, "pg_hba.conf"), []byte(hba), 0o644); hbaErr != nil {
		return fmt.Errorf("write the recovery HBA: %w", hbaErr)
	}

	image, err := pinnedImage(composeFile, "MAESTRO_PG_IMAGE")
	if err != nil {
		return err
	}
	pgDir, err := c.Roots.ServiceDataDir(paths.ServicePostgres)
	if err != nil {
		return fmt.Errorf("resolve the postgres data directory: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, recoveryServerTimeout)
	defer cancel()

	args := []string{
		"run", "--detach",
		"--name", marker.Container,
		"--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
		// No network at all. The absence of a listener is the security
		// boundary; this is defence in depth behind it.
		"--network", "none",
		"--volume", pgDir + ":/var/lib/postgresql/data",
		"--volume", hbaDir + ":/maestro-recovery:ro",
		"--env", "PGDATA=/var/lib/postgresql/data/pgdata",
		image,
		"-c", "listen_addresses=",
		"-c", "hba_file=/maestro-recovery/pg_hba.conf",
	}
	if out, err := exec.CommandContext(runCtx, "docker", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("start the recovery server: %w\n%s", err, out)
	}

	return waitRecoveryServerReady(runCtx, marker.Container)
}

// waitRecoveryServerReady blocks until the isolated server answers on its
// socket.
//
// Without it, the probe's failure would be ambiguous: a server that has not
// finished starting refuses connections exactly as a wrong password does,
// and the probe would report "not yet applied" for a plane where it had.
func waitRecoveryServerReady(ctx context.Context, container string) error {
	var lastOut []byte
	var lastErr error
	for {
		out, err := dockerExec(ctx, container, nil, "pg_isready", "-q")
		if err == nil {
			return nil
		}
		lastOut, lastErr = out, err

		select {
		case <-ctx.Done():
			return fmt.Errorf("the recovery server never accepted socket connections: %w\n%s",
				lastErr, lastOut)
		case <-time.After(time.Second):
		}
	}
}

// dockerExec runs a command inside a container, with optional environment.
func dockerExec(ctx context.Context, container string, env []string, command ...string) ([]byte, error) {
	args := []string{"exec"}
	for _, entry := range env {
		args = append(args, "--env", entry)
	}
	args = append(args, container)
	args = append(args, command...)
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("docker exec %s: %w", strings.Join(command, " "), err)
	}
	return out, nil
}

// removeRecoveryContainer stops and removes the recovery container if it
// exists, and is a no-op if it does not.
//
// Called before every step rather than only at the end, because the
// container can outlive the process that started it and the survivor still
// owns PGDATA.
func removeRecoveryContainer(ctx context.Context, container string) error {
	rmCtx, cancel := context.WithTimeout(ctx, recoveryStepTimeout)
	defer cancel()

	out, err := exec.CommandContext(rmCtx, "docker", "rm", "--force", container).CombinedOutput()
	if err == nil {
		return nil
	}
	// "No such container" is the ordinary case and not a failure.
	if strings.Contains(string(out), "No such container") {
		return nil
	}
	return fmt.Errorf("remove the recovery container %s: %w\n%s", container, err, out)
}

// verifyNewCredential proves the recovered plane authenticates OVER THE
// NETWORK, by service name.
//
// Not from inside the container: the image's generated pg_hba trusts
// in-container connections, so an in-container check accepts any password.
// Item 7 recorded that trap after a whole round of measurements turned out
// vacuous. `up`'s healthcheck already connects by service name, so this
// reuses it rather than inventing a second notion of "authenticates".
func verifyNewCredential(ctx context.Context, c *Config, composeFile string) error {
	rootKey, keyErr := rootKeyFor(c, lifecycleUp)
	if keyErr != nil {
		return fmt.Errorf("read the recovered key: %w", keyErr)
	}
	env, envErr := c.composeEnv(rootKey)
	if envErr != nil {
		return envErr
	}
	if err := postgresHealthy(ctx, c.ProjectName, composeFile, env); err != nil {
		return fmt.Errorf("the recovered credential does not authenticate over the network: %w", err)
	}
	return nil
}

// quoteIdentifier renders a SQL identifier safely.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

//go:build integration

package stack

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
)

// New-key recovery, test-plan items 18-21.
//
// This is ADR 0022's second restore branch, and the one whose failure modes
// are worst: it runs on a plane somebody is already anxious about, it
// deletes every secret irreversibly, and for part of its run a Postgres
// server is up whose HBA trusts anyone who can reach it. Each test below
// exists because one of those can go wrong quietly.

// recoverySecretName is the credential seeded before recovery and asserted
// gone afterwards.
const recoverySecretName = "recovery-victim"

// seedSecret writes one secret through the real vault, and returns the
// organization and user that own it.
//
// Through the vault rather than by direct INSERT: what recovery deletes is
// whatever the vault wrote, and a hand-built row could differ from it in a
// way that made the wipe look complete when it was not.
func seedSecret(t *testing.T, cfg *Config, seed crossStoreSeed) uuid.UUID {
	t.Helper()
	seam := openSeam(t, cfg)

	var userID uuid.UUID
	if err := openPlane(t, cfg).QueryRowContext(t.Context(),
		`SELECT user_id FROM users WHERE organization_id = $1 LIMIT 1`,
		seed.OrganizationID).Scan(&userID); err != nil {
		t.Fatalf("find the seeded user: %v", err)
	}

	created, err := seam.CreateIndividualSecret(t.Context(), store.CreateSecretInput{
		Name:           recoverySecretName,
		Plaintext:      secret.NewValue([]byte("a token nobody can decrypt after recovery")),
		Scope:          store.ConfigScope{Type: configkeys.ScopeOrganization, ID: seed.OrganizationID},
		OrganizationID: seed.OrganizationID,
		ActingUserID:   userID,
	})
	if err != nil {
		t.Fatalf("seed a secret: %v", err)
	}
	return created.ID
}

// countSecrets reports how many rows the vault holds.
func countSecrets(t *testing.T, cfg *Config) int {
	t.Helper()
	var count int
	if err := openPlane(t, cfg).QueryRowContext(t.Context(), `SELECT count(*) FROM secrets`).Scan(&count); err != nil {
		t.Fatalf("count secrets: %v", err)
	}
	return count
}

// TestRecoverKeyReKeysAPlaneWhoseKeyIsGone is test-plan item 18.
//
// Every clause is asserted because each is a different way the operation can
// look successful and not be: the data can be lost, the secrets can survive
// as undecryptable ciphertext nobody notices, or the credential can fail to
// move while the key file says it did.
func TestRecoverKeyReKeysAPlaneWhoseKeyIsGone(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	seed := seedCrossStore(t, cfg)
	seedSecret(t, cfg, seed)
	if before := countSecrets(t, cfg); before != 1 {
		t.Fatalf("the vault holds %d secrets before recovery, want 1: a wipe that deleted nothing "+
			"would pass every assertion below", before)
	}

	oldKey, err := os.ReadFile(cfg.Roots.KeyPath())
	if err != nil {
		t.Fatalf("read the original key: %v", err)
	}
	if err := Down(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Down: %v", err)
	}
	// The situation ADR 0022 describes: the plane is here, the key is not.
	if err := os.Remove(cfg.Roots.KeyPath()); err != nil {
		t.Fatalf("remove the key: %v", err)
	}
	if _, keyErr := rootKeyFor(cfg, lifecycleUp); !errors.Is(keyErr, ErrPlaneLocked) {
		t.Fatalf("the plane does not report itself locked (%v), so recovery would refuse to run "+
			"and this test would prove nothing", keyErr)
	}

	if err := RecoverKey(t.Context(), cfg, testComposeFile(), true); err != nil {
		t.Fatalf("RecoverKey: %v", err)
	}

	// A NEW key, not the old one restored from somewhere.
	newKey, err := os.ReadFile(cfg.Roots.KeyPath())
	if err != nil {
		t.Fatalf("read the recovered key: %v", err)
	}
	if string(newKey) == string(oldKey) {
		t.Fatal("the recovered key is byte-identical to the original: nothing was re-keyed")
	}

	// The data survives. Both stores, because MinIO's credentials derive
	// from the same key and item 7's claim is that the store simply follows
	// it -- a claim worth re-proving here rather than inheriting.
	assertCrossStoreIntact(t, cfg, seed)

	// And the secrets are gone, because their ciphertext was written under
	// a key nobody has.
	if after := countSecrets(t, cfg); after != 0 {
		t.Errorf("the vault still holds %d secrets after recovery: they are undecryptable "+
			"ciphertext, and leaving them is worse than deleting them -- every read fails in a way "+
			"that looks like corruption", after)
	}

	// The marker is gone, or the next ordinary operation would resume a
	// recovery that has finished.
	if _, err := os.Stat(recoveryMarkerPath(cfg)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the recovery marker survived a completed recovery: %v", err)
	}
	// So is the staged key, which is now the real one.
	if _, err := os.Stat(stagedKeyPath(cfg)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the staged key survived installation: %v", err)
	}
	// And no recovery container is left running against PGDATA.
	assertNoRecoveryContainer(t, cfg)
}

// TestRecoverKeyRefusesAPlaneThatIsNotLocked is the guard that keeps this
// from becoming a general-purpose key rotation.
//
// ADR 0022 describes recovery for a plane whose key is GONE. Rotating a
// working plane's key is a different operation with different hazards --
// among them that it would delete every secret of a plane that was working
// perfectly well.
func TestRecoverKeyRefusesAPlaneThatIsNotLocked(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	seed := seedCrossStore(t, cfg)
	seedSecret(t, cfg, seed)

	err := RecoverKey(t.Context(), cfg, testComposeFile(), true)
	if !errors.Is(err, ErrRecoveryNotAuthorized) {
		t.Fatalf("RecoverKey = %v, want a refusal against a plane whose key opens it", err)
	}
	// The refusal is worth nothing if it happened after the damage.
	if count := countSecrets(t, cfg); count != 1 {
		t.Errorf("the vault holds %d secrets after a REFUSED recovery, want 1: it deleted before "+
			"it decided", count)
	}
}

// TestRecoverKeyProbeRejectsAWrongPassword is test-plan item 21, and it is
// the assertion the whole resume table rests on.
//
// The probe decides whether the credential has already moved. If it ran
// through the TRUST HBA, it would authenticate whether or not `ALTER USER`
// ever ran -- item 7's in-container authentication trap, in a new place --
// and every branch of D8's table would take the same path regardless of what
// actually happened. So the probe must be able to FAIL, and this is what
// says so.
func TestRecoverKeyProbeRejectsAWrongPassword(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := Down(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Down: %v", err)
	}

	marker := &recoveryMarker{Container: recoveryContainerName(cfg), StagedKey: stagedKeyPath(cfg)}
	t.Cleanup(func() {
		_ = removeRecoveryContainer(context.WithoutCancel(t.Context()), marker.Container)
	})

	// The plane's REAL password must be accepted, or "rejects a wrong
	// password" would be satisfied by a probe that rejects everything --
	// which would make the resume permanently take the not-yet-applied
	// branch and re-run the transaction forever.
	rootKey, err := paths.EnsureKey(cfg.Roots.Config)
	if err != nil {
		t.Fatalf("read the root key: %v", err)
	}
	realPassword, err := secret.Derive(rootKey, secret.ContextPostgresPassword)
	if err != nil {
		t.Fatalf("derive the real password: %v", err)
	}

	accepted, err := probeRecoveredPassword(t.Context(), cfg, testComposeFile(), marker, realPassword)
	if err != nil {
		t.Fatalf("probe with the correct password: %v", err)
	}
	if !accepted {
		t.Error("the probe rejected the plane's own password: it would report 'not yet applied' " +
			"forever, and recovery would re-run its transaction on every resume")
	}

	rejected, err := probeRecoveredPassword(t.Context(), cfg, testComposeFile(), marker,
		"definitely-not-the-password")
	if err != nil {
		t.Fatalf("probe with a wrong password: %v", err)
	}
	if rejected {
		t.Error("the probe ACCEPTED a wrong password: it is running through the trust HBA, so it " +
			"cannot tell whether the credential moved and every resume branch collapses into one")
	}
}

// TestRecoveryServerPublishesNoListener is the security boundary, asserted
// rather than argued.
//
// The recovery server's HBA trusts whoever connects, which means the absence
// of a listener is the ONLY thing between it and whatever can route to the
// host -- during the one operation whose entire purpose is restoring data
// somebody cares about. Item 7 measured this once; a test keeps it true.
func TestRecoveryServerPublishesNoListener(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := Down(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Down: %v", err)
	}

	marker := &recoveryMarker{Container: recoveryContainerName(cfg), StagedKey: stagedKeyPath(cfg)}
	t.Cleanup(func() {
		_ = removeRecoveryContainer(context.WithoutCancel(t.Context()), marker.Container)
	})
	if err := startRecoveryServer(t.Context(), cfg, testComposeFile(), marker, hbaTrust); err != nil {
		t.Fatalf("startRecoveryServer: %v", err)
	}

	// No published ports at all.
	ports, err := exec.CommandContext(t.Context(), "docker", "port", marker.Container).CombinedOutput()
	if err == nil && len(strings.TrimSpace(string(ports))) != 0 {
		t.Errorf("the recovery container publishes %q: its HBA trusts anyone who connects",
			strings.TrimSpace(string(ports)))
	}

	// And no network attachment to reach it through.
	networks, err := exec.CommandContext(t.Context(), "docker", "inspect", "--format",
		"{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}", marker.Container).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect the recovery container: %v\n%s", err, networks)
	}
	for _, network := range strings.Fields(string(networks)) {
		if network != "none" {
			t.Errorf("the recovery container is attached to network %q, want only 'none'", network)
		}
	}
}

// TestRecoveryAdoptsAStagedKeyRatherThanMintingASecond is half of test-plan
// item 20: a resume must continue the recovery it finds, not start a new one.
//
// Minting a second key on resume would be silently catastrophic in the one
// window that matters -- after the credential moved and before the key was
// installed -- because the plane's password derives from the FIRST staged
// key and a second one derives a different password that opens nothing.
func TestRecoveryAdoptsAStagedKeyRatherThanMintingASecond(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := Down(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Down: %v", err)
	}

	// The state a killed recovery leaves after staging: key and marker both
	// on disk, nothing applied.
	staged, err := paths.NewKeyMaterial()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := writeFileSynced(stagedKeyPath(cfg), paths.EncodeKey(staged)); err != nil {
		t.Fatalf("stage a key: %v", err)
	}
	marker := recoveryMarker{Container: recoveryContainerName(cfg), StagedKey: stagedKeyPath(cfg)}
	if err := writeRecoveryMarker(cfg, marker); err != nil {
		t.Fatalf("write the marker: %v", err)
	}

	existing, err := readRecoveryMarker(cfg)
	if err != nil {
		t.Fatalf("read the marker: %v", err)
	}
	adopted, _, err := stageRecovery(cfg, existing)
	if err != nil {
		t.Fatalf("stageRecovery on resume: %v", err)
	}
	if string(adopted) != string(staged) {
		t.Error("the resume minted a NEW key instead of adopting the staged one: after the " +
			"credential has moved, the plane's password derives from the first key and a second " +
			"one opens nothing")
	}
}

// TestRecoveryCleansAStagedKeyWithNoMarker is the other half of item 20, and
// the asymmetry with the case above is the point.
//
// A staged key with NO marker means nothing downstream can have run -- the
// marker is written before the isolated server ever starts -- so the key is
// debris and adopting it would silently reuse material whose provenance this
// process cannot establish. The reverse, a marker with no key, is
// incoherent and refuses instead.
func TestRecoveryCleansAStagedKeyWithNoMarker(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := cfg.Roots.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	orphan, err := paths.NewKeyMaterial()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := writeFileSynced(stagedKeyPath(cfg), paths.EncodeKey(orphan)); err != nil {
		t.Fatalf("stage an orphan: %v", err)
	}

	fresh, _, err := stageRecovery(cfg, nil)
	if err != nil {
		t.Fatalf("stageRecovery with no marker: %v", err)
	}
	if string(fresh) == string(orphan) {
		t.Error("the orphaned staged key was adopted: its provenance cannot be established, and " +
			"an attempt that died before writing its marker may have been a different attempt " +
			"entirely")
	}
}

// TestRecoveryRefusesAMarkerWhoseStagedKeyIsGone is the incoherent state.
//
// It refuses rather than guessing, because the credential may already have
// been changed to a key this process cannot reproduce. Minting a fresh one
// there would leave a plane whose password nothing derives.
func TestRecoveryRefusesAMarkerWhoseStagedKeyIsGone(t *testing.T) {
	cfg := isolatedPlane(t)
	if err := cfg.Roots.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	marker := recoveryMarker{Container: recoveryContainerName(cfg), StagedKey: stagedKeyPath(cfg)}
	if err := writeRecoveryMarker(cfg, marker); err != nil {
		t.Fatalf("write the marker: %v", err)
	}

	existing, err := readRecoveryMarker(cfg)
	if err != nil {
		t.Fatalf("read the marker: %v", err)
	}
	if _, _, err := stageRecovery(cfg, existing); !errors.Is(err, ErrRecoveryIncoherent) {
		t.Fatalf("stageRecovery = %v, want a refusal: the credential may already have moved to a "+
			"key that no longer exists, and minting a fresh one would leave a plane nothing opens", err)
	}
}

// assertNoRecoveryContainer fails if the recovery container still exists.
//
// It is outside the Compose project by design, so `compose down` never
// touches it and nothing else would ever clean it up. A survivor holds
// PGDATA open against whatever runs next.
func assertNoRecoveryContainer(t *testing.T, cfg *Config) {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "docker", "ps", "--all", "--filter",
		"name=^"+recoveryContainerName(cfg)+"$", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	if name := strings.TrimSpace(string(out)); name != "" {
		t.Errorf("the recovery container %s still exists: it is outside the Compose project, so "+
			"nothing else will ever remove it, and it holds PGDATA open", name)
	}
}

// The three crash windows of D8's resume table.
//
// ON METHOD, because the accepted test plan says "kill the CLI process" and
// this does something adjacent. A timing-based kill cannot select WHICH
// window it lands in: the three are seconds apart on a fast machine and the
// boundaries move with disk speed, so a suite of three killed runs would in
// practice sample one window three times and report three passes. What
// matters about a kill is not the signal — it is the DURABLE STATE it
// leaves, and that state is exactly enumerable.
//
// So each window below is constructed as the kill would leave it, including
// the residue a kill leaves and an error return does not: a SURVIVING
// RECOVERY CONTAINER holding PGDATA open. That container is the thing the
// plan's "not an injected error return" clause is really about, and it is
// present in every case here. One test below additionally kills a real
// child process, to prove the orphan handling works against an orphan
// nothing in this package created.
//
// What this does not cover is a kill landing between two statements inside
// one window, and that is stated rather than implied.

// leaveRecoveryContainer starts a recovery server and abandons it, exactly
// as a killed CLI would.
func leaveRecoveryContainer(t *testing.T, cfg *Config, marker *recoveryMarker) {
	t.Helper()
	if err := startRecoveryServer(t.Context(), cfg, testComposeFile(), marker, hbaTrust); err != nil {
		t.Fatalf("start the container a kill would have orphaned: %v", err)
	}
	t.Cleanup(func() {
		_ = removeRecoveryContainer(context.WithoutCancel(t.Context()), marker.Container)
	})
}

// stageAsAKillWould reproduces the artifacts every window shares: a staged
// key, its marker, and an orphaned container.
func stageAsAKillWould(t *testing.T, cfg *Config) []byte {
	t.Helper()
	staged, err := paths.NewKeyMaterial()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := writeFileSynced(stagedKeyPath(cfg), paths.EncodeKey(staged)); err != nil {
		t.Fatalf("stage: %v", err)
	}
	marker := recoveryMarker{Container: recoveryContainerName(cfg), StagedKey: stagedKeyPath(cfg)}
	if err := writeRecoveryMarker(cfg, marker); err != nil {
		t.Fatalf("marker: %v", err)
	}
	leaveRecoveryContainer(t, cfg, &marker)
	return staged
}

// assertRecovered is the convergence every window must reach.
func assertRecovered(t *testing.T, cfg *Config, seed crossStoreSeed, staged []byte) {
	t.Helper()
	installed, err := paths.LoadKeyFile(cfg.Roots.KeyPath())
	if err != nil {
		t.Fatalf("read the installed key: %v", err)
	}
	if string(installed) != string(staged) {
		t.Error("the resume installed a DIFFERENT key from the one it found staged: the credential " +
			"derives from the staged key, so any other key opens nothing")
	}
	if _, err := os.Stat(recoveryMarkerPath(cfg)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the marker survived a completed resume: %v", err)
	}
	if _, err := os.Stat(stagedKeyPath(cfg)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the staged key survived installation: %v", err)
	}
	if count := countSecrets(t, cfg); count != 0 {
		t.Errorf("the vault holds %d secrets after recovery, want 0", count)
	}
	assertNoRecoveryContainer(t, cfg)
	assertCrossStoreIntact(t, cfg, seed)
}

// lockedPlaneWithSecret builds the starting point every window shares: a
// populated plane, a secret, and the key removed.
func lockedPlaneWithSecret(t *testing.T) (*Config, crossStoreSeed) {
	t.Helper()
	cfg := isolatedPlane(t)
	if err := Up(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	seed := seedCrossStore(t, cfg)
	seedSecret(t, cfg, seed)
	if err := Down(t.Context(), cfg, testComposeFile()); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if err := os.Remove(cfg.Roots.KeyPath()); err != nil {
		t.Fatalf("remove the key: %v", err)
	}
	return cfg, seed
}

// Window 1: killed BEFORE the credential transaction committed.
//
// The cluster is unchanged and the plane is still honestly locked, which is
// the property the staging order buys: the real key is installed last, so an
// interrupted recovery never leaves a key that opens nothing. The resume's
// probe must FAIL and the transaction must run.
func TestRecoveryResumesAfterAKillBeforeTheCredentialMoved(t *testing.T) {
	cfg, seed := lockedPlaneWithSecret(t)
	staged := stageAsAKillWould(t, cfg)

	// Still locked: nothing downstream ran.
	if _, keyErr := rootKeyFor(cfg, lifecycleUp); !errors.Is(keyErr, ErrPlaneLocked) {
		t.Fatalf("the plane is not locked after a pre-commit kill (%v): the staging order is "+
			"supposed to guarantee exactly that", keyErr)
	}

	if err := RecoverKey(t.Context(), cfg, testComposeFile(), true); err != nil {
		t.Fatalf("resume after a pre-commit kill: %v", err)
	}
	assertRecovered(t, cfg, seed, staged)
}

// Window 2: killed AFTER the transaction committed, before the key was
// installed.
//
// This is the window that most needs resume, and the one where minting a
// fresh key would be silently catastrophic: the cluster's password now
// derives from the STAGED key, so a second key opens nothing and the plane
// would be unrecoverable by its own recovery tool. The probe must SUCCEED
// and the transaction must be skipped.
func TestRecoveryResumesAfterAKillBetweenCommitAndKeyInstall(t *testing.T) {
	cfg, seed := lockedPlaneWithSecret(t)
	staged := stageAsAKillWould(t, cfg)

	// Move the credential exactly as the interrupted run had, so the state
	// is the real post-commit one rather than a description of it.
	marker := &recoveryMarker{Container: recoveryContainerName(cfg), StagedKey: stagedKeyPath(cfg)}
	password, err := secret.Derive(staged, secret.ContextPostgresPassword)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if err := changeCredentialAndDropSecrets(t.Context(), cfg, testComposeFile(), marker, password); err != nil {
		t.Fatalf("apply the credential change a kill would have left committed: %v", err)
	}
	leaveRecoveryContainer(t, cfg, marker)

	// The live key is still absent, which is what makes this a resume rather
	// than a completed run.
	if _, statErr := os.Stat(cfg.Roots.KeyPath()); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the live key exists (%v): this is not the window under test", statErr)
	}

	if err := RecoverKey(t.Context(), cfg, testComposeFile(), true); err != nil {
		t.Fatalf("resume after a post-commit kill: %v", err)
	}
	assertRecovered(t, cfg, seed, staged)
}

// Window 3: killed AFTER the key was installed, before the marker cleared.
//
// The plane is fully recovered and therefore no longer reports itself
// locked, so the marker is the ONLY thing authorizing the resume. An
// authorization rule that required ErrPlaneLocked would strand exactly this
// state — a plane that is recovered but cannot be confirmed so, with a
// marker no operation will clear.
func TestRecoveryResumesAfterAKillBetweenKeyInstallAndMarkerRemoval(t *testing.T) {
	cfg, seed := lockedPlaneWithSecret(t)
	staged := stageAsAKillWould(t, cfg)

	marker := &recoveryMarker{Container: recoveryContainerName(cfg), StagedKey: stagedKeyPath(cfg)}
	password, err := secret.Derive(staged, secret.ContextPostgresPassword)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if err := changeCredentialAndDropSecrets(t.Context(), cfg, testComposeFile(), marker, password); err != nil {
		t.Fatalf("apply the credential change: %v", err)
	}
	if err := installStagedKey(cfg, marker); err != nil {
		t.Fatalf("install the key a kill would have left installed: %v", err)
	}
	leaveRecoveryContainer(t, cfg, marker)

	// No longer locked: the key is in place and opens the plane. Recovery
	// must proceed anyway, on the marker's authority alone.
	if _, keyErr := rootKeyFor(cfg, lifecycleRecoverKey); keyErr != nil {
		t.Fatalf("the plane still reports a key problem (%v): this is not the window under test", keyErr)
	}

	if err := RecoverKey(t.Context(), cfg, testComposeFile(), true); err != nil {
		t.Fatalf("resume after a post-install kill: %v", err)
	}
	assertRecovered(t, cfg, seed, staged)
}

// TestRecoveryRemovesAnOrphanNothingInThisProcessStarted is the residue
// clause of test-plan item 20, with a GENUINELY killed process.
//
// The container outliving its process is the whole reason the marker records
// a deterministic name, and it is the one part of a kill that no constructed
// state reproduces faithfully: here the orphan is left by a child this test
// killed, holding PGDATA open, with no in-process handle to it at all.
func TestRecoveryRemovesAnOrphanNothingInThisProcessStarted(t *testing.T) {
	cfg, seed := lockedPlaneWithSecret(t)

	// A real child, really killed, mid-recovery.
	binary := buildDataplanectl(t)
	command := exec.Command(binary, "-force", "recover-key") //nolint:noctx // killed deliberately
	command.Dir = "../../.."
	command.Env = os.Environ()
	var output strings.Builder
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatalf("start the child recovery: %v", err)
	}

	// Kill once the child has started its recovery container, which is the
	// state that needs cleaning.
	waitForRecoveryContainer(t, cfg)
	if err := command.Process.Kill(); err != nil {
		t.Fatalf("kill the child: %v", err)
	}
	_ = command.Wait()
	t.Cleanup(func() {
		_ = removeRecoveryContainer(context.WithoutCancel(t.Context()), recoveryContainerName(cfg))
	})

	// The orphan survives the process that made it. That is the premise.
	if !recoveryContainerExists(t, cfg) {
		t.Fatalf("no orphaned container survives the kill, so this test proves nothing about "+
			"removing one\nchild output:\n%s", output.String())
	}

	if err := RecoverKey(t.Context(), cfg, testComposeFile(), true); err != nil {
		t.Fatalf("resume over an orphaned container: %v", err)
	}
	staged, err := paths.LoadKeyFile(cfg.Roots.KeyPath())
	if err != nil {
		t.Fatalf("read the installed key: %v", err)
	}
	assertRecovered(t, cfg, seed, staged)
}

// recoveryContainerExists reports whether the recovery container is present.
func recoveryContainerExists(t *testing.T, cfg *Config) bool {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "docker", "ps", "--all", "--filter",
		"name=^"+recoveryContainerName(cfg)+"$", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	return strings.TrimSpace(string(out)) != ""
}

// waitForRecoveryContainer blocks until the recovery container appears.
func waitForRecoveryContainer(t *testing.T, cfg *Config) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if recoveryContainerExists(t, cfg) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the child never started a recovery container")
}

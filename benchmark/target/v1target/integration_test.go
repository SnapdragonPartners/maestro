package v1target

// Docker-guarded end-to-end tests: the real engine drives this adapter
// with fake-maestro standing in for the v1 binary, against a real (local)
// Gitea and a local bare fixture. Skips when Docker is absent — unless
// BENCHMARK_REQUIRE_DOCKER=1 (set in CI), where skipping becomes failure:
// a green PR must have executed the Gitea lifecycle (design_adapter_v1.md).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/engine"
	"github.com/SnapdragonPartners/maestro/benchmark/internal/contenthash"
	"github.com/SnapdragonPartners/maestro/benchmark/internal/gitx"
	"github.com/SnapdragonPartners/maestro/benchmark/mph"
	"github.com/SnapdragonPartners/maestro/benchmark/results"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/story"
	"github.com/SnapdragonPartners/maestro/benchmark/target"
)

func requireDocker(t *testing.T) {
	t.Helper()
	if dockerAvailable(context.Background()) {
		return
	}
	if os.Getenv("BENCHMARK_REQUIRE_DOCKER") == "1" {
		t.Fatalf("Docker is required (BENCHMARK_REQUIRE_DOCKER=1) but unavailable")
	}
	t.Skip("Docker not available; set BENCHMARK_REQUIRE_DOCKER=1 to make this a failure")
}

// makeLocalFixture builds the bare fixture the engine clones.
func makeLocalFixture(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	bare := filepath.Join(root, "fixture.git")
	if _, err := gitx.Run(ctx, ".", "init", "--bare", "--quiet", "--initial-branch=main", bare); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	seed := filepath.Join(root, "seed")
	if err := gitx.Clone(ctx, bare, seed); err != nil {
		t.Fatalf("clone seed: %v", err)
	}
	if _, err := gitx.Run(ctx, seed, "checkout", "--quiet", "-b", "main"); err != nil {
		t.Fatalf("branch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seed, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := gitx.Run(ctx, seed, "add", "-A"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := gitx.Run(ctx, seed, "-c", "user.name=seed", "-c", "user.email=seed@invalid",
		"commit", "--quiet", "-m", "seed"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := gitx.Push(ctx, seed, "main"); err != nil {
		t.Fatalf("push: %v", err)
	}
	pin, err := gitx.Head(ctx, seed)
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	return bare, pin
}

func e2eStory(t *testing.T, repo, pin string) *story.Loaded {
	t.Helper()
	def := &story.Definition{
		SchemaVersion: story.SchemaVersion,
		ID:            "v1-e2e",
		Title:         "fake-maestro end to end",
		Level:         story.LevelStory,
		Fixture:       story.Fixture{Repo: repo, Commit: pin, BaseBranch: "main"},
		Prompt:        story.Prompt{Text: "write solution.txt containing done"},
		Expectations: story.Expectations{
			AllowedPaths:      []string{"solution.txt"},
			RequiredArtifacts: []string{"pr"},
			EvidenceShape:     []string{"diff", "test-output"},
		},
		Validators: []story.Validator{{Name: "solution-exists", Command: "test -f solution.txt"}},
		Checks: []story.Check{
			{Name: "diff-confined", Type: story.CheckFilesChangedWithin},
			{Name: "marker", Type: story.CheckFileContains, Path: "solution.txt", Contains: "done"},
		},
		Budget: story.Budget{MaxTokens: 100000, MaxWallClockSeconds: 300, MaxCostUSD: 5},
	}
	if err := def.Validate(); err != nil {
		t.Fatalf("story: %v", err)
	}
	hash, err := contenthash.CanonicalJSON(def)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return &story.Loaded{Definition: def, Hash: hash, Path: "in-memory"}
}

func e2eBundle(t *testing.T, fakeBin, sourceDir string) *mph.Loaded {
	t.Helper()
	bundle := &mph.Bundle{
		SchemaVersion: mph.SchemaVersion,
		Name:          "v1-e2e-bundle",
		Description:   "fake-maestro bundle",
		Model:         mph.ModelRouting{Default: "model-x"},
		Prompt:        mph.PromptRef{Pack: "v1-embedded"},
		Harness: mph.HarnessSettings{
			Adapter: "v1-as-patched",
			Settings: map[string]string{
				"maestro_bin":   fakeBin,
				"source_dir":    sourceDir,
				"poll_interval": "500ms",
			},
		},
		Budget: mph.DeclaredBudget{ExpectedTokensPerRun: 1000, ExpectedCostUSDPerRun: 0.1, MaxCostUSDPerRun: 5, MaxCostUSDPerSuite: 50},
	}
	if err := bundle.Validate(); err != nil {
		t.Fatalf("bundle: %v", err)
	}
	hash, err := contenthash.CanonicalJSON(bundle)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return &mph.Loaded{Bundle: bundle, Hash: hash, Path: "in-memory"}
}

// prepareFakeBin stages fake-maestro with FAKE_DB wired via a wrapper.
// extraEnv lines are exported verbatim before exec (e.g. "FAKE_NO_USAGE=1").
func prepareFakeBin(t *testing.T, extraEnv ...string) string {
	t.Helper()
	script, err := filepath.Abs("testdata/fake-maestro.sh")
	if err != nil {
		t.Fatalf("script path: %v", err)
	}
	if err := os.Chmod(script, 0o755); err != nil { //nolint:gosec // test executable
		t.Fatalf("chmod script: %v", err)
	}
	cannedDB := writeCannedDB(t)
	wrapper := filepath.Join(t.TempDir(), "fake-maestro")
	content := "#!/bin/sh\nexport FAKE_DB=\"" + cannedDB + "\"\n"
	for _, env := range extraEnv {
		content += "export " + env + "\n"
	}
	content += "exec \"" + script + "\" \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(content), 0o755); err != nil { //nolint:gosec // test executable
		t.Fatalf("wrapper: %v", err)
	}
	return wrapper
}

func TestV1AdapterEndToEnd(t *testing.T) {
	requireDocker(t)
	sourceDir := sourceRoot(t)
	adapter := New()
	t.Cleanup(func() {
		if err := adapter.Close(); err != nil {
			t.Errorf("adapter close: %v", err)
		}
	})
	repo, pin := makeLocalFixture(t)
	store, err := results.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	eng := &engine.Engine{
		Adapters: map[string]target.Adapter{"v1-as-patched": adapter},
		Store:    store,
		Workdir:  t.TempDir(),
		Logf:     t.Logf,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rec, err := eng.RunAttempt(ctx, e2eStory(t, repo, pin), e2eBundle(t, prepareFakeBin(t), sourceDir), "suite-v1-e2e", 1)
	if err != nil {
		t.Fatalf("run attempt: %v", err)
	}
	if rec.Verdict != runrecord.VerdictAccepted {
		for i := range rec.Evidence {
			location := rec.Evidence[i].Location
			if rec.Evidence[i].Kind == "evidence-dir" {
				location = filepath.Join(location, "maestro-launch.log")
			}
			if strings.HasSuffix(location, "maestro-launch.log") {
				if raw, readErr := os.ReadFile(location); readErr == nil {
					t.Logf("launch log:\n%s", raw)
				}
			}
		}
		t.Fatalf("want accepted, got %s/%s (%s; reason: %s)", rec.Verdict, rec.FailureKind, rec.InvalidReason,
			rec.Metrics[runrecord.MetricTokensTotal].Reason)
	}
	if rec.Target.BudgetEnforcement != runrecord.EnforcementStreamed {
		t.Fatalf("v1-as-patched streams via the P-1 usage surface, got %s", rec.Target.BudgetEnforcement)
	}
	if tokens, ok := rec.Metrics[runrecord.MetricTokensTotal].Float64(); !ok || tokens != 12000 {
		t.Fatalf("canonical tokens from the usage log, got %+v", rec.Metrics[runrecord.MetricTokensTotal])
	}
	if calls, ok := rec.Metrics[runrecord.MetricLLMCalls].Float64(); !ok || calls != 2 {
		t.Fatalf("llm_calls from the usage log, got %+v", rec.Metrics[runrecord.MetricLLMCalls])
	}
	if cost, ok := rec.Metrics[runrecord.MetricCostUSD].Float64(); !ok || cost != 0.75 {
		t.Fatalf("canonical cost from the usage log, got %+v", rec.Metrics[runrecord.MetricCostUSD])
	}
	assertEvidence(t, rec, "pr", "diff", "test-output", "db", "usage")
	// The fixture must be untouched: no refs beyond main.
	refs, err := gitx.LsRemoteHeads(ctx, ".", repo, "refs/heads/*")
	if err != nil || len(refs) != 1 {
		t.Fatalf("fixture must hold only main after the run: %v (%v)", refs, err)
	}
}

// TestV1AdapterFailsWithoutUsageLog pins the streamed-enforcement contract:
// a run whose usage log never appears must FAIL, not succeed with
// unavailable usage — otherwise the target spends with enforcement blind
// while still declaring streamed.
func TestV1AdapterFailsWithoutUsageLog(t *testing.T) {
	requireDocker(t)
	sourceDir := sourceRoot(t)
	adapter := New()
	t.Cleanup(func() {
		if err := adapter.Close(); err != nil {
			t.Errorf("adapter close: %v", err)
		}
	})
	repo, pin := makeLocalFixture(t)
	store, err := results.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	eng := &engine.Engine{
		Adapters: map[string]target.Adapter{"v1-as-patched": adapter},
		Store:    store,
		Workdir:  t.TempDir(),
		Logf:     t.Logf,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rec, err := eng.RunAttempt(ctx, e2eStory(t, repo, pin), e2eBundle(t, prepareFakeBin(t, "FAKE_NO_USAGE=1"), sourceDir), "suite-v1-e2e-nousage", 1)
	if err != nil {
		t.Fatalf("run attempt: %v", err)
	}
	if rec.Verdict != runrecord.VerdictFailed || rec.FailureKind != runrecord.FailureTargetError {
		t.Fatalf("run without a usage log must fail as target-error, got %s/%s", rec.Verdict, rec.FailureKind)
	}
}

func assertEvidence(t *testing.T, rec *runrecord.RunRecord, kinds ...string) {
	t.Helper()
	have := map[string]string{}
	for i := range rec.Evidence {
		have[rec.Evidence[i].Kind] = rec.Evidence[i].Location
	}
	for _, kind := range kinds {
		location, ok := have[kind]
		if !ok {
			t.Fatalf("missing %s evidence: %+v", kind, rec.Evidence)
		}
		if _, err := os.Stat(location); err != nil {
			t.Fatalf("%s evidence must be durable at %s: %v", kind, location, err)
		}
	}
}

func TestGiteaLifecycle(t *testing.T) {
	requireDocker(t)
	manager := newGiteaManager()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := manager.ensureRunning(ctx); err != nil {
		t.Fatalf("ensure gitea: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.teardown(context.Background()); err != nil {
			t.Errorf("teardown: %v", err)
		}
	})
	workspace := t.TempDir()
	repo, pin := makeLocalFixture(t)
	if err := gitx.Clone(ctx, repo, filepath.Join(workspace, "ws")); err != nil {
		t.Fatalf("clone: %v", err)
	}
	ws := filepath.Join(workspace, "ws")
	cloneURL, authURL, err := manager.createSeededRepo(ctx, "lifecycle-test--r1", ws, pin)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if cloneURL == "" || authURL == "" {
		t.Fatalf("urls must be returned")
	}
	// The seeded repo serves the pin.
	head, err := gitx.Run(ctx, ws, "ls-remote", authURL, "refs/heads/main")
	if err != nil || !strings.Contains(head, pin) {
		t.Fatalf("seeded main must be the pin: %q (%v)", head, err)
	}
	if err := manager.deleteRepo(ctx, "lifecycle-test--r1"); err != nil {
		t.Fatalf("delete repo: %v", err)
	}
	// Teardown is idempotent: a second call (nothing left to remove) still
	// succeeds — retryability preserved.
	if err := manager.teardown(ctx); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if err := manager.teardown(ctx); err != nil {
		t.Fatalf("teardown must be idempotent: %v", err)
	}
}

func TestSessionContainerSweep(t *testing.T) {
	requireDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	sessionID := "sweep-test-session"
	// Simulate a leaked v1 coder container carrying the session label.
	if err := dockerRun(ctx, "run", "-d", "--name", "golden-sweep-test",
		"--label", sessionLabel+"="+sessionID,
		giteaImage, "sleep", "300"); err != nil {
		t.Fatalf("start labeled container: %v", err)
	}
	t.Cleanup(func() { _ = dockerRemoveIfExists(context.Background(), "rm", "-f", "golden-sweep-test") }) //nolint:errcheck // best-effort cleanup
	if err := sweepSessionContainers(ctx, sessionID); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	// Sweeping an already-clean session succeeds (idempotent).
	if err := sweepSessionContainers(ctx, sessionID); err != nil {
		t.Fatalf("sweep must be idempotent: %v", err)
	}
}

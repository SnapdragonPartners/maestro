package engine_test

// Hermetic end-to-end engine tests: local bare git fixtures in TempDir
// (file:// remotes) driven through the scripted stub target. No network,
// no tokens, plain test tag (design_engine.md).

import (
	"context"
	"os"
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
	"github.com/SnapdragonPartners/maestro/benchmark/target/stubtarget"
)

// makeFixture creates a local bare fixture repo with one seed commit and
// returns its URL and pinned commit.
func makeFixture(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	bare := root + "/fixture.git"
	if _, err := gitx.Run(ctx, ".", "init", "--bare", "--quiet", "--initial-branch=main", bare); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	seed := root + "/seed"
	if err := gitx.Clone(ctx, bare, seed); err != nil {
		t.Fatalf("clone seed: %v", err)
	}
	if _, err := gitx.Run(ctx, seed, "checkout", "--quiet", "-b", "main"); err != nil {
		t.Fatalf("seed branch: %v", err)
	}
	writeFile(t, seed+"/base.txt", "base content\n")
	if _, err := gitx.Run(ctx, seed, "add", "-A"); err != nil {
		t.Fatalf("seed add: %v", err)
	}
	if _, err := gitx.Run(ctx, seed, "-c", "user.name=seed", "-c", "user.email=seed@invalid",
		"commit", "--quiet", "-m", "seed"); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
	if err := gitx.Push(ctx, seed, "main"); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	pin, err := gitx.Head(ctx, seed)
	if err != nil {
		t.Fatalf("seed head: %v", err)
	}
	return bare, pin
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// testStory builds a loaded story against the fixture.
func testStory(t *testing.T, repoURL, pin string, budget story.Budget) *story.Loaded {
	t.Helper()
	def := &story.Definition{
		SchemaVersion: story.SchemaVersion,
		ID:            "stub-story",
		Title:         "Stub story",
		Level:         story.LevelStory,
		Fixture:       story.Fixture{Repo: repoURL, Commit: pin, BaseBranch: "main"},
		Prompt:        story.Prompt{Text: "write solution.txt containing done"},
		Expectations: story.Expectations{
			AllowedPaths:  []string{"solution.txt"},
			EvidenceShape: []string{"diff", "test-output"},
		},
		Validators: []story.Validator{
			{Name: "solution-exists", Command: "test -f solution.txt"},
		},
		Checks: []story.Check{
			{Name: "diff-confined", Type: story.CheckFilesChangedWithin},
			{Name: "marker", Type: story.CheckFileContains, Path: "solution.txt", Contains: "done"},
			{Name: "always", Type: story.CheckCommand, Command: "true"},
		},
		Budget: budget,
	}
	if err := def.Validate(); err != nil {
		t.Fatalf("test story invalid: %v", err)
	}
	hash, err := contenthash.CanonicalJSON(def)
	if err != nil {
		t.Fatalf("story hash: %v", err)
	}
	return &story.Loaded{Definition: def, Hash: hash, Path: "in-memory"}
}

func testBundle(t *testing.T, budget mph.DeclaredBudget) *mph.Loaded {
	t.Helper()
	bundle := &mph.Bundle{
		SchemaVersion: mph.SchemaVersion,
		Name:          "stub-config",
		Description:   "integration test bundle",
		Model:         mph.ModelRouting{Default: "stub:model"},
		Prompt:        mph.PromptRef{Pack: "stub-pack"},
		Harness:       mph.HarnessSettings{Adapter: "stub"},
		Budget:        budget,
	}
	if err := bundle.Validate(); err != nil {
		t.Fatalf("test bundle invalid: %v", err)
	}
	hash, err := contenthash.CanonicalJSON(bundle)
	if err != nil {
		t.Fatalf("bundle hash: %v", err)
	}
	return &mph.Loaded{Bundle: bundle, Hash: hash, Path: "in-memory"}
}

func defaultBudgets() (story.Budget, mph.DeclaredBudget) {
	return story.Budget{MaxTokens: 100000, MaxWallClockSeconds: 60, MaxCostUSD: 1.0},
		mph.DeclaredBudget{
			ExpectedTokensPerRun:  1000,
			ExpectedCostUSDPerRun: 0.05,
			MaxCostUSDPerRun:      1.0,
			MaxCostUSDPerSuite:    10.0,
		}
}

// newEngine builds an engine around a stub and a fresh store.
func newEngine(t *testing.T, stub *stubtarget.Stub) (*engine.Engine, *results.Store) {
	t.Helper()
	store, err := results.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return &engine.Engine{
		Adapters: map[string]target.Adapter{"stub": stub},
		Store:    store,
		Workdir:  t.TempDir(),
	}, store
}

// runOne executes one attempt with the given stub and returns the record.
func runOne(t *testing.T, stub *stubtarget.Stub, sb story.Budget) *runrecord.RunRecord {
	t.Helper()
	repoURL, pin := makeFixture(t)
	db := mph.DeclaredBudget{ExpectedTokensPerRun: 1000, ExpectedCostUSDPerRun: 0.05, MaxCostUSDPerRun: 1.0, MaxCostUSDPerSuite: 10.0}
	eng, _ := newEngine(t, stub)
	rec, err := eng.RunAttempt(context.Background(), testStory(t, repoURL, pin, sb), testBundle(t, db), "suite-int", 1)
	if err != nil {
		t.Fatalf("run attempt: %v", err)
	}
	if vErr := rec.Validate(); vErr != nil {
		t.Fatalf("record must be contract-valid: %v", vErr)
	}
	return rec
}

func solutionStub() *stubtarget.Stub {
	return &stubtarget.Stub{Files: map[string]string{"solution.txt": "done\n"}}
}

func TestAcceptedEndToEnd(t *testing.T) {
	sb, _ := defaultBudgets()
	rec := runOne(t, solutionStub(), sb)
	if rec.Verdict != runrecord.VerdictAccepted {
		t.Fatalf("want accepted, got %s/%s (%s)", rec.Verdict, rec.FailureKind, rec.InvalidReason)
	}
	if rec.SolutionCommit == "" || !rec.Isolation.CleanupVerified {
		t.Fatalf("accepted record incomplete: %+v", rec)
	}
}

func TestInPlaceSolutionSnapshot(t *testing.T) {
	sb, _ := defaultBudgets()
	stub := solutionStub()
	stub.InPlace = true
	rec := runOne(t, stub, sb)
	if rec.Verdict != runrecord.VerdictAccepted {
		t.Fatalf("in-place solution must be snapshot-bound and accepted, got %s/%s (%s)", rec.Verdict, rec.FailureKind, rec.InvalidReason)
	}
}

func TestWallClockOverrun(t *testing.T) {
	sb, _ := defaultBudgets()
	sb.MaxWallClockSeconds = 1
	stub := solutionStub()
	stub.SleepFor = 3 * time.Second
	rec := runOne(t, stub, sb)
	if rec.Verdict != runrecord.VerdictFailed || rec.FailureKind != runrecord.FailureBudgetOverrun {
		t.Fatalf("want failed/budget-overrun, got %s/%s", rec.Verdict, rec.FailureKind)
	}
	if rec.Metrics[runrecord.MetricTokensTotal].Status != runrecord.StatusUnavailable {
		t.Fatalf("aborted attempts must synthesize unavailable metrics, got %+v", rec.Metrics[runrecord.MetricTokensTotal])
	}
}

func TestStreamedUsageOverrun(t *testing.T) {
	sb, _ := defaultBudgets()
	sb.MaxTokens = 500
	stub := solutionStub()
	stub.Usage = []target.UsageDelta{{Tokens: 400}, {Tokens: 400}}
	stub.SleepFor = 5 * time.Second // must be cut short by the usage abort
	start := time.Now()
	rec := runOne(t, stub, sb)
	if rec.Verdict != runrecord.VerdictFailed || rec.FailureKind != runrecord.FailureBudgetOverrun {
		t.Fatalf("want failed/budget-overrun, got %s/%s", rec.Verdict, rec.FailureKind)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("streamed overrun must abort promptly, took %s", elapsed)
	}
}

func TestOrphanSolutionRejected(t *testing.T) {
	sb, _ := defaultBudgets()
	stub := solutionStub()
	stub.OrphanSolution = true
	rec := runOne(t, stub, sb)
	if rec.Verdict != runrecord.VerdictFailed || rec.FailureKind != runrecord.FailureBranchState {
		t.Fatalf("non-descendant solutions must fail branch-state, got %s/%s", rec.Verdict, rec.FailureKind)
	}
}

func TestLeftBehindRefIsInvalid(t *testing.T) {
	sb, _ := defaultBudgets()
	stub := solutionStub()
	stub.LeaveRef = true
	rec := runOne(t, stub, sb)
	if rec.Verdict != runrecord.VerdictInvalid {
		t.Fatalf("unverifiable cleanup must be invalid, got %s/%s", rec.Verdict, rec.FailureKind)
	}
	if !strings.Contains(rec.InvalidReason, "refs left behind") {
		t.Fatalf("reason must name the leftover refs, got %q", rec.InvalidReason)
	}
}

func TestPinMismatchIsInvalid(t *testing.T) {
	repoURL, _ := makeFixture(t)
	sb, db := defaultBudgets()
	bogus := strings.Repeat("ab", 20)
	eng, _ := newEngine(t, solutionStub())
	rec, err := eng.RunAttempt(context.Background(), testStory(t, repoURL, bogus, sb), testBundle(t, db), "suite-int", 1)
	if err != nil {
		t.Fatalf("run attempt: %v", err)
	}
	if rec.Verdict != runrecord.VerdictInvalid || !strings.Contains(rec.InvalidReason, "isolation") {
		t.Fatalf("unpinnable fixtures must be invalid, got %s (%s)", rec.Verdict, rec.InvalidReason)
	}
}

func TestValidatorFailure(t *testing.T) {
	sb, _ := defaultBudgets()
	rec := runOne(t, &stubtarget.Stub{}, sb) // no solution.txt written
	if rec.Verdict != runrecord.VerdictFailed || rec.FailureKind != runrecord.FailureValidatorFailed {
		t.Fatalf("want failed/validator-failed, got %s/%s", rec.Verdict, rec.FailureKind)
	}
}

func TestChecksFailureOnDiffOutsideAllowedPaths(t *testing.T) {
	sb, _ := defaultBudgets()
	stub := solutionStub()
	stub.Files["sneaky.txt"] = "outside allowed paths\n"
	rec := runOne(t, stub, sb)
	if rec.Verdict != runrecord.VerdictFailed || rec.FailureKind != runrecord.FailureChecksFailed {
		t.Fatalf("want failed/checks-failed, got %s/%s", rec.Verdict, rec.FailureKind)
	}
}

func TestEvidenceMissing(t *testing.T) {
	sb, _ := defaultBudgets()
	stub := solutionStub()
	stub.EvidenceKinds = []string{"diff"} // story also expects test-output
	rec := runOne(t, stub, sb)
	if rec.Verdict != runrecord.VerdictFailed || rec.FailureKind != runrecord.FailureEvidenceMissing {
		t.Fatalf("want failed/evidence-missing, got %s/%s", rec.Verdict, rec.FailureKind)
	}
}

func TestRepeatIsolation(t *testing.T) {
	repoURL, pin := makeFixture(t)
	sb, db := defaultBudgets()
	eng, store := newEngine(t, solutionStub())
	st, bundle := testStory(t, repoURL, pin, sb), testBundle(t, db)
	first, err := eng.RunAttempt(context.Background(), st, bundle, "suite-int", 1)
	if err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	second, err := eng.RunAttempt(context.Background(), st, bundle, "suite-int", 2)
	if err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if first.RunID == second.RunID || first.Isolation.WorkspaceDir == second.Isolation.WorkspaceDir {
		t.Fatalf("repeats must not share identity or workspace: %s vs %s", first.RunID, second.RunID)
	}
	for _, rec := range []*runrecord.RunRecord{first, second} {
		if rec.Verdict != runrecord.VerdictAccepted {
			t.Fatalf("repeat not accepted: %s/%s (%s)", rec.Verdict, rec.FailureKind, rec.InvalidReason)
		}
	}
	back, err := store.ReadSuite("suite-int")
	if err != nil || len(back) != 2 {
		t.Fatalf("expected 2 stored records, got %d (%v)", len(back), err)
	}
	refs, err := gitx.LsRemoteHeads(context.Background(), ".", repoURL, "refs/heads/golden/*")
	if err != nil || len(refs) != 0 {
		t.Fatalf("fixture must be clean after runs: %v %v", refs, err)
	}
}

func TestSuiteBudgetAdmissionAndManifest(t *testing.T) {
	repoURL, pin := makeFixture(t)
	sb, _ := defaultBudgets()
	db := mph.DeclaredBudget{
		ExpectedTokensPerRun:  1000,
		ExpectedCostUSDPerRun: 0.6,
		MaxCostUSDPerRun:      0.8,
		MaxCostUSDPerSuite:    1.0, // admits exactly one attempt at 0.6
	}
	eng, store := newEngine(t, solutionStub())
	manifest, err := eng.RunSuite(context.Background(), engine.SuiteParams{
		SuiteRunID: "suite-budget",
		Stories:    []*story.Loaded{testStory(t, repoURL, pin, sb)},
		Bundles:    []*mph.Loaded{testBundle(t, db)},
		Repeats:    2,
	})
	if err != nil {
		t.Fatalf("run suite: %v", err)
	}
	if manifest.StopReason != results.StopSuiteBudgetExhausted {
		t.Fatalf("want suite-budget-exhausted, got %s", manifest.StopReason)
	}
	statuses := map[string]int{}
	for _, attempt := range manifest.Attempts {
		statuses[attempt.Status]++
	}
	if statuses[results.AttemptCompleted] != 1 || statuses[results.AttemptSkipped] != 1 {
		t.Fatalf("want 1 completed + 1 skipped, got %v", statuses)
	}
	back, err := store.ReadManifest("suite-budget")
	if err != nil || back.StopReason != manifest.StopReason {
		t.Fatalf("manifest must round-trip: %v %v", back, err)
	}
}

func TestSuiteCompletes(t *testing.T) {
	repoURL, pin := makeFixture(t)
	sb, db := defaultBudgets()
	eng, _ := newEngine(t, solutionStub())
	manifest, err := eng.RunSuite(context.Background(), engine.SuiteParams{
		SuiteRunID: "suite-full",
		Stories:    []*story.Loaded{testStory(t, repoURL, pin, sb)},
		Bundles:    []*mph.Loaded{testBundle(t, db)},
		Repeats:    2,
	})
	if err != nil {
		t.Fatalf("run suite: %v", err)
	}
	if manifest.StopReason != results.StopCompleted {
		t.Fatalf("want completed, got %s", manifest.StopReason)
	}
	for _, attempt := range manifest.Attempts {
		if attempt.Status != results.AttemptCompleted {
			t.Fatalf("every attempt must complete: %+v", attempt)
		}
	}
}

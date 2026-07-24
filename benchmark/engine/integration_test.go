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
	writeFile(t, seed+"/.gitignore", "ignored.txt\n")
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
		SchemaVersion: story.SchemaV1,
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

// testLocalBundle builds a local (token-dimension) stub bundle with the
// given per-run and per-suite token caps.
func testLocalBundle(t *testing.T, perRun, perSuite int64) *mph.Loaded {
	t.Helper()
	bundle := &mph.Bundle{
		SchemaVersion: mph.SchemaVersion,
		Name:          "stub-local",
		Description:   "integration test local bundle",
		Model:         mph.ModelRouting{Default: "stub:model"},
		Prompt:        mph.PromptRef{Pack: "stub-pack"},
		Harness:       mph.HarnessSettings{Adapter: "stub"},
		Local:         true,
		Budget: mph.DeclaredBudget{
			ExpectedTokensPerRun: 100,
			MaxTokensPerRun:      perRun,
			MaxTokensPerSuite:    perSuite,
		},
	}
	if err := bundle.Validate(); err != nil {
		t.Fatalf("test local bundle invalid: %v", err)
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
	// test-output is engine-contributed now, so the uncoverable kind must
	// be one neither the target nor the engine supplies: a required "pr"
	// artifact from a stub that doesn't produce one.
	repoURL, pin := makeFixture(t)
	sb, db := defaultBudgets()
	loaded := testStory(t, repoURL, pin, sb)
	loaded.Definition.Expectations.RequiredArtifacts = []string{"pr"}
	eng, _ := newEngine(t, solutionStub())
	rec, err := eng.RunAttempt(context.Background(), loaded, testBundle(t, db), "suite-int", 1)
	if err != nil {
		t.Fatalf("run attempt: %v", err)
	}
	if rec.Verdict != runrecord.VerdictFailed || rec.FailureKind != runrecord.FailureEvidenceMissing {
		t.Fatalf("want failed/evidence-missing, got %s/%s", rec.Verdict, rec.FailureKind)
	}
}

func TestEngineContributesValidatorEvidence(t *testing.T) {
	sb, _ := defaultBudgets()
	rec := runOne(t, solutionStub(), sb)
	found := ""
	for i := range rec.Evidence {
		if rec.Evidence[i].Kind == "test-output" && strings.Contains(rec.Evidence[i].Location, "validator-00") {
			found = rec.Evidence[i].Location
		}
	}
	if found == "" {
		t.Fatalf("engine must contribute validator test-output evidence: %+v", rec.Evidence)
	}
	if _, err := os.Stat(found); err != nil {
		t.Fatalf("evidence file must survive cleanup: %v", err)
	}
}

// TestAttemptPersistsFullValidatorOutput exercises the REAL migrated path —
// RunAttempt → verify → engine.Verify → writeValidatorEvidence — rather than
// calling the pieces by hand. A validator emits far more than any truncation
// limit with a tail sentinel; the persisted validator evidence must retain
// that sentinel, which it would not if attempt.verify dropped or misrouted
// ValidatorOutputs (e.g. wrote the truncated Detail instead of the full
// output).
func TestAttemptPersistsFullValidatorOutput(t *testing.T) {
	const sentinel = "_ATTEMPT_TAIL_SENTINEL_"
	repoURL, pin := makeFixture(t)
	sb, db := defaultBudgets()

	def := &story.Definition{
		SchemaVersion: story.SchemaV1,
		ID:            "big-validator-story",
		Title:         "Big validator output",
		Level:         story.LevelStory,
		Fixture:       story.Fixture{Repo: repoURL, Commit: pin, BaseBranch: "main"},
		Prompt:        story.Prompt{Text: "write solution.txt containing done"},
		Expectations: story.Expectations{
			AllowedPaths:  []string{"solution.txt"},
			EvidenceShape: []string{"diff", "test-output"},
		},
		Validators: []story.Validator{
			// ~5000 'A's (well beyond any detail-truncation limit) then the
			// sentinel on its own line, exiting 0 so the attempt proceeds.
			{Name: "big", Command: "head -c 5000 /dev/zero | tr '\\0' 'A'; echo " + sentinel},
		},
		Checks: []story.Check{{Name: "always", Type: story.CheckCommand, Command: "true"}},
		Budget: sb,
	}
	if err := def.Validate(); err != nil {
		t.Fatalf("story invalid: %v", err)
	}
	hash, err := contenthash.CanonicalJSON(def)
	if err != nil {
		t.Fatalf("story hash: %v", err)
	}
	loaded := &story.Loaded{Definition: def, Hash: hash, Path: "in-memory"}

	eng, _ := newEngine(t, solutionStub())
	rec, err := eng.RunAttempt(context.Background(), loaded, testBundle(t, db), "suite-int", 1)
	if err != nil {
		t.Fatalf("run attempt: %v", err)
	}

	var evPath string
	for i := range rec.Evidence {
		if rec.Evidence[i].Kind == "test-output" && strings.Contains(rec.Evidence[i].Location, "validator-00") {
			evPath = rec.Evidence[i].Location
		}
	}
	if evPath == "" {
		t.Fatalf("no validator test-output evidence produced: %+v", rec.Evidence)
	}
	body, readErr := os.ReadFile(evPath)
	if readErr != nil {
		t.Fatalf("read evidence: %v", readErr)
	}
	if !strings.Contains(string(body), sentinel) {
		t.Fatal("persisted validator evidence lost the tail sentinel — attempt→verify dropped the full output")
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
	// Admission reserves the effective per-run cap (0.8). The suite cap
	// admits exactly one reservation; after it settles to the observed
	// $0.01, the next reservation (0.01 + 0.8) still exceeds the cap.
	db := mph.DeclaredBudget{
		ExpectedTokensPerRun:  1000,
		ExpectedCostUSDPerRun: 0.6,
		MaxCostUSDPerRun:      0.8,
		MaxCostUSDPerSuite:    0.8,
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

// TestLocalSuiteTokenAdmissionAndSettlement covers the token budget
// dimension end-to-end (item 5.1): the suite reserves the effective per-run
// token cap, settles to the observed tokens, and the second reservation
// no longer fits — with per-config token accounting in the v2 manifest.
func TestLocalSuiteTokenAdmissionAndSettlement(t *testing.T) {
	repoURL, pin := makeFixture(t)
	sb, _ := defaultBudgets() // story MaxTokens 100000
	// effective per-run reserve = min(100000, 2000) = 2000; the observed
	// tokens (stub reports 1000) settle the charge, and 1000+2000 > the 2000
	// suite cap, so the second attempt is skipped.
	eng, _ := newEngine(t, solutionStub())
	manifest, err := eng.RunSuite(context.Background(), engine.SuiteParams{
		SuiteRunID: "suite-local",
		Stories:    []*story.Loaded{testStory(t, repoURL, pin, sb)},
		Bundles:    []*mph.Loaded{testLocalBundle(t, 2000, 2000)},
		Repeats:    2,
	})
	if err != nil {
		t.Fatalf("run suite: %v", err)
	}
	if manifest.StopReason != results.StopSuiteBudgetExhausted {
		t.Fatalf("want suite-budget-exhausted, got %s", manifest.StopReason)
	}
	statuses := map[string]int{}
	for _, a := range manifest.Attempts {
		statuses[a.Status]++
	}
	if statuses[results.AttemptCompleted] != 1 || statuses[results.AttemptSkipped] != 1 {
		t.Fatalf("want 1 completed + 1 skipped, got %v", statuses)
	}
	if len(manifest.BudgetAccounts) != 1 {
		t.Fatalf("want 1 budget account, got %d", len(manifest.BudgetAccounts))
	}
	acct := manifest.BudgetAccounts[0]
	if acct.Dimension != results.DimensionTokens || acct.Cap != 2000 || acct.Charged != 1000 || acct.Observed != 1000 {
		t.Fatalf("token account must settle to observed tokens: %+v", acct)
	}
}

// TestLocalCostViolationAndReservationRetention covers two P1 invariants at
// once: streamed USD under a local config fails the attempt (a local config
// must not spend dollars), and the resulting unavailable token metric keeps
// the full suite reservation (never settled down).
func TestLocalCostViolationAndReservationRetention(t *testing.T) {
	repoURL, pin := makeFixture(t)
	sb, _ := defaultBudgets()
	stub := solutionStub()
	// Cost-only stream: trips the local-cost invariant, and with zero tokens
	// the failed record's tokens_total is unavailable.
	stub.Usage = []target.UsageDelta{{Tokens: 0, CostUSD: 0.5}}
	eng, _ := newEngine(t, stub)
	rec, err := eng.RunAttempt(context.Background(), testStory(t, repoURL, pin, sb), testLocalBundle(t, 2000, 10000), "suite-local-viol", 1)
	if err != nil {
		t.Fatalf("run attempt: %v", err)
	}
	if rec.Verdict != runrecord.VerdictFailed || rec.FailureKind != runrecord.FailureTargetError {
		t.Fatalf("streamed USD under local must fail as target-error, got %s/%s", rec.Verdict, rec.FailureKind)
	}
	if rec.Metrics[runrecord.MetricTokensTotal].Status != runrecord.StatusUnavailable {
		t.Fatalf("failed local run must have unavailable tokens, got %+v", rec.Metrics[runrecord.MetricTokensTotal])
	}
	if rec.Metrics[runrecord.MetricCostUSD].Status == runrecord.StatusValue {
		t.Fatalf("local cost must never be a measured value, got %+v", rec.Metrics[runrecord.MetricCostUSD])
	}
	// Now the suite view: the reservation is retained (unavailable ⇒ keep max).
	manifest, err := eng.RunSuite(context.Background(), engine.SuiteParams{
		SuiteRunID: "suite-local-retain",
		Stories:    []*story.Loaded{testStory(t, repoURL, pin, sb)},
		Bundles:    []*mph.Loaded{testLocalBundle(t, 2000, 10000)},
		Repeats:    1,
	})
	if err != nil {
		t.Fatalf("run suite: %v", err)
	}
	acct := manifest.BudgetAccounts[0]
	if acct.Charged != 2000 || acct.Observed != 0 {
		t.Fatalf("unavailable tokens must retain the full reservation, got %+v", acct)
	}
}

// TestMixedHostedLocalSuiteAccounts pins that a suite mixing a hosted (USD)
// and a local (token) config produces independent per-config budget_accounts
// in their own dimensions.
func TestMixedHostedLocalSuiteAccounts(t *testing.T) {
	repoURL, pin := makeFixture(t)
	sb, db := defaultBudgets()
	eng, _ := newEngine(t, solutionStub())
	manifest, err := eng.RunSuite(context.Background(), engine.SuiteParams{
		SuiteRunID: "suite-mixed",
		Stories:    []*story.Loaded{testStory(t, repoURL, pin, sb)},
		Bundles:    []*mph.Loaded{testBundle(t, db), testLocalBundle(t, 5000, 20000)},
		Repeats:    1,
	})
	if err != nil {
		t.Fatalf("run suite: %v", err)
	}
	if len(manifest.BudgetAccounts) != 2 {
		t.Fatalf("mixed suite must carry 2 accounts, got %d", len(manifest.BudgetAccounts))
	}
	byDim := map[string]results.BudgetAccount{}
	for _, a := range manifest.BudgetAccounts {
		byDim[a.Dimension] = a
	}
	usd, okU := byDim[results.DimensionUSD]
	tok, okT := byDim[results.DimensionTokens]
	if !okU || !okT {
		t.Fatalf("mixed suite must have one usd and one tokens account, got %+v", manifest.BudgetAccounts)
	}
	if usd.Config != "stub-config" || tok.Config != "stub-local" {
		t.Fatalf("accounts must map to their configs, got usd=%q tokens=%q", usd.Config, tok.Config)
	}
	if tok.Observed != 1000 || usd.Observed != 0.01 {
		t.Fatalf("each account accrues in its own dimension: usd=%+v tokens=%+v", usd, tok)
	}
}

func TestStreamedTotalsSurviveAbort(t *testing.T) {
	sb, _ := defaultBudgets()
	sb.MaxTokens = 500
	stub := solutionStub()
	stub.Usage = []target.UsageDelta{{Tokens: 400, CostUSD: 0.02}, {Tokens: 400, CostUSD: 0.02}}
	stub.SleepFor = 5 * time.Second
	rec := runOne(t, stub, sb)
	if rec.FailureKind != runrecord.FailureBudgetOverrun {
		t.Fatalf("want budget-overrun, got %s/%s", rec.Verdict, rec.FailureKind)
	}
	tokens, ok := rec.Metrics[runrecord.MetricTokensTotal].Float64()
	if !ok || tokens != 800 {
		t.Fatalf("aborted attempts must retain streamed token totals (ADR 0025), got %+v", rec.Metrics[runrecord.MetricTokensTotal])
	}
	cost, ok := rec.Metrics[runrecord.MetricCostUSD].Float64()
	if !ok || cost != 0.04 {
		t.Fatalf("aborted attempts must retain streamed cost, got %+v", rec.Metrics[runrecord.MetricCostUSD])
	}
}

func TestWallClockCoversVerification(t *testing.T) {
	// The story's validator hangs; the attempt-wide deadline must bound it
	// and classify the attempt as budget-overrun.
	repoURL, pin := makeFixture(t)
	sb, db := defaultBudgets()
	sb.MaxWallClockSeconds = 1
	loaded := testStory(t, repoURL, pin, sb)
	loaded.Definition.Validators = append(loaded.Definition.Validators, story.Validator{Name: "hung", Command: "sleep 30"})
	eng, _ := newEngine(t, solutionStub())
	start := time.Now()
	rec, err := eng.RunAttempt(context.Background(), loaded, testBundle(t, db), "suite-int", 1)
	if err != nil {
		t.Fatalf("run attempt: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("verification must be bounded by the wall-clock cap, took %s", elapsed)
	}
	if rec.Verdict != runrecord.VerdictFailed || rec.FailureKind != runrecord.FailureBudgetOverrun {
		t.Fatalf("hung verification must be budget-overrun, got %s/%s", rec.Verdict, rec.FailureKind)
	}
}

func TestStreamedTotalsAreCanonicalOnSuccess(t *testing.T) {
	// The stub streams more usage than its final observation reports; the
	// record must carry the streamed totals — the values that enforced the
	// cap are the values suite settlement reads.
	sb, _ := defaultBudgets()
	stub := solutionStub()
	stub.Usage = []target.UsageDelta{{Tokens: 5000, CostUSD: 0.5}}
	rec := runOne(t, stub, sb)
	if rec.Verdict != runrecord.VerdictAccepted {
		t.Fatalf("want accepted, got %s/%s", rec.Verdict, rec.FailureKind)
	}
	tokens, _ := rec.Metrics[runrecord.MetricTokensTotal].Float64()
	cost, _ := rec.Metrics[runrecord.MetricCostUSD].Float64()
	if tokens != 5000 || cost != 0.5 {
		t.Fatalf("streamed totals must be canonical when larger: got %v tokens, %v cost", tokens, cost)
	}
}

func TestWallClockMetricIsEngineMeasured(t *testing.T) {
	sb, _ := defaultBudgets()
	rec := runOne(t, solutionStub(), sb)
	// The stub reports 1s; the engine overrides with its own measurement
	// of the budgeted interval. On an accepted run that measurement can
	// never exceed the declared cap — the deadline and the metric share
	// one boundary, excluding engine overhead like the clone.
	wall, ok := rec.Metrics[runrecord.MetricWallClockSeconds].Float64()
	if !ok || wall <= 0 || wall == 1 {
		t.Fatalf("wall clock must be engine-measured, got %v (ok=%v)", wall, ok)
	}
	if wall > float64(sb.MaxWallClockSeconds) {
		t.Fatalf("accepted run's measured wall clock %v exceeds its cap %d", wall, sb.MaxWallClockSeconds)
	}
}

func TestUntrackedJunkCleanedBeforeValidation(t *testing.T) {
	repoURL, pin := makeFixture(t)
	sb, db := defaultBudgets()
	loaded := testStory(t, repoURL, pin, sb)
	loaded.Definition.Validators = append(loaded.Definition.Validators,
		story.Validator{Name: "no-junk", Command: "test ! -f junk.txt"})
	stub := solutionStub()
	stub.JunkFiles = map[string]string{"junk.txt": "left behind by the target\n"}
	eng, _ := newEngine(t, stub)
	rec, err := eng.RunAttempt(context.Background(), loaded, testBundle(t, db), "suite-int", 1)
	if err != nil {
		t.Fatalf("run attempt: %v", err)
	}
	if rec.Verdict != runrecord.VerdictAccepted {
		t.Fatalf("untracked junk must be cleaned before validation, got %s/%s (%s)", rec.Verdict, rec.FailureKind, detailOf(rec, "no-junk"))
	}
}

func TestInPlaceSnapshotIncludesIgnoredFiles(t *testing.T) {
	// The fixture ignores ignored.txt; an in-place solution writing it must
	// have it force-added to the snapshot, where diff-confinement sees it.
	sb, _ := defaultBudgets()
	stub := solutionStub()
	stub.InPlace = true
	stub.Files["ignored.txt"] = "sneaky state validators could otherwise use\n"
	rec := runOne(t, stub, sb)
	if rec.Verdict != runrecord.VerdictFailed || rec.FailureKind != runrecord.FailureChecksFailed {
		t.Fatalf("ignored files must be part of the snapshot and trip diff confinement, got %s/%s", rec.Verdict, rec.FailureKind)
	}
	if detail := detailOf(rec, "diff-confined"); !strings.Contains(detail, "ignored.txt") {
		t.Fatalf("diff confinement must name the ignored file, got %q", detail)
	}
}

func detailOf(rec *runrecord.RunRecord, name string) string {
	for i := range rec.Checks {
		if rec.Checks[i].Name == name {
			return rec.Checks[i].Detail
		}
	}
	for i := range rec.Validators {
		if rec.Validators[i].Name == name {
			return rec.Validators[i].Detail
		}
	}
	return ""
}

func TestFatalSuiteErrorFinalizesManifest(t *testing.T) {
	repoURL, pin := makeFixture(t)
	sb, db := defaultBudgets()
	bundle := testBundle(t, db)
	bundle.Bundle.Harness.Adapter = "missing" // resolves to an engine error
	eng, store := newEngine(t, solutionStub())
	_, err := eng.RunSuite(context.Background(), engine.SuiteParams{
		SuiteRunID: "suite-fatal",
		Stories:    []*story.Loaded{testStory(t, repoURL, pin, sb)},
		Bundles:    []*mph.Loaded{bundle},
		Repeats:    2,
	})
	if err == nil {
		t.Fatalf("unknown adapter must surface an error")
	}
	manifest, readErr := store.ReadManifest("suite-fatal")
	if readErr != nil {
		t.Fatalf("fatal exits must leave a readable manifest: %v", readErr)
	}
	if manifest.StopReason != results.StopInterrupted {
		t.Fatalf("fatal exits must finalize as interrupted, got %s", manifest.StopReason)
	}
	for _, attempt := range manifest.Attempts {
		if attempt.Status == results.AttemptPlanned {
			t.Fatalf("no attempt may remain planned after a fatal exit: %+v", attempt)
		}
	}
	// Manifest-only suites are discoverable.
	ids, err := store.SuiteRunIDs()
	if err != nil || len(ids) != 1 || ids[0] != "suite-fatal" {
		t.Fatalf("manifest-only suites must be listed, got %v (%v)", ids, err)
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

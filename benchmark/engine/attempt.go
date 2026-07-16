package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/internal/gitx"
	"github.com/SnapdragonPartners/maestro/benchmark/mph"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/story"
	"github.com/SnapdragonPartners/maestro/benchmark/target"
)

// errUsageOverrun marks a cancellation caused by streamed-usage caps.
var errUsageOverrun = errors.New("streamed usage exceeded a declared cap") //nolint:gochecknoglobals // sentinel error

// usageTracker accumulates streamed usage and cancels the attempt the
// moment a cap is crossed (design_engine.md budget contract). Totals are
// retained for the record: failed-attempt costs count (ADR 0025).
type usageTracker struct {
	cancel    context.CancelCauseFunc
	mu        sync.Mutex
	tokens    int64
	costUSD   float64
	maxTokens int64
	maxCost   float64
	tripped   bool
}

func (u *usageTracker) report(delta target.UsageDelta) {
	// Malformed deltas are rejected outright: a negative or non-finite
	// delta could otherwise walk totals back under the cap and bypass
	// cancellation.
	if delta.Tokens < 0 || delta.CostUSD < 0 || math.IsNaN(delta.CostUSD) || math.IsInf(delta.CostUSD, 0) {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if delta.Tokens > math.MaxInt64-u.tokens {
		u.tokens = math.MaxInt64 // saturate rather than wrap
	} else {
		u.tokens += delta.Tokens
	}
	// Finite deltas can still sum to +Inf; saturate so the record's
	// measured cost stays finite and validation/append cannot fail.
	if sum := u.costUSD + delta.CostUSD; math.IsInf(sum, 1) {
		u.costUSD = math.MaxFloat64
	} else {
		u.costUSD = sum
	}
	if !u.tripped && (u.tokens > u.maxTokens || u.costUSD > u.maxCost) {
		u.tripped = true
		u.cancel(errUsageOverrun)
	}
}

func (u *usageTracker) overrun() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.tripped
}

// totals returns the accumulated streamed usage.
func (u *usageTracker) totals() (int64, float64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.tokens, u.costUSD
}

// attempt carries the state of one isolated attempt.
type attempt struct {
	engine         *Engine
	spec           *target.AttemptSpec
	story          *story.Loaded
	bundle         *mph.Loaded
	obs            *target.Observation
	tracker        *usageTracker
	adapter        target.Adapter
	engineEvidence []runrecord.EvidencePointer
	solution       string
	started        time.Time
	// workStarted and workEnded bound the budgeted phase (target run
	// through verification). The wall-clock deadline and the canonical
	// wall_clock_seconds metric share exactly this interval — engine
	// overhead (describe, clone) is neither budgeted nor measured, so a
	// slow clone cannot make an accepted record exceed its own cap.
	workStarted time.Time
	workEnded   time.Time
	desc        runrecord.TargetDescriptor
	out         outcome
}

// RunAttempt executes one fully isolated attempt and appends its record to
// the store. The returned record is always contract-valid; the error is
// engine-level (store append, adapter resolution), never a story failure.
func (e *Engine) RunAttempt(ctx context.Context, st *story.Loaded, bundle *mph.Loaded, suiteRunID string, repeat int) (*runrecord.RunRecord, error) {
	adapter, err := e.adapterFor(bundle.Bundle.Harness.Adapter)
	if err != nil {
		return nil, err
	}
	runID, err := newRunID(st.Definition.ID, bundle.Bundle.Name, repeat)
	if err != nil {
		return nil, err
	}
	evidenceDir, err := e.Store.EvidenceDir(runID)
	if err != nil {
		return nil, fmt.Errorf("evidence dir: %w", err)
	}
	a := &attempt{
		engine:  e,
		adapter: adapter,
		story:   st,
		bundle:  bundle,
		started: time.Now(),
		spec: &target.AttemptSpec{
			Story:           st.Definition,
			Bundle:          bundle.Bundle,
			Budget:          effectiveBudget(st.Definition.Budget, bundle.Bundle.Budget),
			RunID:           runID,
			SuiteRunID:      suiteRunID,
			StoryHash:       st.Hash,
			BundleHash:      bundle.Hash,
			WorkspaceDir:    filepath.Join(e.Workdir, runID),
			EvidenceDir:     evidenceDir,
			BranchNamespace: "golden/" + runID,
		},
	}
	rec := a.execute(ctx)
	if appendErr := e.Store.Append(rec); appendErr != nil {
		return rec, fmt.Errorf("append record: %w", appendErr)
	}
	e.logf("%s: %s %s%s", runID, rec.Verdict, rec.FailureKind, invalidSuffix(rec))
	return rec, nil
}

func invalidSuffix(rec *runrecord.RunRecord) string {
	if rec.Verdict == runrecord.VerdictInvalid {
		return " (" + rec.InvalidReason + ")"
	}
	return ""
}

// effectiveBudget bounds the story budget by the bundle's per-run cost cap.
func effectiveBudget(sb story.Budget, db mph.DeclaredBudget) story.Budget {
	if db.MaxCostUSDPerRun < sb.MaxCostUSD {
		sb.MaxCostUSD = db.MaxCostUSDPerRun
	}
	return sb
}

// execute runs the lifecycle: describe → isolate → run → bind → verify →
// cleanup → compose and assemble. The wall-clock cap bounds the whole
// budgeted phase — target run through verification — not just Adapter.Run,
// so a hung validator cannot exceed the declared cap. Cleanup always
// precedes record assembly (the store is append-only) and runs on a fresh
// bounded context.
func (a *attempt) execute(ctx context.Context) *runrecord.RunRecord {
	a.describe(ctx)
	if a.out.describeErr == nil {
		a.isolate(ctx)
	}
	a.workStarted = time.Now()
	if a.out.describeErr == nil && a.out.isolationErr == nil {
		deadline := time.Duration(a.spec.Budget.MaxWallClockSeconds) * time.Second
		actx, cancel := context.WithTimeout(ctx, deadline)
		a.runTarget(actx)
		a.bindSolution(actx)
		a.verify(actx)
		if errors.Is(actx.Err(), context.DeadlineExceeded) {
			a.out.overrun = true
		}
		cancel()
		a.checkPostHocBudget()
	}
	a.workEnded = time.Now()
	a.cleanup() //nolint:contextcheck // cleanup deliberately uses a fresh bounded context, never the (possibly expired) attempt context (design_engine.md)
	return a.assemble()
}

// describe obtains the target descriptor pre-run so error-path records
// still say exactly what they measured.
func (a *attempt) describe(ctx context.Context) {
	dctx, cancel := context.WithTimeout(ctx, DefaultDescribeTimeout)
	defer cancel()
	desc, err := a.adapter.Describe(dctx, a.spec)
	if err != nil {
		a.out.describeErr = err
		a.desc = placeholderDescriptor(a.adapter, a.spec)
		return
	}
	if err := desc.Validate(); err != nil {
		a.out.describeErr = fmt.Errorf("descriptor invalid: %w", err)
		a.desc = placeholderDescriptor(a.adapter, a.spec)
		return
	}
	a.desc = desc
}

// isolate creates the fresh workspace: clone, pin checkout, pin check.
func (a *attempt) isolate(ctx context.Context) {
	fixture := a.story.Definition.Fixture
	if err := os.MkdirAll(a.engine.Workdir, 0o755); err != nil {
		a.out.isolationErr = fmt.Errorf("workdir: %w", err)
		return
	}
	if err := gitx.Clone(ctx, fixture.Repo, a.spec.WorkspaceDir); err != nil {
		a.out.isolationErr = fmt.Errorf("clone fixture: %w", err)
		return
	}
	if err := gitx.Checkout(ctx, a.spec.WorkspaceDir, fixture.Commit); err != nil {
		a.out.isolationErr = fmt.Errorf("checkout pin: %w", err)
		return
	}
	head, err := gitx.Head(ctx, a.spec.WorkspaceDir)
	if err != nil {
		a.out.isolationErr = fmt.Errorf("verify pin: %w", err)
		return
	}
	if head != fixture.Commit {
		a.out.isolationErr = fmt.Errorf("pin mismatch: HEAD %s, want %s", head, fixture.Commit)
	}
}

// runTarget drives the adapter under the attempt deadline and
// streamed-usage caps.
func (a *attempt) runTarget(ctx context.Context) {
	rctx, cancelCause := context.WithCancelCause(ctx)
	defer cancelCause(nil)
	a.tracker = &usageTracker{
		cancel:    cancelCause,
		maxTokens: a.spec.Budget.MaxTokens,
		maxCost:   a.spec.Budget.MaxCostUSD,
	}
	a.spec.ReportUsage = a.tracker.report

	obs, err := a.adapter.Run(rctx, a.spec)
	if a.tracker.overrun() || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		a.out.overrun = true
		return
	}
	if err != nil {
		a.out.runErr = err
		return
	}
	if err := obs.Validate(a.adapter.Capabilities()); err != nil {
		a.out.obsInvalidErr = err
		return
	}
	a.obs = obs
	a.out.terminal = obs.TerminalStateReached
}

// bindSolution resolves the observation's solution to an immutable,
// ancestry-verified commit and checks it out for validation.
func (a *attempt) bindSolution(ctx context.Context) {
	if a.obs == nil {
		return
	}
	commit, err := a.resolveSolutionCommit(ctx)
	if err != nil {
		a.out.bindErr = err
		return
	}
	ok, err := gitx.IsAncestor(ctx, a.spec.WorkspaceDir, a.story.Definition.Fixture.Commit, commit)
	if err != nil {
		a.out.bindErr = fmt.Errorf("ancestry check: %w", err)
		return
	}
	if !ok {
		a.out.bindErr = fmt.Errorf("solution %s does not descend from the story pin", commit)
		return
	}
	if err := gitx.Checkout(ctx, a.spec.WorkspaceDir, commit); err != nil {
		a.out.bindErr = fmt.Errorf("checkout solution: %w", err)
		return
	}
	if a.obs.SolutionBranch != "" {
		// Branch solutions validate against exactly the resolved commit:
		// untracked or ignored files the target left in the workspace are
		// not part of the solution and must not influence validators.
		if err := gitx.CleanUntracked(ctx, a.spec.WorkspaceDir); err != nil {
			a.out.bindErr = fmt.Errorf("clean workspace for validation: %w", err)
			return
		}
	}
	a.solution = commit
	a.out.solutionOK = true
}

func (a *attempt) resolveSolutionCommit(ctx context.Context) (string, error) {
	branch := a.obs.SolutionBranch
	if branch == "" {
		commit, err := gitx.SnapshotCommit(ctx, a.spec.WorkspaceDir)
		if err != nil {
			return "", fmt.Errorf("snapshot in-place solution: %w", err)
		}
		return commit, nil
	}
	if !strings.HasPrefix(branch, a.spec.BranchNamespace+"/") {
		return "", fmt.Errorf("solution branch %q is outside the run namespace %q", branch, a.spec.BranchNamespace)
	}
	// Local workspace ref first (adapters that import the solution into the
	// workspace themselves), then origin.
	if commit, err := gitx.Run(ctx, a.spec.WorkspaceDir, "rev-parse", "--verify", "refs/heads/"+branch); err == nil {
		return commit, nil
	}
	commit, err := gitx.ResolveRemoteBranch(ctx, a.spec.WorkspaceDir, branch)
	if err != nil {
		return "", fmt.Errorf("resolve solution branch: %w", err)
	}
	return commit, nil
}

// verify runs the story's validators and checks at the bound solution,
// exports the validators' full outputs as engine-contributed test-output
// evidence (the authoritative test run produces the authoritative test
// evidence), and computes evidence coverage.
func (a *attempt) verify(ctx context.Context) {
	if !a.out.solutionOK {
		return
	}
	def := a.story.Definition
	var outputs []string
	a.out.validators, outputs = runValidators(ctx, a.spec.WorkspaceDir, def.Validators)
	a.engineEvidence = writeValidatorEvidence(a.spec.EvidenceDir, def.Validators, outputs, a.engine.logf)
	a.out.checks = runChecks(ctx, a.spec.WorkspaceDir, def, def.Fixture.Commit, a.solution)
	a.out.evidenceMissing = evidenceCoverage(def, append(append([]runrecord.EvidencePointer{}, a.obs.Evidence...), a.engineEvidence...))
	a.out.verified = true
}

// writeValidatorEvidence persists each validator's full output under the
// durable evidence dir and returns test-output evidence pointers.
func writeValidatorEvidence(evidenceDir string, validators []story.Validator, outputs []string, logf func(string, ...any)) []runrecord.EvidencePointer {
	pointers := make([]runrecord.EvidencePointer, 0, len(outputs))
	for i := range outputs {
		name := fmt.Sprintf("validator-%02d-%s.txt", i, sanitizeName(validators[i].Name))
		path := filepath.Join(evidenceDir, name)
		if err := os.WriteFile(path, []byte(outputs[i]+"\n"), 0o644); err != nil {
			logf("evidence write %s: %v", path, err)
			continue
		}
		pointers = append(pointers, runrecord.EvidencePointer{Kind: "test-output", Location: path})
	}
	return pointers
}

// sanitizeName keeps evidence filenames filename-safe.
func sanitizeName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// checkPostHocBudget compares observed usage against the caps for targets
// whose enforcement is post-hoc (and as a backstop for everyone else).
func (a *attempt) checkPostHocBudget() {
	if a.obs == nil {
		return
	}
	if cost, ok := a.obs.Metrics[runrecord.MetricCostUSD].Float64(); ok && cost > a.spec.Budget.MaxCostUSD {
		a.out.postHocOver = true
	}
	if tokens, ok := a.obs.Metrics[runrecord.MetricTokensTotal].Float64(); ok && int64(tokens) > a.spec.Budget.MaxTokens {
		a.out.postHocOver = true
	}
}

// cleanup runs on every exit path under a fresh bounded context — never
// the attempt context — and verifies nothing was left behind. It must
// complete before the record is assembled: the store is append-only.
func (a *attempt) cleanup() {
	cctx, cancel := context.WithTimeout(context.Background(), a.engine.cleanupTimeout())
	defer cancel()
	var problems []string
	if err := a.adapter.Cleanup(cctx, a.spec); err != nil {
		problems = append(problems, "adapter cleanup: "+err.Error())
	}
	if refs, err := gitx.LsRemoteHeads(cctx, ".", a.story.Definition.Fixture.Repo, "refs/heads/"+a.spec.BranchNamespace+"/*"); err != nil {
		problems = append(problems, "namespace verification: "+err.Error())
	} else if len(refs) > 0 {
		problems = append(problems, "refs left behind: "+strings.Join(refs, ", "))
	}
	if err := os.RemoveAll(a.spec.WorkspaceDir); err != nil {
		problems = append(problems, "workspace removal: "+err.Error())
	}
	if len(problems) > 0 {
		a.out.cleanupOK = false
		a.out.cleanupReason = strings.Join(problems, "; ")
		return
	}
	a.out.cleanupOK = true
}

// assemble composes the verdict and builds the one record this attempt
// appends.
func (a *attempt) assemble() *runrecord.RunRecord {
	verdict, kind, invalidReason := a.out.compose()
	rec := &runrecord.RunRecord{
		SchemaVersion:  runrecord.SchemaVersion,
		RunID:          a.spec.RunID,
		SuiteRunID:     a.spec.SuiteRunID,
		StoryID:        a.story.Definition.ID,
		StoryHash:      a.spec.StoryHash,
		ConfigName:     a.bundle.Bundle.Name,
		ConfigHash:     a.spec.BundleHash,
		StartedAt:      a.started,
		FinishedAt:     time.Now(),
		Verdict:        verdict,
		FailureKind:    kind,
		InvalidReason:  invalidReason,
		SolutionCommit: a.solution,
		Validators:     a.out.validators,
		Checks:         a.out.checks,
		Target:         a.desc,
		Metrics:        a.metrics(),
		Isolation: runrecord.Isolation{
			WorkspaceDir:    a.spec.WorkspaceDir,
			BranchNamespace: a.spec.BranchNamespace,
			CleanupVerified: a.out.cleanupOK,
		},
		TerminalStateReached: a.out.terminal,
	}
	if a.obs != nil {
		rec.Evidence = append(rec.Evidence, a.obs.Evidence...)
	}
	rec.Evidence = append(rec.Evidence, a.engineEvidence...)
	// Adapters export evidence files before returning errors; when the
	// observation was lost to the error path, the durable directory is
	// still worth a pointer — failed attempts need their story told.
	if a.obs == nil && dirHasEntries(a.spec.EvidenceDir) {
		rec.Evidence = append(rec.Evidence, runrecord.EvidencePointer{Kind: "evidence-dir", Location: a.spec.EvidenceDir})
	}
	return rec
}

func dirHasEntries(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// metrics returns the record's complete metrics map: the observation's (or
// a synthesized one), overlaid with the engine-owned canonical wall-clock
// and — on aborted attempts — the retained streamed usage totals, because
// failed-attempt costs count (ADR 0025).
func (a *attempt) metrics() runrecord.Metrics {
	caps := a.adapter.Capabilities()
	base := synthesizeMetrics(caps, a.failureReason())
	if a.obs != nil {
		base = make(runrecord.Metrics, len(a.obs.Metrics))
		maps.Copy(base, a.obs.Metrics)
	}
	// The canonical wall-clock is engine-measured over exactly the
	// budgeted interval: adapters cannot measure different intervals, and
	// the metric can never exceed the cap by including engine overhead.
	base[runrecord.MetricWallClockSeconds] = runrecord.Measured(a.workEnded.Sub(a.workStarted).Seconds())
	a.overlayStreamedUsage(base, caps)
	return base
}

// overlayStreamedUsage reconciles the tracker totals into the record for
// streamed targets: the values that enforced the cap must not disagree
// with the values suite settlement reads. The larger of streamed and
// observed wins — a final observation may be more complete than the
// stream, and a stream is more trustworthy than a smaller claim.
func (a *attempt) overlayStreamedUsage(base runrecord.Metrics, caps target.Capabilities) {
	if a.tracker == nil {
		return
	}
	tokens, cost := a.tracker.totals()
	if tokens > 0 && caps.Supports(runrecord.MetricTokensTotal) {
		if observed, ok := base[runrecord.MetricTokensTotal].Float64(); !ok || float64(tokens) > observed {
			base[runrecord.MetricTokensTotal] = runrecord.Measured(float64(tokens))
		}
	}
	if cost > 0 && caps.Supports(runrecord.MetricCostUSD) {
		if observed, ok := base[runrecord.MetricCostUSD].Float64(); !ok || cost > observed {
			base[runrecord.MetricCostUSD] = runrecord.Measured(cost)
		}
	}
}

func (a *attempt) failureReason() string {
	switch {
	case a.out.overrun:
		return "attempt aborted on budget overrun"
	case a.out.describeErr != nil:
		return "target describe failed: " + a.out.describeErr.Error()
	case a.out.isolationErr != nil:
		return "isolation failed: " + a.out.isolationErr.Error()
	case a.out.runErr != nil:
		return "target error: " + a.out.runErr.Error()
	case a.out.obsInvalidErr != nil:
		return "observation invalid: " + a.out.obsInvalidErr.Error()
	default:
		return "metrics not collected"
	}
}

// placeholderDescriptor is the honest fallback when Describe fails: the
// adapter identity is known, everything else is explicitly zeroed (40/64
// zero-hex placeholders keep the record contract-valid while being
// unmistakably not real content).
func placeholderDescriptor(adapter target.Adapter, spec *target.AttemptSpec) runrecord.TargetDescriptor {
	identity := adapter.Identity()
	model, pack, promptHash := "undescribed", "undescribed", ""
	if spec != nil && spec.Bundle != nil {
		model = spec.Bundle.Model.Default
		pack = spec.Bundle.Prompt.Pack
		promptHash = spec.Bundle.Prompt.Hash
	}
	if promptHash == "" {
		promptHash = zeroIdentity()
	}
	return runrecord.TargetDescriptor{
		AdapterName:       identity.Name,
		AdapterVersion:    identity.Version,
		CommitHash:        strings.Repeat("0", 40),
		BinaryIdentity:    "undescribed",
		BudgetEnforcement: runrecord.EnforcementPostHoc,
		MPH: runrecord.MPHIdentity{
			Model:       model,
			PromptPack:  pack,
			PromptHash:  promptHash,
			HarnessHash: zeroIdentity(),
		},
		Capabilities: adapter.Capabilities().Metrics,
	}
}

func zeroIdentity() string {
	return "sha256:" + strings.Repeat("0", 64)
}

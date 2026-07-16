// Package stubtarget is a scripted adapter that performs real git
// operations in the engine-provided workspace — the hermetic integration
// seam for engine tests (local file:// fixtures, no network, no tokens).
package stubtarget

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/internal/contenthash"
	"github.com/SnapdragonPartners/maestro/benchmark/internal/gitx"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/target"
)

// Stub drives scripted git behavior for engine tests. Zero value produces
// an accepted-shape run: it writes Files, commits them on a namespace
// branch, pushes, and cleans the branch up.
type Stub struct {
	// Files are written relative to the workspace before committing.
	Files map[string]string
	// JunkFiles are written AFTER the solution is committed and pushed —
	// untracked debris the engine must clean before validating.
	JunkFiles map[string]string
	// Usage is streamed through spec.ReportUsage before doing work.
	Usage []target.UsageDelta
	// Evidence kinds to report (defaults to "diff" and "test-output").
	EvidenceKinds []string
	// SleepFor delays inside Run, respecting context cancellation.
	SleepFor time.Duration
	// InPlace leaves changes uncommitted in the workspace and reports an
	// empty SolutionBranch (exercises the engine's snapshot path).
	InPlace bool
	// OrphanSolution commits the solution on an orphan branch that does
	// not descend from the pin (exercises ancestry rejection).
	OrphanSolution bool
	// LeaveRef skips deleting the pushed branch (exercises cleanup
	// verification flagging the run invalid).
	LeaveRef bool
	// TerminalNotReached reports terminal_state_reached=false.
	TerminalNotReached bool
}

// Identity implements target.Adapter.
func (s *Stub) Identity() target.Identity {
	return target.Identity{Name: "stub", Version: "0.0.1"}
}

// Capabilities implements target.Adapter: the stub reports tokens, cost,
// and wall-clock only; everything else is unsupported.
func (s *Stub) Capabilities() target.Capabilities {
	return target.Capabilities{Metrics: []runrecord.MetricKey{
		runrecord.MetricTokensTotal,
		runrecord.MetricCostUSD,
		runrecord.MetricWallClockSeconds,
	}}
}

// Describe implements target.Adapter.
func (s *Stub) Describe(_ context.Context, spec *target.AttemptSpec) (runrecord.TargetDescriptor, error) {
	if err := spec.Validate(); err != nil {
		return runrecord.TargetDescriptor{}, fmt.Errorf("stub describe: %w", err)
	}
	promptHash := spec.Bundle.Prompt.Hash
	if promptHash == "" {
		var err error
		promptHash, err = contenthash.CanonicalJSON("stub-prompt-v1")
		if err != nil {
			return runrecord.TargetDescriptor{}, fmt.Errorf("stub prompt hash: %w", err)
		}
	}
	harnessHash, err := contenthash.CanonicalJSON(spec.Bundle.Harness)
	if err != nil {
		return runrecord.TargetDescriptor{}, fmt.Errorf("stub harness hash: %w", err)
	}
	return runrecord.TargetDescriptor{
		AdapterName:       "stub",
		AdapterVersion:    "0.0.1",
		CommitHash:        spec.Story.Fixture.Commit,
		BinaryIdentity:    "stubtarget (in-process)",
		BudgetEnforcement: runrecord.EnforcementStreamed,
		MPH: runrecord.MPHIdentity{
			Model:       spec.Bundle.Model.Default,
			PromptPack:  spec.Bundle.Prompt.Pack,
			PromptHash:  promptHash,
			HarnessHash: harnessHash,
		},
		Capabilities: s.Capabilities().Metrics,
	}, nil
}

// Run implements target.Adapter with the scripted behavior.
func (s *Stub) Run(ctx context.Context, spec *target.AttemptSpec) (*target.Observation, error) {
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("stub run: %w", err)
	}
	s.streamUsage(spec)
	if s.SleepFor > 0 {
		select {
		case <-time.After(s.SleepFor):
		case <-ctx.Done():
			return nil, fmt.Errorf("stub run: %w", ctx.Err())
		}
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("stub run: %w", ctx.Err())
	}
	branch, err := s.produceSolution(ctx, spec)
	if err != nil {
		return nil, err
	}
	for name, content := range s.JunkFiles {
		if err := os.WriteFile(filepath.Join(spec.WorkspaceDir, name), []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("stub junk write: %w", err)
		}
	}
	return s.observation(spec, branch), nil
}

func (s *Stub) streamUsage(spec *target.AttemptSpec) {
	if spec.ReportUsage == nil {
		return
	}
	for _, delta := range s.Usage {
		spec.ReportUsage(delta)
	}
}

// produceSolution writes the scripted files and commits them per the
// scripted mode, returning the solution branch name ("" for in-place).
func (s *Stub) produceSolution(ctx context.Context, spec *target.AttemptSpec) (string, error) {
	for name, content := range s.Files {
		path := filepath.Join(spec.WorkspaceDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", fmt.Errorf("stub mkdir: %w", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("stub write: %w", err)
		}
	}
	if s.InPlace {
		return "", nil
	}
	branch := spec.BranchNamespace + "/solution"
	if s.OrphanSolution {
		if _, err := gitx.Run(ctx, spec.WorkspaceDir, "checkout", "--quiet", "--orphan", branch); err != nil {
			return "", fmt.Errorf("stub orphan branch: %w", err)
		}
		if _, err := gitx.Run(ctx, spec.WorkspaceDir, "add", "-A"); err != nil {
			return "", fmt.Errorf("stub orphan add: %w", err)
		}
		if _, err := gitx.Run(ctx, spec.WorkspaceDir,
			"-c", "user.name=stub", "-c", "user.email=stub@invalid",
			"commit", "--quiet", "--allow-empty", "-m", "stub orphan solution"); err != nil {
			return "", fmt.Errorf("stub orphan commit: %w", err)
		}
	} else if _, err := gitx.CommitAllOnBranch(ctx, spec.WorkspaceDir, branch, "stub solution"); err != nil {
		return "", fmt.Errorf("stub commit: %w", err)
	}
	if err := gitx.Push(ctx, spec.WorkspaceDir, branch); err != nil {
		return "", fmt.Errorf("stub push: %w", err)
	}
	return branch, nil
}

func (s *Stub) observation(spec *target.AttemptSpec, branch string) *target.Observation {
	metrics := make(runrecord.Metrics, len(runrecord.Registry()))
	for _, ms := range runrecord.Registry() {
		metrics[ms.Key] = runrecord.Unsupported()
	}
	metrics[runrecord.MetricTokensTotal] = runrecord.Measured(1000)
	metrics[runrecord.MetricCostUSD] = runrecord.Measured(0.01)
	metrics[runrecord.MetricWallClockSeconds] = runrecord.Measured(1)
	kinds := s.EvidenceKinds
	if kinds == nil {
		kinds = []string{"diff", "test-output"}
	}
	evidence := make([]runrecord.EvidencePointer, 0, len(kinds))
	for _, kind := range kinds {
		evidence = append(evidence, runrecord.EvidencePointer{Kind: kind, Location: spec.WorkspaceDir + "/stub-" + kind})
	}
	return &target.Observation{
		Metrics:              metrics,
		Evidence:             evidence,
		SolutionBranch:       branch,
		TerminalStateReached: !s.TerminalNotReached,
	}
}

// Cleanup implements target.Adapter, deleting the pushed solution branch
// unless scripted to leave it behind.
func (s *Stub) Cleanup(ctx context.Context, spec *target.AttemptSpec) error {
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("stub cleanup: %w", err)
	}
	if s.InPlace || s.LeaveRef {
		return nil
	}
	if _, err := os.Stat(spec.WorkspaceDir); os.IsNotExist(err) {
		return nil
	}
	if err := gitx.DeleteRemoteBranch(ctx, spec.WorkspaceDir, spec.BranchNamespace+"/solution"); err != nil {
		return fmt.Errorf("stub cleanup: %w", err)
	}
	return nil
}

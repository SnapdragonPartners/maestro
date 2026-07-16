// Package target defines the per-target adapter contract (ADR 0025): the
// interface every benchmark target implements, the attempt specification
// the engine hands it, and the normalized observation it returns.
//
// Division of labor: the engine owns isolation, budget enforcement,
// validator execution, deterministic checks, verdict composition, and
// record assembly. The adapter owns target invocation, observation, and
// normalization. Adapters never write records and never report validator
// outcomes — the acceptance boundary never rests on target self-reporting.
package target

import (
	"context"
	"fmt"
	"slices"

	"github.com/SnapdragonPartners/maestro/benchmark/mph"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/story"
)

// Identity identifies an adapter implementation and version; both appear in
// every run record so adapter drift is separable from target performance.
type Identity struct {
	Name    string
	Version string
}

// Capabilities declares which registry metrics a target can report. Every
// registry key outside the set is expected to be reported unsupported.
type Capabilities struct {
	Metrics []runrecord.MetricKey
}

// Supports reports whether key is a declared capability.
func (c Capabilities) Supports(key runrecord.MetricKey) bool {
	return slices.Contains(c.Metrics, key)
}

// AttemptSpec fully describes one isolated attempt. The engine constructs
// it and passes it by pointer; adapters treat it as read-only.
type AttemptSpec struct {
	Story *story.Definition
	// Bundle is the MPH configuration under test. Harness settings inside
	// it are adapter-interpreted; the engine never reads them.
	Bundle     *mph.Bundle
	RunID      string
	SuiteRunID string
	StoryHash  string
	BundleHash string
	// WorkspaceDir is the engine-provided fresh, run-scoped checkout.
	WorkspaceDir string
	// BranchNamespace is the run-scoped branch prefix; every branch the
	// target creates must live under it (repeat isolation).
	BranchNamespace string
	// Budget is the effective budget for this attempt (story caps; the
	// engine enforces it and aborts with failure kind budget-overrun).
	Budget story.Budget
}

// Validate checks the spec is complete before an adapter runs it.
func (s *AttemptSpec) Validate() error {
	switch {
	case s == nil:
		return fmt.Errorf("attempt spec is required")
	case s.Story == nil:
		return fmt.Errorf("story is required")
	case s.Bundle == nil:
		return fmt.Errorf("bundle is required")
	case s.RunID == "":
		return fmt.Errorf("run_id is required")
	case s.SuiteRunID == "":
		return fmt.Errorf("suite_run_id is required")
	case s.StoryHash == "":
		return fmt.Errorf("story_hash is required")
	case s.BundleHash == "":
		return fmt.Errorf("bundle_hash is required")
	case s.WorkspaceDir == "":
		return fmt.Errorf("workspace_dir is required")
	case s.BranchNamespace == "":
		return fmt.Errorf("branch_namespace is required")
	}
	return nil
}

// Observation is everything an adapter saw during one attempt, normalized:
// the target descriptor, a complete metrics map, raw evidence pointers, and
// the target-specific observable facts the verdict needs.
type Observation struct {
	Metrics  runrecord.Metrics
	Evidence []runrecord.EvidencePointer
	Target   runrecord.TargetDescriptor
	// TerminalStateReached reports whether the expected branch/PR terminal
	// state was reached — an observable fact, not a self-graded verdict.
	TerminalStateReached bool
}

// Validate checks the observation satisfies the normalized contract,
// including metric completeness and capability coherence.
func (o *Observation) Validate() error {
	if o == nil {
		return fmt.Errorf("nil observation")
	}
	if err := o.Metrics.Validate(); err != nil {
		return fmt.Errorf("observation metrics: %w", err)
	}
	if err := o.Target.Validate(); err != nil {
		return fmt.Errorf("observation target descriptor: %w", err)
	}
	if err := runrecord.CapabilityCoherence(o.Target.Capabilities, o.Metrics); err != nil {
		return fmt.Errorf("observation: %w", err)
	}
	for i := range o.Evidence {
		if o.Evidence[i].Kind == "" || o.Evidence[i].Location == "" {
			return fmt.Errorf("observation evidence pointers require kind and location")
		}
	}
	return nil
}

// Adapter drives one benchmark target through attempts, black-box.
type Adapter interface {
	// Identity returns the adapter's name and version.
	Identity() Identity
	// Capabilities declares which registry metrics this target reports.
	Capabilities() Capabilities
	// Run drives the target end-to-end for one attempt and returns the
	// normalized observation. Run must respect ctx cancellation — the
	// engine cancels on budget overrun. The spec is read-only.
	Run(ctx context.Context, spec *AttemptSpec) (*Observation, error)
	// Cleanup removes target-side state for the attempt. The engine records
	// failures loudly; an attempt whose cleanup cannot be verified is
	// flagged invalid, never silently included.
	Cleanup(ctx context.Context, spec *AttemptSpec) error
}

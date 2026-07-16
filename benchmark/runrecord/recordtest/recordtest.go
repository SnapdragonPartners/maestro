// Package recordtest builds contract-valid run records for tests across
// this module.
package recordtest

import (
	"strings"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
)

// hexDigest returns a deterministic 64-hex digest for test identities.
func hexDigest() string {
	return strings.Repeat("ab", 32)
}

// CompleteMetrics returns a metrics map with every registry key measured.
func CompleteMetrics() runrecord.Metrics {
	metrics := make(runrecord.Metrics, len(runrecord.Registry()))
	for i, spec := range runrecord.Registry() {
		metrics[spec.Key] = runrecord.Measured(float64(i))
	}
	return metrics
}

// AllCapabilities returns every registry key, matching CompleteMetrics'
// all-measured map (capability coherence: value requires the capability).
func AllCapabilities() []runrecord.MetricKey {
	specs := runrecord.Registry()
	keys := make([]runrecord.MetricKey, 0, len(specs))
	for _, spec := range specs {
		keys = append(keys, spec.Key)
	}
	return keys
}

// Accepted returns a contract-valid accepted record. Tests mutate copies to
// probe validation failures.
func Accepted() *runrecord.RunRecord {
	started := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	return &runrecord.RunRecord{
		SchemaVersion: runrecord.SchemaVersion,
		RunID:         "run-0001",
		SuiteRunID:    "suite-0001",
		StoryID:       "dep-bump-001",
		StoryHash:     "sha256:" + hexDigest(),
		ConfigName:    "paired-default",
		ConfigHash:    "sha256:" + hexDigest(),
		StartedAt:     started,
		FinishedAt:    started.Add(10 * time.Minute),
		Verdict:       runrecord.VerdictAccepted,
		Validators: []runrecord.CheckResult{
			{Name: "build", Passed: true},
			{Name: "test", Passed: true},
		},
		Checks: []runrecord.CheckResult{
			{Name: "diff-confined", Passed: true},
		},
		Evidence: []runrecord.EvidencePointer{
			{Kind: "pr", Location: "https://example.invalid/pr/1"},
		},
		Metrics: CompleteMetrics(),
		Target: runrecord.TargetDescriptor{
			AdapterName:    "fake",
			AdapterVersion: "0.0.0",
			CommitHash:     hexDigest()[:40],
			BinaryIdentity: "fake-binary",
			MPH: runrecord.MPHIdentity{
				Model:          "provider:model-x",
				PromptPack:     "v1-embedded",
				PromptHash:     "sha256:" + hexDigest(),
				HarnessHash:    "sha256:" + hexDigest(),
				MaestroVersion: "v1-as-patched",
			},
			Capabilities: AllCapabilities(),
		},
		Isolation: runrecord.Isolation{
			WorkspaceDir:    "/tmp/run-0001",
			BranchNamespace: "golden/run-0001",
			CleanupVerified: true,
		},
		TerminalStateReached: true,
	}
}

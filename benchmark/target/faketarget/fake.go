// Package faketarget provides a scripted, in-memory Adapter for unit tests
// across this module and the item 3 engine — no target, no tokens.
package faketarget

import (
	"context"
	"fmt"
	"sync"

	"github.com/SnapdragonPartners/maestro/benchmark/internal/contenthash"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/target"
)

// Fake is a scripted target adapter. Zero value is not usable; construct
// with New.
type Fake struct {
	// RunFunc produces the observation for each Run call. Defaults to a
	// fully populated accepted-shape observation via Observe.
	RunFunc func(ctx context.Context, spec *target.AttemptSpec) (*target.Observation, error)
	// CleanupErr is returned by every Cleanup call.
	CleanupErr error

	runCalls     []target.AttemptSpec
	cleanupCalls []target.AttemptSpec
	identity     target.Identity
	capabilities target.Capabilities
	mu           sync.Mutex
}

// New returns a Fake reporting every registry metric as a capability.
func New() *Fake {
	specs := runrecord.Registry()
	keys := make([]runrecord.MetricKey, 0, len(specs))
	for _, spec := range specs {
		keys = append(keys, spec.Key)
	}
	return &Fake{
		identity:     target.Identity{Name: "fake", Version: "0.0.0"},
		capabilities: target.Capabilities{Metrics: keys},
	}
}

// Identity implements target.Adapter.
func (f *Fake) Identity() target.Identity { return f.identity }

// Capabilities implements target.Adapter.
func (f *Fake) Capabilities() target.Capabilities { return f.capabilities }

// Run implements target.Adapter, recording the call and delegating to
// RunFunc (or Observe when unset). Like a real adapter, it rejects an
// incomplete spec with an error instead of panicking.
func (f *Fake) Run(ctx context.Context, spec *target.AttemptSpec) (*target.Observation, error) {
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("faketarget run: %w", err)
	}
	f.mu.Lock()
	f.runCalls = append(f.runCalls, *spec)
	f.mu.Unlock()
	if f.RunFunc != nil {
		return f.RunFunc(ctx, spec)
	}
	obs := Observe(spec)
	return &obs, nil
}

// Cleanup implements target.Adapter, recording the call. It rejects an
// incomplete spec with an error instead of panicking.
func (f *Fake) Cleanup(_ context.Context, spec *target.AttemptSpec) error {
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("faketarget cleanup: %w", err)
	}
	f.mu.Lock()
	f.cleanupCalls = append(f.cleanupCalls, *spec)
	f.mu.Unlock()
	return f.CleanupErr
}

// RunCalls returns a copy of the specs Run has been called with.
func (f *Fake) RunCalls() []target.AttemptSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]target.AttemptSpec(nil), f.runCalls...)
}

// CleanupCalls returns a copy of the specs Cleanup has been called with.
func (f *Fake) CleanupCalls() []target.AttemptSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]target.AttemptSpec(nil), f.cleanupCalls...)
}

// Observe builds a complete, contract-valid observation for spec: every
// registry metric measured, one evidence pointer, terminal state reached.
// A nil spec yields a zero observation, which fails validation normally.
func Observe(spec *target.AttemptSpec) target.Observation {
	if spec == nil {
		return target.Observation{}
	}
	metrics := make(runrecord.Metrics, len(runrecord.Registry()))
	for i, ms := range runrecord.Registry() {
		metrics[ms.Key] = runrecord.Measured(float64(i))
	}
	promptHash := ""
	bundleModel := "fake-model"
	pack := "fake-pack"
	if spec.Bundle != nil {
		promptHash = spec.Bundle.Prompt.Hash
		bundleModel = spec.Bundle.Model.Default
		pack = spec.Bundle.Prompt.Pack
	}
	if promptHash == "" {
		promptHash = contenthash.Prefix + fakeDigest("prompt")
	}
	return target.Observation{
		Metrics: metrics,
		Evidence: []runrecord.EvidencePointer{
			{Kind: "log", Location: spec.WorkspaceDir + "/fake.log"},
		},
		Target: runrecord.TargetDescriptor{
			AdapterName:    "fake",
			AdapterVersion: "0.0.0",
			CommitHash:     fakeDigest("commit")[:40],
			BinaryIdentity: "faketarget",
			MPH: runrecord.MPHIdentity{
				Model:       bundleModel,
				PromptPack:  pack,
				PromptHash:  promptHash,
				HarnessHash: contenthash.Prefix + fakeDigest("harness"),
			},
			Capabilities: capabilitiesOf(),
		},
		TerminalStateReached: true,
	}
}

func capabilitiesOf() []runrecord.MetricKey {
	specs := runrecord.Registry()
	keys := make([]runrecord.MetricKey, 0, len(specs))
	for _, spec := range specs {
		keys = append(keys, spec.Key)
	}
	return keys
}

// fakeDigest returns a deterministic 64-hex string seeded by s.
func fakeDigest(s string) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 64)
	for i := range out {
		out[i] = hexDigits[(i+len(s))%len(hexDigits)]
	}
	return string(out)
}

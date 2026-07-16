package target_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/mph"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/story"
	"github.com/SnapdragonPartners/maestro/benchmark/target"
	"github.com/SnapdragonPartners/maestro/benchmark/target/faketarget"
)

// The fake must satisfy the adapter contract at compile time.
var _ target.Adapter = (*faketarget.Fake)(nil)

func spec(t *testing.T) *target.AttemptSpec {
	t.Helper()
	loadedStory, err := story.LoadFile("../story/testdata/valid.toml")
	if err != nil {
		t.Fatalf("load story fixture: %v", err)
	}
	loadedBundle, err := mph.LoadFile("../mph/testdata/paired.toml")
	if err != nil {
		t.Fatalf("load bundle fixture: %v", err)
	}
	return &target.AttemptSpec{
		Story:           loadedStory.Definition,
		Bundle:          loadedBundle.Bundle,
		Budget:          loadedStory.Definition.Budget,
		RunID:           "run-0001",
		SuiteRunID:      "suite-0001",
		StoryHash:       loadedStory.Hash,
		BundleHash:      loadedBundle.Hash,
		WorkspaceDir:    t.TempDir(),
		BranchNamespace: "golden/run-0001",
	}
}

func TestAttemptSpecValidate(t *testing.T) {
	good := spec(t)
	if err := good.Validate(); err != nil {
		t.Fatalf("complete spec must validate: %v", err)
	}
	missing := *good
	missing.BranchNamespace = ""
	if err := missing.Validate(); err == nil || !strings.Contains(err.Error(), "branch_namespace") {
		t.Fatalf("incomplete spec must fail, got %v", err)
	}
	var absent *target.AttemptSpec
	if err := absent.Validate(); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("nil spec must fail, got %v", err)
	}
}

func TestFakeRunProducesValidObservation(t *testing.T) {
	fake := faketarget.New()
	obs, err := fake.Run(context.Background(), spec(t))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := obs.Validate(); err != nil {
		t.Fatalf("default observation must satisfy the contract: %v", err)
	}
	if calls := fake.RunCalls(); len(calls) != 1 || calls[0].RunID != "run-0001" {
		t.Fatalf("run call not recorded: %+v", calls)
	}
}

func TestFakeScriptingAndCleanup(t *testing.T) {
	fake := faketarget.New()
	wantErr := errors.New("target exploded")
	fake.RunFunc = func(_ context.Context, _ *target.AttemptSpec) (*target.Observation, error) {
		return nil, wantErr
	}
	if _, err := fake.Run(context.Background(), spec(t)); !errors.Is(err, wantErr) {
		t.Fatalf("scripted error not returned: %v", err)
	}
	fake.CleanupErr = wantErr
	if err := fake.Cleanup(context.Background(), spec(t)); !errors.Is(err, wantErr) {
		t.Fatalf("cleanup error not returned: %v", err)
	}
	if calls := fake.CleanupCalls(); len(calls) != 1 {
		t.Fatalf("cleanup call not recorded: %+v", calls)
	}
}

func TestObservationValidateEnforcesMetricCompleteness(t *testing.T) {
	obs := faketarget.Observe(spec(t))
	delete(obs.Metrics, runrecord.MetricCostUSD)
	if err := obs.Validate(); err == nil || !strings.Contains(err.Error(), string(runrecord.MetricCostUSD)) {
		t.Fatalf("incomplete observation must fail, got %v", err)
	}
}

func TestNilObservationValidate(t *testing.T) {
	var obs *target.Observation
	if err := obs.Validate(); err == nil {
		t.Fatalf("nil observation must fail validation, not panic")
	}
}

func TestObservationValidateRejectsUnknownCapabilities(t *testing.T) {
	obs := faketarget.Observe(spec(t))
	obs.Target.Capabilities = append(obs.Target.Capabilities, "made_up")
	if err := obs.Validate(); err == nil || !strings.Contains(err.Error(), "made_up") {
		t.Fatalf("unknown capability must fail at the adapter boundary, got %v", err)
	}
}

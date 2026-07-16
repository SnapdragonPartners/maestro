package runrecord_test

import (
	"strings"
	"testing"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord/recordtest"
)

func TestAcceptedRecordValidates(t *testing.T) {
	if err := recordtest.Accepted().Validate(); err != nil {
		t.Fatalf("baseline record must validate: %v", err)
	}
}

func TestRecordValidatePairings(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*runrecord.RunRecord)
		wantErr string
	}{
		{"wrong schema version", func(r *runrecord.RunRecord) { r.SchemaVersion = 99 }, "schema version"},
		{"missing run id", func(r *runrecord.RunRecord) { r.RunID = "" }, "run_id"},
		{"unprefixed story hash", func(r *runrecord.RunRecord) { r.StoryHash = "abc" }, "story_hash"},
		{"unprefixed config hash", func(r *runrecord.RunRecord) { r.ConfigHash = "abc" }, "config_hash"},
		{"accepted with failure kind", func(r *runrecord.RunRecord) { r.FailureKind = runrecord.FailureChecksFailed }, "accepted"},
		{"accepted without terminal state", func(r *runrecord.RunRecord) { r.TerminalStateReached = false }, "terminal_state_reached"},
		{"failed without kind", func(r *runrecord.RunRecord) {
			r.Verdict = runrecord.VerdictFailed
		}, "failure kind"},
		{"failed with unknown kind", func(r *runrecord.RunRecord) {
			r.Verdict = runrecord.VerdictFailed
			r.FailureKind = "sideways"
		}, "failure kind"},
		{"invalid without reason", func(r *runrecord.RunRecord) {
			r.Verdict = runrecord.VerdictInvalid
		}, "invalid reason"},
		{"unknown verdict", func(r *runrecord.RunRecord) { r.Verdict = "meh" }, "verdict"},
		{"zero timestamps", func(r *runrecord.RunRecord) { r.StartedAt = time.Time{} }, "started_at"},
		{"finish before start", func(r *runrecord.RunRecord) {
			r.FinishedAt = r.StartedAt.Add(-1)
		}, "precedes"},
		{"missing adapter identity", func(r *runrecord.RunRecord) { r.Target.AdapterName = "" }, "adapter_name"},
		{"missing mph model", func(r *runrecord.RunRecord) { r.Target.MPH.Model = "" }, "model"},
		{"unprefixed prompt hash", func(r *runrecord.RunRecord) { r.Target.MPH.PromptHash = "zzz" }, "prompt_hash"},
		{"nonregistry capability", func(r *runrecord.RunRecord) {
			r.Target.Capabilities = []runrecord.MetricKey{"made_up"}
		}, "capability"},
		{"unnamed validator", func(r *runrecord.RunRecord) {
			r.Validators = append(r.Validators, runrecord.CheckResult{Passed: true})
		}, "validator"},
		{"evidence without location", func(r *runrecord.RunRecord) {
			r.Evidence = append(r.Evidence, runrecord.EvidencePointer{Kind: "log"})
		}, "evidence"},
		{"missing isolation", func(r *runrecord.RunRecord) { r.Isolation.BranchNamespace = "" }, "isolation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := recordtest.Accepted()
			tc.mutate(rec)
			err := rec.Validate()
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestFailedAndInvalidRecordsValidate(t *testing.T) {
	failed := recordtest.Accepted()
	failed.Verdict = runrecord.VerdictFailed
	failed.FailureKind = runrecord.FailureBudgetOverrun
	failed.TerminalStateReached = false
	if err := failed.Validate(); err != nil {
		t.Fatalf("failed record must validate: %v", err)
	}

	invalid := recordtest.Accepted()
	invalid.Verdict = runrecord.VerdictInvalid
	invalid.InvalidReason = "cleanup unverifiable"
	invalid.Isolation.CleanupVerified = false
	if err := invalid.Validate(); err != nil {
		t.Fatalf("invalid record must validate: %v", err)
	}
}

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

// onlyTokensCapableMetrics is coherent with capabilities = [tokens_total]:
// tokens measured, everything else unsupported.
func onlyTokensCapableMetrics() runrecord.Metrics {
	metrics := make(runrecord.Metrics, len(runrecord.Registry()))
	for _, spec := range runrecord.Registry() {
		metrics[spec.Key] = runrecord.Unsupported()
	}
	metrics[runrecord.MetricTokensTotal] = runrecord.Measured(1)
	return metrics
}

func TestNotApplicableIsCoherentEitherWay(t *testing.T) {
	// With the capability declared.
	rec := recordtest.Accepted()
	rec.Metrics[runrecord.MetricHumanAttentionSeconds] = runrecord.NotApplicable()
	if err := rec.Validate(); err != nil {
		t.Fatalf("not_applicable with capability must validate: %v", err)
	}
	// Without the capability declared.
	rec = recordtest.Accepted()
	rec.Target.Capabilities = []runrecord.MetricKey{runrecord.MetricTokensTotal}
	rec.Metrics = onlyTokensCapableMetrics()
	rec.Metrics[runrecord.MetricHumanAttentionSeconds] = runrecord.NotApplicable()
	if err := rec.Validate(); err != nil {
		t.Fatalf("not_applicable without capability must validate: %v", err)
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
		{"malformed story hash digest", func(r *runrecord.RunRecord) { r.StoryHash = "sha256:x" }, "story_hash"},
		{"uppercase config hash digest", func(r *runrecord.RunRecord) {
			r.ConfigHash = "sha256:" + strings.ToUpper(strings.Repeat("ab", 32))
		}, "config_hash"},
		{"accepted with empty validators", func(r *runrecord.RunRecord) { r.Validators = nil }, "nonempty"},
		{"accepted with failed check", func(r *runrecord.RunRecord) {
			r.Checks = append(r.Checks, runrecord.CheckResult{Name: "sneaky", Passed: false})
		}, "failed check"},
		{"accepted with unverified cleanup", func(r *runrecord.RunRecord) {
			r.Isolation.CleanupVerified = false
		}, "unverified cleanup"},
		{"failed with unverified cleanup", func(r *runrecord.RunRecord) {
			r.Verdict = runrecord.VerdictFailed
			r.FailureKind = runrecord.FailureTargetError
			r.TerminalStateReached = false
			r.Isolation.CleanupVerified = false
		}, "unverified cleanup"},
		{"unsupported despite declared capability", func(r *runrecord.RunRecord) {
			r.Metrics[runrecord.MetricToolCalls] = runrecord.Unsupported()
		}, "unsupported"},
		{"value without declared capability", func(r *runrecord.RunRecord) {
			r.Target.Capabilities = []runrecord.MetricKey{runrecord.MetricTokensTotal}
			r.Metrics = onlyTokensCapableMetrics()
			r.Metrics[runrecord.MetricCostUSD] = runrecord.Measured(1)
		}, "without a declared capability"},
		{"unavailable without declared capability", func(r *runrecord.RunRecord) {
			r.Target.Capabilities = []runrecord.MetricKey{runrecord.MetricTokensTotal}
			r.Metrics = onlyTokensCapableMetrics()
			r.Metrics[runrecord.MetricCostUSD] = runrecord.Unavailable("crashed")
		}, "without a declared capability"},
		{"duplicate capability", func(r *runrecord.RunRecord) {
			r.Target.Capabilities = append(r.Target.Capabilities, runrecord.MetricTokensTotal)
		}, "twice"},
		{"short commit hash", func(r *runrecord.RunRecord) { r.Target.CommitHash = "abc123" }, "40-hex"},
		{"missing binary identity", func(r *runrecord.RunRecord) { r.Target.BinaryIdentity = "" }, "binary_identity"},
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

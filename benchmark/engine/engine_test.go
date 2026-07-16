package engine

import (
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/mph"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/story"
	"github.com/SnapdragonPartners/maestro/benchmark/target"
)

func passed(name string) runrecord.CheckResult {
	return runrecord.CheckResult{Name: name, Passed: true}
}

func failed(name string) runrecord.CheckResult {
	return runrecord.CheckResult{Name: name, Passed: false}
}

// acceptedOutcome is the baseline every table case mutates.
func acceptedOutcome() outcome {
	return outcome{
		validators: []runrecord.CheckResult{passed("build")},
		checks:     []runrecord.CheckResult{passed("diff")},
		solutionOK: true,
		terminal:   true,
		cleanupOK:  true,
		verified:   true,
	}
}

func TestVerdictComposition(t *testing.T) {
	cases := []struct {
		name        string
		mutate      func(*outcome)
		wantVerdict runrecord.Verdict
		wantKind    runrecord.FailureKind
	}{
		{"accepted", func(_ *outcome) {}, runrecord.VerdictAccepted, ""},
		{"isolation beats everything", func(o *outcome) {
			o.isolationErr = errTest
			o.overrun = true
		}, runrecord.VerdictInvalid, ""},
		{"cleanup beats failure kinds", func(o *outcome) {
			o.cleanupOK = false
			o.cleanupReason = "ref left behind"
			o.overrun = true
		}, runrecord.VerdictInvalid, ""},
		{"overrun outranks target error", func(o *outcome) {
			o.overrun = true
			o.runErr = errTest
		}, runrecord.VerdictFailed, runrecord.FailureBudgetOverrun},
		{"post-hoc overrun", func(o *outcome) { o.postHocOver = true }, runrecord.VerdictFailed, runrecord.FailureBudgetOverrun},
		{"target error outranks branch state", func(o *outcome) {
			o.runErr = errTest
			o.terminal = false
		}, runrecord.VerdictFailed, runrecord.FailureTargetError},
		{"describe error is target error", func(o *outcome) { o.describeErr = errTest }, runrecord.VerdictFailed, runrecord.FailureTargetError},
		{"invalid observation is target error", func(o *outcome) { o.obsInvalidErr = errTest }, runrecord.VerdictFailed, runrecord.FailureTargetError},
		{"bind failure is branch state", func(o *outcome) { o.bindErr = errTest }, runrecord.VerdictFailed, runrecord.FailureBranchState},
		{"terminal not reached is branch state", func(o *outcome) { o.terminal = false }, runrecord.VerdictFailed, runrecord.FailureBranchState},
		{"validator failure", func(o *outcome) {
			o.validators = append(o.validators, failed("test"))
		}, runrecord.VerdictFailed, runrecord.FailureValidatorFailed},
		{"check failure", func(o *outcome) {
			o.checks = append(o.checks, failed("diff-confined"))
		}, runrecord.VerdictFailed, runrecord.FailureChecksFailed},
		{"evidence missing", func(o *outcome) {
			o.evidenceMissing = []string{"evidence:diff"}
		}, runrecord.VerdictFailed, runrecord.FailureEvidenceMissing},
		{"unverified never accepted", func(o *outcome) { o.verified = false }, runrecord.VerdictFailed, runrecord.FailureTargetError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := acceptedOutcome()
			tc.mutate(&o)
			verdict, kind, reason := o.compose()
			if verdict != tc.wantVerdict || kind != tc.wantKind {
				t.Fatalf("got %s/%s (%s), want %s/%s", verdict, kind, reason, tc.wantVerdict, tc.wantKind)
			}
			if verdict == runrecord.VerdictInvalid && reason == "" {
				t.Fatalf("invalid verdicts require a reason")
			}
		})
	}
}

func TestSynthesizedMetricsAreCompleteAndCoherent(t *testing.T) {
	caps := target.Capabilities{Metrics: []runrecord.MetricKey{runrecord.MetricTokensTotal, runrecord.MetricCostUSD}}
	metrics := synthesizeMetrics(caps, "target crashed")
	if err := metrics.Validate(); err != nil {
		t.Fatalf("synthesized metrics must be complete: %v", err)
	}
	if err := runrecord.CapabilityCoherence(caps.Metrics, metrics); err != nil {
		t.Fatalf("synthesized metrics must be coherent: %v", err)
	}
	if metrics[runrecord.MetricTokensTotal].Status != runrecord.StatusUnavailable {
		t.Fatalf("capable keys must be unavailable, got %+v", metrics[runrecord.MetricTokensTotal])
	}
	if metrics[runrecord.MetricLLMCalls].Status != runrecord.StatusUnsupported {
		t.Fatalf("incapable keys must be unsupported, got %+v", metrics[runrecord.MetricLLMCalls])
	}
}

func TestEffectiveBudgetBoundsCost(t *testing.T) {
	sb := effectiveBudget(
		storyBudget(100, 60, 8.0),
		bundleBudget(3.0),
	)
	if sb.MaxCostUSD != 3.0 {
		t.Fatalf("bundle per-run cap must bound the story cap, got %v", sb.MaxCostUSD)
	}
	sb = effectiveBudget(storyBudget(100, 60, 2.0), bundleBudget(3.0))
	if sb.MaxCostUSD != 2.0 {
		t.Fatalf("tighter story cap must survive, got %v", sb.MaxCostUSD)
	}
}

func TestBundleAccountAdmission(t *testing.T) {
	account := &bundleAccount{capUSD: 10}
	admitted := 0
	for range 5 {
		if account.admit(3.0) {
			admitted++
		}
	}
	if admitted != 3 {
		t.Fatalf("cap 10 at expected 3.0 must admit exactly 3, got %d", admitted)
	}
	// Observed above expectation tops up; unavailable stays at expectation.
	account = &bundleAccount{capUSD: 10}
	if !account.admit(3.0) {
		t.Fatalf("first admission must pass")
	}
	account.topUp(3.0, 5.0, true)
	if account.chargedUSD != 5.0 {
		t.Fatalf("observed above expectation must top up, got %v", account.chargedUSD)
	}
	account.topUp(3.0, 0, false)
	if account.chargedUSD != 5.0 {
		t.Fatalf("unknown observed cost must not change the charge, got %v", account.chargedUSD)
	}
}

func TestPathAllowed(t *testing.T) {
	allowed := []string{"go.mod", "llms/providers/openai/"}
	cases := map[string]bool{
		"go.mod":                                true,
		"go.sum":                                false,
		"llms/providers/openai/chatconvert.go":  true,
		"llms/providers/google/convert.go":      false,
		"llms/providers/openai-extra/cheeky.go": false,
	}
	for path, want := range cases {
		if got := pathAllowed(path, allowed); got != want {
			t.Fatalf("pathAllowed(%q) = %v, want %v", path, got, want)
		}
	}
}

var errTest = errStub("boom") //nolint:gochecknoglobals // test sentinel

type errStub string

func (e errStub) Error() string { return string(e) }

func storyBudget(tokens, wallSeconds int64, cost float64) story.Budget {
	return story.Budget{MaxTokens: tokens, MaxWallClockSeconds: wallSeconds, MaxCostUSD: cost}
}

func bundleBudget(perRunCap float64) mph.DeclaredBudget {
	return mph.DeclaredBudget{
		ExpectedTokensPerRun:  1,
		ExpectedCostUSDPerRun: 0.01,
		MaxCostUSDPerRun:      perRunCap,
		MaxCostUSDPerSuite:    perRunCap * 10,
	}
}

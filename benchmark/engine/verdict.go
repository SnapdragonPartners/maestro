package engine

import (
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/target"
)

// outcome collects everything verdict composition needs, gathered across
// the attempt lifecycle.
type outcome struct {
	describeErr   error
	runErr        error
	obsInvalidErr error
	bindErr       error
	isolationErr  error

	cleanupReason string

	validators      []runrecord.CheckResult
	checks          []runrecord.CheckResult
	evidenceMissing []string

	solutionOK  bool
	terminal    bool
	overrun     bool
	postHocOver bool
	cleanupOK   bool
	verified    bool
}

// compose derives the verdict per the design's precedence: invalid above
// all; then exactly one failure kind in fixed order; else accepted.
func (o *outcome) compose() (runrecord.Verdict, runrecord.FailureKind, string) {
	if o.isolationErr != nil {
		reason := "isolation unverifiable: " + o.isolationErr.Error()
		if !o.cleanupOK {
			reason += "; cleanup: " + o.cleanupReason
		}
		return runrecord.VerdictInvalid, "", reason
	}
	if !o.cleanupOK {
		return runrecord.VerdictInvalid, "", "cleanup unverifiable: " + o.cleanupReason
	}
	if kind, failed := o.failureKind(); failed {
		return runrecord.VerdictFailed, kind, ""
	}
	return runrecord.VerdictAccepted, "", ""
}

// failureKind returns the single failure kind, first match in the fixed
// precedence order, or false when the attempt reached acceptance.
func (o *outcome) failureKind() (runrecord.FailureKind, bool) {
	switch {
	case o.overrun || o.postHocOver:
		return runrecord.FailureBudgetOverrun, true
	case o.describeErr != nil || o.runErr != nil || o.obsInvalidErr != nil:
		return runrecord.FailureTargetError, true
	case o.bindErr != nil || !o.solutionOK || !o.terminal:
		return runrecord.FailureBranchState, true
	case anyFailed(o.validators):
		return runrecord.FailureValidatorFailed, true
	case anyFailed(o.checks):
		return runrecord.FailureChecksFailed, true
	case len(o.evidenceMissing) > 0:
		return runrecord.FailureEvidenceMissing, true
	case !o.verified:
		// Verification never ran (early failure without a specific cause);
		// classify as target error rather than inventing acceptance.
		return runrecord.FailureTargetError, true
	}
	return "", false
}

func anyFailed(rs []runrecord.CheckResult) bool {
	for i := range rs {
		if !rs[i].Passed {
			return true
		}
	}
	return false
}

// synthesizeMetrics builds the complete metrics map for attempts whose
// observation is missing or invalid: unavailable (with reason) for every
// capability-declared key, unsupported for the rest. Missing is never
// missing (design_runner.md).
func synthesizeMetrics(caps target.Capabilities, reason string) runrecord.Metrics {
	metrics := make(runrecord.Metrics, len(runrecord.Registry()))
	for _, spec := range runrecord.Registry() {
		if caps.Supports(spec.Key) {
			metrics[spec.Key] = runrecord.Unavailable(reason)
		} else {
			metrics[spec.Key] = runrecord.Unsupported()
		}
	}
	return metrics
}

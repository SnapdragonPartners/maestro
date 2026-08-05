package benchmarkimport

import (
	"fmt"
	"math"
	"regexp"
)

// The shapes the record contract fixes. Mirrored from the runner rather than
// imported, for the reason the package comment gives.
//
//nolint:gochecknoglobals // Package-level compiled regexes for performance.
var (
	commitPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	contentIDPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	suiteRunIDPattern = regexp.MustCompile(`^[a-z0-9_-]+$`)
	// runIDPattern is stricter than the suite rule by one character class,
	// matching design D8 and migration 000017's own CHECK. run_id is used as
	// a path component, and while neither pattern admits `.` or a separator —
	// so `..` and `../x` cannot be spelled by either — D8 fixed this shape
	// and the schema enforces it.
	runIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

// The closed vocabularies.
//
//nolint:gochecknoglobals // Package-level sets, immutable after init.
var (
	knownVerdicts = map[string]bool{"accepted": true, "failed": true, "invalid": true}

	knownFailureKinds = map[string]bool{
		"budget-overrun": true, "checks-failed": true, "validator-failed": true,
		"evidence-missing": true, "branch-state": true, "target-error": true,
	}

	knownEnforcement = map[string]bool{
		"streamed": true, "self-enforced": true, "post-hoc": true,
	}

	knownMetricStatus = map[string]bool{
		"value": true, "unsupported": true, "not_applicable": true, "unavailable": true,
	}
)

// Validate enforces the per-record semantics.
//
// Nothing is inherited from the runner. An earlier draft of the design
// claimed results.ReadSuite had already run runrecord.Validate on every line,
// and that was wrong twice over: this package cannot call it, and even if it
// could, that validation ran when the runner WROTE the file. It is a fact
// about a past process, not about the bytes on disk now — truncation, a hand
// edit, a partial write or tampering all happen afterwards.
func (r *Record) Validate() error {
	for _, check := range []func() error{
		r.validateSchema, r.validateIdentity, r.validateVerdict,
		r.validateTimestamps, r.validateTarget, r.validateResults,
		r.validateMetrics, r.validateIsolation,
	} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func (r *Record) validateSchema() error {
	if r.SchemaVersion != SchemaVersion {
		return reject(r.RunID, ReasonSchemaVersion,
			fmt.Sprintf("record is version %d, this build reads %d", r.SchemaVersion, SchemaVersion))
	}
	return nil
}

func (r *Record) validateIdentity() error {
	switch {
	case !runIDPattern.MatchString(r.RunID):
		// Also the containment rule: run_id is joined into
		// <results>/evidence/<run_id>, and the engine joins it into a
		// workspace path. A value with a separator, or `.`/`..`, would
		// escape the directory it is supposed to name.
		return reject(r.RunID, ReasonRunID, fmt.Sprintf("%q", r.RunID))
	case !suiteRunIDPattern.MatchString(r.SuiteRunID):
		return reject(r.RunID, ReasonSuiteRunID, fmt.Sprintf("%q", r.SuiteRunID))
	case r.StoryID == "":
		return reject(r.RunID, ReasonStoryID, "")
	case r.ConfigName == "":
		return reject(r.RunID, ReasonConfigName, "")
	case !contentIDPattern.MatchString(r.StoryHash):
		return reject(r.RunID, ReasonStoryHash, fmt.Sprintf("%q", r.StoryHash))
	case !contentIDPattern.MatchString(r.ConfigHash):
		return reject(r.RunID, ReasonConfigHash, fmt.Sprintf("%q", r.ConfigHash))
	}
	return nil
}

func (r *Record) validateVerdict() error {
	if !knownVerdicts[r.Verdict] {
		return reject(r.RunID, ReasonVerdict, fmt.Sprintf("%q", r.Verdict))
	}
	switch r.Verdict {
	case "accepted":
		return r.validateAccepted()
	case "failed":
		if !knownFailureKinds[r.FailureKind] {
			return reject(r.RunID, ReasonFailureKind, fmt.Sprintf("%q", r.FailureKind))
		}
		if r.InvalidReason != "" {
			return reject(r.RunID, ReasonInvalidReason, "a failed record carries an invalid reason")
		}
	case "invalid":
		if r.InvalidReason == "" {
			return reject(r.RunID, ReasonInvalidReason, "an invalid record carries no reason")
		}
		if r.FailureKind != "" {
			return reject(r.RunID, ReasonFailureKind, "an invalid record carries a failure kind")
		}
	}
	return nil
}

// validateAccepted enforces what benchmark acceptance MEANS: every validator
// and check ran and passed, and the terminal state was reached. An accepted
// record with failed or absent results is a contradiction, and importing it
// would put that contradiction in the plane permanently.
func (r *Record) validateAccepted() error {
	switch {
	case r.FailureKind != "":
		return reject(r.RunID, ReasonFailureKind, "an accepted record carries a failure kind")
	case r.InvalidReason != "":
		return reject(r.RunID, ReasonInvalidReason, "an accepted record carries an invalid reason")
	case !r.TerminalStateReached:
		return reject(r.RunID, ReasonTerminalState, "")
	case !commitPattern.MatchString(r.SolutionCommit):
		return reject(r.RunID, ReasonSolutionCommit, fmt.Sprintf("%q", r.SolutionCommit))
	case len(r.Validators) == 0 || len(r.Checks) == 0:
		return reject(r.RunID, ReasonResultsMissing, "")
	}
	for _, group := range [][]CheckResult{r.Validators, r.Checks} {
		for i := range group {
			if !group[i].Passed {
				return reject(r.RunID, ReasonResultFailed, fmt.Sprintf("%q", group[i].Name))
			}
		}
	}
	return nil
}

func (r *Record) validateTimestamps() error {
	if r.StartedAt == nil || r.FinishedAt == nil {
		return reject(r.RunID, ReasonTimestamps, "")
	}
	if r.FinishedAt.Before(*r.StartedAt) {
		return reject(r.RunID, ReasonTimeOrder,
			fmt.Sprintf("%s precedes %s", r.FinishedAt, r.StartedAt))
	}
	return nil
}

func (r *Record) validateTarget() error {
	target := &r.Target
	switch {
	case target.AdapterName == "" || target.AdapterVersion == "":
		return reject(r.RunID, ReasonAdapterIdentity, "")
	case !commitPattern.MatchString(target.CommitHash):
		return reject(r.RunID, ReasonCommitHash, fmt.Sprintf("%q", target.CommitHash))
	case target.BinaryIdentity == "":
		return reject(r.RunID, ReasonBinaryIdentity, "")
	case target.MPH.Model == "" || target.MPH.PromptPack == "":
		return reject(r.RunID, ReasonMPHIdentity, "")
	case !contentIDPattern.MatchString(target.MPH.PromptHash):
		return reject(r.RunID, ReasonMPHHash, fmt.Sprintf("prompt_hash %q", target.MPH.PromptHash))
	case !contentIDPattern.MatchString(target.MPH.HarnessHash):
		return reject(r.RunID, ReasonMPHHash, fmt.Sprintf("harness_hash %q", target.MPH.HarnessHash))
	case !knownEnforcement[target.BudgetEnforcement]:
		return reject(r.RunID, ReasonBudgetEnforce, fmt.Sprintf("%q", target.BudgetEnforcement))
	}
	return nil
}

func (r *Record) validateResults() error {
	for _, group := range [][]CheckResult{r.Validators, r.Checks} {
		for i := range group {
			if group[i].Name == "" {
				return reject(r.RunID, ReasonResultUnnamed, "")
			}
		}
	}
	for i := range r.Evidence {
		if r.Evidence[i].Kind == "" || r.Evidence[i].Location == "" {
			return reject(r.RunID, ReasonEvidencePointer, "")
		}
	}
	return nil
}

// validateMetrics checks each metric's SHAPE and deliberately not the
// registry's completeness.
//
// The metric registry is the runner's own vocabulary. Mirroring the key list
// here would be precisely the hand-maintained enumeration this repository has
// been burned by, and it would go stale the first time the runner adds a key.
// The importer stores what it is given; a record missing a registry key is
// the runner's rejection to make, and the corpus DECLARES that divergence
// rather than hiding it.
func (r *Record) validateMetrics() error {
	for key := range r.Metrics {
		metric := r.Metrics[key]
		if !knownMetricStatus[metric.Status] {
			return reject(r.RunID, ReasonMetricStatus, fmt.Sprintf("%s: %q", key, metric.Status))
		}
		switch metric.Status {
		case "value":
			if metric.Value == nil {
				return reject(r.RunID, ReasonMetricValue, key+": status value with no value")
			}
			if math.IsNaN(*metric.Value) || math.IsInf(*metric.Value, 0) {
				return reject(r.RunID, ReasonMetricValue, fmt.Sprintf("%s: %v is not finite", key, *metric.Value))
			}
		default:
			if metric.Value != nil {
				return reject(r.RunID, ReasonMetricValue,
					fmt.Sprintf("%s: status %q carries a value", key, metric.Status))
			}
		}
	}
	return nil
}

func (r *Record) validateIsolation() error {
	if r.Isolation.WorkspaceDir == "" || r.Isolation.BranchNamespace == "" {
		return reject(r.RunID, ReasonIsolationMissing, "")
	}
	// An attempt whose cleanup could not be verified may have leaked state
	// into the next one, so its results must never enter an aggregation
	// (ADR 0025). The record contract expresses that by forcing the invalid
	// verdict, and importing one that does not would put an unverifiable
	// measurement in the plane looking exactly like a verifiable one.
	if !r.Isolation.CleanupVerified && r.Verdict != "invalid" {
		return reject(r.RunID, ReasonCleanupUnverified, fmt.Sprintf("verdict is %q", r.Verdict))
	}
	return nil
}

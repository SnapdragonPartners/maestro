package engine

import (
	"context"

	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/story"
)

// VerifyResult is the per-item outcome of running a story's validators and
// checks against a prepared solution.
type VerifyResult struct {
	Validators []runrecord.CheckResult
	// ValidatorOutputs holds each validator's FULL captured output, aligned
	// index-for-index with Validators. The engine attempt flow persists these
	// as `test-output` evidence (writeValidatorEvidence), which evidence
	// coverage then requires; a caller that only wants pass/fail (runner
	// verify) ignores them. Dropping them would break evidence coverage.
	ValidatorOutputs []string
	Checks           []runrecord.CheckResult
	OK               bool // true iff every validator and check passed
}

// Verify runs a story's validators then its checks against the bound solution
// at boundDir. It is the single check executor: the engine's attempt flow and
// the `runner verify` subcommand both call it, so neither drifts from the
// other. loaded carries the retained oracle bytes an oracle check materialises;
// pin and solution are the commits files_changed_within diffs.
func Verify(ctx context.Context, boundDir string, loaded *story.Loaded, pin, solution string) VerifyResult {
	def := loaded.Definition
	validators, outputs := runValidators(ctx, boundDir, def.Validators)
	checks := runChecks(ctx, boundDir, def, pin, solution)
	return VerifyResult{
		Validators:       validators,
		ValidatorOutputs: outputs,
		Checks:           checks,
		OK:               allPassed(validators) && allPassed(checks),
	}
}

// allPassed reports whether every result passed.
func allPassed(results []runrecord.CheckResult) bool {
	for i := range results {
		if !results[i].Passed {
			return false
		}
	}
	return true
}

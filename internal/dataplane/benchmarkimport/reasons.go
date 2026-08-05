// Package benchmarkimport reads the golden runner's results store as data.
//
// It does NOT import the runner's Go packages. ADR 0025's black-box rule
// forbids the runner depending on the orchestrator, not the reverse, so a
// module dependency would not violate it directly — but it would pull the
// runner into the build, test and lint walkers it is deliberately outside
// of, and couple the plane's build to a module whose whole point is that it
// can be versioned against targets that do not exist yet. An importer is a
// reader of FILES that happen to have been written by Go.
//
// The cost of that is a second implementation of the record contract's
// semantics, and the drift alarm is the two-sided conformance corpus at
// benchmark/testdata/import_corpus (design D1). Both validators run every
// case and must agree, unless a case DECLARES that they differ.
package benchmarkimport

// Reason names why a record was refused.
//
// The constants live in ONE block because a test walks this file's AST and
// requires every one of them to be exercised by at least one corpus case. A
// hand-maintained coverage list would be the fourth enumeration this
// repository has had to fix after it silently fell behind; deriving it from
// the source cannot.
type Reason string

// The rejection reasons. Each is a distinct rule a record can break.
const (
	ReasonSchemaVersion      Reason = "record_schema_version is not a version this build reads"
	ReasonUnknownField       Reason = "record carries a field this build does not know"
	ReasonTrailingContent    Reason = "record line carries content after the object"
	ReasonRunID              Reason = "run_id is missing or is not a single path component"
	ReasonSuiteRunID         Reason = "suite_run_id is missing or malformed"
	ReasonStoryID            Reason = "story_id is missing"
	ReasonConfigName         Reason = "config_name is missing"
	ReasonStoryHash          Reason = "story_hash is not a complete content identity"
	ReasonConfigHash         Reason = "config_hash is not a complete content identity"
	ReasonVerdict            Reason = "verdict is not one of accepted, failed, invalid"
	ReasonFailureKind        Reason = "failure_kind is absent, unknown, or present on a record that forbids it"
	ReasonInvalidReason      Reason = "invalid_reason is absent, or present on a record that forbids it"
	ReasonTerminalState      Reason = "an accepted record does not report reaching its terminal state"
	ReasonSolutionCommit     Reason = "solution_commit is missing or is not a 40-hex commit"
	ReasonResultsMissing     Reason = "an accepted record carries no validators or no checks"
	ReasonResultFailed       Reason = "an accepted record carries a failed validator or check"
	ReasonResultUnnamed      Reason = "a validator or check result has no name"
	ReasonEvidencePointer    Reason = "an evidence pointer is missing its kind or location"
	ReasonTimestamps         Reason = "started_at or finished_at is missing"
	ReasonTimeOrder          Reason = "finished_at precedes started_at"
	ReasonAdapterIdentity    Reason = "the target descriptor is missing its adapter name or version"
	ReasonCommitHash         Reason = "the target descriptor's commit_hash is not a 40-hex commit"
	ReasonBinaryIdentity     Reason = "the target descriptor carries no binary identity"
	ReasonMPHIdentity        Reason = "the MPH signature is missing its model or prompt pack"
	ReasonMPHHash            Reason = "an MPH hash is not a complete content identity"
	ReasonBudgetEnforce      Reason = "budget_enforcement is not one of streamed, self-enforced, post-hoc"
	ReasonMetricStatus       Reason = "a metric carries an unknown status"
	ReasonMetricValue        Reason = "a metric's value contradicts its status, or is not finite"
	ReasonMetricNegative     Reason = "a metric value is negative"
	ReasonMetricFractional   Reason = "a count metric carries a fractional value"
	ReasonMetricMissing      Reason = "a registry metric key is absent"
	ReasonMetricUnknownKey   Reason = "a metric key is outside the registry"
	ReasonCapabilityKey      Reason = "a declared capability is not a registry metric key"
	ReasonCapabilityDup      Reason = "a capability is declared twice"
	ReasonCapabilityConflict Reason = "a metric contradicts the target's declared capabilities"
	ReasonIsolationMissing   Reason = "the isolation block is missing its workspace or branch namespace"
	ReasonCleanupUnverified  Reason = "unverified cleanup requires the invalid verdict"
)

// There is deliberately no Reasons() accessor here.
//
// The coverage test discovers these constants by walking this file's AST,
// which is the point: a hand-written slice would be another enumeration to
// keep in step, and production code has no use for the list at all. Parsing
// its own source to satisfy a test would be the tail wagging the dog.

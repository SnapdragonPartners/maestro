package benchmarkimport

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// SchemaVersion is the record contract this build reads.
//
// It mirrors the runner's runrecord.SchemaVersion. The mirror is deliberate
// (see the package comment) and the corpus is what keeps the two honest: a
// bump on either side without the other fails the fixture round-trip.
const SchemaVersion = 1

// Rejection is a refused record, naming the rule that refused it.
type Rejection struct {
	Reason Reason
	Detail string
	// RunID is the record's own identity where it could be read; a record
	// too malformed to name itself carries an empty one.
	RunID string
}

func (e *Rejection) Error() string {
	where := "record"
	if e.RunID != "" {
		where = "record " + e.RunID
	}
	if e.Detail == "" {
		return fmt.Sprintf("%s refused: %s", where, e.Reason)
	}
	return fmt.Sprintf("%s refused: %s (%s)", where, e.Reason, e.Detail)
}

// reject builds a rejection.
func reject(runID string, reason Reason, detail string) error {
	return &Rejection{Reason: reason, Detail: detail, RunID: runID}
}

// Metric is one normalized metric observation.
//
// Value is a pointer because a measured zero is a measurement and must
// survive the round trip, while an absent value is a different fact.
type Metric struct {
	Value  *float64 `json:"value,omitempty"`
	Status string   `json:"status"`
	Reason string   `json:"reason,omitempty"`
}

// CheckResult is one engine-executed validator or check.
type CheckResult struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	Passed bool   `json:"passed"`
}

// EvidencePointer is a raw pointer into whatever the target exposed.
//
// Carried through as PROVENANCE and never used to locate bytes: the location
// is an absolute path recorded on the machine that ran the attempt, and the
// results store is portable while those paths are not (design D8).
type EvidencePointer struct {
	Kind     string `json:"kind"`
	Location string `json:"location"`
}

// MPHIdentity is the Model/Prompt/Harness identity of the configuration under
// test.
type MPHIdentity struct {
	Model          string `json:"model"`
	PromptPack     string `json:"prompt_pack"`
	PromptHash     string `json:"prompt_hash"`
	HarnessHash    string `json:"harness_hash"`
	MaestroVersion string `json:"maestro_version,omitempty"`
}

// TargetDescriptor records what a run measured.
type TargetDescriptor struct {
	AdapterName       string      `json:"adapter_name"`
	AdapterVersion    string      `json:"adapter_version"`
	CommitHash        string      `json:"commit_hash"`
	BinaryIdentity    string      `json:"binary_identity"`
	MPH               MPHIdentity `json:"mph"`
	BudgetEnforcement string      `json:"budget_enforcement"`
	Capabilities      []string    `json:"capabilities"`
}

// Isolation records the attempt's repeat-isolation facts.
type Isolation struct {
	WorkspaceDir    string `json:"workspace_dir"`
	BranchNamespace string `json:"branch_namespace"`
	CleanupVerified bool   `json:"cleanup_verified"`
}

// Record is one attempt's normalized result as it appears on disk.
type Record struct {
	StartedAt            *time.Time        `json:"started_at"`
	FinishedAt           *time.Time        `json:"finished_at"`
	Metrics              map[string]Metric `json:"metrics"`
	RunID                string            `json:"run_id"`
	SuiteRunID           string            `json:"suite_run_id"`
	StoryID              string            `json:"story_id"`
	StoryHash            string            `json:"story_hash"`
	ConfigName           string            `json:"config_name"`
	ConfigHash           string            `json:"config_hash"`
	Verdict              string            `json:"verdict"`
	FailureKind          string            `json:"failure_kind,omitempty"`
	InvalidReason        string            `json:"invalid_reason,omitempty"`
	SolutionCommit       string            `json:"solution_commit,omitempty"`
	Validators           []CheckResult     `json:"validators"`
	Checks               []CheckResult     `json:"checks"`
	Evidence             []EvidencePointer `json:"evidence,omitempty"`
	Target               TargetDescriptor  `json:"target"`
	Isolation            Isolation         `json:"isolation"`
	SchemaVersion        int               `json:"record_schema_version"`
	TerminalStateReached bool              `json:"terminal_state_reached"`
}

// DecodeRecord parses one JSONL line strictly.
//
// Unknown fields are refused rather than ignored. A record carrying a field
// this build does not know was written by a contract this build does not
// speak, and record_schema_version is supposed to be what catches that — so a
// disagreement here means the version lied, which is not a condition to read
// past. Exhaustion is proven by decoding a SECOND value and requiring io.EOF,
// not by Decoder.More: More answers "is there another element in the array or
// object I am inside?", and after a top-level value there is none, so it
// reports false for a stray `]` or `}` — the one form of trailing garbage
// that looks like an ending.
func DecodeRecord(line string) (Record, error) {
	var record Record
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return record, reject("", ReasonUnknownField, err.Error())
		}
		return record, fmt.Errorf("decode record: %w", err)
	}
	var rest json.RawMessage
	if err := decoder.Decode(&rest); err == nil {
		return record, reject(record.RunID, ReasonTrailingContent, "a second value follows the record")
	} else if !isEOF(err) {
		return record, reject(record.RunID, ReasonTrailingContent, err.Error())
	}
	return record, nil
}

func isEOF(err error) bool { return err == io.EOF } //nolint:errorlint // json returns io.EOF unwrapped

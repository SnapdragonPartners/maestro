package runrecord

import (
	"fmt"
	"regexp"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/internal/contenthash"
)

// SchemaVersion is the current run-record contract version. Every record
// self-describes with it; readers reject versions they do not know.
const SchemaVersion = 1

//nolint:gochecknoglobals // Package-level compiled regex for performance.
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Verdict is the runner's terminal judgment of one attempt.
type Verdict string

// Verdicts. Invalid attempts (unverifiable isolation or cleanup) are
// excluded from every aggregation and counted separately (ADR 0025).
const (
	// VerdictAccepted means benchmark acceptance was reached: deterministic
	// checks pass, validators pass, required artifacts and evidence shapes
	// are present, and the expected branch/PR terminal state was reached.
	VerdictAccepted Verdict = "accepted"
	// VerdictFailed means a valid attempt that did not reach acceptance.
	VerdictFailed Verdict = "failed"
	// VerdictInvalid means the attempt's isolation or cleanup could not be
	// verified; its results must never enter comparisons.
	VerdictInvalid Verdict = "invalid"
)

// FailureKind classifies a failed attempt.
type FailureKind string

// Failure kinds. A failed record carries exactly one.
const (
	// FailureBudgetOverrun means a declared budget cap was exceeded and the
	// attempt was aborted; its costs still count (ADR 0025).
	FailureBudgetOverrun FailureKind = "budget-overrun"
	// FailureChecksFailed means one or more deterministic checks failed.
	FailureChecksFailed FailureKind = "checks-failed"
	// FailureValidatorFailed means an engine-executed validator failed.
	FailureValidatorFailed FailureKind = "validator-failed"
	// FailureEvidenceMissing means required artifacts or evidence shapes
	// were absent.
	FailureEvidenceMissing FailureKind = "evidence-missing"
	// FailureBranchState means the expected branch/PR terminal state was
	// not reached.
	FailureBranchState FailureKind = "branch-state"
	// FailureTargetError means the target errored or crashed mid-attempt.
	FailureTargetError FailureKind = "target-error"
)

// knownFailureKind reports whether k is in the closed failure-kind set.
func knownFailureKind(k FailureKind) bool {
	switch k {
	case FailureBudgetOverrun, FailureChecksFailed, FailureValidatorFailed,
		FailureEvidenceMissing, FailureBranchState, FailureTargetError:
		return true
	default:
		return false
	}
}

// CheckResult is the outcome of one engine-executed validator or check.
type CheckResult struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	Passed bool   `json:"passed"`
}

// EvidencePointer is a raw pointer into whatever the target exposes — a log
// path, a SQLite file, a PR URL. Kinds are adapter-defined.
type EvidencePointer struct {
	Kind     string `json:"kind"`
	Location string `json:"location"`
}

// MPHIdentity is the Model/Prompt/Harness identity of the configuration
// under test, derived from content, never location (ADRs 0021, 0025).
type MPHIdentity struct {
	Model          string `json:"model"`
	PromptPack     string `json:"prompt_pack"`
	PromptHash     string `json:"prompt_hash"`
	HarnessHash    string `json:"harness_hash"`
	MaestroVersion string `json:"maestro_version,omitempty"`
}

// TargetDescriptor records what a run measured (ADR 0025).
type TargetDescriptor struct {
	AdapterName    string      `json:"adapter_name"`
	AdapterVersion string      `json:"adapter_version"`
	CommitHash     string      `json:"commit_hash"`
	BinaryIdentity string      `json:"binary_identity"`
	MPH            MPHIdentity `json:"mph"`
	// Capabilities lists the metric keys this target can report; every
	// other registry key is expected to be unsupported.
	Capabilities []MetricKey `json:"capabilities"`
}

// Isolation records the attempt's repeat-isolation facts.
type Isolation struct {
	WorkspaceDir    string `json:"workspace_dir"`
	BranchNamespace string `json:"branch_namespace"`
	// CleanupVerified is false when cleanup could not be confirmed; such
	// attempts must carry VerdictInvalid.
	CleanupVerified bool `json:"cleanup_verified"`
}

// RunRecord is one attempt's complete normalized result — the unit the
// results store appends and reports aggregate over.
type RunRecord struct {
	StartedAt            time.Time         `json:"started_at"`
	FinishedAt           time.Time         `json:"finished_at"`
	Metrics              Metrics           `json:"metrics"`
	RunID                string            `json:"run_id"`
	SuiteRunID           string            `json:"suite_run_id"`
	StoryID              string            `json:"story_id"`
	StoryHash            string            `json:"story_hash"`
	ConfigName           string            `json:"config_name"`
	ConfigHash           string            `json:"config_hash"`
	Verdict              Verdict           `json:"verdict"`
	FailureKind          FailureKind       `json:"failure_kind,omitempty"`
	InvalidReason        string            `json:"invalid_reason,omitempty"`
	Validators           []CheckResult     `json:"validators"`
	Checks               []CheckResult     `json:"checks"`
	Evidence             []EvidencePointer `json:"evidence,omitempty"`
	Target               TargetDescriptor  `json:"target"`
	Isolation            Isolation         `json:"isolation"`
	SchemaVersion        int               `json:"record_schema_version"`
	TerminalStateReached bool              `json:"terminal_state_reached"`
}

// Validate checks the record's internal coherence: identity fields, verdict
// pairing rules, timestamp ordering, metric completeness, and check shapes.
func (r *RunRecord) Validate() error {
	if r == nil {
		return fmt.Errorf("nil run record")
	}
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("record schema version %d: this runner knows only version %d", r.SchemaVersion, SchemaVersion)
	}
	if err := r.validateIdentity(); err != nil {
		return err
	}
	if err := r.validateVerdict(); err != nil {
		return err
	}
	if err := r.validateTimestamps(); err != nil {
		return err
	}
	if err := r.Target.Validate(); err != nil {
		return fmt.Errorf("target descriptor: %w", err)
	}
	if err := r.validateResults(); err != nil {
		return err
	}
	if err := r.Metrics.Validate(); err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	if err := CapabilityCoherence(r.Target.Capabilities, r.Metrics); err != nil {
		return err
	}
	if r.Isolation.WorkspaceDir == "" || r.Isolation.BranchNamespace == "" {
		return fmt.Errorf("isolation workspace_dir and branch_namespace are required")
	}
	if r.Verdict != VerdictInvalid && !r.Isolation.CleanupVerified {
		return fmt.Errorf("unverified cleanup requires verdict %q: such attempts never enter aggregations", VerdictInvalid)
	}
	return nil
}

// CapabilityCoherence enforces that a metrics map cannot contradict its
// target's declared capabilities: capabilities must be registry keys with
// no duplicates; unsupported iff the capability is absent; value and
// unavailable only when present. not_applicable is story-dependent and
// legal either way.
func CapabilityCoherence(capabilities []MetricKey, metrics Metrics) error {
	capable := make(map[MetricKey]bool, len(capabilities))
	for _, key := range capabilities {
		if !inRegistry(key) {
			return fmt.Errorf("capability %q is not a registry metric key", key)
		}
		if capable[key] {
			return fmt.Errorf("capability %q declared twice", key)
		}
		capable[key] = true
	}
	for _, spec := range Registry() {
		metric, ok := metrics[spec.Key]
		if !ok {
			continue // completeness is Metrics.Validate's job
		}
		switch metric.Status {
		case StatusUnsupported:
			if capable[spec.Key] {
				return fmt.Errorf("metric %q reported unsupported by a target that declares the capability", spec.Key)
			}
		case StatusValue, StatusUnavailable:
			if !capable[spec.Key] {
				return fmt.Errorf("metric %q reported %s without a declared capability", spec.Key, metric.Status)
			}
		case StatusNotApplicable:
			// Story-dependent; legal with or without the capability.
		}
	}
	return nil
}

func (r *RunRecord) validateIdentity() error {
	switch {
	case r.RunID == "":
		return fmt.Errorf("run_id is required")
	case r.SuiteRunID == "":
		return fmt.Errorf("suite_run_id is required")
	case r.StoryID == "":
		return fmt.Errorf("story_id is required")
	case r.ConfigName == "":
		return fmt.Errorf("config_name is required")
	case !contenthash.Valid(r.StoryHash):
		return fmt.Errorf("story_hash must be a complete %q content identity, got %q", contenthash.Prefix, r.StoryHash)
	case !contenthash.Valid(r.ConfigHash):
		return fmt.Errorf("config_hash must be a complete %q content identity, got %q", contenthash.Prefix, r.ConfigHash)
	}
	return nil
}

func (r *RunRecord) validateVerdict() error {
	switch r.Verdict {
	case VerdictAccepted:
		return r.validateAccepted()
	case VerdictFailed:
		if !knownFailureKind(r.FailureKind) {
			return fmt.Errorf("failed records require a known failure kind, got %q", r.FailureKind)
		}
		if r.InvalidReason != "" {
			return fmt.Errorf("failed records must not carry an invalid reason")
		}
	case VerdictInvalid:
		if r.InvalidReason == "" {
			return fmt.Errorf("invalid records require an invalid reason")
		}
		if r.FailureKind != "" {
			return fmt.Errorf("invalid records must not carry a failure kind")
		}
	default:
		return fmt.Errorf("unknown verdict %q", r.Verdict)
	}
	return nil
}

// validateAccepted enforces what benchmark acceptance means: every
// validator and check ran and passed, and the terminal state was reached.
// An accepted record with failed or absent results is a contradiction.
func (r *RunRecord) validateAccepted() error {
	if r.FailureKind != "" || r.InvalidReason != "" {
		return fmt.Errorf("accepted records must not carry a failure kind or invalid reason")
	}
	if !r.TerminalStateReached {
		return fmt.Errorf("accepted records require terminal_state_reached")
	}
	if len(r.Validators) == 0 || len(r.Checks) == 0 {
		return fmt.Errorf("accepted records require nonempty validator and check results")
	}
	for i := range r.Validators {
		if !r.Validators[i].Passed {
			return fmt.Errorf("accepted records cannot contain failed validator %q", r.Validators[i].Name)
		}
	}
	for i := range r.Checks {
		if !r.Checks[i].Passed {
			return fmt.Errorf("accepted records cannot contain failed check %q", r.Checks[i].Name)
		}
	}
	return nil
}

func (r *RunRecord) validateTimestamps() error {
	if r.StartedAt.IsZero() || r.FinishedAt.IsZero() {
		return fmt.Errorf("started_at and finished_at are required")
	}
	if r.FinishedAt.Before(r.StartedAt) {
		return fmt.Errorf("finished_at %s precedes started_at %s", r.FinishedAt, r.StartedAt)
	}
	return nil
}

func (r *RunRecord) validateResults() error {
	for i := range r.Validators {
		if r.Validators[i].Name == "" {
			return fmt.Errorf("validator results require names")
		}
	}
	for i := range r.Checks {
		if r.Checks[i].Name == "" {
			return fmt.Errorf("check results require names")
		}
	}
	for i := range r.Evidence {
		if r.Evidence[i].Kind == "" || r.Evidence[i].Location == "" {
			return fmt.Errorf("evidence pointers require kind and location")
		}
	}
	return nil
}

// Validate checks the descriptor is a complete, well-formed comparison key:
// adapter identity, 40-hex commit, binary identity, and MPH content hashes.
func (d *TargetDescriptor) Validate() error {
	if d.AdapterName == "" || d.AdapterVersion == "" {
		return fmt.Errorf("adapter_name and adapter_version are required")
	}
	if !commitPattern.MatchString(d.CommitHash) {
		return fmt.Errorf("commit_hash %q must be a full 40-hex commit: it is a comparison key", d.CommitHash)
	}
	if d.BinaryIdentity == "" {
		return fmt.Errorf("binary_identity is required: run records say exactly what they measured")
	}
	mph := d.MPH
	if mph.Model == "" || mph.PromptPack == "" {
		return fmt.Errorf("mph model and prompt_pack are required")
	}
	if !contenthash.Valid(mph.PromptHash) {
		return fmt.Errorf("mph prompt_hash must be a complete %q content identity, got %q", contenthash.Prefix, mph.PromptHash)
	}
	if !contenthash.Valid(mph.HarnessHash) {
		return fmt.Errorf("mph harness_hash must be a complete %q content identity, got %q", contenthash.Prefix, mph.HarnessHash)
	}
	// Capability keys themselves are validated by CapabilityCoherence,
	// which every record and observation runs through.
	return nil
}

// inRegistry reports whether key is part of the metric registry.
func inRegistry(key MetricKey) bool {
	for _, spec := range Registry() {
		if spec.Key == key {
			return true
		}
	}
	return false
}

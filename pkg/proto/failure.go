// Package proto - failure taxonomy for typed agent failure classification.
//
// This file defines the failure kinds and structured failure info that flow through
// the system from coder → state machine → supervisor → requeue → architect.
//
// See docs/FAILURE_RECOVERY_V2_SPEC.md for the full recovery architecture.
package proto

import (
	"time"

	"github.com/google/uuid"
)

// FailureKind classifies the cause of an agent failure.
// Used to drive different recovery paths in the supervisor and architect.
type FailureKind string

const (
	// FailureKindTransient represents temporary external service unavailability
	// (API rate limits, network timeouts). Recovery: SUSPEND state, auto-resume.
	// Already implemented via SUSPEND + pollAPIHealth — listed for taxonomy completeness.
	FailureKindTransient FailureKind = "transient"

	// FailureKindStoryInvalid means the story requirements are unclear, contradictory,
	// or impossible to implement. Recovery: coder → ERROR, architect must rewrite story.
	// Requires LLM agency — the coder must explain why the story is invalid.
	FailureKindStoryInvalid FailureKind = "story_invalid"

	// FailureKindExternal is a deprecated v1 umbrella kind for infrastructure/environment
	// issues. Replaced by FailureKindEnvironment and FailureKindPrerequisite in Phase 2.
	// Kept for backward compatibility with failure records created before the split.
	// Use NormalizeFailureKind() to map to the current taxonomy.
	FailureKindExternal FailureKind = "external"

	// FailureKindEnvironment means the local or shared execution environment is broken
	// or inconsistent. Examples: corrupted clone state, broken toolchain, invalid workspace
	// state, unrecoverable local container issues, disk space, permissions.
	FailureKindEnvironment FailureKind = "environment"

	// FailureKindPrerequisite means progress depends on an external prerequisite that
	// is missing, invalid, expired, or unavailable. Examples: invalid API credentials,
	// revoked access, unavailable third-party service, missing configuration.
	FailureKindPrerequisite FailureKind = "prerequisite"
)

// NormalizeFailureKind maps deprecated kind values to the current taxonomy.
// Records created before the Phase 2 kind split used "external" for both
// environment and prerequisite failures. This maps "external" → "environment"
// (the more common case). Returns the kind unchanged if already current.
func NormalizeFailureKind(kind FailureKind) FailureKind {
	if kind == FailureKindExternal {
		return FailureKindEnvironment
	}
	return kind
}

// FailureScope describes the blast radius of a failure.
type FailureScope string

const (
	// FailureScopeAttempt means isolated to one agent attempt or one local workspace.
	FailureScopeAttempt FailureScope = "attempt"
	// FailureScopeStory means affects only the current story.
	FailureScopeStory FailureScope = "story"
	// FailureScopeEpoch means affects multiple stories in the current requirements epoch.
	FailureScopeEpoch FailureScope = "epoch"
	// FailureScopeSystem means affects the shared execution environment.
	FailureScopeSystem FailureScope = "system"
)

// FailureSource identifies who/what reported the failure.
type FailureSource string

// FailureSource constants identify who/what reported the failure.
const (
	FailureSourceLLMReport      FailureSource = "llm_report"
	FailureSourceAutoClassifier FailureSource = "auto_classifier"
	FailureSourceArchitect      FailureSource = "architect"
	FailureSourceOrchestrator   FailureSource = "orchestrator"
)

// FailureOwner identifies who is currently responsible for recovery.
type FailureOwner string

// FailureOwner constants identify who is responsible for recovery.
const (
	FailureOwnerOrchestrator FailureOwner = "orchestrator"
	FailureOwnerArchitect    FailureOwner = "architect"
	FailureOwnerPM           FailureOwner = "pm"
	FailureOwnerHuman        FailureOwner = "human"
)

// FailureAction describes the recovery action being taken.
type FailureAction string

// FailureAction constants for recovery actions.
const (
	FailureActionRetryAttempt      FailureAction = "retry_attempt"
	FailureActionRewriteStory      FailureAction = "rewrite_story"
	FailureActionRewriteEpoch      FailureAction = "rewrite_epoch"
	FailureActionRepairEnvironment FailureAction = "repair_environment"
	FailureActionValidatePrereq    FailureAction = "validate_prerequisite"
	FailureActionAskHuman          FailureAction = "ask_human"
	FailureActionMarkFailed        FailureAction = "mark_failed"
)

// FailureResolutionStatus tracks the progress of a recovery action.
type FailureResolutionStatus string

// FailureResolutionStatus constants for tracking recovery progress.
const (
	FailureResolutionPending   FailureResolutionStatus = "pending"
	FailureResolutionRunning   FailureResolutionStatus = "running"
	FailureResolutionSucceeded FailureResolutionStatus = "succeeded"
	FailureResolutionFailed    FailureResolutionStatus = "failed"
	FailureResolutionEscalated FailureResolutionStatus = "escalated"
)

// KeyFailureInfo is the metadata key used to pass FailureInfo through
// StateChangeNotification metadata and state data.
const KeyFailureInfo = "failure_info"

// FailureEvidence captures a diagnostic artifact from a failure.
type FailureEvidence struct {
	Kind    string `json:"kind"`              // e.g., "tool_output", "git_error", "build_log"
	Summary string `json:"summary"`           // Human-readable summary
	Snippet string `json:"snippet,omitempty"` // Truncated raw output
}

// FailureInfo carries structured failure context through the system.
// Stored as a value type (not pointer) in metadata maps to survive transport.
//
// The struct is organized into logical groups:
//   - Identity: ID, timestamps
//   - Context: session, spec, story, attempt
//   - Report: source, kind, scope guess, explanation, evidence (from reporter)
//   - Triage: resolved kind/scope, human-needed decision (from triage)
//   - Resolution: owner, action, status, outcome (recovery tracking)
//   - Analytics: model, provider, base commit, tags
type FailureInfo struct {
	// Identity
	ID        string    `json:"id,omitempty"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`

	// Context
	SessionID     string `json:"session_id,omitempty"`
	SpecID        string `json:"spec_id,omitempty"`
	StoryID       string `json:"story_id,omitempty"`
	AttemptNumber int    `json:"attempt_number,omitempty"`

	// Report (original v1 fields kept at top level for backward compat)
	Kind        FailureKind `json:"kind"`                // Classification of the failure
	Explanation string      `json:"explanation"`         // Human-readable reason
	FailedState string      `json:"failed_state"`        // State where failure occurred (e.g., "CODING")
	ToolName    string      `json:"tool_name,omitempty"` // Tool that triggered failure

	// Report (v2 fields)
	Source           FailureSource     `json:"source,omitempty"`
	ScopeGuess       FailureScope      `json:"scope_guess,omitempty"`
	HumanNeededGuess bool              `json:"human_needed_guess,omitempty"`
	Evidence         []FailureEvidence `json:"evidence,omitempty"`

	// Triage (resolved by architect/orchestrator)
	ResolvedKind     FailureKind  `json:"resolved_kind,omitempty"`
	ResolvedScope    FailureScope `json:"resolved_scope,omitempty"`
	HumanNeeded      bool         `json:"human_needed,omitempty"`
	AffectedStoryIDs []string     `json:"affected_story_ids,omitempty"`
	TriageSummary    string       `json:"triage_summary,omitempty"`

	// Resolution
	Owner             FailureOwner            `json:"owner,omitempty"`
	Action            FailureAction           `json:"action,omitempty"`
	ResolutionStatus  FailureResolutionStatus `json:"resolution_status,omitempty"`
	ResolutionOutcome string                  `json:"resolution_outcome,omitempty"`

	// Analytics
	Tags       []string `json:"tags,omitempty"`
	Model      string   `json:"model,omitempty"`
	Provider   string   `json:"provider,omitempty"`
	BaseCommit string   `json:"base_commit,omitempty"`
}

// NewFailureInfo creates a FailureInfo with the v1 parameters.
// This is the backward-compatible constructor used by report_blocked and auto-classifier.
func NewFailureInfo(kind FailureKind, explanation, failedState, toolName string) FailureInfo {
	now := time.Now().UTC()
	return FailureInfo{
		ID:          GenerateFailureID(),
		Kind:        kind,
		Explanation: explanation,
		FailedState: failedState,
		ToolName:    toolName,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// NewFailureInfoV2 creates a fully-populated Tier 1 FailureInfo with a generated ID.
func NewFailureInfoV2(
	kind FailureKind,
	explanation, failedState, toolName string,
	source FailureSource,
	scopeGuess FailureScope,
	storyID, specID, sessionID string,
	attemptNumber int,
) FailureInfo {
	now := time.Now().UTC()
	return FailureInfo{
		ID:               GenerateFailureID(),
		CreatedAt:        now,
		UpdatedAt:        now,
		SessionID:        sessionID,
		SpecID:           specID,
		StoryID:          storyID,
		AttemptNumber:    attemptNumber,
		Kind:             kind,
		Explanation:      explanation,
		FailedState:      failedState,
		ToolName:         toolName,
		Source:           source,
		ScopeGuess:       scopeGuess,
		ResolutionStatus: FailureResolutionPending,
	}
}

// GenerateFailureID generates a unique ID for a failure record.
func GenerateFailureID() string {
	return uuid.New().String()
}

// SetResolution updates the resolution fields on a FailureInfo.
func (fi *FailureInfo) SetResolution(owner FailureOwner, action FailureAction, status FailureResolutionStatus) {
	fi.Owner = owner
	fi.Action = action
	fi.ResolutionStatus = status
	fi.UpdatedAt = time.Now().UTC()
}

// SetTriage updates the triage fields on a FailureInfo.
func (fi *FailureInfo) SetTriage(resolvedKind FailureKind, resolvedScope FailureScope, humanNeeded bool, affectedStoryIDs []string, summary string) {
	fi.ResolvedKind = resolvedKind
	fi.ResolvedScope = resolvedScope
	fi.HumanNeeded = humanNeeded
	fi.AffectedStoryIDs = affectedStoryIDs
	fi.TriageSummary = summary
	fi.UpdatedAt = time.Now().UTC()
}

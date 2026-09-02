// Package work registers the first production Management artifact types:
// the governing records of the work hierarchy, and the Story completion
// (Phase 3 item 3, design D14).
//
// They exist because item 2's schema makes a dispatch's governing references
// NOT NULL: no Story can be dispatched until it has an accepted governing
// artifact of a known type, and no predecessor edge can be satisfied without
// an accepted completion. That is the consumer; this is the vocabulary.
//
// Each payload carries ONLY what no row owns. Title, repository, lineage and
// dependencies are columns in migrations 000002/000003/000021, so they do
// not appear here — one authority per fact, and nothing for a reviewed
// payload to disagree with. Version 1 is the ADR 0024 / ADR 0023 contract
// as stated, not a placeholder: under ADR 0028 a new OPTIONAL field extends
// this version, and only a newly required or incompatible one would force
// a new version, which is a decision a later item makes against that ADR's
// cost.
//
// The package sits under the data plane rather than in the Orchestrator
// because the seam's dispatch validation names these types (design D10) and
// the Orchestrator declares them; both import this, neither imports the
// other.
package work

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"orchestrator/internal/dataplane/registry"
)

// The artifact types. The `work.` prefix follows the importer's
// `benchmark.` convention.
const (
	// TypeEpicRecord governs an Epic (epics.governing_artifact_id). ADR
	// 0024's Epic record: intent content and the triage outputs the row
	// does not hold. `repository` and `dependencies` ARE rows
	// (epics.repository_id, epic_dependencies) and are not repeated.
	TypeEpicRecord registry.Type = "work.epic_record"

	// TypeStoryRecord governs a Story (stories.governing_artifact_id): the
	// Architect-owned decomposition unit, a PR-sized chunk of an Epic.
	TypeStoryRecord registry.Type = "work.story_record"

	// TypeStoryCompletion is what satisfies a dependency edge
	// (story_dependencies.satisfying_completion_*) and enters a dispatch
	// basis. ADR 0023's merge policy: the Architect's review record after
	// final code review gates the Story→Epic merge; this is that record,
	// naming the reviewed head. The branch name derives from the Story id
	// and the merge commit follows acceptance as Audit data, so neither is
	// here.
	//
	// Acceptance is necessary and not sufficient for the edge: the
	// satisfying pointer is set only after the merge succeeds (item 10's
	// obligation), because ADR 0023 lets the merge conflict and return to
	// the Coder.
	TypeStoryCompletion registry.Type = "work.story_completion"
)

// PayloadVersion is the schema version all three payloads are written at.
const PayloadVersion = 1

// Epic modes, ADR 0024's triage output.
const (
	ModeWorkbench = "workbench"
	ModeFactory   = "factory"
)

// EpicRecordPayload is the body of a work.epic_record.
type EpicRecordPayload struct {
	// Intent is the Epic's intent content: what this Epic is for, in the
	// author's words. Required and non-blank.
	Intent string `json:"intent"`
	// Mode is `workbench` or `factory`. Required.
	Mode string `json:"mode"`
}

// StoryRecordPayload is the body of a work.story_record.
type StoryRecordPayload struct {
	// Intent is what the Story must accomplish. Required and non-blank.
	Intent string `json:"intent"`
}

// StoryCompletionPayload is the body of a work.story_completion.
type StoryCompletionPayload struct {
	// HeadCommit is the reviewed head of the Story branch: the commit the
	// Architect's final review accepted and the merge carries into the
	// Epic branch. If conflict resolution moves the head, the completion is
	// amended to name the merged one before the edge is satisfied.
	HeadCommit string `json:"head_commit"`
}

// commitPattern is a full 40-hex SHA-1, the same shape the importer holds
// solution commits to.
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ErrInvalidPayload is the sentinel every validator wraps, so a caller can
// tell a malformed payload from a registry or transport failure.
var ErrInvalidPayload = errors.New("invalid work payload")

// RegistryEntries returns the registrations this package writes.
//
// Returned rather than registered globally, for the reason the importer
// gives: the registry is immutable by construction and built once at
// startup. No extractors, deliberately — none of these payloads names
// evidence, so acceptance requires exactly zero pins for each.
func RegistryEntries() map[registry.Type]registry.Entry {
	return map[registry.Type]registry.Entry{
		TypeEpicRecord: {
			Category:       registry.CategoryManagement,
			CurrentVersion: PayloadVersion,
			Validators: map[int]registry.Validator{
				PayloadVersion: registry.ValidatorFunc(ValidateEpicRecord),
			},
		},
		TypeStoryRecord: {
			Category:       registry.CategoryManagement,
			CurrentVersion: PayloadVersion,
			Validators: map[int]registry.Validator{
				PayloadVersion: registry.ValidatorFunc(ValidateStoryRecord),
			},
		},
		TypeStoryCompletion: {
			Category:       registry.CategoryManagement,
			CurrentVersion: PayloadVersion,
			Validators: map[int]registry.Validator{
				PayloadVersion: registry.ValidatorFunc(ValidateStoryCompletion),
			},
		},
	}
}

// ValidateEpicRecord checks a version-1 Epic record payload.
func ValidateEpicRecord(payload []byte) error {
	var body EpicRecordPayload
	if err := decodeStrict(payload, &body); err != nil {
		return fmt.Errorf("%s: %w", TypeEpicRecord, err)
	}
	if strings.TrimSpace(body.Intent) == "" {
		return fmt.Errorf("%s: %w: intent is required", TypeEpicRecord, ErrInvalidPayload)
	}
	switch body.Mode {
	case ModeWorkbench, ModeFactory:
	default:
		return fmt.Errorf("%s: %w: mode %q is not %q or %q", TypeEpicRecord, ErrInvalidPayload,
			body.Mode, ModeWorkbench, ModeFactory)
	}
	return nil
}

// ValidateStoryRecord checks a version-1 Story record payload.
func ValidateStoryRecord(payload []byte) error {
	var body StoryRecordPayload
	if err := decodeStrict(payload, &body); err != nil {
		return fmt.Errorf("%s: %w", TypeStoryRecord, err)
	}
	if strings.TrimSpace(body.Intent) == "" {
		return fmt.Errorf("%s: %w: intent is required", TypeStoryRecord, ErrInvalidPayload)
	}
	return nil
}

// ValidateStoryCompletion checks a version-1 Story completion payload.
func ValidateStoryCompletion(payload []byte) error {
	var body StoryCompletionPayload
	if err := decodeStrict(payload, &body); err != nil {
		return fmt.Errorf("%s: %w", TypeStoryCompletion, err)
	}
	if !commitPattern.MatchString(body.HeadCommit) {
		return fmt.Errorf("%s: %w: head_commit %q is not a 40-hex commit", TypeStoryCompletion,
			ErrInvalidPayload, body.HeadCommit)
	}
	return nil
}

// decodeStrict refuses unknown fields and trailing content.
//
// Unknown fields are refused because ADR 0028 makes the reader the only
// compatibility layer: a field this version does not know is either a typo
// or a newer version's, and either way silently dropping it would store a
// payload whose digest covers bytes no validator saw.
func decodeStrict[T any](payload []byte, into *T) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPayload, err)
	}
	if decoder.More() {
		return fmt.Errorf("%w: trailing content after the payload object", ErrInvalidPayload)
	}
	return nil
}

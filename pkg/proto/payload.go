// Package proto provides the MessagePayload discriminated union for type-safe message payloads.
//
// The MessagePayload design addresses a critical issue where map[string]any payloads caused
// silent failures when payload structures changed. With discriminated unions, payload
// mismatches result in explicit errors rather than silent bugs.
//
// Design benefits:
// - Forced serialization/deserialization prevents silent type assertion failures
// - Clear payload contracts at compile time
// - Explicit errors when wrong payload type is accessed
// - Trivial to log/persist messages with proper typing
package proto

import (
	"encoding/json"
	"fmt"
)

// PayloadKind identifies the type of payload in a message.
type PayloadKind string

// Payload kind constants define the discriminator values for the union.
const (
	// Request payloads.
	PayloadKindQuestionRequest PayloadKind = "question_request"
	PayloadKindApprovalRequest PayloadKind = "approval_request"
	PayloadKindMergeRequest    PayloadKind = "merge_request"
	PayloadKindRequeueRequest  PayloadKind = "requeue_request"

	// Response payloads.
	PayloadKindQuestionResponse PayloadKind = "question_response"
	PayloadKindApprovalResponse PayloadKind = "approval_response"
	PayloadKindMergeResponse    PayloadKind = "merge_response"
	PayloadKindRequeueResponse  PayloadKind = "requeue_response"

	// Legacy message type payloads.
	PayloadKindStory    PayloadKind = "story"
	PayloadKindSpec     PayloadKind = "spec"
	PayloadKindError    PayloadKind = "error"
	PayloadKindShutdown PayloadKind = "shutdown"

	// Generic key-value payloads for miscellaneous data.
	PayloadKindGeneric PayloadKind = "generic"
)

// MessagePayload represents a typed, discriminated union payload for agent messages.
// It forces proper serialization/deserialization and prevents silent type assertion failures.
//
// The discriminated union pattern ensures:
//  1. Sender must specify the Kind when creating payload
//  2. Receiver must deserialize using the correct Extract method
//  3. Mismatches produce explicit errors, not silent failures
type MessagePayload struct {
	Kind PayloadKind     `json:"kind"` // Discriminator field
	Data json.RawMessage `json:"data"` // Lazily unmarshaled payload data
}

// Question request/response payloads

// NewQuestionRequestPayload creates a payload for question requests.
func NewQuestionRequestPayload(data *QuestionRequestPayload) *MessagePayload {
	raw, _ := json.Marshal(data) // Struct marshaling should never fail
	return &MessagePayload{
		Kind: PayloadKindQuestionRequest,
		Data: raw,
	}
}

// ExtractQuestionRequest extracts and validates a question request payload.
func (p *MessagePayload) ExtractQuestionRequest() (*QuestionRequestPayload, error) {
	if p.Kind != PayloadKindQuestionRequest {
		return nil, fmt.Errorf("expected question_request payload, got %s", p.Kind)
	}
	var result QuestionRequestPayload
	if err := json.Unmarshal(p.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal question request: %w", err)
	}
	return &result, nil
}

// NewQuestionResponsePayload creates a payload for question responses.
func NewQuestionResponsePayload(data *QuestionResponsePayload) *MessagePayload {
	raw, _ := json.Marshal(data)
	return &MessagePayload{
		Kind: PayloadKindQuestionResponse,
		Data: raw,
	}
}

// ExtractQuestionResponse extracts and validates a question response payload.
func (p *MessagePayload) ExtractQuestionResponse() (*QuestionResponsePayload, error) {
	if p.Kind != PayloadKindQuestionResponse {
		return nil, fmt.Errorf("expected question_response payload, got %s", p.Kind)
	}
	var result QuestionResponsePayload
	if err := json.Unmarshal(p.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal question response: %w", err)
	}
	return &result, nil
}

// Approval request/response payloads

// NewApprovalRequestPayload creates a payload for approval requests.
func NewApprovalRequestPayload(data *ApprovalRequestPayload) *MessagePayload {
	raw, _ := json.Marshal(data)
	return &MessagePayload{
		Kind: PayloadKindApprovalRequest,
		Data: raw,
	}
}

// ExtractApprovalRequest extracts and validates an approval request payload.
func (p *MessagePayload) ExtractApprovalRequest() (*ApprovalRequestPayload, error) {
	if p.Kind != PayloadKindApprovalRequest {
		return nil, fmt.Errorf("expected approval_request payload, got %s", p.Kind)
	}
	var result ApprovalRequestPayload
	if err := json.Unmarshal(p.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal approval request: %w", err)
	}
	return &result, nil
}

// NewApprovalResponsePayload creates a payload for approval responses using ApprovalResult.
func NewApprovalResponsePayload(data *ApprovalResult) *MessagePayload {
	raw, _ := json.Marshal(data)
	return &MessagePayload{
		Kind: PayloadKindApprovalResponse,
		Data: raw,
	}
}

// ExtractApprovalResponse extracts and validates an approval response payload.
func (p *MessagePayload) ExtractApprovalResponse() (*ApprovalResult, error) {
	if p.Kind != PayloadKindApprovalResponse {
		return nil, fmt.Errorf("expected approval_response payload, got %s", p.Kind)
	}
	var result ApprovalResult
	if err := json.Unmarshal(p.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal approval response: %w", err)
	}
	return &result, nil
}

// Merge request/response payloads

// NewMergeRequestPayload creates a payload for merge requests.
func NewMergeRequestPayload(data *MergeRequestPayload) *MessagePayload {
	raw, _ := json.Marshal(data)
	return &MessagePayload{
		Kind: PayloadKindMergeRequest,
		Data: raw,
	}
}

// ExtractMergeRequest extracts and validates a merge request payload.
func (p *MessagePayload) ExtractMergeRequest() (*MergeRequestPayload, error) {
	if p.Kind != PayloadKindMergeRequest {
		return nil, fmt.Errorf("expected merge_request payload, got %s", p.Kind)
	}
	var result MergeRequestPayload
	if err := json.Unmarshal(p.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal merge request: %w", err)
	}
	return &result, nil
}

// NewMergeResponsePayload creates a payload for merge responses.
func NewMergeResponsePayload(data *MergeResponsePayload) *MessagePayload {
	raw, _ := json.Marshal(data)
	return &MessagePayload{
		Kind: PayloadKindMergeResponse,
		Data: raw,
	}
}

// ExtractMergeResponse extracts and validates a merge response payload.
func (p *MessagePayload) ExtractMergeResponse() (*MergeResponsePayload, error) {
	if p.Kind != PayloadKindMergeResponse {
		return nil, fmt.Errorf("expected merge_response payload, got %s", p.Kind)
	}
	var result MergeResponsePayload
	if err := json.Unmarshal(p.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal merge response: %w", err)
	}
	return &result, nil
}

// Requeue request/response payloads

// NewRequeueRequestPayload creates a payload for requeue requests.
func NewRequeueRequestPayload(data *RequeueRequestPayload) *MessagePayload {
	raw, _ := json.Marshal(data)
	return &MessagePayload{
		Kind: PayloadKindRequeueRequest,
		Data: raw,
	}
}

// ExtractRequeueRequest extracts and validates a requeue request payload.
func (p *MessagePayload) ExtractRequeueRequest() (*RequeueRequestPayload, error) {
	if p.Kind != PayloadKindRequeueRequest {
		return nil, fmt.Errorf("expected requeue_request payload, got %s", p.Kind)
	}
	var result RequeueRequestPayload
	if err := json.Unmarshal(p.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal requeue request: %w", err)
	}
	return &result, nil
}

// NewRequeueResponsePayload creates a payload for requeue responses.
func NewRequeueResponsePayload(data *RequeueResponsePayload) *MessagePayload {
	raw, _ := json.Marshal(data)
	return &MessagePayload{
		Kind: PayloadKindRequeueResponse,
		Data: raw,
	}
}

// ExtractRequeueResponse extracts and validates a requeue response payload.
func (p *MessagePayload) ExtractRequeueResponse() (*RequeueResponsePayload, error) {
	if p.Kind != PayloadKindRequeueResponse {
		return nil, fmt.Errorf("expected requeue_response payload, got %s", p.Kind)
	}
	var result RequeueResponsePayload
	if err := json.Unmarshal(p.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal requeue response: %w", err)
	}
	return &result, nil
}

// Generic and legacy payloads

// NewGenericPayload creates a generic key-value payload for simple messages (story, spec, error, etc).
func NewGenericPayload(kind PayloadKind, data map[string]any) *MessagePayload {
	raw, _ := json.Marshal(data)
	return &MessagePayload{
		Kind: kind,
		Data: raw,
	}
}

// ExtractGeneric extracts a generic payload as a map.
func (p *MessagePayload) ExtractGeneric() (map[string]any, error) {
	var result map[string]any
	if err := json.Unmarshal(p.Data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal generic payload: %w", err)
	}
	return result, nil
}

// NewShutdownPayload creates an empty payload for shutdown messages.
func NewShutdownPayload() *MessagePayload {
	return &MessagePayload{
		Kind: PayloadKindShutdown,
		Data: json.RawMessage("{}"),
	}
}

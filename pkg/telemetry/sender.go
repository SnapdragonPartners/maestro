// Package telemetry sends structured failure data to the maestro-issues service.
// Reports are sent at session shutdown (and retried on next startup for crashed sessions).
// All data is sanitized before transmission — secrets stripped, paths normalized.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"orchestrator/pkg/issueservice"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/utils"
	"orchestrator/pkg/version"
)

const (
	// maxFailureEntries caps the number of failure records per report.
	maxFailureEntries = 100
	// maxReportBytes caps the total serialized report size.
	maxReportBytes = 256 * 1024 // 256 KB
	// sendTimeout is the HTTP timeout for sending telemetry.
	sendTimeout = 10 * time.Second
	// maxExplanationLen caps sanitized explanation text.
	maxExplanationLen = 2000
	// maxEvidenceSnippetLen caps sanitized evidence snippet text.
	maxEvidenceSnippetLen = 1000
	// maxEvidenceEntries caps the number of evidence entries per failure.
	maxEvidenceEntries = 10
)

// Report is the JSON payload sent to POST /api/v1/telemetry.
//
//nolint:govet // fieldalignment: JSON field ordering is more readable than optimal alignment
type Report struct {
	InstallationID string                     `json:"installation_id"`
	Signature      string                     `json:"signature"`
	MaestroVersion string                     `json:"maestro_version"`
	SessionID      string                     `json:"session_id"`
	Summary        persistence.SessionSummary `json:"session_summary"`
	Failures       []FailureEntry             `json:"failures"`
	Truncated      bool                       `json:"truncated,omitempty"`
	OverflowCounts map[string]int             `json:"overflow_counts,omitempty"` // kind→count of dropped entries
}

// FailureEntry is the per-failure data in a telemetry report.
//
//nolint:govet // fieldalignment: JSON field ordering is more readable than optimal alignment
type FailureEntry struct {
	ID                string          `json:"id"`
	Kind              string          `json:"kind"`
	Source            string          `json:"source,omitempty"`
	ScopeGuess        string          `json:"scope_guess,omitempty"`
	ResolvedScope     string          `json:"resolved_scope,omitempty"`
	FailedState       string          `json:"failed_state,omitempty"`
	ToolName          string          `json:"tool_name,omitempty"`
	Action            string          `json:"action,omitempty"`
	ResolutionStatus  string          `json:"resolution_status,omitempty"`
	ResolutionOutcome string          `json:"resolution_outcome,omitempty"`
	Explanation       string          `json:"explanation"`
	Evidence          []EvidenceEntry `json:"evidence,omitempty"`
	Model             string          `json:"model,omitempty"`
	Provider          string          `json:"provider,omitempty"`
}

// EvidenceEntry is a sanitized evidence artifact.
type EvidenceEntry struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	Snippet string `json:"snippet,omitempty"`
}

// BuildReport creates a telemetry report from failure records and session data.
// Applies sanitization, caps entry count and total size.
func BuildReport(installationID, sessionID string, summary *persistence.SessionSummary, records []*persistence.FailureRecord) *Report {
	report := &Report{
		InstallationID: installationID,
		Signature:      issueservice.ComputeHMAC(installationID),
		MaestroVersion: version.Version,
		SessionID:      sessionID,
		Summary:        *summary,
		Failures:       make([]FailureEntry, 0, min(len(records), maxFailureEntries)),
	}

	// Track overflow by kind for dropped entries
	overflow := make(map[string]int)

	for i, r := range records {
		if i >= maxFailureEntries {
			overflow[r.Kind]++
			continue
		}

		entry := FailureEntry{
			ID:                r.ID,
			Kind:              r.Kind,
			Source:            r.Source,
			ScopeGuess:        r.ScopeGuess,
			ResolvedScope:     r.ResolvedScope,
			FailedState:       r.FailedState,
			ToolName:          r.ToolName,
			Action:            r.Action,
			ResolutionStatus:  r.ResolutionStatus,
			ResolutionOutcome: r.ResolutionOutcome,
			Explanation:       utils.SanitizeString(r.Explanation, maxExplanationLen),
			Model:             r.Model,
			Provider:          r.Provider,
		}

		// Parse and sanitize evidence (stored as JSON in the DB)
		if r.Evidence != "" {
			entry.Evidence = sanitizeEvidence(r.Evidence)
		}

		report.Failures = append(report.Failures, entry)
	}

	// Check total size and trim failures until under limit (can trim to 0)
	if data, err := json.Marshal(report); err == nil && len(data) > maxReportBytes {
		for len(report.Failures) > 0 {
			last := report.Failures[len(report.Failures)-1]
			overflow[last.Kind]++
			report.Failures = report.Failures[:len(report.Failures)-1]
			if data, err = json.Marshal(report); err == nil && len(data) <= maxReportBytes {
				break
			}
		}
	}

	if len(overflow) > 0 {
		report.Truncated = true
		report.OverflowCounts = overflow
	}

	return report
}

// sanitizeEvidence parses the JSON evidence blob from the DB and sanitizes each entry.
func sanitizeEvidence(evidenceJSON string) []EvidenceEntry {
	type rawEvidence struct {
		Kind    string `json:"kind"`
		Summary string `json:"summary"`
		Snippet string `json:"snippet"`
	}

	var raw []rawEvidence
	if err := json.Unmarshal([]byte(evidenceJSON), &raw); err != nil {
		return nil
	}

	// Cap evidence entries to keep payload size bounded
	if len(raw) > maxEvidenceEntries {
		raw = raw[:maxEvidenceEntries]
	}

	entries := make([]EvidenceEntry, 0, len(raw))
	for i := range raw {
		entries = append(entries, EvidenceEntry{
			Kind:    raw[i].Kind,
			Summary: utils.SanitizeString(raw[i].Summary, 500),
			Snippet: utils.SanitizeString(raw[i].Snippet, maxEvidenceSnippetLen),
		})
	}
	return entries
}

// SendReport posts the telemetry report to the maestro-issues service.
// Returns nil on success. Errors are informational — callers should log but not fail.
func SendReport(ctx context.Context, report *Report) error {
	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal telemetry report: %w", err)
	}

	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	url := issueservice.BaseURL() + "/api/v1/telemetry"
	req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create telemetry request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send telemetry: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telemetry service returned %d", resp.StatusCode)
	}

	return nil
}

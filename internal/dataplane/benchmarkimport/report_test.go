package benchmarkimport_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/benchmarkimport"
	"orchestrator/internal/dataplane/registry"
)

// reportContract returns the registered validator and extractor for
// benchmark.suite_report.
//
// Reached through the REGISTRY rather than called directly, because the
// registry is what the seam and acceptance consult. A test that called the
// functions would pass even if the type registered neither, which is the
// one failure that matters most here: acceptance reads a missing extractor
// as "this type carries no evidence", requires exactly zero pins, and
// refuses every fully-pinned report the importer writes.
func reportContract(t *testing.T) (registry.Validator, registry.Extractor) {
	t.Helper()
	entry, registered := benchmarkimport.RegistryEntries()[benchmarkimport.TypeSuiteReport]
	if !registered {
		t.Fatal("benchmark.suite_report is not registered")
	}
	if entry.Category != registry.CategoryManagement {
		t.Errorf("benchmark.suite_report is category %q; only Management artifacts may hold a pin",
			entry.Category)
	}
	validator, present := entry.Validators[benchmarkimport.PayloadVersion]
	if !present {
		t.Fatalf("no validator for version %d", benchmarkimport.PayloadVersion)
	}
	extractor, present := entry.Extractors[benchmarkimport.PayloadVersion]
	if !present {
		t.Fatalf("no extractor for version %d: acceptance would require zero pins and refuse every "+
			"report this importer writes", benchmarkimport.PayloadVersion)
	}
	return validator, extractor
}

// sampleDigest is a well-formed 64-hex content digest.
const sampleDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// validReport is the shape assembly produces: one terminal suite, one
// attempt, one evidence file.
func validReport(t *testing.T) benchmarkimport.SuiteReportPayload {
	t.Helper()
	return benchmarkimport.SuiteReportPayload{
		SuiteRunID: "golden-all-probe",
		Manifest: benchmarkimport.Manifest{
			SuiteRunID:    "golden-all-probe",
			StopReason:    "completed",
			SchemaVersion: benchmarkimport.ManifestSchemaVersion,
			Attempts: []benchmarkimport.ManifestAttempt{{
				Story: "story-a", Config: "config", Status: "completed",
				RunID: "story-a--config--r1--aaaa1111", Repeat: 1,
			}},
		},
		Attempts: []benchmarkimport.ReportAttempt{{
			RunID:               "story-a--config--r1--aaaa1111",
			StoryID:             "story-a",
			ConfigName:          "config",
			Verdict:             "accepted",
			RecordDigest:        sampleDigest,
			RunRecordArtifactID: uuid.Must(uuid.NewV7()).String(),
			Evidence: []benchmarkimport.ReportEvidence{{
				Path: "pr.json", Digest: sampleDigest, MediaType: "application/json",
				AttachmentID: uuid.Must(uuid.NewV7()).String(), SizeBytes: 12,
			}},
		}},
	}
}

// encode marshals a payload for the validator.
func encode(t *testing.T, payload any) []byte {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return raw
}

// asMap round-trips a payload into a map so a test can break one field
// without the Go type refusing to express it.
func asMap(t *testing.T, payload benchmarkimport.SuiteReportPayload) map[string]any {
	t.Helper()
	var generic map[string]any
	if err := json.Unmarshal(encode(t, payload), &generic); err != nil {
		t.Fatalf("decode to map: %v", err)
	}
	return generic
}

// attemptsIn reads the payload's attempt list out of a generic map.
func attemptsIn(t *testing.T, payload map[string]any) []any {
	t.Helper()
	attempts, ok := payload["attempts"].([]any)
	if !ok {
		t.Fatalf("attempts are %T, not a list", payload["attempts"])
	}
	return attempts
}

// firstAttempt is the one attempt of the control payload.
func firstAttempt(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	attempt, ok := attemptsIn(t, payload)[0].(map[string]any)
	if !ok {
		t.Fatal("the first attempt is not an object")
	}
	return attempt
}

// TestTheControlReportValidates is the control. Every rejection below is one
// mutation away from this, so without it they could all be failing for an
// unrelated reason.
func TestTheControlReportValidates(t *testing.T) {
	validator, _ := reportContract(t)
	if err := validator.Validate(encode(t, validReport(t))); err != nil {
		t.Fatalf("the control report was refused: %v", err)
	}
}

// Each rule, broken independently.
func TestTheReportValidatorRefusesWhatItMustNotHold(t *testing.T) {
	validator, _ := reportContract(t)

	cases := []struct {
		name   string
		damage func(t *testing.T, payload map[string]any)
		want   string
	}{
		{"unknown field", func(_ *testing.T, payload map[string]any) {
			// A field this build cannot interpret is one written by a schema
			// this build does not read; dropping it silently would let a
			// reader summarize an artifact while ignoring part of what it says.
			payload["conclusion"] = "looks good"
		}, "conclusion"},

		{"malformed suite id", func(_ *testing.T, payload map[string]any) {
			payload["suite_run_id"] = "Golden Probe"
		}, "suite_run_id"},

		{"manifest names another suite", func(t *testing.T, payload map[string]any) {
			manifest, ok := payload["manifest"].(map[string]any)
			if !ok {
				t.Fatal("manifest is not an object")
			}
			manifest["suite_run_id"] = "some-other-suite"
		}, "quotes a manifest for"},

		{"a suite still running", func(t *testing.T, payload map[string]any) {
			// Only a terminal suite gets a report. A report over a running
			// suite is a claim about a thing still happening, and the
			// manifest it quotes will legitimately differ by the time
			// anyone reads it.
			manifest, ok := payload["manifest"].(map[string]any)
			if !ok {
				t.Fatal("manifest is not an object")
			}
			manifest["stop_reason"] = "running"
		}, "only a terminal suite"},

		{"unknown verdict", func(t *testing.T, payload map[string]any) {
			firstAttempt(t, payload)["verdict"] = "probably fine"
		}, "verdict"},

		{"failure kind outside the vocabulary", func(t *testing.T, payload map[string]any) {
			firstAttempt(t, payload)["failure_kind"] = "vibes"
		}, "failure_kind"},

		{"record digest that is not a digest", func(t *testing.T, payload map[string]any) {
			firstAttempt(t, payload)["record_digest"] = "sha256:" + sampleDigest
		}, "record_digest"},

		{"the nil artifact id", func(t *testing.T, payload map[string]any) {
			// It parses cleanly and names nothing, which is exactly why it
			// has to be refused rather than left to the foreign key.
			firstAttempt(t, payload)["run_record_artifact_id"] = uuid.Nil.String()
		}, "names nothing"},

		{"an evidence path that climbs out", func(t *testing.T, payload map[string]any) {
			attempt := firstAttempt(t, payload)
			evidence, ok := attempt["evidence"].([]any)
			if !ok {
				t.Fatal("evidence is not a list")
			}
			file, ok := evidence[0].(map[string]any)
			if !ok {
				t.Fatal("evidence entry is not an object")
			}
			file["path"] = "../../etc/passwd"
		}, "clean relative path"},

		{"a negative evidence size", func(t *testing.T, payload map[string]any) {
			attempt := firstAttempt(t, payload)
			evidence, ok := attempt["evidence"].([]any)
			if !ok {
				t.Fatal("evidence is not a list")
			}
			file, ok := evidence[0].(map[string]any)
			if !ok {
				t.Fatal("evidence entry is not an object")
			}
			file["size_bytes"] = -1
		}, "size"},

		{"a skip reason outside the vocabulary", func(t *testing.T, payload map[string]any) {
			// Closed, because this field is read by grouping — how much
			// evidence is the cap costing us? — and free text answers
			// nothing a query can ask.
			firstAttempt(t, payload)["skipped_evidence"] = []any{
				map[string]any{"path": "big.log", "reason": "seemed large"},
			}
		}, "reason"},

		{"one attempt listed twice", func(t *testing.T, payload map[string]any) {
			// Two entries for one attempt double-count every verdict the
			// report is read for, and offer the same pin twice under a set
			// comparison that cannot see the difference.
			attempts := attemptsIn(t, payload)
			payload["attempts"] = []any{attempts[0], attempts[0]}
		}, "appears twice"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			payload := asMap(t, validReport(t))
			testCase.damage(t, payload)
			err := validator.Validate(encode(t, payload))
			if err == nil {
				t.Fatalf("the validator accepted a report with %s", testCase.name)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("the refusal does not name %q: %v", testCase.want, err)
			}
		})
	}
}

// One attachment may not be claimed by two attempts.
//
// The pin set is compared as a SET, so an attachment named twice collapses
// to one member: a payload naming it under two attempts describes evidence
// the pins cannot distinguish and an attempt whose evidence is really
// another's.
func TestOneAttachmentCannotBeClaimedByTwoAttempts(t *testing.T) {
	validator, _ := reportContract(t)
	report := validReport(t)
	second := report.Attempts[0]
	second.RunID = "story-a--config--r2--bbbb2222"
	second.RunRecordArtifactID = uuid.Must(uuid.NewV7()).String()
	// Same attachment id, deliberately.
	report.Attempts = append(report.Attempts, second)

	err := validator.Validate(encode(t, report))
	if err == nil {
		t.Fatal("two attempts claimed one attachment and the report validated")
	}
	if !strings.Contains(err.Error(), "more than once") {
		t.Errorf("the refusal does not name the repeated attachment: %v", err)
	}
}

// The extractor names every run record and every attachment, and nothing
// else.
func TestTheExtractorNamesEveryRecordAndAttachment(t *testing.T) {
	_, extractor := reportContract(t)
	report := validReport(t)
	second := benchmarkimport.ReportAttempt{
		RunID: "story-a--config--r2--bbbb2222", StoryID: "story-a", ConfigName: "config",
		Verdict: "failed", FailureKind: "checks-failed",
		RecordDigest:        sampleDigest,
		RunRecordArtifactID: uuid.Must(uuid.NewV7()).String(),
		// No evidence: an attempt whose directory was pruned still has a
		// record to pin, and contributes exactly one reference.
	}
	report.Attempts = append(report.Attempts, second)

	references, err := extractor.References(encode(t, report))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	audits, attachments := 0, 0
	for _, reference := range references {
		switch {
		case reference.AuditArtifactID != nil && reference.AttachmentID != nil:
			t.Error("a reference names both an Audit artifact and an attachment; the schema's arc is exclusive")
		case reference.AuditArtifactID != nil:
			audits++
		case reference.AttachmentID != nil:
			attachments++
		default:
			t.Error("a reference names nothing")
		}
	}
	if audits != 2 {
		t.Errorf("the extractor names %d run records, want 2", audits)
	}
	if attachments != 1 {
		t.Errorf("the extractor names %d attachments, want 1", attachments)
	}
}

// An attempt with no evidence still contributes its record, and a report
// with no attempts at all names nothing.
//
// The empty case matters: a terminal suite whose every cell was skipped is
// a real outcome, and an extractor that errored on it would make that suite
// unreportable.
func TestAnEmptyReportNamesNothing(t *testing.T) {
	validator, extractor := reportContract(t)
	report := validReport(t)
	report.Attempts = nil
	report.Manifest.Attempts = []benchmarkimport.ManifestAttempt{{
		Story: "story-a", Config: "config", Status: "skipped", Repeat: 1,
	}}

	if err := validator.Validate(encode(t, report)); err != nil {
		t.Fatalf("a terminal suite with no completed attempts was refused: %v", err)
	}
	references, err := extractor.References(encode(t, report))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(references) != 0 {
		t.Errorf("an empty report names %d references, want 0", len(references))
	}
}

// The extractor refuses a payload the validator would refuse, rather than
// returning a partial set.
//
// Acceptance runs the extractor over stored bytes, and a set that silently
// dropped an unparseable reference would be a set the reviewer never saw —
// which is the one thing the expected set exists to be.
func TestTheExtractorRefusesAnUnreadableReference(t *testing.T) {
	_, extractor := reportContract(t)
	payload := asMap(t, validReport(t))
	firstAttempt(t, payload)["run_record_artifact_id"] = "not-a-uuid"

	if _, err := extractor.References(encode(t, payload)); err == nil {
		t.Fatal("the extractor accepted a reference it could not parse")
	}
}

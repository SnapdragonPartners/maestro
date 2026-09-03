package work

import (
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/registry"
)

// TestRegistryEntriesConstruct: the registrations pass the registry's own
// construction checks, are all Management, and carry no extractors.
func TestRegistryEntriesConstruct(t *testing.T) {
	entries := RegistryEntries()
	if _, err := registry.New(entries); err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	for artifactType, entry := range entries {
		if entry.Category != registry.CategoryManagement {
			t.Errorf("%s is %s; every governing record is reviewed work product", artifactType, entry.Category)
		}
		if entry.CurrentVersion != PayloadVersion {
			t.Errorf("%s writes at %d, want %d", artifactType, entry.CurrentVersion, PayloadVersion)
		}
		if len(entry.Extractors) != 0 {
			t.Errorf("%s registers an extractor, but names no evidence", artifactType)
		}
	}
	for _, want := range []registry.Type{TypeEpicRecord, TypeStoryRecord, TypeStoryCompletion} {
		if _, ok := entries[want]; !ok {
			t.Errorf("%s is not registered", want)
		}
	}
	if len(entries) != 3 {
		t.Fatalf("%d entries; item 3 registers exactly three (the Feature record is item 11's)", len(entries))
	}
}

// Each validator's table: the one minimal valid payload, and every way it
// can be wrong that the validator is responsible for.
func TestValidators(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	cases := []struct {
		name     string
		validate func([]byte) error
		payload  string
		want     string // "" = valid; otherwise a fragment of the error
	}{
		{"epic minimal", ValidateEpicRecord, `{"intent":"ship it","mode":"factory"}`, ""},
		{"epic workbench", ValidateEpicRecord, `{"intent":"ship it","mode":"workbench"}`, ""},
		{"epic no intent", ValidateEpicRecord, `{"mode":"factory"}`, "intent is required"},
		{"epic blank intent", ValidateEpicRecord, `{"intent":"  ","mode":"factory"}`, "intent is required"},
		{"epic no mode", ValidateEpicRecord, `{"intent":"x"}`, "mode"},
		{"epic bad mode", ValidateEpicRecord, `{"intent":"x","mode":"auto"}`, `mode "auto"`},
		{"epic title duplicated", ValidateEpicRecord, `{"intent":"x","mode":"factory","title":"t"}`, "unknown field"},
		{"epic repository duplicated", ValidateEpicRecord, `{"intent":"x","mode":"factory","repository":"r"}`, "unknown field"},
		{"epic not an object", ValidateEpicRecord, `[]`, "invalid work payload"},
		{"epic trailing", ValidateEpicRecord, `{"intent":"x","mode":"factory"} {}`, "trailing"},

		{"story minimal", ValidateStoryRecord, `{"intent":"add the flag"}`, ""},
		{"story no intent", ValidateStoryRecord, `{}`, "intent is required"},
		{"story title duplicated", ValidateStoryRecord, `{"intent":"x","title":"t"}`, "unknown field"},
		{"story empty", ValidateStoryRecord, ``, "invalid work payload"},

		{"completion minimal", ValidateStoryCompletion, `{"head_commit":"` + sha + `"}`, ""},
		{"completion short sha", ValidateStoryCompletion, `{"head_commit":"abc123"}`, "not a 40-hex"},
		{"completion uppercase", ValidateStoryCompletion, `{"head_commit":"` + strings.ToUpper(sha) + `"}`, "not a 40-hex"},
		{"completion missing", ValidateStoryCompletion, `{}`, "not a 40-hex"},
		{"completion branch duplicated", ValidateStoryCompletion, `{"head_commit":"` + sha + `","branch":"b"}`, "unknown field"},
		{"completion merge commit is audit data", ValidateStoryCompletion, `{"head_commit":"` + sha + `","merge_commit":"` + sha + `"}`, "unknown field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.validate([]byte(tc.payload))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("valid payload refused: %v", err)
			case tc.want != "" && err == nil:
				t.Fatalf("invalid payload accepted")
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("error %q lacks %q", err, tc.want)
			case tc.want != "" && !errors.Is(err, ErrInvalidPayload):
				t.Fatalf("refusal does not wrap ErrInvalidPayload: %v", err)
			}
		})
	}
}

package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const hex64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestParsePromptSelectorAdmitsExactlyTwoShapes(t *testing.T) {
	id := uuid.New()
	byContent, err := ParsePromptSelector([]byte(`{"content_id": "` + id.String() + `"}`))
	if err != nil || byContent.ContentID == nil || *byContent.ContentID != id || byContent.Identity != nil {
		t.Fatalf("by content: %+v, %v", byContent, err)
	}
	byIdentity, err := ParsePromptSelector([]byte(` {"identity": "pack-jcs-sha256-v1:` + hex64 + `"} `))
	if err != nil || byIdentity.Identity == nil || byIdentity.Identity.Digest != hex64 ||
		byIdentity.Identity.Scheme != PromptSchemePackJCS || byIdentity.ContentID != nil {
		t.Fatalf("by identity: %+v, %v", byIdentity, err)
	}

	// Round trip through the marshaller the writer verbs use.
	for _, selector := range []PromptSelector{byContent, byIdentity} {
		raw, err := json.Marshal(selector)
		if err != nil {
			t.Fatal(err)
		}
		back, err := ParsePromptSelector(raw)
		if err != nil || back.String() != selector.String() {
			t.Fatalf("round trip of %s: %s, %v", selector, back, err)
		}
	}
}

// TestParsePromptSelectorRefusesANameByName is design D7's "refuses a bare
// name explicitly, with an error saying that a name is a label".
func TestParsePromptSelectorRefusesANameByName(t *testing.T) {
	for _, raw := range []string{`"default"`, `{"name": "default"}`, ` "default" `} {
		_, err := ParsePromptSelector([]byte(raw))
		if !errors.Is(err, ErrPromptSelectorIsAName) {
			t.Errorf("%s: err = %v, want ErrPromptSelectorIsAName", raw, err)
		}
		if err != nil && !strings.Contains(err.Error(), "a name is a label") {
			t.Errorf("%s: the refusal does not teach the rule: %v", raw, err)
		}
	}
}

func TestParsePromptSelectorRefusesEverythingElse(t *testing.T) {
	for name, raw := range map[string]string{
		"empty object":         `{}`,
		"both":                 `{"content_id": "` + uuid.New().String() + `", "identity": "pack-jcs-sha256-v1:` + hex64 + `"}`,
		"unknown field":        `{"pack": "x"}`,
		"non-uuid content id":  `{"content_id": "not-a-uuid"}`,
		"legacy scheme":        `{"identity": "v1-manifest-sha256:sha256:` + hex64 + `"}`,
		"bare hex":             `{"identity": "` + hex64 + `"}`,
		"uppercase hex":        `{"identity": "pack-jcs-sha256-v1:` + strings.ToUpper(hex64) + `"}`,
		"third scheme":         `{"identity": "pack-jcs-sha256-v2:` + hex64 + `"}`,
		"null":                 `null`,
		"array":                `[]`,
		"number":               `1`,
		"trailing garbage":     `{"content_id": "` + uuid.New().String() + `"} x`,
		"explicit null fields": `{"content_id": null}`,
	} {
		if _, err := ParsePromptSelector([]byte(raw)); err == nil {
			t.Errorf("%s: %s parsed", name, raw)
		} else if errors.Is(err, ErrPromptSelectorIsAName) {
			t.Errorf("%s: refused as a name, which it is not: %v", name, err)
		}
	}
	if err := PromptSelectorSchema().Validate([]byte(`{}`)); err == nil {
		t.Error("the schema admits an empty selector")
	}
}

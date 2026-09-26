package orchestrator_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/orchestrator"
)

// TestKeysRegistersTheSelectorAndNothingElse: the registry's first live
// reader (item 4 design, D7), at all three lineage levels, with a schema
// that refuses a name by name. Exactly one key -- a second would be a guess
// about a future caller until it has a reader.
func TestKeysRegistersTheSelectorAndNothingElse(t *testing.T) {
	keys := orchestrator.Keys()
	if got := keys.Keys(); !slices.Equal(got, []configkeys.Key{store.PromptPackKey}) {
		t.Fatalf("Keys() registers %v, want exactly [%s]", got, store.PromptPackKey)
	}
	registration, err := keys.Lookup(store.PromptPackKey)
	if err != nil {
		t.Fatal(err)
	}
	want := []configkeys.Scope{configkeys.ScopeOrganization, configkeys.ScopeProduct, configkeys.ScopeRepository}
	if !slices.Equal(registration.PermittedScopes, want) || registration.Sensitive {
		t.Fatalf("registration = %+v, want scopes %v and not sensitive", registration, want)
	}

	valid := []byte(`{"identity": "pack-jcs-sha256-v1:` + strings.Repeat("a", 64) + `"}`)
	for _, scope := range want {
		if writeErr := keys.ValidateWrite(store.PromptPackKey, scope, valid); writeErr != nil {
			t.Errorf("a valid selector at %s: %v", scope, writeErr)
		}
	}
	err = keys.ValidateWrite(store.PromptPackKey, configkeys.ScopeOrganization, []byte(`"default"`))
	if !errors.Is(err, configkeys.ErrInvalidValue) || !errors.Is(err, store.ErrPromptSelectorIsAName) {
		t.Fatalf("a name as the selector: %v, want ErrInvalidValue wrapping ErrPromptSelectorIsAName", err)
	}
}

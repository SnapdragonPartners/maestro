//go:build integration

package plane_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/plane"
	"orchestrator/internal/dataplane/planetest"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/store"
)

// TestOpenThreadsTheCallerKeyRegistry: a key the caller registered is
// writable through the seam, and one it did not is refused.
//
// THE MUTANT this must kill: drop postgres.WithConfigKeys(c.Keys) from
// plane.Open. The store then keeps its fail-closed empty registry and the
// REGISTERED write is refused — which is exactly the state every composer
// was in before item 3, when Composition had no Keys field at all.
func TestOpenThreadsTheCallerKeyRegistry(t *testing.T) {
	ctx := context.Background()
	const registered configkeys.Key = "test.threaded"
	keys, err := configkeys.New(map[configkeys.Key]configkeys.Entry{
		registered: {
			Schema:          configkeys.ValidatorFunc(func([]byte) error { return nil }),
			PermittedScopes: []configkeys.Scope{configkeys.ScopeOrganization},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	types, err := registry.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := planetest.Blob(t, "keys")
	seam, err := plane.Open(ctx, plane.Composition{
		DSN: planetest.DSN(t, "keys"), Objects: blob, RootKey: planetest.RootKey(t), Types: types, Keys: keys,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(seam.Close)

	org, err := seam.BootstrapOrganization(ctx, store.BootstrapOrganizationInput{Slug: "keys", DisplayName: "Keys"})
	if err != nil {
		t.Fatal(err)
	}
	scope := store.ConfigScope{Type: configkeys.ScopeOrganization, ID: org.Record.OrganizationID}

	if _, writeErr := seam.CreateConfigurationRecord(ctx, store.CreateConfigurationRecordInput{
		OrganizationID: org.Record.OrganizationID, Scope: scope, Key: registered, Value: json.RawMessage(`{"ok":true}`),
	}); writeErr != nil {
		t.Fatalf("a key the caller registered was refused: %v", writeErr)
	}

	_, writeErr := seam.CreateConfigurationRecord(ctx, store.CreateConfigurationRecordInput{
		OrganizationID: org.Record.OrganizationID, Scope: scope, Key: "test.unregistered", Value: json.RawMessage(`{}`),
	})
	if !errors.Is(writeErr, configkeys.ErrUnknownKey) {
		t.Fatalf("an unregistered key was not refused as unknown: %v", writeErr)
	}
}

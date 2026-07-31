//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
)

// The configuration family, behaviourally, against the real Postgres
// (item 7 design, D1 and D7).

const (
	// retriesKey is settable at every level, so the precedence tests have
	// something to set at all three.
	retriesKey configkeys.Key = "forge.retries"
	// orgOnlyKey is organization-wide by nature, which is the whole point
	// of PermittedScopes existing.
	orgOnlyKey configkeys.Key = "forge.tenant-mode"
	// tokenKey is credential-shaped and belongs in the vault.
	tokenKey configkeys.Key = "forge.token"
)

// objectSchema admits a JSON object and refuses anything else, so the
// schema-refusal test fails for a stated reason rather than by accident.
var objectSchema = configkeys.ValidatorFunc(func(value []byte) error {
	var decoded map[string]any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return errors.New("value must be a JSON object")
	}
	return nil
})

// configKeys is the test's own vocabulary. The package ships none, so every
// test that writes configuration declares what it is writing.
func configKeys(t *testing.T) *configkeys.Registry {
	t.Helper()
	built, err := configkeys.New(map[configkeys.Key]configkeys.Entry{
		retriesKey: {
			Schema: objectSchema,
			PermittedScopes: []configkeys.Scope{
				configkeys.ScopeOrganization, configkeys.ScopeProduct, configkeys.ScopeRepository,
			},
		},
		orgOnlyKey: {
			Schema:          objectSchema,
			PermittedScopes: []configkeys.Scope{configkeys.ScopeOrganization},
		},
		tokenKey: {Sensitive: true},
	})
	if err != nil {
		t.Fatalf("build config key registry: %v", err)
	}
	return built
}

// configStore rebuilds the seam over the fixture's pool with a configuration
// vocabulary. newFixture deliberately builds one WITHOUT keys, which is the
// state every other suite runs in.
func configStore(t *testing.T, f *fixture) *postgres.Store {
	t.Helper()
	built, err := postgres.New(f.pool, testRegistry(t), f.blob, postgres.WithConfigKeys(configKeys(t)))
	if err != nil {
		t.Fatalf("store with config keys: %v", err)
	}
	return built
}

// setConfig writes one value at one scope and fails the test if it does not
// land, so the precedence tests read as a list of what was set.
func setConfig(
	t *testing.T, s *postgres.Store, org uuid.UUID, key configkeys.Key,
	scope store.ConfigScope, value string,
) *store.ConfigurationRecord {
	t.Helper()
	record, err := s.CreateConfigurationRecord(context.Background(), store.CreateConfigurationRecordInput{
		OrganizationID: org,
		Key:            key,
		Scope:          scope,
		Value:          json.RawMessage(value),
	})
	if err != nil {
		t.Fatalf("set %s at %s: %v", key, scope.Type, err)
	}
	return record
}

// countConfigRows reads the table directly, for the assertions about what
// did NOT land. A refusal that still wrote a row is the failure these
// tests exist to catch, and the seam cannot be asked about it.
func countConfigRows(t *testing.T, f *fixture, org uuid.UUID, key configkeys.Key) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM configuration_records WHERE organization_id = $1 AND key = $2`,
		org, string(key)).Scan(&count); err != nil {
		t.Fatalf("count configuration rows: %v", err)
	}
	return count
}

// sameJSON compares two JSON values by DECODING them.
//
// The value column is jsonb, so Postgres reparses what was written and
// renders it in its own normal form: {"n":1} comes back as {"n": 1}. A byte
// comparison against the literal a test wrote is a comparison against a
// format the column never promised to keep, and it fails for a reason that
// has nothing to do with the behaviour under test.
func sameJSON(t *testing.T, got json.RawMessage, want string) bool {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode stored value %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode expected value %s: %v", want, err)
	}
	return reflect.DeepEqual(gotValue, wantValue)
}

// TestConfigurationResolvesMostSpecificFirst walks the three levels down,
// removing one at a time. Seeding all three and asserting once would pass
// for a resolver that always returned the repository row.
func TestConfigurationResolvesMostSpecificFirst(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	s := configStore(t, f)
	ctx := context.Background()

	orgScope := store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID}
	productScope := store.ConfigScope{Type: configkeys.ScopeProduct, ID: f.product}
	repoScope := store.ConfigScope{Type: configkeys.ScopeRepository, ID: f.repository}

	setConfig(t, s, f.organizationID, retriesKey, orgScope, `{"n":1}`)
	product := setConfig(t, s, f.organizationID, retriesKey, productScope, `{"n":2}`)
	repository := setConfig(t, s, f.organizationID, retriesKey, repoScope, `{"n":3}`)

	steps := []struct {
		name      string
		wantValue string
		wantScope configkeys.Scope
		remove    *store.ConfigurationRecord
	}{
		{name: "repository wins over both", wantValue: `{"n":3}`, wantScope: configkeys.ScopeRepository, remove: repository},
		{name: "product wins once the repository is gone", wantValue: `{"n":2}`, wantScope: configkeys.ScopeProduct, remove: product},
		{name: "organization answers last", wantValue: `{"n":1}`, wantScope: configkeys.ScopeOrganization},
	}

	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			got, err := s.ResolveConfiguration(ctx, f.organizationID, f.repository, retriesKey)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if !sameJSON(t, got.Value, step.wantValue) {
				t.Errorf("value = %s, want %s", got.Value, step.wantValue)
			}
			// The level is asserted, not just the value. A caller that
			// cannot tell which level answered cannot explain why a value
			// is what it is, which is what resolution is for.
			if got.Scope.Type != step.wantScope {
				t.Errorf("scope = %q, want %q", got.Scope.Type, step.wantScope)
			}
		})
		if step.remove != nil {
			if err := s.DeleteConfigurationRecord(ctx, f.organizationID, step.remove.ID, step.remove.Version); err != nil {
				t.Fatalf("delete %s override: %v", step.remove.Scope.Type, err)
			}
		}
	}
}

// TestConfigurationIdentifiersAreUUIDv7 extends the identifier rule to this
// family. uuid.New() returns v4, which is what this used to call: v7 is
// time-ordered, so keys cluster by creation time instead of scattering
// across the index.
//
// Asserted rather than assumed, because the two constructors differ by one
// word at one call site and a v4 id is indistinguishable from a v7 in every
// behavioural test — the whole suite passed with the wrong one.
func TestConfigurationIdentifiersAreUUIDv7(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	s := configStore(t, f)

	record := setConfig(t, s, f.organizationID, retriesKey,
		store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID}, `{"n":1}`)
	if got := record.ID.Version(); got != 7 {
		t.Errorf("configuration record id is UUID version %d, want 7", got)
	}
}

// TestConfigurationResolvesNothingWhenUnset keeps "no record" distinct from
// "a record holding an empty value". A resolver returning a zero record for
// both would make an unset key indistinguishable from one set to null.
func TestConfigurationResolvesNothingWhenUnset(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	s := configStore(t, f)
	ctx := context.Background()

	_, err := s.ResolveConfiguration(ctx, f.organizationID, f.repository, retriesKey)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("resolve of an unset key returned %v, want ErrNotFound", err)
	}

	setConfig(t, s, f.organizationID, retriesKey,
		store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID}, `{}`)

	got, err := s.ResolveConfiguration(ctx, f.organizationID, f.repository, retriesKey)
	if err != nil {
		t.Fatalf("resolve of a key set to an empty object: %v", err)
	}
	if !sameJSON(t, got.Value, `{}`) {
		t.Errorf("value = %s, want an empty object", got.Value)
	}
}

// TestConfigurationFollowsOnlyThePrimaryProduct is the case that makes the
// lineage a chain rather than a graph (ADR 0018).
//
// A repository can belong to several Products through product_repositories.
// If resolution consulted membership, this repository would have two
// competing parents with no defined precedence, and the answer would depend
// on which row the planner reached first.
func TestConfigurationFollowsOnlyThePrimaryProduct(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	s := configStore(t, f)
	ctx := context.Background()

	// A second Product that also contains this repository, but is not its
	// primary.
	secondary := uuid.New()
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO products (product_id, organization_id, user_id, slug, display_name)
		 VALUES ($1, $2, $3, 'p2', 'P2')`, secondary, f.organizationID, f.userID); err != nil {
		t.Fatalf("insert secondary product: %v", err)
	}
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO product_repositories (product_id, repository_id, organization_id)
		 VALUES ($1, $2, $3)`, secondary, f.repository, f.organizationID); err != nil {
		t.Fatalf("insert secondary membership: %v", err)
	}

	setConfig(t, s, f.organizationID, retriesKey,
		store.ConfigScope{Type: configkeys.ScopeProduct, ID: secondary}, `{"n":99}`)
	setConfig(t, s, f.organizationID, retriesKey,
		store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID}, `{"n":1}`)

	got, err := s.ResolveConfiguration(ctx, f.organizationID, f.repository, retriesKey)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !sameJSON(t, got.Value, `{"n":1}`) {
		t.Errorf("value = %s, want the organization's {\"n\":1}; a value set on a NON-primary "+
			"Product answered, which makes the lineage a graph", got.Value)
	}
	if got.Scope.Type != configkeys.ScopeOrganization {
		t.Errorf("scope = %q, want organization", got.Scope.Type)
	}
}

// TestConfigurationRefusesUngovernedWrites is the registry acting as a gate
// rather than as documentation.
//
// Every case asserts that NOTHING LANDED, read from the table rather than
// from the seam. A refusal reported after a successful insert is the defect
// these cases exist to catch, and the returned error looks identical either
// way.
func TestConfigurationRefusesUngovernedWrites(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	s := configStore(t, f)
	ctx := context.Background()

	tests := []struct {
		name  string
		key   configkeys.Key
		scope store.ConfigScope
		value string
		want  error
		// wantMentions is checked for the sensitive case, where the whole
		// value of the refusal is that it says where the value DOES go.
		wantMentions string
	}{
		{
			name:  "unregistered key",
			key:   "forge.unheard-of",
			scope: store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID},
			value: `{}`,
			want:  configkeys.ErrUnknownKey,
		},
		{
			name:  "value failing the registered schema",
			key:   retriesKey,
			scope: store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID},
			value: `"not an object"`,
			want:  configkeys.ErrInvalidValue,
		},
		{
			name:  "scope the key does not permit",
			key:   orgOnlyKey,
			scope: store.ConfigScope{Type: configkeys.ScopeRepository, ID: f.repository},
			value: `{}`,
			want:  configkeys.ErrScopeNotPermitted,
		},
		{
			name:         "credential-shaped key",
			key:          tokenKey,
			scope:        store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID},
			value:        `"ghp_secret"`,
			want:         configkeys.ErrSensitiveKey,
			wantMentions: "vault",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.CreateConfigurationRecord(ctx, store.CreateConfigurationRecordInput{
				OrganizationID: f.organizationID,
				Key:            tc.key,
				Scope:          tc.scope,
				Value:          json.RawMessage(tc.value),
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("create returned %v, want %v", err, tc.want)
			}
			if tc.wantMentions != "" && !strings.Contains(err.Error(), tc.wantMentions) {
				t.Errorf("error %q does not say where the value belongs (want it to mention %q)",
					err, tc.wantMentions)
			}
			if got := countConfigRows(t, f, f.organizationID, tc.key); got != 0 {
				t.Errorf("%d row(s) landed for a refused write; validation must happen "+
					"before the statement, not after it", got)
			}
		})
	}
}

// TestConfigurationUpdateValidatesAgainstTheStoredKey covers the half a
// creation-only guard misses.
//
// An update names a RECORD, not a key. If the new value were validated
// against anything other than the row's own key -- or against nothing --
// then a value the schema forbids reaches the column through the update
// path while the create path holds the line.
func TestConfigurationUpdateValidatesAgainstTheStoredKey(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	s := configStore(t, f)
	ctx := context.Background()

	record := setConfig(t, s, f.organizationID, retriesKey,
		store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID}, `{"n":1}`)

	_, err := s.UpdateConfigurationRecord(ctx, f.organizationID, record.ID, record.Version,
		json.RawMessage(`"not an object"`))
	if !errors.Is(err, configkeys.ErrInvalidValue) {
		t.Fatalf("update returned %v, want ErrInvalidValue", err)
	}

	// The stored value is unchanged: the refusal happened before the write.
	current, err := s.GetConfigurationRecord(ctx, f.organizationID, record.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !sameJSON(t, current.Value, `{"n":1}`) {
		t.Errorf("value = %s, want the original; a refused update still wrote", current.Value)
	}
	if current.Version != record.Version {
		t.Errorf("version = %d, want %d; a refused update still bumped the version",
			current.Version, record.Version)
	}
}

// TestConfigurationUpdateIsConditional pins ADR 0027's rule that shared
// mutable state is not resolved by last-writer-wins.
func TestConfigurationUpdateIsConditional(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	s := configStore(t, f)
	ctx := context.Background()

	record := setConfig(t, s, f.organizationID, retriesKey,
		store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID}, `{"n":1}`)

	updated, err := s.UpdateConfigurationRecord(ctx, f.organizationID, record.ID, record.Version,
		json.RawMessage(`{"n":2}`))
	if err != nil {
		t.Fatalf("first update: %v", err)
	}
	if updated.Version != record.Version+1 {
		t.Errorf("version = %d, want %d", updated.Version, record.Version+1)
	}

	// The second writer read the same version the first did.
	_, err = s.UpdateConfigurationRecord(ctx, f.organizationID, record.ID, record.Version,
		json.RawMessage(`{"n":3}`))
	if !errors.Is(err, store.ErrConfigurationConflict) {
		t.Fatalf("stale update returned %v, want ErrConfigurationConflict", err)
	}

	current, err := s.GetConfigurationRecord(ctx, f.organizationID, record.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !sameJSON(t, current.Value, `{"n":2}`) {
		t.Errorf("value = %s, want the first writer's; the loser overwrote it", current.Value)
	}
}

// TestStaleWriterIsToldTheVersionMoved pins the ORDER of the mutation's two
// judgements, which is invisible to a test whose write fails only one rule.
//
// A stale caller proposing a value the schema forbids fails both: its
// version has moved AND its value is invalid. Validating first answers the
// question it did not ask. The value never mattered — the record it was
// written against had already changed — and "your value is malformed" sends
// the caller to fix a value it would then re-submit against the same stale
// version, failing again for the reason it should have been given first.
func TestStaleWriterIsToldTheVersionMoved(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	s := configStore(t, f)
	ctx := context.Background()

	record := setConfig(t, s, f.organizationID, retriesKey,
		store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID}, `{"n":1}`)

	// Somebody else moves the record on.
	if _, err := s.UpdateConfigurationRecord(ctx, f.organizationID, record.ID, record.Version,
		json.RawMessage(`{"n":2}`)); err != nil {
		t.Fatalf("first update: %v", err)
	}

	// The stale writer's value is ALSO invalid, so both rules refuse it.
	_, err := s.UpdateConfigurationRecord(ctx, f.organizationID, record.ID, record.Version,
		json.RawMessage(`"not an object"`))
	if errors.Is(err, configkeys.ErrInvalidValue) {
		t.Fatalf("stale update was refused for its VALUE (%v); existence and version are settled "+
			"first, so the caller must be told its write did not apply", err)
	}
	if !errors.Is(err, store.ErrConfigurationConflict) {
		t.Fatalf("stale update returned %v, want ErrConfigurationConflict", err)
	}
}

// blockingConfigKeys builds a vocabulary whose schema PAUSES inside
// validation when it sees a sentinel value.
//
// The pause is the barrier. Validation runs after the update has taken the
// row lock and before it writes, so a test that blocks there holds the lock
// open at exactly the instant another mutation must be made to contend with
// it. Releasing two goroutines at once does not do that: they are free to
// run one after the other, and a test built on that timing reports a winner
// and a loser whether or not any lock was ever taken.
func blockingConfigKeys(t *testing.T, entered chan<- struct{}, release <-chan struct{}) *configkeys.Registry {
	t.Helper()
	var once sync.Once
	schema := configkeys.ValidatorFunc(func(value []byte) error {
		if bytes.Contains(value, []byte(`"pause"`)) {
			once.Do(func() { close(entered) })
			<-release
		}
		return objectSchema.Validate(value)
	})
	built, err := configkeys.New(map[configkeys.Key]configkeys.Entry{
		retriesKey: {
			Schema: schema,
			PermittedScopes: []configkeys.Scope{
				configkeys.ScopeOrganization, configkeys.ScopeProduct, configkeys.ScopeRepository,
			},
		},
	})
	if err != nil {
		t.Fatalf("build blocking config key registry: %v", err)
	}
	return built
}

// TestDeleteWaitsForTheUpdatesRowLock forces the overlap the lock exists for,
// and distinguishes the two outcomes by their ERROR rather than by timing.
//
// The update takes the row lock, then parks inside its validator. The delete
// then arrives holding the same version the update read.
//
//   - WITH the lock, the delete blocks at its own SELECT ... FOR UPDATE. It
//     wakes after the update commits, reads version 2, and reports a
//     CONFLICT — the honest answer, since the value it meant to remove is
//     not the value that is there.
//   - WITHOUT it, the delete reads version 1 unimpeded and classifies itself
//     as free to proceed. Its DELETE then blocks on the write lock anyway,
//     wakes to find no row at version 1, and returns an INVARIANT failure:
//     the seam said the row was deletable and the SQL guard disagreed.
//
// So the assertion is that the loser is refused for the reason it actually
// lost, and specifically that the backstop was never the thing that caught
// it. A test asserting only "one won and one lost" passes in both worlds.
func TestDeleteWaitsForTheUpdatesRowLock(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	ctx := context.Background()

	// releaseNow is idempotent and registered as a cleanup, because the
	// parked goroutine holds a pooled CONNECTION. pgxpool.Close blocks until
	// every connection is returned, and the fixture closes the pool in its
	// own cleanup — so a test that fails before releasing the validator does
	// not fail, it HANGS, and takes the rest of the package with it. Learned
	// the hard way: the first mutation run of this test deadlocked instead
	// of reporting.
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseNow := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseNow)

	blocking, err := postgres.New(f.pool, testRegistry(t), f.blob,
		postgres.WithConfigKeys(blockingConfigKeys(t, entered, release)))
	if err != nil {
		t.Fatalf("store with blocking keys: %v", err)
	}

	record := setConfig(t, blocking, f.organizationID, retriesKey,
		store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID}, `{"n":1}`)

	updated := make(chan error, 1)
	go func() {
		_, updateErr := blocking.UpdateConfigurationRecord(ctx, f.organizationID, record.ID,
			record.Version, json.RawMessage(`{"n":2,"why":"pause"}`))
		updated <- updateErr
	}()

	// The update now holds the row lock and is parked in its validator.
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the update never reached its validator")
	}

	deleted := make(chan error, 1)
	go func() {
		deleted <- blocking.DeleteConfigurationRecord(ctx, f.organizationID, record.ID, record.Version)
	}()

	// The delete must be waiting on a lock rather than already finished.
	// Asserted against Postgres, not inferred from a sleep.
	waitForBlockedBackend(t, f)
	select {
	case err := <-deleted:
		t.Fatalf("the delete completed while the update held the row lock (%v); nothing serialised them", err)
	default:
	}

	releaseNow()

	if updateErr := <-updated; updateErr != nil {
		t.Fatalf("update: %v", updateErr)
	}
	deleteErr := <-deleted
	if errors.Is(deleteErr, store.ErrInvariant) {
		t.Fatalf("the delete classified itself as writable and was caught by the SQL backstop (%v); "+
			"it read the row without waiting for the lock", deleteErr)
	}
	if !errors.Is(deleteErr, store.ErrConfigurationConflict) {
		t.Fatalf("delete returned %v, want ErrConfigurationConflict", deleteErr)
	}

	// The update's value survived: the delete did not erase a write it
	// never saw.
	current, err := blocking.GetConfigurationRecord(ctx, f.organizationID, record.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !sameJSON(t, current.Value, `{"n":2,"why":"pause"}`) {
		t.Errorf("value = %s, want the update's", current.Value)
	}
}

// waitForBlockedBackend waits until some backend on this database is stuck
// on a lock, so the test proceeds on an observed state rather than a sleep.
func waitForBlockedBackend(t *testing.T, f *fixture) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked int
		if err := f.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_locks WHERE NOT granted`).Scan(&blocked); err != nil {
			t.Fatalf("read pg_locks: %v", err)
		}
		if blocked > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no backend ever blocked on a lock; the delete was never made to contend for the row")
}

// TestConcurrentConfigurationUpdatesSerialize runs the race rather than
// simulating it with two sequential calls.
func TestConcurrentConfigurationUpdatesSerialize(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	s := configStore(t, f)
	ctx := context.Background()

	record := setConfig(t, s, f.organizationID, retriesKey,
		store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID}, `{"n":0}`)

	const writers = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		conflicts int
		other     []error
	)
	start := make(chan struct{})

	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.UpdateConfigurationRecord(ctx, f.organizationID, record.ID, record.Version,
				json.RawMessage(`{"n":`+string(rune('1'+i))+`}`))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, store.ErrConfigurationConflict):
				conflicts++
			default:
				other = append(other, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected errors: %v", other)
	}
	if succeeded != 1 {
		t.Errorf("%d writers succeeded, want exactly 1; the rest overwrote each other", succeeded)
	}
	if conflicts != writers-1 {
		t.Errorf("%d conflicts, want %d", conflicts, writers-1)
	}
}

// TestConfigurationDeleteRestoresInheritance is why deletion exists at all.
func TestConfigurationDeleteRestoresInheritance(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	s := configStore(t, f)
	ctx := context.Background()

	setConfig(t, s, f.organizationID, retriesKey,
		store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID}, `{"n":1}`)
	override := setConfig(t, s, f.organizationID, retriesKey,
		store.ConfigScope{Type: configkeys.ScopeRepository, ID: f.repository}, `{"n":3}`)

	got, err := s.ResolveConfiguration(ctx, f.organizationID, f.repository, retriesKey)
	if err != nil {
		t.Fatalf("resolve with override: %v", err)
	}
	if !sameJSON(t, got.Value, `{"n":3}`) {
		t.Fatalf("value = %s, want the override", got.Value)
	}

	if err := s.DeleteConfigurationRecord(ctx, f.organizationID, override.ID, override.Version); err != nil {
		t.Fatalf("delete override: %v", err)
	}

	got, err = s.ResolveConfiguration(ctx, f.organizationID, f.repository, retriesKey)
	if err != nil {
		t.Fatalf("resolve after delete: %v", err)
	}
	if !sameJSON(t, got.Value, `{"n":1}`) || got.Scope.Type != configkeys.ScopeOrganization {
		t.Errorf("value = %s at %q, want the organization's inherited value; removing an override "+
			"is the only way back to inheritance", got.Value, got.Scope.Type)
	}
}

// TestConfigurationDeleteIsConditional stops an operator removing what they
// believe is stale from erasing a value somebody set a moment earlier.
func TestConfigurationDeleteIsConditional(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	s := configStore(t, f)
	ctx := context.Background()

	record := setConfig(t, s, f.organizationID, retriesKey,
		store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID}, `{"n":1}`)

	if _, err := s.UpdateConfigurationRecord(ctx, f.organizationID, record.ID, record.Version,
		json.RawMessage(`{"n":2}`)); err != nil {
		t.Fatalf("update: %v", err)
	}

	// The deleter is working from the version it read before the update.
	err := s.DeleteConfigurationRecord(ctx, f.organizationID, record.ID, record.Version)
	if !errors.Is(err, store.ErrConfigurationConflict) {
		t.Fatalf("stale delete returned %v, want ErrConfigurationConflict", err)
	}
	if got := countConfigRows(t, f, f.organizationID, retriesKey); got != 1 {
		t.Errorf("%d rows remain, want 1; the stale delete erased a value it never saw", got)
	}
}

// TestConfigurationDeleteDistinguishesMissingFromConflict keeps the two
// zero-row outcomes apart. A caller re-reads on a conflict and gives up on a
// missing record, so collapsing them makes the right response unknowable.
func TestConfigurationDeleteDistinguishesMissingFromConflict(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	s := configStore(t, f)
	ctx := context.Background()

	if err := s.DeleteConfigurationRecord(ctx, f.organizationID, uuid.New(), 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete of an absent record returned %v, want ErrNotFound", err)
	}

	record := setConfig(t, s, f.organizationID, retriesKey,
		store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID}, `{"n":1}`)
	err := s.DeleteConfigurationRecord(ctx, f.organizationID, record.ID, record.Version+7)
	if !errors.Is(err, store.ErrConfigurationConflict) {
		t.Errorf("delete at a wrong version returned %v, want ErrConfigurationConflict", err)
	}
}

// TestConfigurationIsTenantIsolated asserts the boundary every read on this
// seam holds: another organization's record is NOT FOUND, not forbidden, so
// a caller cannot probe for identifiers it should not know.
func TestConfigurationIsTenantIsolated(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	s := configStore(t, f)
	ctx := context.Background()

	record := setConfig(t, s, f.organizationID, retriesKey,
		store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID}, `{"n":1}`)

	if _, err := s.GetConfigurationRecord(ctx, f.otherOrgID, record.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cross-tenant get returned %v, want ErrNotFound", err)
	}
	if _, err := s.ResolveConfiguration(ctx, f.otherOrgID, f.repository, retriesKey); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cross-tenant resolve returned %v, want ErrNotFound", err)
	}
	if _, err := s.UpdateConfigurationRecord(ctx, f.otherOrgID, record.ID, record.Version,
		json.RawMessage(`{"n":9}`)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cross-tenant update returned %v, want ErrNotFound", err)
	}
	if err := s.DeleteConfigurationRecord(ctx, f.otherOrgID, record.ID, record.Version); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cross-tenant delete returned %v, want ErrNotFound", err)
	}

	// The row is untouched by any of it.
	current, err := s.GetConfigurationRecord(ctx, f.organizationID, record.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !sameJSON(t, current.Value, `{"n":1}`) || current.Version != record.Version {
		t.Errorf("record changed under a cross-tenant write: %s at version %d",
			current.Value, current.Version)
	}
}

// TestStoreWithoutConfigKeysRefusesEveryWrite pins the default the other
// suites run under. An empty registry is the honest starting state for a
// package that ships no vocabulary, and it must refuse rather than admit.
func TestStoreWithoutConfigKeysRefusesEveryWrite(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)
	ctx := context.Background()

	_, err := f.store.CreateConfigurationRecord(ctx, store.CreateConfigurationRecordInput{
		OrganizationID: f.organizationID,
		Key:            retriesKey,
		Scope:          store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID},
		Value:          json.RawMessage(`{"n":1}`),
	})
	if !errors.Is(err, configkeys.ErrUnknownKey) {
		t.Fatalf("create against a store with no vocabulary returned %v, want ErrUnknownKey", err)
	}
	if got := countConfigRows(t, f, f.organizationID, retriesKey); got != 0 {
		t.Errorf("%d row(s) landed", got)
	}
}

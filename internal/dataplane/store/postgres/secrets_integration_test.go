//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/secret"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/dataplane/store/postgres"
)

// The secrets vault, behaviourally, against the real Postgres
// (item 7 design, D2, D5 and D7).

const forgeToken = "forge.token"

// vault is a fixture with a root key, a second user in the same
// organization, and the lineage the ladder walks.
type vault struct {
	*fixture
	store *postgres.Store
	// second is another member of the SAME organization. Every ownership
	// property is invisible with one user: a cross-user filter cannot be
	// seen by a test that has nobody to be filtered from.
	second uuid.UUID
}

func newVault(t *testing.T) *vault {
	t.Helper()
	f := newFixture(t)
	f.seedLineage(t)

	// A real key file in a temp root, not a constant: the provider is part
	// of what is under test, and a stub one would prove the envelope works
	// with key material nothing in production produces.
	built, err := postgres.New(f.pool, testRegistry(t), f.blob,
		postgres.WithRootKey(secret.KeyFile(t.TempDir(), secret.MayCreate)))
	if err != nil {
		t.Fatalf("store with root key: %v", err)
	}

	second := uuid.New()
	if _, err := f.pool.Exec(context.Background(),
		`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1, $2, $3, $4)`,
		second, f.organizationID, "second", "Second"); err != nil {
		t.Fatalf("insert second user: %v", err)
	}

	return &vault{fixture: f, store: built, second: second}
}

func (v *vault) orgScope() store.ConfigScope {
	return store.ConfigScope{Type: configkeys.ScopeOrganization, ID: v.organizationID}
}

func (v *vault) productScope() store.ConfigScope {
	return store.ConfigScope{Type: configkeys.ScopeProduct, ID: v.product}
}

func (v *vault) repoScope() store.ConfigScope {
	return store.ConfigScope{Type: configkeys.ScopeRepository, ID: v.repository}
}

// put writes one secret and fails the test if it does not land.
func (v *vault) put(
	t *testing.T, actor uuid.UUID, scope store.ConfigScope, shared bool, plaintext string,
) *store.Secret {
	t.Helper()
	created, err := v.store.CreateSecret(context.Background(), store.CreateSecretInput{
		OrganizationID: v.organizationID,
		Name:           forgeToken,
		Scope:          scope,
		ActingUserID:   actor,
		Shared:         shared,
		Plaintext:      []byte(plaintext),
	})
	if err != nil {
		t.Fatalf("create %s secret at %s: %v", ownershipWord(shared), scope.Type, err)
	}
	return created
}

func ownershipWord(shared bool) string {
	if shared {
		return "shared"
	}
	return "individual"
}

// reveal decrypts and returns the plaintext, failing the test on error.
func (v *vault) reveal(t *testing.T, actor, secretID uuid.UUID) string {
	t.Helper()
	value, err := v.store.RevealSecret(context.Background(), v.organizationID, secretID, actor)
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	return string(value.Reveal())
}

// storedEnvelope reads the raw envelope columns, for the assertions the seam
// deliberately cannot answer — it never returns a ciphertext.
func (v *vault) storedEnvelope(t *testing.T, secretID uuid.UUID) (nonce, ciphertext []byte) {
	t.Helper()
	if err := v.pool.QueryRow(context.Background(),
		`SELECT nonce, ciphertext FROM secrets WHERE secret_id = $1`, secretID).
		Scan(&nonce, &ciphertext); err != nil {
		t.Fatalf("read stored envelope: %v", err)
	}
	return nonce, ciphertext
}

// TestSecretLadderWalksAllSixSteps is the marquee ordering property.
//
// All six rows are seeded, then removed one at a time from the top. Seeding
// all six and asserting once would pass for a resolver that always returned
// the first row it found; walking down forces every rung to answer in turn.
//
// The order is specificity OUTER, ownership INNER. The alternative —
// ownership outer — is also consistent with "prefer the individual", and the
// two disagree: it would reach past a repository deploy key for a personal
// organization-wide token that may have no access to that repository at all.
// A credential for the wrong resource does not work no matter whose it is.
func TestSecretLadderWalksAllSixSteps(t *testing.T) {
	v := newVault(t)
	ctx := context.Background()

	// Seeded bottom-up so each rung's plaintext names its own position.
	rungs := []struct {
		name      string
		scope     store.ConfigScope
		shared    bool
		plaintext string
		wantScope configkeys.Scope
		wantOwn   store.SecretOwnership
	}{
		{"1 repository / caller", v.repoScope(), false, "repo-mine", configkeys.ScopeRepository, store.SecretIndividual},
		{"2 repository / shared", v.repoScope(), true, "repo-shared", configkeys.ScopeRepository, store.SecretShared},
		{"3 product / caller", v.productScope(), false, "product-mine", configkeys.ScopeProduct, store.SecretIndividual},
		{"4 product / shared", v.productScope(), true, "product-shared", configkeys.ScopeProduct, store.SecretShared},
		{"5 organization / caller", v.orgScope(), false, "org-mine", configkeys.ScopeOrganization, store.SecretIndividual},
		{"6 organization / shared", v.orgScope(), true, "org-shared", configkeys.ScopeOrganization, store.SecretShared},
	}

	seeded := make([]*store.Secret, len(rungs))
	for i, rung := range rungs {
		seeded[i] = v.put(t, v.userID, rung.scope, rung.shared, rung.plaintext)
	}

	for i, rung := range rungs {
		t.Run(rung.name, func(t *testing.T) {
			got, err := v.store.ResolveSecret(ctx, v.organizationID, v.repository, v.userID, forgeToken)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got.ID != seeded[i].ID {
				t.Fatalf("rung %q did not answer; got the secret at %s/%s instead",
					rung.name, got.Scope.Type, got.Ownership)
			}
			if got.Scope.Type != rung.wantScope || got.Ownership != rung.wantOwn {
				t.Errorf("answered at %s/%s, want %s/%s",
					got.Scope.Type, got.Ownership, rung.wantScope, rung.wantOwn)
			}
			// Attribution is only useful if the value matches the label.
			if plaintext := v.reveal(t, v.userID, got.ID); plaintext != rung.plaintext {
				t.Errorf("plaintext = %q, want %q", plaintext, rung.plaintext)
			}
		})

		if err := v.store.DeleteSecret(ctx, v.organizationID, seeded[i].ID, v.userID, seeded[i].Version); err != nil {
			t.Fatalf("remove rung %q: %v", rung.name, err)
		}
	}

	if _, err := v.store.ResolveSecret(ctx, v.organizationID, v.repository, v.userID, forgeToken); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("resolve with every rung removed returned %v, want ErrNotFound", err)
	}
}

// TestSecretRoundTripsWithoutStoringPlaintext is the base guarantee.
func TestSecretRoundTripsWithoutStoringPlaintext(t *testing.T) {
	v := newVault(t)
	const plaintext = "ghp_a_real_looking_token_value"

	created := v.put(t, v.userID, v.orgScope(), false, plaintext)
	if got := v.reveal(t, v.userID, created.ID); got != plaintext {
		t.Fatalf("revealed %q, want %q", got, plaintext)
	}
	if created.ID.Version() != 7 {
		t.Errorf("secret id is UUID version %d, want 7", created.ID.Version())
	}

	// The stored column is neither the plaintext nor a prefix of it. The
	// prefix half matters: a "cipher" that concatenated a header onto the
	// plaintext would pass an equality check.
	_, ciphertext := v.storedEnvelope(t, created.ID)
	if bytes.Equal(ciphertext, []byte(plaintext)) {
		t.Error("the ciphertext column holds the plaintext")
	}
	if bytes.Contains(ciphertext, []byte(plaintext)) {
		t.Error("the ciphertext column CONTAINS the plaintext")
	}
}

// TestIdenticalPlaintextsGetDifferentCiphertexts pins that the envelope is
// not deterministic. Two equal tokens producing equal ciphertexts would let
// anyone with table access learn that two users hold the same credential.
func TestIdenticalPlaintextsGetDifferentCiphertexts(t *testing.T) {
	v := newVault(t)
	const same = "identical-token"

	first := v.put(t, v.userID, v.orgScope(), false, same)
	second := v.put(t, v.second, v.orgScope(), false, same)

	firstNonce, firstCipher := v.storedEnvelope(t, first.ID)
	secondNonce, secondCipher := v.storedEnvelope(t, second.ID)

	if bytes.Equal(firstCipher, secondCipher) {
		t.Error("two secrets with the same plaintext have the same ciphertext")
	}
	if bytes.Equal(firstNonce, secondNonce) {
		t.Error("two secrets share a nonce")
	}
}

// TestReplaceRotatesAndUnaddressesTheOldValue covers what rotation does and,
// as importantly, states what it does not.
func TestReplaceRotatesAndUnaddressesTheOldValue(t *testing.T) {
	v := newVault(t)
	ctx := context.Background()

	created := v.put(t, v.userID, v.orgScope(), false, "old-token")
	oldNonce, oldCipher := v.storedEnvelope(t, created.ID)

	replaced, err := v.store.ReplaceSecret(ctx, v.organizationID, created.ID, v.userID,
		created.Version, []byte("new-token"))
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if replaced.Version != created.Version+1 {
		t.Errorf("version = %d, want %d", replaced.Version, created.Version+1)
	}

	newNonce, newCipher := v.storedEnvelope(t, created.ID)
	if bytes.Equal(oldNonce, newNonce) {
		t.Error("replacement reused the nonce; the version is part of the key context precisely so " +
			"every stored ciphertext has its own key")
	}
	if bytes.Equal(oldCipher, newCipher) {
		t.Error("replacement left the ciphertext unchanged")
	}

	// The vault no longer returns the old value. That is the honest
	// promise: the old ciphertext is not ERASED — it survives in the dead
	// tuple until vacuum, in the WAL, and in every prior backup, and its
	// key stays derivable because HKDF is deterministic. What ends is its
	// addressability through this seam.
	if got := v.reveal(t, v.userID, created.ID); got != "new-token" {
		t.Errorf("revealed %q after rotation, want the new value", got)
	}
}

// TestSecretMutationsAreConditional covers both write verbs against a stale
// version, and asserts the row survived.
func TestSecretMutationsAreConditional(t *testing.T) {
	v := newVault(t)
	ctx := context.Background()

	created := v.put(t, v.userID, v.orgScope(), false, "v1")
	if _, err := v.store.ReplaceSecret(ctx, v.organizationID, created.ID, v.userID,
		created.Version, []byte("v2")); err != nil {
		t.Fatalf("first replace: %v", err)
	}

	t.Run("stale replace", func(t *testing.T) {
		_, err := v.store.ReplaceSecret(ctx, v.organizationID, created.ID, v.userID,
			created.Version, []byte("v3"))
		if !errors.Is(err, store.ErrSecretConflict) {
			t.Fatalf("returned %v, want ErrSecretConflict", err)
		}
	})
	t.Run("stale delete", func(t *testing.T) {
		err := v.store.DeleteSecret(ctx, v.organizationID, created.ID, v.userID, created.Version)
		if !errors.Is(err, store.ErrSecretConflict) {
			t.Fatalf("returned %v, want ErrSecretConflict", err)
		}
	})

	if got := v.reveal(t, v.userID, created.ID); got != "v2" {
		t.Errorf("revealed %q; a stale write applied anyway", got)
	}
}

// TestConcurrentReplacementsSerialize runs the rotation race.
func TestConcurrentReplacementsSerialize(t *testing.T) {
	v := newVault(t)
	ctx := context.Background()

	created := v.put(t, v.userID, v.orgScope(), false, "v0")

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
			_, err := v.store.ReplaceSecret(ctx, v.organizationID, created.ID, v.userID,
				created.Version, fmt.Appendf(nil, "rotation-%d", i))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, store.ErrSecretConflict):
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
	if succeeded != 1 || conflicts != writers-1 {
		t.Errorf("%d succeeded and %d conflicted, want 1 and %d; a rotation was silently overwritten",
			succeeded, conflicts, writers-1)
	}
}

// TestOneUserCannotTouchAnothersSecret is asserted for all three verbs.
//
// A read-only ownership test passes with the write side wide open, which is
// the more damaging half: a caller who cannot READ a secret also cannot tell
// what they destroyed.
func TestOneUserCannotTouchAnothersSecret(t *testing.T) {
	v := newVault(t)
	ctx := context.Background()

	mine := v.put(t, v.userID, v.orgScope(), false, "mine")

	t.Run("read", func(t *testing.T) {
		if _, err := v.store.GetSecret(ctx, v.organizationID, mine.ID, v.second); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("returned %v, want ErrNotFound", err)
		}
		if _, err := v.store.RevealSecret(ctx, v.organizationID, mine.ID, v.second); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("reveal returned %v, want ErrNotFound", err)
		}
	})
	t.Run("replace", func(t *testing.T) {
		_, err := v.store.ReplaceSecret(ctx, v.organizationID, mine.ID, v.second, mine.Version, []byte("theirs"))
		if !errors.Is(err, store.ErrSecretConflict) {
			t.Errorf("returned %v, want ErrSecretConflict", err)
		}
	})
	t.Run("delete", func(t *testing.T) {
		if err := v.store.DeleteSecret(ctx, v.organizationID, mine.ID, v.second, mine.Version); !errors.Is(err, store.ErrSecretConflict) {
			t.Errorf("returned %v, want ErrSecretConflict", err)
		}
	})

	if got := v.reveal(t, v.userID, mine.ID); got != "mine" {
		t.Errorf("revealed %q; another user's write landed", got)
	}
}

// TestResolutionNeverReachesAnotherUsersSecret covers the ownership filter
// on the LADDER, which is a different statement from the one GetSecret uses
// and fails independently of it.
//
// This gap was found by mutation: weakening the resolve query's individual
// branch to a tautology left every ownership test passing, because they all
// went through GetSecret. A caller would then have resolved somebody else's
// personal token and used it, believing it was theirs — and the level and
// ownership reported back would have looked entirely ordinary.
func TestResolutionNeverReachesAnotherUsersSecret(t *testing.T) {
	v := newVault(t)
	ctx := context.Background()

	theirs := v.put(t, v.second, v.orgScope(), false, "not-yours")

	// Nothing else exists, so a resolver that ignored ownership would
	// happily return it.
	got, err := v.store.ResolveSecret(ctx, v.organizationID, v.repository, v.userID, forgeToken)
	if err == nil {
		t.Fatalf("resolved another user's individual secret (%s at %s/%s); the ownership filter "+
			"belongs in the query, where no caller can forget it", got.ID, got.Scope.Type, got.Ownership)
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("returned %v, want ErrNotFound", err)
	}

	// The owner still reaches it, so the filter is not simply refusing
	// everything — the half that makes the assertion above meaningful.
	owned, err := v.store.ResolveSecret(ctx, v.organizationID, v.repository, v.second, forgeToken)
	if err != nil {
		t.Fatalf("the owner could not resolve their own secret: %v", err)
	}
	if owned.ID != theirs.ID {
		t.Errorf("owner resolved %s, want their own %s", owned.ID, theirs.ID)
	}
}

// TestResolutionStillReachesSharedSecrets is the opposite branch, and it
// fails in the opposite direction.
//
// Without the `owner_user_id IS NULL` arm a shared credential matches
// nobody, so the vault silently stops working for exactly the secrets a team
// holds in common. That is not a security hole, which is why it would
// survive review: it is an availability defect that reports itself as "no
// such secret".
func TestResolutionStillReachesSharedSecrets(t *testing.T) {
	v := newVault(t)

	shared := v.put(t, v.userID, v.orgScope(), true, "team-token")

	for _, actor := range []struct {
		name string
		id   uuid.UUID
	}{{"creator", v.userID}, {"another member", v.second}} {
		t.Run(actor.name, func(t *testing.T) {
			got, err := v.store.ResolveSecret(context.Background(), v.organizationID,
				v.repository, actor.id, forgeToken)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got.ID != shared.ID || got.Ownership != store.SecretShared {
				t.Errorf("resolved %s (%s), want the shared secret %s", got.ID, got.Ownership, shared.ID)
			}
		})
	}
}

// TestEachUserGetsTheirOwnSlot is the case a poisoned slot would break.
func TestEachUserGetsTheirOwnSlot(t *testing.T) {
	v := newVault(t)

	mine := v.put(t, v.userID, v.orgScope(), false, "mine")
	theirs := v.put(t, v.second, v.orgScope(), false, "theirs")

	if mine.ID == theirs.ID {
		t.Fatal("both users got the same row")
	}
	if got := v.reveal(t, v.userID, mine.ID); got != "mine" {
		t.Errorf("first user revealed %q", got)
	}
	if got := v.reveal(t, v.second, theirs.ID); got != "theirs" {
		t.Errorf("second user revealed %q", got)
	}
}

// TestTwoSharedSecretsAreRefused exercises the partial unique index branch
// that a seeded owner can never reach.
//
// A plain UNIQUE over the tuple including owner_user_id does NOT do this: in
// Postgres NULL is not equal to itself, so it permits any number of shared
// secrets with the same name at the same scope — exactly the duplicates that
// make resolution non-deterministic. The test therefore creates them WITHOUT
// an owner, because that is the branch that is wrong by default.
func TestTwoSharedSecretsAreRefused(t *testing.T) {
	v := newVault(t)

	v.put(t, v.userID, v.orgScope(), true, "first-shared")

	_, err := v.store.CreateSecret(context.Background(), store.CreateSecretInput{
		OrganizationID: v.organizationID,
		Name:           forgeToken,
		Scope:          v.orgScope(),
		ActingUserID:   v.second,
		Shared:         true,
		Plaintext:      []byte("second-shared"),
	})
	if err == nil {
		t.Fatal("a second shared secret with the same name and scope was accepted; resolution " +
			"between them would be whichever row the planner reached first")
	}
}

// TestSecretCreationRequiresMembership covers the guard that only matters
// for SHARED secrets, whose null owner means nothing else on the row
// mentions the caller at all.
func TestSecretCreationRequiresMembership(t *testing.T) {
	v := newVault(t)
	outsider := uuid.New()
	if _, err := v.pool.Exec(context.Background(),
		`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1, $2, $3, $4)`,
		outsider, v.otherOrgID, "outsider", "Outsider"); err != nil {
		t.Fatalf("insert outsider: %v", err)
	}

	for _, shared := range []bool{true, false} {
		t.Run(ownershipWord(shared), func(t *testing.T) {
			_, err := v.store.CreateSecret(context.Background(), store.CreateSecretInput{
				OrganizationID: v.organizationID,
				Name:           forgeToken,
				Scope:          v.orgScope(),
				ActingUserID:   outsider,
				Shared:         shared,
				Plaintext:      []byte("smuggled"),
			})
			if err == nil {
				t.Fatal("a non-member created a secret in this organization")
			}
			if shared && !errors.Is(err, store.ErrActingUserNotAMember) {
				t.Errorf("returned %v, want ErrActingUserNotAMember", err)
			}
		})
	}

	var count int
	if err := v.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM secrets WHERE organization_id = $1`, v.organizationID).Scan(&count); err != nil {
		t.Fatalf("count secrets: %v", err)
	}
	if count != 0 {
		t.Errorf("%d secret(s) landed from a non-member", count)
	}
}

// TestSecretsAreTenantIsolated keeps another organization's secret not
// found rather than forbidden.
func TestSecretsAreTenantIsolated(t *testing.T) {
	v := newVault(t)
	ctx := context.Background()

	mine := v.put(t, v.userID, v.orgScope(), false, "mine")

	if _, err := v.store.GetSecret(ctx, v.otherOrgID, mine.ID, v.userID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cross-tenant get returned %v, want ErrNotFound", err)
	}
	if _, err := v.store.ResolveSecret(ctx, v.otherOrgID, v.repository, v.userID, forgeToken); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cross-tenant resolve returned %v, want ErrNotFound", err)
	}
}

// TestTamperedRowsFailToOpen is the AAD's whole purpose, and every case here
// mutates metadata on the row the ciphertext ALREADY belongs to.
//
// Moving a ciphertext to a different row is not tested, deliberately: a
// different secret id derives a different key, so it fails before the
// authenticated data is ever consulted. A test of that would pass with no
// AAD at all, which is exactly how the earlier version of this case managed
// to prove nothing.
func TestTamperedRowsFailToOpen(t *testing.T) {
	tests := []struct {
		name   string
		update string
	}{
		{
			name:   "owner_user_id changed",
			update: `UPDATE secrets SET owner_user_id = $2 WHERE secret_id = $1`,
		},
		{
			name:   "name changed",
			update: `UPDATE secrets SET name = 'other.token' WHERE secret_id = $1`,
		},
		{
			name:   "ciphertext byte flipped",
			update: `UPDATE secrets SET ciphertext = overlay(ciphertext placing '\x00'::bytea from 1 for 1) WHERE secret_id = $1`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := newVault(t)
			ctx := context.Background()
			created := v.put(t, v.userID, v.orgScope(), false, "tamper-me")

			args := []any{created.ID}
			actor := v.userID
			if tc.name == "owner_user_id changed" {
				args = append(args, v.second)
				actor = v.second
			}
			if _, err := v.pool.Exec(ctx, tc.update, args...); err != nil {
				t.Fatalf("tamper: %v", err)
			}

			// Read as whoever can now reach the row: the point is that the
			// row does not open, not that it is unreachable.
			_, err := v.store.RevealSecret(ctx, v.organizationID, created.ID, actor)
			if err == nil {
				t.Fatal("a tampered row decrypted; the authenticated data binds every field that " +
					"decides who may read this secret and what it is for")
			}
			if !errors.Is(err, secret.ErrDecrypt) {
				t.Errorf("returned %v, want a decryption failure", err)
			}
		})
	}
}

// TestVaultWithoutARootKeyRefuses pins the default. A store that answered
// vault operations by encrypting under a zero key would be worse than one
// that refuses.
func TestVaultWithoutARootKeyRefuses(t *testing.T) {
	f := newFixture(t)
	f.seedLineage(t)

	_, err := f.store.CreateSecret(context.Background(), store.CreateSecretInput{
		OrganizationID: f.organizationID,
		Name:           forgeToken,
		Scope:          store.ConfigScope{Type: configkeys.ScopeOrganization, ID: f.organizationID},
		ActingUserID:   f.userID,
		Plaintext:      []byte("nope"),
	})
	if !errors.Is(err, store.ErrInvariant) {
		t.Fatalf("returned %v, want a refusal naming the missing provider", err)
	}
}

//go:build integration

package migrations_test

import "testing"

// Schema rules for the configuration and secrets families (item 7, D1/D5).
//
// These were established by hand against a disposable database while the
// migration was written, which proves a schema once and protects nothing.
// They are the same cases, committed.
//
// The nonce and ciphertext here are the shortest values the envelope's own
// checks accept: twelve bytes and sixteen. Anything shorter is not a small
// secret but an envelope that cannot decrypt.
const (
	validNonce      = `\x000102030405060708090a0b`
	validCiphertext = `\x00112233445566778899aabbccddeeff`
	testScheme      = "aes-256-gcm/hkdf-sha256/v1"
)

// insertSecret writes an organization-scoped secret, letting a case vary
// only what it is testing.
func (f *fixture) insertSecret(id, name string, owner any) error {
	_, err := f.tx.Exec(
		`INSERT INTO secrets
		   (secret_id, organization_id, name, owner_user_id, scope_type,
		    scope_organization_id, scheme, nonce, ciphertext)
		 VALUES ($1,$2,$3,$4,'organization',$5,$6,$7,$8)`,
		id, f.org, name, owner, f.org, testScheme, validNonce, validCiphertext)
	return err
}

// TestSharedSecretsAreUniquePerNameAndScope is the case a single UNIQUE
// constraint over the whole tuple would silently permit.
//
// In Postgres NULL is not equal to itself, so `UNIQUE (organization_id,
// name, owner_user_id, scope_type, scope_id)` admits any number of SHARED
// secrets with one name at one scope — and resolution then returns whichever
// row the planner reached first. Two partial unique indexes are what state
// the rule honestly, and this is the half that is wrong by default.
//
// It deliberately seeds NO owner. A test that supplies one never reaches
// this branch and passes against the broken schema.
func TestSharedSecretsAreUniquePerNameAndScope(t *testing.T) {
	f := seed(t, openPlane(t))

	if err := f.insertSecret("70000000-0000-7000-8000-000000000001", "token", nil); err != nil {
		t.Fatalf("first shared secret: %v", err)
	}
	f.rejects(t, "a second shared secret with the same name and scope was accepted, so resolution "+
		"between them is whichever row the planner reaches first",
		`INSERT INTO secrets
		   (secret_id, organization_id, name, owner_user_id, scope_type,
		    scope_organization_id, scheme, nonce, ciphertext)
		 VALUES ($1,$2,'token',NULL,'organization',$3,$4,$5,$6)`,
		"70000000-0000-7000-8000-000000000002", f.org, f.org, testScheme, validNonce, validCiphertext)
}

// TestIndividualSecretsAreOnePerUser is the other half of the same rule:
// the slot is per user, so two people may hold a credential of the same
// name at the same scope, and neither may hold two.
//
// The first assertion is what a poisoned slot would break — if creation
// accepted an arbitrary owner, one user could occupy another's.
func TestIndividualSecretsAreOnePerUser(t *testing.T) {
	f := seed(t, openPlane(t))

	second := "70000000-0000-7000-8000-0000000000a1"
	if _, err := f.tx.Exec(
		`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1,$2,'u2','U2')`,
		second, f.org); err != nil {
		t.Fatalf("seed second user: %v", err)
	}

	if err := f.insertSecret("70000000-0000-7000-8000-000000000011", "token", f.user); err != nil {
		t.Fatalf("first user's secret: %v", err)
	}
	if err := f.insertSecret("70000000-0000-7000-8000-000000000012", "token", second); err != nil {
		t.Fatalf("second user's secret with the same name and scope was refused: %v", err)
	}
	f.rejects(t, "one user held two secrets of the same name at the same scope",
		`INSERT INTO secrets
		   (secret_id, organization_id, name, owner_user_id, scope_type,
		    scope_organization_id, scheme, nonce, ciphertext)
		 VALUES ($1,$2,'token',$3,'organization',$4,$5,$6,$7)`,
		"70000000-0000-7000-8000-000000000013", f.org, f.user, f.org, testScheme, validNonce, validCiphertext)
}

// TestSecretOwnerMustBelongToTheOrganization covers the composite foreign
// key. A single-column reference to users would let a secret name somebody
// from another tenant, which is the boundary this whole seam rests on.
func TestSecretOwnerMustBelongToTheOrganization(t *testing.T) {
	f := seed(t, openPlane(t))

	otherOrg := "70000000-0000-7000-8000-0000000000b1"
	otherUser := "70000000-0000-7000-8000-0000000000b2"
	if _, err := f.tx.Exec(
		`INSERT INTO organizations (organization_id, slug, display_name) VALUES ($1,'other','Other')`,
		otherOrg); err != nil {
		t.Fatalf("seed other org: %v", err)
	}
	if _, err := f.tx.Exec(
		`INSERT INTO users (user_id, organization_id, handle, display_name) VALUES ($1,$2,'ou','OU')`,
		otherUser, otherOrg); err != nil {
		t.Fatalf("seed other user: %v", err)
	}

	f.rejects(t, "a secret took its owner from another organization",
		`INSERT INTO secrets
		   (secret_id, organization_id, name, owner_user_id, scope_type,
		    scope_organization_id, scheme, nonce, ciphertext)
		 VALUES ($1,$2,'token',$3,'organization',$4,$5,$6,$7)`,
		"70000000-0000-7000-8000-0000000000b3", f.org, otherUser, f.org, testScheme, validNonce, validCiphertext)
}

// TestOrganizationScopeIsTenantBound is the check the other two scope arms
// get for free from their composite foreign keys.
//
// scope_organization_id references organizations directly, so a presence
// check alone would let a row owned by one organization be scoped to
// another — a cross-tenant record that every organization-scoped read would
// then resolve.
func TestOrganizationScopeIsTenantBound(t *testing.T) {
	f := seed(t, openPlane(t))

	otherOrg := "70000000-0000-7000-8000-0000000000c1"
	if _, err := f.tx.Exec(
		`INSERT INTO organizations (organization_id, slug, display_name) VALUES ($1,'other','Other')`,
		otherOrg); err != nil {
		t.Fatalf("seed other org: %v", err)
	}

	f.rejects(t, "a configuration record owned by one organization was scoped to another",
		`INSERT INTO configuration_records
		   (configuration_record_id, organization_id, key, scope_type, scope_organization_id, value)
		 VALUES ($1,$2,'k','organization',$3,'{}')`,
		"70000000-0000-7000-8000-0000000000c2", f.org, otherOrg)

	f.rejects(t, "a secret owned by one organization was scoped to another",
		`INSERT INTO secrets
		   (secret_id, organization_id, name, scope_type, scope_organization_id, scheme, nonce, ciphertext)
		 VALUES ($1,$2,'token','organization',$3,$4,$5,$6)`,
		"70000000-0000-7000-8000-0000000000c3", f.org, otherOrg, testScheme, validNonce, validCiphertext)
}

// TestScopeTypeMustAgreeWithItsScopeColumn stops a row claiming one level
// while carrying another's id. Every resolution query trusts scope_type, so
// a disagreeing row is read at the wrong level rather than rejected.
func TestScopeTypeMustAgreeWithItsScopeColumn(t *testing.T) {
	f := seed(t, openPlane(t))

	f.rejects(t, "a configuration record claimed product scope while carrying an organization id",
		`INSERT INTO configuration_records
		   (configuration_record_id, organization_id, key, scope_type, scope_organization_id, value)
		 VALUES ($1,$2,'k','product',$3,'{}')`,
		"70000000-0000-7000-8000-0000000000d1", f.org, f.org)

	f.rejects(t, "a secret claimed repository scope while carrying a product id",
		`INSERT INTO secrets
		   (secret_id, organization_id, name, scope_type, scope_product_id, scheme, nonce, ciphertext)
		 VALUES ($1,$2,'token','repository',$3,$4,$5,$6)`,
		"70000000-0000-7000-8000-0000000000d2", f.org, f.product, testScheme, validNonce, validCiphertext)
}

// TestEnvelopeRefusesAnUndecryptableShape covers the two length checks.
//
// The ciphertext bound is sixteen rather than one because AES-GCM emits its
// authentication tag even for empty plaintext: a shorter value is not a
// small secret, it is an envelope nothing can open, and `> 0` admits exactly
// those.
func TestEnvelopeRefusesAnUndecryptableShape(t *testing.T) {
	f := seed(t, openPlane(t))

	f.rejects(t, "a nonce that is not twelve bytes was accepted",
		`INSERT INTO secrets
		   (secret_id, organization_id, name, scope_type, scope_organization_id, scheme, nonce, ciphertext)
		 VALUES ($1,$2,'token','organization',$3,$4,'\x0001',$5)`,
		"70000000-0000-7000-8000-0000000000e1", f.org, f.org, testScheme, validCiphertext)

	f.rejects(t, "a ciphertext shorter than GCM's authentication tag was accepted",
		`INSERT INTO secrets
		   (secret_id, organization_id, name, scope_type, scope_organization_id, scheme, nonce, ciphertext)
		 VALUES ($1,$2,'token','organization',$3,$4,$5,'\x0011223344556677')`,
		"70000000-0000-7000-8000-0000000000e2", f.org, f.org, testScheme, validNonce)
}

// TestConfigurationIsOneRowPerKeyAndScope is what makes most-specific-wins
// well defined. Two rows at one level would resolve to whichever the planner
// reached first — an intermittently wrong value rather than an error.
func TestConfigurationIsOneRowPerKeyAndScope(t *testing.T) {
	f := seed(t, openPlane(t))

	if _, err := f.tx.Exec(
		`INSERT INTO configuration_records
		   (configuration_record_id, organization_id, key, scope_type, scope_repository_id, value)
		 VALUES ($1,$2,'model','repository',$3,'{"v":1}')`,
		"70000000-0000-7000-8000-0000000000f1", f.org, f.repo); err != nil {
		t.Fatalf("first record: %v", err)
	}

	// The same key at a DIFFERENT level is the whole point of the lineage.
	if _, err := f.tx.Exec(
		`INSERT INTO configuration_records
		   (configuration_record_id, organization_id, key, scope_type, scope_product_id, value)
		 VALUES ($1,$2,'model','product',$3,'{"v":2}')`,
		"70000000-0000-7000-8000-0000000000f2", f.org, f.product); err != nil {
		t.Fatalf("the same key at another level was refused: %v", err)
	}

	f.rejects(t, "two configuration records shared one key at one scope",
		`INSERT INTO configuration_records
		   (configuration_record_id, organization_id, key, scope_type, scope_repository_id, value)
		 VALUES ($1,$2,'model','repository',$3,'{"v":3}')`,
		"70000000-0000-7000-8000-0000000000f3", f.org, f.repo)
}

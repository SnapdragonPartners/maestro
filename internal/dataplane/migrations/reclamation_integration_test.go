//go:build integration

package migrations_test

import (
	"database/sql"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Migrations 000013 and 000014 add the two tables that make reclamation
// safe: staging leases and deletion claims (item 6 design, D6).
//
// Every constraint on them is load-bearing in the same direction --
// each one stops a delete being pointed at storage it was not meant to
// remove -- so each is tested by producing the row it must refuse, and by a
// positive control proving it still admits the row it must accept.
//
// The invariants, and what fails if one is absent:
//
//	a lease's key is under ITS organization's staging prefix
//	    -- otherwise cleanup, which deletes every version of the leased
//	       key, can be aimed at a digest key holding evidence, or across
//	       the tenant boundary at another organization's staging object;
//	a lease's term is non-empty
//	    -- an already-expired lease is one cleanup may act on immediately,
//	       which is the timer failure the token exists to prevent;
//	one lease per key
//	    -- two live writers on one staging key is the state a lease exists
//	       to make impossible;
//	a claim names a well-formed digest
//	    -- the same 64-hex address shape as the rest of the schema;
//	a claim names at least one id
//	    -- a claim condemning nothing still blocks the existing-object
//	       shortcut for as long as it survives;
//	no id is NULL or blank
//	    -- the reconciler would skip a NULL and clear the claim, reporting
//	       storage reclaimed that is still there; and a BLANK id names the
//	       KEY rather than a version, which on a versioned bucket writes a
//	       delete marker and reclaims nothing, or aborts every upload on a
//	       reused digest key;
//	one live claim per digest
//	    -- the row's existence is what "live" means, so two sweeps could
//	       otherwise condemn one digest concurrently.

const (
	reclamationOrg   = "'11111111-1111-4111-8111-111111111111'"
	otherOrg         = "'99999999-9999-4999-8999-999999999999'"
	reclamationOrgID = "11111111-1111-4111-8111-111111111111"
	// A digest of the right shape, so a rejection is never the digest
	// check firing by accident.
	someDigest = "'" + "aa11bb22cc33dd44ee55ff66aa77bb88cc99dd00ee11ff22aa33bb44cc55dd66" + "'"
)

// seedReclamationOrgs inserts the two organizations these rows hang off.
func seedReclamationOrgs(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO organizations (organization_id, slug, display_name)
		VALUES (` + reclamationOrg + `, 'probe', 'Probe'),
		       (` + otherOrg + `, 'other', 'Other');`); err != nil {
		t.Fatalf("seed organizations: %v", err)
	}
}

func TestReclamationConstraints(t *testing.T) {
	dsn := disposableDatabase(t)
	db := openDB(t, dsn)
	seedReclamationOrgs(t, db)

	// Written out rather than built with a helper: the difference between
	// a good row and a bad one is the whole point of each case, and a
	// helper that filled in the correct value would hide it.
	const leaseInsert = `INSERT INTO staging_leases
		(staging_lease_id, organization_id, staging_key, owner_token, expires_at) VALUES `
	const claimInsert = `INSERT INTO deletion_claims
		(deletion_claim_id, organization_id, object_digest, version_ids, upload_ids) VALUES `

	goodKey := "'staging/" + reclamationOrgID + "/upload-a'"

	cases := []struct {
		name    string
		bad     string
		good    string
		wantCon string
	}{
		{
			// The lease covers a DIGEST key. Cleanup deletes every version
			// of the key it is given, so this is a lease authorising the
			// destruction of content-addressed evidence.
			name: "lease over a digest key",
			bad: leaseInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'org/aa/bb/digest', gen_random_uuid(), now() + interval '1 hour')`,
			good: leaseInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, ` + goodKey + `, gen_random_uuid(), now() + interval '1 hour')`,
			wantCon: "staging_leases_key_scope_check",
		},
		{
			// Right prefix, WRONG organization. This is the cross-tenant
			// case, and it is why the check compares against the row's own
			// organization rather than just matching 'staging/'.
			name: "lease over another organization's staging key",
			bad: leaseInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'staging/99999999-9999-4999-8999-999999999999/upload-b', gen_random_uuid(),
				now() + interval '1 hour')`,
			good: leaseInsert + `(gen_random_uuid(), ` + otherOrg +
				`, 'staging/99999999-9999-4999-8999-999999999999/upload-b', gen_random_uuid(),
				now() + interval '1 hour')`,
			wantCon: "staging_leases_key_scope_check",
		},
		{
			name: "lease that expires before it begins",
			bad: leaseInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'staging/` + reclamationOrgID + `/upload-c', gen_random_uuid(),
				now() - interval '1 hour')`,
			good: leaseInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'staging/` + reclamationOrgID + `/upload-c', gen_random_uuid(),
				now() + interval '1 second')`,
			wantCon: "staging_leases_term_check",
		},
		{
			name: "claim on a malformed digest",
			bad: claimInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'not-a-digest', ARRAY['v1'], ARRAY[]::text[])`,
			good: claimInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, ` + someDigest + `, ARRAY['v1'], ARRAY[]::text[])`,
			wantCon: "deletion_claims_digest_check",
		},
		{
			name: "claim naming nothing at all",
			bad: claimInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'bb11bb22cc33dd44ee55ff66aa77bb88cc99dd00ee11ff22aa33bb44cc55dd66',
				ARRAY[]::text[], ARRAY[]::text[])`,
			// Uploads alone are enough: a digest key can carry incomplete
			// uploads and no version at all.
			good: claimInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'bb11bb22cc33dd44ee55ff66aa77bb88cc99dd00ee11ff22aa33bb44cc55dd66',
				ARRAY[]::text[], ARRAY['upload-1'])`,
			wantCon: "deletion_claims_names_something_check",
		},
		{
			name: "claim with a NULL version id",
			bad: claimInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'cc11bb22cc33dd44ee55ff66aa77bb88cc99dd00ee11ff22aa33bb44cc55dd66',
				ARRAY['v1', NULL], ARRAY[]::text[])`,
			good: claimInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'cc11bb22cc33dd44ee55ff66aa77bb88cc99dd00ee11ff22aa33bb44cc55dd66',
				ARRAY['v1', 'v2'], ARRAY[]::text[])`,
			wantCon: "deletion_claims_ids_present_check",
		},
		{
			// The upload half separately: the constraint is a conjunction,
			// and a case for only one side leaves the other removable with
			// the suite still green.
			name: "claim with a NULL upload id",
			bad: claimInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'dd11bb22cc33dd44ee55ff66aa77bb88cc99dd00ee11ff22aa33bb44cc55dd66',
				ARRAY['v1'], ARRAY[NULL]::text[])`,
			good: claimInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'dd11bb22cc33dd44ee55ff66aa77bb88cc99dd00ee11ff22aa33bb44cc55dd66',
				ARRAY['v1'], ARRAY['upload-1'])`,
			wantCon: "deletion_claims_ids_present_check",
		},
		{
			name: "claim with a blank version id",
			bad: claimInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'ee11bb22cc33dd44ee55ff66aa77bb88cc99dd00ee11ff22aa33bb44cc55dd66',
				ARRAY['v1', ''], ARRAY[]::text[])`,
			good: claimInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'ee11bb22cc33dd44ee55ff66aa77bb88cc99dd00ee11ff22aa33bb44cc55dd66',
				ARRAY['v1'], ARRAY[]::text[])`,
			wantCon: "deletion_claims_ids_named_check",
		},
		{
			name: "claim with a blank upload id",
			bad: claimInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'ff11bb22cc33dd44ee55ff66aa77bb88cc99dd00ee11ff22aa33bb44cc55dd66',
				ARRAY[]::text[], ARRAY[''])`,
			good: claimInsert + `(gen_random_uuid(), ` + reclamationOrg +
				`, 'ff11bb22cc33dd44ee55ff66aa77bb88cc99dd00ee11ff22aa33bb44cc55dd66',
				ARRAY[]::text[], ARRAY['upload-1'])`,
			wantCon: "deletion_claims_ids_named_check",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := db.Exec(testCase.bad)
			if err == nil {
				t.Fatal("the database accepted a row its constraints should refuse")
			}
			// Name the constraint: a rejection by some unrelated rule — a
			// null violation, a foreign key — must not masquerade as this
			// one working.
			if !strings.Contains(err.Error(), testCase.wantCon) {
				t.Fatalf("rejected by something other than %s: %v", testCase.wantCon, err)
			}
			if _, err := db.Exec(testCase.good); err != nil {
				t.Fatalf("the positive control was rejected, so the constraint refuses valid rows: %v", err)
			}
		})
	}
}

// TestOneLeasePerStagingKey covers the state a lease exists to make
// impossible: two live writers holding one staging key, each believing it
// may promote what it finds there.
func TestOneLeasePerStagingKey(t *testing.T) {
	dsn := disposableDatabase(t)
	db := openDB(t, dsn)
	seedReclamationOrgs(t, db)

	const insert = `INSERT INTO staging_leases
		(staging_lease_id, organization_id, staging_key, owner_token, expires_at)
		VALUES (gen_random_uuid(), ` + reclamationOrg + `, 'staging/` + reclamationOrgID +
		`/contended', gen_random_uuid(), now() + interval '1 hour')`

	if _, err := db.Exec(insert); err != nil {
		t.Fatalf("first lease: %v", err)
	}
	_, err := db.Exec(insert)
	if err == nil {
		t.Fatal("a second writer took a lease on a key that was already leased")
	}
	if !strings.Contains(err.Error(), "staging_leases_key_unique") {
		t.Fatalf("rejected by something other than the key uniqueness: %v", err)
	}

	// A different key is unaffected — the constraint is per key, not a
	// limit of one lease per organization.
	if _, err := db.Exec(`INSERT INTO staging_leases
		(staging_lease_id, organization_id, staging_key, owner_token, expires_at)
		VALUES (gen_random_uuid(), ` + reclamationOrg + `, 'staging/` + reclamationOrgID +
		`/uncontended', gen_random_uuid(), now() + interval '1 hour')`); err != nil {
		t.Fatalf("a lease on a different key was refused: %v", err)
	}
}

// TestOneLiveClaimPerDigest covers the sweep's own exclusion. A claim's
// existence is what makes it live -- clearing one deletes the row -- so two
// sweeps must not be able to condemn the same digest at once.
func TestOneLiveClaimPerDigest(t *testing.T) {
	dsn := disposableDatabase(t)
	db := openDB(t, dsn)
	seedReclamationOrgs(t, db)

	insert := func(org, versions string) error {
		_, err := db.Exec(`INSERT INTO deletion_claims
			(deletion_claim_id, organization_id, object_digest, version_ids, upload_ids)
			VALUES (gen_random_uuid(), ` + org + `, ` + someDigest + `, ` + versions +
			`, ARRAY[]::text[])`)
		return err
	}

	if err := insert(reclamationOrg, `ARRAY['v1']`); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	err := insert(reclamationOrg, `ARRAY['v2']`)
	if err == nil {
		t.Fatal("two live claims were recorded against one digest")
	}
	if !strings.Contains(err.Error(), "deletion_claims_digest_unique") {
		t.Fatalf("rejected by something other than the digest uniqueness: %v", err)
	}

	// The same digest in ANOTHER organization is a different object: keys
	// are organization-scoped, so deduplication never crosses the boundary.
	if err := insert(otherOrg, `ARRAY['v1']`); err != nil {
		t.Fatalf("a claim in another organization was refused: %v", err)
	}

	// And clearing the claim frees the digest to be condemned again, which
	// is what makes the reconciler's re-run possible.
	if _, err := db.Exec(`DELETE FROM deletion_claims WHERE organization_id = ` +
		reclamationOrg); err != nil {
		t.Fatalf("clear claim: %v", err)
	}
	if err := insert(reclamationOrg, `ARRAY['v3']`); err != nil {
		t.Fatalf("a cleared digest could not be claimed again: %v", err)
	}
}

// TestReclamationRowsAreOrganizationScoped pins the tenant boundary on both
// tables. Every other table in this schema restricts deletion of an
// organization that still has rows, and these two hold the authority to
// destroy storage -- an orphaned lease or claim would be one no
// organization owns and no scoped query would ever find.
func TestReclamationRowsAreOrganizationScoped(t *testing.T) {
	dsn := disposableDatabase(t)
	db := openDB(t, dsn)
	seedReclamationOrgs(t, db)

	const unknownOrg = "'55555555-5555-4555-8555-555555555555'"
	for name, statement := range map[string]string{
		"lease": `INSERT INTO staging_leases
			(staging_lease_id, organization_id, staging_key, owner_token, expires_at)
			VALUES (gen_random_uuid(), ` + unknownOrg +
			`, 'staging/55555555-5555-4555-8555-555555555555/x', gen_random_uuid(),
			now() + interval '1 hour')`,
		"claim": `INSERT INTO deletion_claims
			(deletion_claim_id, organization_id, object_digest, version_ids, upload_ids)
			VALUES (gen_random_uuid(), ` + unknownOrg + `, ` + someDigest +
			`, ARRAY['v1'], ARRAY[]::text[])`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := db.Exec(statement)
			if err == nil {
				t.Fatal("a row was accepted for an organization that does not exist")
			}
			if !strings.Contains(err.Error(), "organization_id_fkey") {
				t.Fatalf("rejected by something other than the organization key: %v", err)
			}
		})
	}
}

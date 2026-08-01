package store

import (
	"reflect"
	"slices"
	"testing"
)

// The vault's creation input, asserted STRUCTURALLY (item 7, design D5 and
// D7).
//
// This property is about which code can be written, not about any value a
// run produces, so no behavioural test can hold it. "Create as A, read as B"
// exercises the read filter and nothing else: it passes whether or not the
// creation API accepts an owner, because a test that never supplies one
// cannot discover that supplying one is possible.

// permittedCreateSecretFields is the EXACT field set of CreateSecretInput.
//
// An allow-list, not a deny-list on the name "Owner". A check that only
// refused that one word is defeated by UserID, PrincipalID, OnBehalfOf, or
// AsUser — the same defeat the queries structure test already documents,
// where a name-only allow-list was beaten by rewriting an approved
// statement. Listing what IS permitted means every addition fails here and
// has to be argued for, which is the reviewable act.
var permittedCreateSecretFields = []string{
	// The credential itself.
	"Plaintext",
	// What it is and what it is for.
	"Name",
	"Scope",
	"OrganizationID",
	// WHO IS ACTING. Not who owns: the owner is derived from this, inside
	// the seam, where the caller cannot reach it.
	"ActingUserID",
}

// There is deliberately no ownership field of ANY kind, boolean included.
// Ownership is chosen by calling CreateIndividualSecret or
// CreateSharedSecret, so it cannot be defaulted, omitted, or set from
// deserialised input — a bool's zero value is a decision nobody wrote down.

// TestCreateSecretInputDeclaresExactlyItsPermittedFields is the guard.
//
// The damage a caller-supplied owner does is not a mislabelled row. The
// partial unique index gives each user exactly one slot per name and scope,
// so a secret created AS somebody else OCCUPIES that slot: the victim's own
// creation then fails against a row they cannot read, replace, or delete.
// A poisoned slot is not a labelling mistake, it is a denial of service that
// the victim cannot diagnose or clear.
func TestCreateSecretInputDeclaresExactlyItsPermittedFields(t *testing.T) {
	inputType := reflect.TypeOf(CreateSecretInput{})

	var declared []string
	for i := range inputType.NumField() {
		declared = append(declared, inputType.Field(i).Name)
	}

	slices.Sort(declared)
	permitted := slices.Clone(permittedCreateSecretFields)
	slices.Sort(permitted)

	if slices.Equal(declared, permitted) {
		return
	}

	for _, field := range declared {
		if !slices.Contains(permitted, field) {
			t.Errorf("CreateSecretInput declares %q, which is not a permitted field.\n"+
				"If this is meant to name an owner, it must not exist: an owner the caller supplies "+
				"is an owner the caller can lie about, and a secret created as somebody else occupies "+
				"the one slot the partial unique index gives them.\n"+
				"If it is something else, add it to permittedCreateSecretFields and say why.",
				field)
		}
	}
	for _, field := range permitted {
		if !slices.Contains(declared, field) {
			t.Errorf("CreateSecretInput no longer declares %q, which the seam is documented to accept",
				field)
		}
	}
}

// TestCreateSecretInputCarriesNoOwnerTyped is the second half, and it fails
// for a different reason than the first.
//
// The field-set test above is defeated by RENAMING rather than adding: swap
// ActingUserID's meaning to "the owner to write" and the set is unchanged.
// Nothing structural can detect a change of meaning — but it can detect the
// shape such a change usually needs, which is a SECOND user-shaped
// identifier alongside the acting one.
//
// So: exactly one user-identifying field. Two would mean the seam is being
// told both who is acting and who to attribute the secret to, and those
// being separable is the entire defect.
func TestCreateSecretInputCarriesNoOwnerTyped(t *testing.T) {
	inputType := reflect.TypeOf(CreateSecretInput{})

	var userFields []string
	for i := range inputType.NumField() {
		field := inputType.Field(i)
		// A user is named by a UUID. OrganizationID is one too, so the name
		// carries the distinction — but the check is over the whole set
		// rather than over one forbidden spelling.
		if field.Type == reflect.TypeOf(CreateSecretInput{}.ActingUserID) &&
			field.Name != "OrganizationID" {
			userFields = append(userFields, field.Name)
		}
	}

	if len(userFields) != 1 || userFields[0] != "ActingUserID" {
		t.Errorf("CreateSecretInput carries user-identifying fields %v, want exactly [ActingUserID].\n"+
			"A second one means the seam is told both who is acting and who to attribute the secret "+
			"to, and those being separable is the whole defect: the owner must be DERIVED from the "+
			"authenticated caller, never accepted from it.", userFields)
	}
}

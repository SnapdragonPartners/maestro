package boundary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"orchestrator/internal/boundary/family"
	"orchestrator/internal/dataplane/canonical"
)

// Substitution (design D6): three forms of input, and a secret is
// substituted before it is digested.
//
//   - RAW: what the caller supplied, in the call stack of Mediate and
//     nowhere else. For a secret slot the caller supplies NOTHING -- the raw
//     form of a secret exists only as the secret.Value gate 3 reveals.
//   - SUBSTITUTED: every secret slot replaced by "secret:<id>@<version>".
//     Digested; presented to the hook (projected to readable fields);
//     compared at gate 3.
//   - PERSISTED PROJECTION: the persist fields, secret slots as their
//     reference, large fields inline or by digest. The record.
//
// The request digest is over the caller's fields alone -- D5's correlation
// key, which a secret rotation between two presentations of one logical
// call must not disturb; the arguments digest is over the substituted form,
// which is what an approval binds to and does not survive rotation.

// SecretReference is the substituted form of a secret slot: a revision, not
// a name (ADR 0030 section 3). Rotation is why the version is in it (D6):
// a reference by name would let a later reveal fetch a token nobody
// approved.
type SecretReference struct {
	SecretID uuid.UUID
	Version  int
}

// secretReferencePrefix opens the textual form.
const secretReferencePrefix = "secret:"

// String renders "secret:<id>@<version>".
func (r SecretReference) String() string {
	return secretReferencePrefix + r.SecretID.String() + "@" + strconv.Itoa(r.Version)
}

// ErrInvalidSecretReference is what a malformed reference is refused with.
var ErrInvalidSecretReference = errors.New("invalid secret reference")

// ParseSecretReference reads the textual form back, as the reconciler does
// from a persisted projection (D5).
func ParseSecretReference(text string) (SecretReference, error) {
	body, found := strings.CutPrefix(text, secretReferencePrefix)
	if !found {
		return SecretReference{}, fmt.Errorf("%w: %q does not start with %q", ErrInvalidSecretReference, text, secretReferencePrefix)
	}
	idText, versionText, found := strings.Cut(body, "@")
	if !found {
		return SecretReference{}, fmt.Errorf("%w: %q names no version", ErrInvalidSecretReference, text)
	}
	id, err := uuid.Parse(idText)
	if err != nil {
		return SecretReference{}, fmt.Errorf("%w: %q: %w", ErrInvalidSecretReference, text, err)
	}
	version, err := strconv.Atoi(versionText)
	if err != nil || version < 1 {
		return SecretReference{}, fmt.Errorf("%w: %q: version %q is not a positive integer", ErrInvalidSecretReference, text, versionText)
	}
	return SecretReference{SecretID: id, Version: version}, nil
}

// LargeInlineLimit is the projection limit for a Large field, in bytes of
// its string value: at or under it the value is persisted inline; over it,
// the projection holds the value's digest and length (see largeReference).
const LargeInlineLimit = 4096

// Refusals a caller's arguments earn. Each is an admission denial in the
// gates; here they are typed so the gate can name the reason code.
var (
	// ErrUnknownField: the caller supplied a field the schema does not
	// declare. A request carrying head, base or story_id for the forge
	// family is refused this way (D13).
	ErrUnknownField = errors.New("unknown argument")
	// ErrSecretSupplied: the caller supplied a value for a secret slot,
	// which is exactly what ADR 0030 section 7 forbids the resource from
	// holding.
	ErrSecretSupplied = errors.New("a secret slot admits no caller-supplied value")
	// ErrMissingField: a required field is absent.
	ErrMissingField = errors.New("missing argument")
	// ErrWrongType: a field's value is not of the declared type.
	ErrWrongType = errors.New("argument has the wrong type")
	// ErrUnresolvedSlot: the boundary handed no reference for a secret
	// slot. A boundary defect, not a caller's.
	ErrUnresolvedSlot = errors.New("no secret reference was resolved for a slot")
)

// Substituted is the outcome of substitution: the three digests-and-forms
// the gates and the record need, from one pass over the schema.
//
//nolint:govet // fieldalignment: grouped by which gate consumes each
type Substituted struct {
	// Request is the caller's fields, validated, as supplied: the raw form
	// of the non-secret fields. Not persisted.
	Request map[string]any
	// RequestDigest is over Request: D5's correlation key.
	RequestDigest string

	// Arguments is the substituted form: Request plus every secret slot as
	// its reference. What the hook decides on and gate 3 compares.
	Arguments map[string]any
	// ArgumentsDigest is over Arguments.
	ArgumentsDigest string

	// Readable is Arguments projected to the fields a hook may read (D4):
	// persist and large fields, and secret slots as references. A
	// digest-only field enters the digest and is not readable, because a
	// decision made on a value the record does not hold could not be
	// audited against it.
	Readable map[string]any

	// Projection is the persisted form, tool_calls.arguments (D6).
	Projection json.RawMessage
}

// Substitute validates arguments against the schema and produces every
// form. references supplies the resolved reference for each secret slot,
// keyed by slot field name; gate 1 resolves them before calling this.
//
// It refuses, in this order, an unknown field, a value for a secret slot, a
// missing required field, and a mistyped value -- each named, since the
// caller is a model and the refusal is what it reads next.
func Substitute(schema family.Schema, arguments map[string]any, references map[string]SecretReference) (Substituted, error) {
	if err := refuseUndeclared(schema, arguments); err != nil {
		return Substituted{}, err
	}
	forms := newForms(len(schema.Fields))
	for i := range schema.Fields {
		if err := forms.place(&schema.Fields[i], arguments, references); err != nil {
			return Substituted{}, err
		}
	}
	return forms.finish()
}

// refuseUndeclared is the first pass: unknown fields and supplied secrets,
// both the caller reaching for something the schema does not offer, are
// refused before any question of what it did supply. Names are visited in
// order so the refusal is deterministic.
func refuseUndeclared(schema family.Schema, arguments map[string]any) error {
	names := make([]string, 0, len(arguments))
	for name := range arguments {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		field, declared := schema.Field(name)
		switch {
		case !declared:
			return fmt.Errorf("%w: %q", ErrUnknownField, name)
		case field.Classification == family.SecretSlot:
			return fmt.Errorf("%w: %q", ErrSecretSupplied, name)
		}
	}
	return nil
}

// forms accumulates the three forms as fields are placed.
type forms struct {
	request, substituted, readable, projection map[string]any
}

func newForms(n int) *forms {
	return &forms{
		request: make(map[string]any, n), substituted: make(map[string]any, n),
		readable: make(map[string]any, n), projection: make(map[string]any, n),
	}
}

// place puts one field into whichever forms its classification admits.
func (f *forms) place(field *family.Field, arguments map[string]any, references map[string]SecretReference) error {
	if field.Classification == family.SecretSlot {
		reference, resolved := references[field.Name]
		if !resolved {
			return fmt.Errorf("%w: %q", ErrUnresolvedSlot, field.Name)
		}
		text := reference.String()
		f.substituted[field.Name], f.readable[field.Name], f.projection[field.Name] = text, text, text
		return nil
	}
	value, supplied := arguments[field.Name]
	if !supplied {
		if field.Required {
			return fmt.Errorf("%w: %q", ErrMissingField, field.Name)
		}
		return nil
	}
	typed, err := coerce(field, value)
	if err != nil {
		return err
	}
	f.request[field.Name], f.substituted[field.Name] = typed, typed
	switch field.Classification {
	case family.Persist:
		f.readable[field.Name], f.projection[field.Name] = typed, typed
	case family.Large:
		f.readable[field.Name], f.projection[field.Name] = typed, projectLarge(typed)
	case family.DigestOnly:
		// In the digest, and nowhere the record or the hook can read.
	case family.SecretSlot, family.KeyedCommitment:
		// Unreachable: a secret slot is handled above and a keyed
		// commitment is refused at registry construction.
		return fmt.Errorf("field %q: classification %q reached substitution", field.Name, field.Classification)
	}
	return nil
}

// finish digests and encodes.
func (f *forms) finish() (Substituted, error) {
	requestDigest, err := canonical.Digest(f.request)
	if err != nil {
		return Substituted{}, fmt.Errorf("digest the request: %w", err)
	}
	argumentsDigest, err := canonical.Digest(f.substituted)
	if err != nil {
		return Substituted{}, fmt.Errorf("digest the substituted arguments: %w", err)
	}
	encoded, err := json.Marshal(f.projection)
	if err != nil {
		return Substituted{}, fmt.Errorf("encode the projection: %w", err)
	}
	return Substituted{
		Request: f.request, RequestDigest: requestDigest,
		Arguments: f.substituted, ArgumentsDigest: argumentsDigest,
		Readable:   f.readable,
		Projection: encoded,
	}, nil
}

// coerce checks a supplied value against its field's declared type and
// returns it in one Go shape per type, so the digest does not depend on
// whether a number arrived as float64 or json.Number.
func coerce(field *family.Field, value any) (any, error) {
	wrong := func() error {
		return fmt.Errorf("%w: %q is not a %s", ErrWrongType, field.Name, field.Type)
	}
	switch field.Type {
	case family.String:
		s, ok := value.(string)
		if !ok {
			return nil, wrong()
		}
		return s, nil
	case family.Boolean:
		b, ok := value.(bool)
		if !ok {
			return nil, wrong()
		}
		return b, nil
	case family.Integer, family.Number:
		return coerceNumber(field, value)
	}
	return nil, fmt.Errorf("field %q: type %q reached substitution unvalidated", field.Name, field.Type)
}

// coerceNumber validates a number ON ITS ORIGINAL REPRESENTATION before
// anything converts it (PR review round 1). A json.Number is checked as the
// literal the caller wrote: ADR 0028's safe range through canonical's own
// checker, and integrality through an exact rational -- because converting
// first would let 9007199254740991.1 round to an integer and 1e-400 to 0,
// and the recorded request would be a value the caller did not send. Once
// the literal is known safe, float64 is exact for it in the sense that
// matters: JCS serializes the literal and the float identically.
//
// A float64 has already been decoded by whoever produced the arguments and
// carries no literal to check; what remains checkable is the magnitude and,
// for an integer field, integrality.
func coerceNumber(field *family.Field, value any) (any, error) {
	refuse := func(why string) error {
		return fmt.Errorf("%w: %q is not a %s: %s", ErrWrongType, field.Name, field.Type, why)
	}
	switch n := value.(type) {
	case json.Number:
		if err := canonical.CheckSafeNumbers([]byte(n.String())); err != nil {
			return nil, refuse(err.Error())
		}
		exact, ok := new(big.Rat).SetString(n.String())
		if !ok {
			return nil, refuse("not a JSON number")
		}
		if field.Type == family.Integer && !exact.IsInt() {
			return nil, refuse("not integral")
		}
		f, err := n.Float64()
		if err != nil {
			return nil, refuse(err.Error())
		}
		return f, nil
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > canonical.SafeIntegerMax {
			return nil, refuse("outside the JCS-safe range")
		}
		if field.Type == family.Integer && n != math.Trunc(n) {
			return nil, refuse("not integral")
		}
		return n, nil
	case int:
		return coerceNumber(field, float64(n))
	case int64:
		return coerceNumber(field, float64(n))
	}
	return nil, refuse("not a number")
}

// largeReference is what the projection holds for a Large field over the
// limit: enough to check a later copy against, and not the value.
//
// The object reference the design names -- an artifact in the Audit family
// -- needs the seam to write it, which the boundary has from commit 3 and
// the first family with a large field (D13's body) exercises in commit 4;
// until then the projection records the digest and length, so the record
// is tamper-evident about what it does not hold.
type largeReference struct {
	Digest string `json:"digest"`
	Bytes  int    `json:"bytes"`
}

// projectLarge keeps a small value inline and reduces a large one. Only a
// string can be over the limit; coerce has already fixed every other type's
// shape, and those are persisted as they are.
func projectLarge(value any) any {
	text, ok := value.(string)
	if !ok || len(text) <= LargeInlineLimit {
		return value
	}
	sum := sha256.Sum256([]byte(text))
	return largeReference{Digest: hex.EncodeToString(sum[:]), Bytes: len(text)}
}

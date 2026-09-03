package secret

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
)

// RootKeyLen is the raw root-of-trust key length in bytes.
//
// It lives HERE, not beside the key file, because the invariant is about
// ROOT KEYS and not about one way of storing them: material handed to the
// process from outside must meet the same bar as material read from disk.
// `paths` re-exports it for the file format, importing this package to do
// so — the edge runs downward, so the persistence seam, which imports this
// package for RootKeyProvider and Value, never reaches the local key-file
// machinery through it (Phase 3 item 3, design D2).
const RootKeyLen = 32

// RootKeyProvider supplies the root-of-trust key material.
//
// It answers ONE question — give me the root key — and that narrowness is
// the design decision (item 7, D3). It is a LOCAL seam: the three backends
// below are three ways of holding a key on this machine.
//
// It is deliberately not the seam cloud mode replaces. Cloud mode does not
// hand Maestro a root key from a provider secret manager; it replaces the
// secrets module entirely, because the provider stores and returns the
// secrets themselves. An implementation of this interface for cloud would
// have to invent a key it has no use for, which is how conflating the two
// boundaries shows up as soon as anybody writes the second one.
type RootKeyProvider interface {
	// RootKey returns the key material. Callers pass it to DeriveKey and do
	// not retain it.
	RootKey() ([]byte, error)

	// Backend names which source answered, for diagnostics that would
	// otherwise have to guess.
	Backend() Backend
}

// Backend identifies a root-key source.
type Backend string

const (
	// BackendKeyFile is the default: a 0600 file under the config root,
	// generated silently at setup. No ceremony, nothing to remember, and
	// safe for unattended operation — which is the constraint that
	// disqualifies a passphrase as the default.
	BackendKeyFile Backend = "key-file"

	// BackendKeychain is named but not implemented; see ErrBackendNotImplemented.
	BackendKeychain Backend = "os-keychain"

	// BackendPassphrase is named but not implemented. It carries a cost the
	// default cannot: a plane that cannot start unattended.
	BackendPassphrase Backend = "passphrase"

	// BackendOperatorProvided is material handed to the process from outside
	// — an environment variable, a flag, or an injected secret — rather than
	// held on this machine by Maestro.
	//
	// It is NOT a fourth way of storing a key locally, and it has no
	// constructor above for that reason: there is nothing for this package to
	// read, create or refuse. It exists so a caller that already holds such
	// material can say where it came from, which is the difference between a
	// provider naming its source and one inheriting somebody else's.
	//
	// This is the route for a plane that does not hold its own key. It is
	// deliberately not a cloud RootKeyProvider: the seam stays local, and a
	// cloud implementation of it would have to invent a key it has no use
	// for, which is the anti-pattern this interface's own comment names.
	BackendOperatorProvided Backend = "operator-provided"
)

// ErrBackendNotImplemented reports a root-key backend that is named but not
// built.
//
// The backends REFUSE rather than falling back, and the distinction is the
// point. A stub returning something — an empty key, or a quiet
// fall-through to the key file — would encrypt real secrets under a key the
// operator did not choose and believes they did not use. That failure is
// silent at the time and unrecoverable later, because the operator's mental
// model of which key protects the vault is wrong.
var ErrBackendNotImplemented = errors.New("root-key backend is not implemented")

// ErrRootKeyLength reports material that is not a root key's length, so a
// caller can tell a malformed key from a missing one without matching text.
var ErrRootKeyLength = fmt.Errorf("root-of-trust key must be exactly %d bytes", RootKeyLen)

// The key-file provider — the local backend, with its create-versus-load
// access mode — lives in `paths`, which owns the key-file protocol. This
// package defines the seam and the vocabulary; it holds nothing that reads
// a disk.

// ResolvedKey wraps key material that has ALREADY been obtained, for callers
// that must hand a provider to something else without making a second
// create-versus-load decision.
//
// It exists because that decision is singular by design (item 7, D4): only
// the code that knows the operation AND whether the data root is empty may
// decide whether a key may be minted, and a second KeyFile constructed
// downstream would make that decision again, somewhere nothing reviews. A
// caller that already holds the key passes it through here instead.
//
// It carries a decision; it does not make one. There is deliberately no path
// from this type to the filesystem.
//
// The SOURCE is a parameter because this type cannot know it and must not
// guess. It previously reported BackendKeyFile unconditionally, which was
// accurate only because every caller happened to resolve its key from the key
// file — a property of the one call path, not of this type. A caller that
// obtains material some other way, which is what a plane not holding its own
// key does, would have made every diagnostic name a backend nobody
// configured. Reporting the wrong source is worse than reporting none: the
// vault's key provenance is exactly what an operator consults when they need
// to know which key protects the data, and ErrBackendNotImplemented already
// exists because guessing there is unrecoverable later.
//
// BOTH arguments are validated, and the reason is that this constructor
// returns an error at all. Its whole purpose is to fail where the mistake is
// made rather than where it surfaces, and checking only one of the two left
// the other still deferred:
//
//   - EMPTY MATERIAL builds a provider that looks usable and fails at the
//     first vault operation, which is a long way from the caller that passed
//     nothing. An operator supplying a key by hand — the case this parameter
//     exists for — is exactly who supplies an empty one by mistake.
//   - AN UNRECOGNISED SOURCE recreates the defect this parameter removed. A
//     typo is not caught by requiring non-empty, and a provider reporting
//     "keyfile" or "operator" would misname the vault's key provenance just as
//     effectively as the old hardcoded constant did, while looking deliberate.
func ResolvedKey(key []byte, source Backend) (RootKeyProvider, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("resolved root key was given no material: %w", ErrNoRootKey)
	}
	// The LENGTH invariant is enforced here, not only where key files are
	// read, and that placement is the point. The key-file loader refuses a
	// file that is not exactly RootKeyLen bytes, so every key that reached this
	// constructor used to satisfy it as a side effect of where it came from.
	// Material handed in from outside has no such history: a one-byte key
	// would derive perfectly usable-looking subkeys through HKDF and unlock
	// the same vault seam at a fraction of the intended entropy, and nothing
	// downstream would object — DeriveKey only rejects empty input.
	//
	// Refusing long material as well as short is deliberate. Truncating or
	// hashing an over-long key to fit would silently accept two different
	// inputs as the same key, and an operator who supplied 64 bytes believing
	// all of them mattered would be wrong in a way nothing reports.
	if len(key) != RootKeyLen {
		return nil, fmt.Errorf("resolved root key is %d bytes, want exactly %d: %w",
			len(key), RootKeyLen, ErrRootKeyLength)
	}
	if !source.known() {
		return nil, fmt.Errorf("resolved root key names backend %q, which is not one of %s: a "+
			"provider that reports a source nobody configured misdirects the operator who needs to "+
			"know which key protects the vault", source, knownBackends)
	}
	return resolvedKeyProvider{key: bytes.Clone(key), source: source}, nil
}

// knownBackends is every source a provider may claim. It is the list rather
// than a range check because Backend is a string: there is no compiler-visible
// set, so the constants have to be enumerated somewhere, and one place beats
// each caller guessing.
//
//nolint:gochecknoglobals // Immutable set, built once at init.
var knownBackends = []Backend{
	BackendKeyFile,
	BackendKeychain,
	BackendPassphrase,
	BackendOperatorProvided,
}

// known reports whether this is a backend the package defines.
func (b Backend) known() bool { return slices.Contains(knownBackends, b) }

// Field order is chosen for alignment rather than for reading: the string
// header leads, which keeps the struct's pointer prefix minimal.
type resolvedKeyProvider struct {
	source Backend
	key    []byte
}

// Backend reports the source the caller named, which is the only party that
// knows it.
func (p resolvedKeyProvider) Backend() Backend { return p.source }

func (p resolvedKeyProvider) RootKey() ([]byte, error) {
	if len(p.key) == 0 {
		return nil, ErrNoRootKey
	}
	return bytes.Clone(p.key), nil
}

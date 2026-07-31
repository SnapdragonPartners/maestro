package secret

import (
	"errors"
	"fmt"

	"orchestrator/internal/dataplane/paths"
)

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

// Access says whether a provider may CREATE key material or only load it.
//
// The distinction is item 7's D4, and it exists because the root key derives
// the Postgres password and the object-store credentials as well as the
// vault's key material. A plane whose data root already holds a cluster and
// whose key is missing must refuse, not mint a new one: the new key produces
// a password the existing cluster does not know, and the failure arrives as
// a readiness timeout that names nothing.
type Access int

const (
	// LoadOnly refuses when no key is present. Every operation against an
	// existing plane uses it.
	LoadOnly Access = iota

	// MayCreate generates a key when none exists. Only first-time setup
	// against an empty data root may pass it.
	MayCreate
)

// KeyFile builds the file-backed provider.
func KeyFile(configRoot string, access Access) RootKeyProvider {
	return keyFileProvider{configRoot: configRoot, access: access}
}

type keyFileProvider struct {
	configRoot string
	access     Access
}

func (p keyFileProvider) Backend() Backend { return BackendKeyFile }

func (p keyFileProvider) RootKey() ([]byte, error) {
	if p.access == MayCreate {
		key, err := paths.EnsureKey(p.configRoot)
		if err != nil {
			return nil, fmt.Errorf("ensure root-of-trust key: %w", err)
		}
		return key, nil
	}
	key, err := paths.LoadKey(p.configRoot)
	if err != nil {
		return nil, fmt.Errorf("load root-of-trust key: %w", err)
	}
	return key, nil
}

// Unimplemented returns a provider that refuses, naming the backend it would
// have been.
//
// Selecting one of these is only possible explicitly — nothing defaults onto
// them, and the refusal happens where the key would have been read rather
// than later, at a decryption that fails for reasons nobody can trace.
func Unimplemented(backend Backend) RootKeyProvider {
	return unimplementedProvider{backend: backend}
}

type unimplementedProvider struct{ backend Backend }

func (p unimplementedProvider) Backend() Backend { return p.backend }

func (p unimplementedProvider) RootKey() ([]byte, error) {
	return nil, fmt.Errorf("%w: %s", ErrBackendNotImplemented, p.backend)
}

// ProviderFor selects a backend by name.
//
// The key file is the only one that resolves to something usable, and the
// other two resolve to a refusal rather than to an error here: the caller
// gets a provider whose failure names the backend at the moment the key is
// needed, which is more useful than a construction error that a caller may
// handle by falling back.
func ProviderFor(backend Backend, configRoot string, access Access) (RootKeyProvider, error) {
	switch backend {
	case BackendKeyFile:
		return KeyFile(configRoot, access), nil
	case BackendKeychain, BackendPassphrase:
		return Unimplemented(backend), nil
	default:
		return nil, fmt.Errorf("unknown root-key backend %q", backend)
	}
}

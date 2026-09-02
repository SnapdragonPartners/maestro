package paths

import (
	"fmt"

	"orchestrator/internal/dataplane/secret"
)

// Access says whether a provider may CREATE key material or only load it.
//
// The distinction is Phase 2 item 7's D4, and it exists because the root key
// derives the Postgres password and the object-store credentials as well as
// the vault's key material. A plane whose data root already holds a cluster
// and whose key is missing must refuse, not mint a new one: the new key
// produces a password the existing cluster does not know, and the failure
// arrives as a readiness timeout that names nothing.
type Access int

const (
	// LoadOnly refuses when no key is present. Every operation against an
	// existing plane uses it.
	LoadOnly Access = iota

	// MayCreate generates a key when none exists. Only first-time setup
	// against an empty data root may pass it.
	MayCreate
)

// KeyFile builds the file-backed root-key provider.
//
// It lives HERE rather than beside secret.RootKeyProvider because it is the
// LOCAL backend: it reads and writes a file under the config root, which is
// this package's protocol (LoadKey, EnsureKey, the permission and length
// checks). `secret` defines the seam and its vocabulary and holds nothing
// that touches a disk, so the persistence seam — which imports `secret` for
// RootKeyProvider and Value — cannot reach this package through it (Phase 3
// item 3, design D2).
func KeyFile(configRoot string, access Access) secret.RootKeyProvider {
	return keyFileProvider{configRoot: configRoot, access: access}
}

type keyFileProvider struct {
	configRoot string
	access     Access
}

func (p keyFileProvider) Backend() secret.Backend { return secret.BackendKeyFile }

func (p keyFileProvider) RootKey() ([]byte, error) {
	if p.access == MayCreate {
		key, err := EnsureKey(p.configRoot)
		if err != nil {
			return nil, fmt.Errorf("ensure root-of-trust key: %w", err)
		}
		return key, nil
	}
	key, err := LoadKey(p.configRoot)
	if err != nil {
		return nil, fmt.Errorf("load root-of-trust key: %w", err)
	}
	return key, nil
}

// ProviderFor selects a local backend by name, and REFUSES an unimplemented
// one at construction (Phase 2 item 7, design D3).
//
// At construction, not at first use. An earlier revision returned a provider
// whose RootKey refused later, which reads as equivalent and is not: a
// deferred refusal is one a caller can hold, pass around, and discover only
// at the moment it needs key material — by which point it may already have
// decided the plane is usable. Failing here means selecting an unbuilt
// backend cannot produce a usable-looking provider at all.
//
// No provider is returned alongside the error, deliberately. A refusal that
// also hands back something callable invites exactly the fall-through this
// design exists to prevent.
func ProviderFor(backend secret.Backend, configRoot string, access Access) (secret.RootKeyProvider, error) {
	switch backend {
	case secret.BackendKeyFile:
		return KeyFile(configRoot, access), nil
	case secret.BackendKeychain, secret.BackendPassphrase:
		return nil, fmt.Errorf("%w: %s", secret.ErrBackendNotImplemented, backend)
	default:
		return nil, fmt.Errorf("unknown root-key backend %q", backend)
	}
}

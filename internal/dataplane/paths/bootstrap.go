package paths

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ErrInvalidBootstrap reports a bootstrap pointer that is missing required
// fields or carries values this build cannot use.
var ErrInvalidBootstrap = errors.New("invalid bootstrap pointer")

const (
	// RootOfTrustKeyFile is the shipped root-of-trust backend. Keychain and
	// passphrase are the opt-in upgrades, added here when implemented so an
	// unimplemented kind cannot validate.
	RootOfTrustKeyFile = "key_file"

	minPort = 1
	maxPort = 65535

	// BootstrapFileName is the data-plane bootstrap pointer, under the
	// config root beside the root-of-trust key.
	BootstrapFileName = "bootstrap.json"

	// BootstrapSchemaVersion is the pointer's own schema version. It is
	// deliberately separate from any artifact schema: this file is read
	// before the data plane exists, so it cannot depend on it.
	BootstrapSchemaVersion = 1

	// bootstrapPerm matches the key file's mode. The pointer holds no
	// secret, but it describes how to reach the data plane and there is no
	// reason for it to be readable beyond its owner.
	bootstrapPerm = 0o600
)

// Bootstrap is the local pointer to a data plane: how to reach it, and
// where its root of trust lives.
//
// It never contains secrets. The Postgres password is derived from the
// root-of-trust key rather than stored, so this file can be read, copied,
// and diffed freely — and so the cold backup, which excludes the key, also
// carries nothing that could unlock anything on its own.
//
//nolint:govet // fieldalignment: written once at setup; readable order preferred.
type Bootstrap struct {
	SchemaVersion int         `json:"schema_version"`
	Postgres      Postgres    `json:"postgres"`
	Objects       ObjectStore `json:"objects"`
	RootOfTrust   RootOfTrust `json:"root_of_trust"`
}

// Postgres locates the relational store. No password: see Bootstrap.
//
//nolint:govet // fieldalignment: readable order preferred over packing.
type Postgres struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
}

// ObjectStore locates the digest-addressed object store.
type ObjectStore struct {
	Endpoint string `json:"endpoint"`
	Bucket   string `json:"bucket"`
}

// RootOfTrust references the external unlock anchor — a reference, never
// the key material itself.
type RootOfTrust struct {
	// Kind is the auth-module backend: "key_file" today, with "keychain"
	// and "passphrase" as the opt-in upgrades behind the same interface.
	Kind string `json:"kind"`
	// Path is set for the key_file kind.
	Path string `json:"path,omitempty"`
}

// WriteBootstrap writes the pointer atomically, replacing any existing one.
//
// Unlike the key file this may legitimately be overwritten — ports and
// endpoints change — so it uses write-temp-then-rename rather than the
// key's link protocol. Rename replaces atomically, so a reader sees either
// the old pointer or the new one, never a partial file.
func WriteBootstrap(configRoot string, b *Bootstrap) error {
	if b == nil {
		return fmt.Errorf("%w: nil bootstrap pointer", ErrInvalidBootstrap)
	}
	b.SchemaVersion = BootstrapSchemaVersion
	if err := b.validate(); err != nil {
		return err
	}

	encoded, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("encode bootstrap pointer: %w", err)
	}
	encoded = append(encoded, '\n')

	if mkErr := os.MkdirAll(configRoot, rootPerm); mkErr != nil {
		return fmt.Errorf("create config root %s: %w", configRoot, mkErr)
	}

	f, tmpErr := os.CreateTemp(configRoot, BootstrapFileName+".tmp-*")
	if tmpErr != nil {
		return fmt.Errorf("create temporary bootstrap pointer in %s: %w", configRoot, tmpErr)
	}
	name := f.Name()
	if err := writeAndClose(f, encoded); err != nil {
		return discardTemp(name, fmt.Errorf("write bootstrap pointer: %w", err))
	}
	if err := os.Chmod(name, bootstrapPerm); err != nil {
		return discardTemp(name, fmt.Errorf("set bootstrap pointer permissions: %w", err))
	}

	path := filepath.Join(configRoot, BootstrapFileName)
	if err := os.Rename(name, path); err != nil {
		return discardTemp(name, fmt.Errorf("install bootstrap pointer %s: %w", path, err))
	}
	if err := syncDir(configRoot); err != nil {
		return err
	}
	return nil
}

// writeAndClose writes b to f, flushes it, and closes it.
func writeAndClose(f *os.File, b []byte) error {
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("flush: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return nil
}

// ReadBootstrap loads the pointer from the config root.
//
// Unknown fields are rejected. This file is hand-editable by design, so a
// tolerant decoder would silently accept a `postgres.password` somebody
// added in good faith — reading as if it worked while the value is ignored,
// which is the worst of both outcomes for a credential. Strictness here is
// what makes "this file carries no secret" a property of the format rather
// than of today's struct definition.
func ReadBootstrap(configRoot string) (Bootstrap, error) {
	path := filepath.Join(configRoot, BootstrapFileName)
	f, err := os.Open(path)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("read bootstrap pointer %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()

	var b Bootstrap
	if err := dec.Decode(&b); err != nil {
		return Bootstrap{}, fmt.Errorf("decode bootstrap pointer %s: %w", path, err)
	}
	// A JSON decoder stops at the end of the first value, so trailing
	// content is silently ignored — including a second, contradictory
	// object that a reader would reasonably assume was in effect.
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Bootstrap{}, fmt.Errorf("%w: %s has content after the first JSON document", ErrInvalidBootstrap, path)
	}
	// A pointer from a future Maestro may describe a plane this build
	// cannot reach. Refuse rather than half-understand it.
	if b.SchemaVersion != BootstrapSchemaVersion {
		return Bootstrap{}, fmt.Errorf("bootstrap pointer %s has schema version %d, want %d", path, b.SchemaVersion, BootstrapSchemaVersion)
	}
	if err := b.validate(); err != nil {
		return Bootstrap{}, fmt.Errorf("bootstrap pointer %s: %w", path, err)
	}
	return b, nil
}

// validate enforces the pointer's required shape on both write and read.
// Validating on write keeps an unusable file from being created; validating
// on read catches hand edits, which are expected for this file.
func (b *Bootstrap) validate() error {
	if err := validateHost(b.Postgres.Host); err != nil {
		return err
	}
	if b.Postgres.Port < minPort || b.Postgres.Port > maxPort {
		return fmt.Errorf("%w: postgres.port %d is outside 1-65535", ErrInvalidBootstrap, b.Postgres.Port)
	}
	if b.Postgres.Database == "" {
		return fmt.Errorf("%w: postgres.database is required", ErrInvalidBootstrap)
	}
	if b.Postgres.User == "" {
		return fmt.Errorf("%w: postgres.user is required", ErrInvalidBootstrap)
	}
	if b.Objects.Bucket == "" {
		return fmt.Errorf("%w: objects.bucket is required", ErrInvalidBootstrap)
	}
	if err := validateEndpoint(b.Objects.Endpoint); err != nil {
		return err
	}
	return b.RootOfTrust.validate()
}

// validateEndpoint requires a bare http(s) origin — scheme, host, optional
// port, and nothing else.
//
// The exclusions are the point, not the scheme check. A URL is a rich
// enough format to smuggle a credential past a file that claims to hold
// none: userinfo (`http://user:password@host`) is the obvious one, and a
// query or fragment carries a token just as well. Rejecting everything
// beyond the origin keeps "this file has no secrets" true by shape rather
// than by reviewers noticing. The path is held to "" or "/" for the same
// reason — a token in a path segment is still a token.
func validateEndpoint(endpoint string) error {
	if endpoint == "" {
		return fmt.Errorf("%w: objects.endpoint is required", ErrInvalidBootstrap)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("%w: objects.endpoint %q is not a URL: %w", ErrInvalidBootstrap, endpoint, err)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("%w: objects.endpoint %q must be http or https", ErrInvalidBootstrap, endpoint)
	case u.Host == "":
		return fmt.Errorf("%w: objects.endpoint %q has no host", ErrInvalidBootstrap, endpoint)
	case u.User != nil:
		return fmt.Errorf("%w: objects.endpoint must not contain userinfo; credentials never live in the bootstrap pointer", ErrInvalidBootstrap)
	case u.RawQuery != "":
		return fmt.Errorf("%w: objects.endpoint must not contain a query string", ErrInvalidBootstrap)
	case u.Fragment != "":
		return fmt.Errorf("%w: objects.endpoint must not contain a fragment", ErrInvalidBootstrap)
	case u.Path != "" && u.Path != "/":
		return fmt.Errorf("%w: objects.endpoint %q must be a bare origin, with no path", ErrInvalidBootstrap, endpoint)
	}
	return nil
}

// forbiddenInHost maps a substring that must not appear in postgres.host
// to the reason it is refused.
//
//nolint:gochecknoglobals // Immutable lookup table.
var forbiddenInHost = map[string]string{
	"://": "a scheme",
	"@":   "userinfo",
	"/":   "a path",
	":":   "a port (use postgres.port)",
	" ":   "whitespace",
}

// validateHost requires a bare hostname or IP. The port is a separate
// field, so anything resembling URL syntax here — a scheme, userinfo, a
// path — is either a mistake or an attempt to smuggle a credential into a
// file that must not carry one.
func validateHost(host string) error {
	if host == "" {
		return fmt.Errorf("%w: postgres.host is required", ErrInvalidBootstrap)
	}
	for substr, reason := range forbiddenInHost {
		if strings.Contains(host, substr) {
			return fmt.Errorf("%w: postgres.host %q must be a bare hostname or IP, but contains %s", ErrInvalidBootstrap, host, reason)
		}
	}
	return nil
}

// validate checks the root-of-trust reference. Unsupported kinds are
// refused rather than defaulted: silently falling back to the key file
// when someone asked for a keychain would put the key somewhere they did
// not intend.
func (r RootOfTrust) validate() error {
	switch r.Kind {
	case RootOfTrustKeyFile:
		if r.Path == "" {
			return fmt.Errorf("%w: root_of_trust.path is required for kind %q", ErrInvalidBootstrap, RootOfTrustKeyFile)
		}
		if !filepath.IsAbs(r.Path) {
			return fmt.Errorf("%w: root_of_trust.path %q must be absolute", ErrInvalidBootstrap, r.Path)
		}
		return nil
	case "":
		return fmt.Errorf("%w: root_of_trust.kind is required", ErrInvalidBootstrap)
	default:
		return fmt.Errorf("%w: unsupported root_of_trust.kind %q", ErrInvalidBootstrap, r.Kind)
	}
}

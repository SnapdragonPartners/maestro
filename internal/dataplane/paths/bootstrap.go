package paths

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
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
	b.SchemaVersion = BootstrapSchemaVersion

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
func ReadBootstrap(configRoot string) (Bootstrap, error) {
	path := filepath.Join(configRoot, BootstrapFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		return Bootstrap{}, fmt.Errorf("read bootstrap pointer %s: %w", path, err)
	}

	var b Bootstrap
	if err := json.Unmarshal(raw, &b); err != nil {
		return Bootstrap{}, fmt.Errorf("decode bootstrap pointer %s: %w", path, err)
	}
	// A pointer from a future Maestro may describe a plane this build
	// cannot reach. Refuse rather than half-understand it.
	if b.SchemaVersion != BootstrapSchemaVersion {
		return Bootstrap{}, fmt.Errorf("bootstrap pointer %s has schema version %d, want %d", path, b.SchemaVersion, BootstrapSchemaVersion)
	}
	return b, nil
}

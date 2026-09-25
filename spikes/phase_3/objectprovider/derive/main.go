// derive prints the object-store credentials the data plane derives from a
// root-of-trust key, so a candidate provider can be started with the exact
// credentials internal/dataplane/planetest will present.
//
// It duplicates internal/dataplane/secret.Derive (HKDF-SHA256, no salt, the
// context string as info, 32 bytes, raw-URL base64) and the key file's
// encoding (32 bytes, hex, one line) because a spike module cannot import an
// internal package. Keep in step with those files by hand.
package main

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	contextAccessKey = "maestro/dataplane/object-access-key/v1"
	contextSecretKey = "maestro/dataplane/object-secret-key/v1"
	keyLen           = 32
)

func main() {
	home := os.Getenv("MAESTRO_HOME")
	if home == "" {
		fmt.Fprintln(os.Stderr, "MAESTRO_HOME is required; the spike runs under an isolated root")
		os.Exit(2)
	}
	path := filepath.Join(home, "config", "root-of-trust.key")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		fresh := make([]byte, keyLen)
		if _, err := rand.Read(fresh); err != nil {
			panic(err)
		}
		raw = []byte(hex.EncodeToString(fresh) + "\n")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			panic(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			panic(err)
		}
	} else if err != nil {
		panic(err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != keyLen {
		panic(fmt.Sprintf("key file %s: want %d hex-encoded bytes: %v", path, keyLen, err))
	}
	for _, c := range []struct{ env, context string }{
		{"AWS_ACCESS_KEY_ID", contextAccessKey}, {"AWS_SECRET_ACCESS_KEY", contextSecretKey},
	} {
		derived, err := hkdf.Key(sha256.New, key, nil, c.context, keyLen)
		if err != nil {
			panic(err)
		}
		fmt.Printf("%s=%s\n", c.env, base64.RawURLEncoding.EncodeToString(derived))
	}
}

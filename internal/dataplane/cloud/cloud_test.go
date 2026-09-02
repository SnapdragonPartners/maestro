package cloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/dataplane/secret"
)

// These tests reach no cloud service. They cover the refusals that must hold
// before anything is built, and the one structural claim worth pinning: that
// this package never invents a root key.
//
// The live sequence — migrate from empty, open, round-trip, close — is in
// cloud_integration_test.go, because what it asserts is what the managed
// services actually do.

// validRootKey is material of exactly the length a root key must be.
func validRootKey() []byte { return bytes.Repeat([]byte{0x5A}, paths.RootKeyLen) }

// TestConfigRefusesAWrongLengthRootKey pins the invariant at the configuration
// boundary.
//
// A cloud plane's key arrives from outside with no history — nothing has already
// refused it for being the wrong size, which is what a key file's loader does.
// Accepting a short one would unlock the same vault at a fraction of the
// intended entropy, and every derivation downstream would succeed.
func TestConfigRefusesAWrongLengthRootKey(t *testing.T) {
	for name, key := range map[string][]byte{
		"one byte":  {0x01},
		"one short": bytes.Repeat([]byte{0x02}, paths.RootKeyLen-1),
		"one long":  bytes.Repeat([]byte{0x03}, paths.RootKeyLen+1),
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Config{DSN: "postgres://example", Bucket: "b", RootKey: key}
			err := cfg.validate()
			if err == nil {
				t.Fatalf("a %d-byte root key was accepted", len(key))
			}
			if !errors.Is(err, secret.ErrRootKeyLength) {
				t.Fatalf("a wrong-length key must be distinguishable from a missing one: %v", err)
			}
		})
	}
}

// TestOpenSeamRefusesANilRegistryBeforeBuildingAClient covers the ordering.
// `plane` refuses a nil registry too, but reaching that costs a network client
// this function would then have to close — and a resource that only needs
// closing on an error path is the one that gets leaked.
func TestOpenSeamRefusesANilRegistryBeforeBuildingAClient(t *testing.T) {
	_, err := OpenSeam(context.Background(), Config{
		DSN: "postgres://example", Bucket: "b", RootKey: validRootKey(),
	}, nil, configkeys.MustNew(nil))
	if err == nil {
		t.Fatal("OpenSeam accepted a nil registry")
	}
	if !strings.Contains(err.Error(), "no artifact registry") {
		t.Fatalf("the failure should name the registry rather than something downstream, which "+
			"would mean a client was built first: %v", err)
	}
}

// TestConfigRefusesAnIncompleteConfiguration covers each input that cannot open
// a plane. Each would otherwise fail somewhere further from the cause.
func TestConfigRefusesAnIncompleteConfiguration(t *testing.T) {
	complete := func() Config {
		return Config{
			DSN:     "postgres://user:pw@127.0.0.1:5433/maestro?sslmode=disable",
			Bucket:  "maestro-objects",
			RootKey: validRootKey(),
		}
	}
	for name, breakIt := range map[string]func(*Config){
		"no DSN":      func(c *Config) { c.DSN = "" },
		"no bucket":   func(c *Config) { c.Bucket = "" },
		"no root key": func(c *Config) { c.RootKey = nil },
		"empty root key": func(c *Config) {
			c.RootKey = []byte{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := complete()
			breakIt(&cfg)
			if err := cfg.validate(); err == nil {
				t.Fatal("an incomplete configuration was accepted, deferring the failure to a " +
					"point further from its cause")
			}
		})
	}
}

// TestConfigAcceptsACompleteConfiguration is the control. Without it the table
// above passes equally against a validate that refuses everything.
func TestConfigAcceptsACompleteConfiguration(t *testing.T) {
	cfg := Config{
		DSN:     "postgres://user:pw@127.0.0.1:5433/maestro?sslmode=disable",
		Bucket:  "maestro-objects",
		RootKey: validRootKey(),
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("a complete configuration was refused: %v", err)
	}
}

// TestMissingRootKeyIsRefusedAsMissingNotDefaulted pins the property that
// separates a legitimate operator-provided composition from the anti-pattern.
//
// The tempting implementation generates a key when none is supplied, because it
// makes the happy path work. That produces a vault whose provenance nobody can
// state, encrypted under a key the operator did not choose and does not know
// they are using — which is exactly the failure ErrBackendNotImplemented exists
// to prevent for the local backends. The refusal must therefore be recognisable
// as a missing key rather than a generic validation complaint.
func TestMissingRootKeyIsRefusedAsMissingNotDefaulted(t *testing.T) {
	cfg := Config{DSN: "postgres://example", Bucket: "b"}
	err := cfg.validate()
	if err == nil {
		t.Fatal("a configuration with no root key was accepted")
	}
	if !errors.Is(err, secret.ErrNoRootKey) {
		t.Fatalf("a missing root key must report as a missing key so a caller cannot mistake it "+
			"for a recoverable configuration slip: %v", err)
	}
	if !strings.Contains(err.Error(), "will not generate one") {
		t.Fatalf("the refusal should say the key is not generated here, since generating one is "+
			"the tempting fix: %v", err)
	}
}

// TestMigrateRefusesWithoutADSN guards the one input Migrate has. It is
// separate from Config.validate because Migrate deliberately does not require a
// bucket or a key: a schema change needs neither, and demanding them would make
// migrating a plane require credentials it does not use.
func TestMigrateRefusesWithoutADSN(t *testing.T) {
	if err := Migrate(context.Background(), Config{Bucket: "b", RootKey: []byte("k")}); err == nil {
		t.Fatal("Migrate accepted an empty DSN")
	}
}

// TestOpenSeamRefusesBeforeBuildingAnything asserts that validation precedes
// client construction.
//
// Order matters for two reasons. A misconfigured call should not open a network
// client it is about to discard, and — the one that bites — a client built
// before the failure is a client something has to remember to close. Refusing
// first means there is nothing to leak.
//
// It reaches no network despite the context: an empty bucket is rejected before
// storage.NewClient, which is the only part that resolves credentials, so this
// passes on a machine that has never authenticated.
func TestOpenSeamRefusesBeforeBuildingAnything(t *testing.T) {
	_, err := OpenSeam(context.Background(), Config{
		DSN:     "postgres://example",
		RootKey: validRootKey(),
	}, nil, configkeys.MustNew(nil))
	if err == nil {
		t.Fatal("OpenSeam accepted a configuration with no bucket")
	}
	if !strings.Contains(err.Error(), "no object bucket") {
		t.Fatalf("the failure should name the missing bucket rather than something downstream, "+
			"which would mean a client was built first: %v", err)
	}
}

// TestEnsureBucketRefusesWithoutABucket covers the same ordering for the
// provisioning path.
func TestEnsureBucketRefusesWithoutABucket(t *testing.T) {
	if _, err := EnsureBucket(context.Background(), Config{}); err == nil {
		t.Fatal("EnsureBucket accepted an empty bucket name")
	}
}

// TestStabilizationIsAPolicyNotAGuarantee pins the constant's meaning where a
// reader will look for it.
//
// It is not a propagation guarantee: Google advises at least 30 seconds and
// bounds nothing. If this is ever lowered to the documented minimum, the value
// stops being conservative and starts being a bet.
func TestStabilizationIsAPolicyNotAGuarantee(t *testing.T) {
	const documentedMinimumSeconds = 30
	if SoftDeleteStabilization.Seconds() <= documentedMinimumSeconds {
		t.Fatalf("stabilization is %s, which is not above the documented minimum of %ds. Google "+
			"gives no upper bound on when a soft-delete change takes effect, so waiting exactly the "+
			"minimum is a bet rather than a conservative choice",
			SoftDeleteStabilization, documentedMinimumSeconds)
	}
}

// maxIdentifier is PostgreSQL's identifier limit in bytes.
//
// Names longer than this are TRUNCATED SILENTLY rather than rejected, which is
// how the first version of this helper worked by accident: it generated 67-byte
// names, and CREATE and DROP truncated identically so nothing failed. Two test
// names agreeing in their first 63 bytes would have collided, one run dropping
// the other's database mid-test.
const maxIdentifier = 63

// freshDatabaseName builds a unique, bounded database name.
//
// Randomness rather than a timestamp: Unix seconds plus a fixed test name
// collides whenever two runs start in the same second, which is exactly what
// happens when someone reruns immediately or a workflow fans out. Eight random
// bytes make that a non-issue without needing coordination.
//
// The test name is included for debuggability but TRUNCATED to fit, and the
// result is asserted against the limit so a future change cannot quietly exceed
// it again.
func freshDatabaseName(t *testing.T) string {
	t.Helper()

	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("generate a database-name nonce: %v", err)
	}
	prefix := "maestro_cloud_" + hex.EncodeToString(nonce)

	slug := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, strings.ToLower(t.Name()))
	if room := maxIdentifier - len(prefix) - 1; room > 0 {
		if len(slug) > room {
			slug = slug[:room]
		}
		prefix += "_" + slug
	}

	if len(prefix) > maxIdentifier {
		t.Fatalf("generated database name is %d bytes, over PostgreSQL's %d-byte limit: it would "+
			"be truncated silently and could collide with another run", len(prefix), maxIdentifier)
	}
	return prefix
}

// TestFreshDatabaseNameFitsPostgresIdentifierLimit guards the naming rule
// without needing a cloud plane.
//
// PostgreSQL truncates over-long identifiers SILENTLY, so the first version of
// this generator produced 67-byte names and appeared to work: CREATE and DROP
// truncated identically. The hazard is two names agreeing in their first 63
// bytes, where one run would drop another's database mid-test.
//
// The test name here is deliberately long, since that is the input that
// overflowed.
func TestFreshDatabaseNameFitsPostgresIdentifierLimitAndIsUniqueAcrossCallsInTheSameSecond(t *testing.T) {
	seen := map[string]bool{}
	for range 64 {
		name := freshDatabaseName(t)
		if len(name) > maxIdentifier {
			t.Fatalf("name %q is %d bytes, over the %d-byte limit; PostgreSQL would truncate it "+
				"silently", name, len(name), maxIdentifier)
		}
		if seen[name] {
			t.Fatalf("name %q was generated twice, so two runs starting together would collide "+
				"and one would drop the other's database", name)
		}
		seen[name] = true
	}
}

// TestOpenSeamRefusesANilKeyRegistryBeforeBuildingAClient is the sibling of
// the artifact-registry case for the registry item 3 threaded through: a
// caller that writes no configuration declares that with an empty registry,
// and nil is a mistake refused before any client is built.
func TestOpenSeamRefusesANilKeyRegistryBeforeBuildingAClient(t *testing.T) {
	types, err := registry.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = OpenSeam(context.Background(), Config{
		DSN: "postgres://example", Bucket: "b", RootKey: validRootKey(),
	}, types, nil)
	if err == nil {
		t.Fatal("OpenSeam accepted a nil configuration-key registry")
	}
	if !strings.Contains(err.Error(), "configuration-key registry") {
		t.Fatalf("the failure should name the key registry: %v", err)
	}
}

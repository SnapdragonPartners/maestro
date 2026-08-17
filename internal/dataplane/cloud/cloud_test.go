package cloud

import (
	"context"
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/dataplane/secret"
)

// These tests reach no cloud service. They cover the refusals that must hold
// before anything is built, and the one structural claim worth pinning: that
// this package never invents a root key.
//
// The live sequence — migrate from empty, open, round-trip, close — is in
// cloud_integration_test.go, because what it asserts is what the managed
// services actually do.

// TestConfigRefusesAnIncompleteConfiguration covers each input that cannot open
// a plane. Each would otherwise fail somewhere further from the cause.
func TestConfigRefusesAnIncompleteConfiguration(t *testing.T) {
	complete := func() Config {
		return Config{
			DSN:     "postgres://user:pw@127.0.0.1:5433/maestro?sslmode=disable",
			Bucket:  "maestro-objects",
			RootKey: []byte("thirty-two-bytes-of-key-material"),
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
		RootKey: []byte("thirty-two-bytes-of-key-material"),
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
		RootKey: []byte("material"),
	}, nil)
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

package stack

import (
	"testing"

	"orchestrator/internal/dataplane/configkeys"
	"orchestrator/internal/dataplane/harness"
	"orchestrator/internal/dataplane/plane"
	"orchestrator/internal/dataplane/registry"
	"orchestrator/internal/prompt"
)

// testHarness is a REAL semantic version inside the Phase 3 band, not "dev":
// a seam composed under "dev" records every declared-range comparison as
// not-evaluated (Phase 3 item 4 design, D8). planetest holds the shared copy;
// this package cannot import it, because planetest imports stack.
func testHarness(t *testing.T) harness.Version {
	t.Helper()
	running, err := harness.Parse("v2.0.0-phase.3.0.0")
	if err != nil {
		t.Fatalf("parse the test harness version: %v", err)
	}
	return running
}

// testCaller is a complete Caller around the registry under test: no
// configuration keys and no prompt slots, each said explicitly.
func testCaller(t *testing.T, types *registry.Registry) plane.Caller {
	t.Helper()
	return plane.Caller{
		Types:   types,
		Keys:    configkeys.MustNew(nil),
		Prompts: prompt.MustNew(nil),
		Harness: testHarness(t),
	}
}

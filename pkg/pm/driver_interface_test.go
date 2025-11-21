package pm

import (
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/utils"
)

// TestDriverInterfaceSatisfaction verifies that *Driver satisfies agent.Driver interface.
func TestDriverInterfaceSatisfaction(t *testing.T) {
	// This test will fail to compile if *Driver doesn't satisfy agent.Driver
	var _ agent.Driver = (*Driver)(nil)

	t.Log("✅ *pm.Driver satisfies agent.Driver interface at compile time")
}

// TestDriverRuntimeTypeAssertion verifies that SafeAssert works with PM Driver at runtime.
func TestDriverRuntimeTypeAssertion(t *testing.T) {
	// Create a nil PM driver (enough for type checking)
	var pmDriver *Driver

	// Test runtime type assertion using SafeAssert (same as dispatcher uses)
	// We need to wrap it in interface{} first to simulate how dispatcher does it
	var agentInterface interface{} = pmDriver
	driver, ok := utils.SafeAssert[agent.Driver](agentInterface)
	if !ok {
		t.Fatalf("❌ SafeAssert failed: *pm.Driver does not satisfy agent.Driver at runtime")
	}

	t.Logf("✅ SafeAssert succeeded: *pm.Driver satisfies agent.Driver at runtime")
	t.Logf("Driver type: %T", driver)
}

// TestDriverMapStorageAndRetrieval simulates how dispatcher stores and retrieves agents.
func TestDriverMapStorageAndRetrieval(t *testing.T) {
	// Create a nil PM driver
	var pmDriver *Driver = nil

	// Simulate dispatcher storage: store as Agent interface type
	type Agent interface {
		GetID() string
	}
	agents := make(map[string]Agent)
	agents["pm-001"] = pmDriver

	// Simulate dispatcher retrieval: get from map and assert to agent.Driver
	agentInterface := agents["pm-001"]
	driver, ok := utils.SafeAssert[agent.Driver](agentInterface)
	if !ok {
		t.Fatalf("❌ SafeAssert failed after map storage/retrieval")
	}

	t.Logf("✅ SafeAssert succeeded after map storage/retrieval")
	t.Logf("Driver type: %T", driver)
}

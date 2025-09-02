package tools

import (
	"context"
	"fmt"
	"testing"
	"time"

	"orchestrator/internal/runtime"
	"orchestrator/internal/state"
	"orchestrator/pkg/config"
)

// mockOrchestrator implements the Orchestrator interface for testing.
type mockOrchestrator struct {
	containers       map[string]*mockContainer // cid -> container
	runtimeState     *state.RuntimeState
	docker           *mockDocker
	repoURL          string
	lastBuiltImageID string
	nextContainerID  int

	// Test behaviors
	shouldFailStart  bool
	shouldFailHealth bool
}

type mockContainer struct {
	started time.Time
	role    state.Role
	cid     string
	name    string
	imageID string
}

type mockDocker struct {
	shouldFailExec          bool
	shouldFailCpToContainer bool
}

func (d *mockDocker) Exec(_ context.Context, _ string, _ ...string) ([]byte, error) {
	if d.shouldFailExec {
		return nil, fmt.Errorf("mock exec failure")
	}
	return []byte("mock exec output"), nil
}

func (d *mockDocker) CpToContainer(_ context.Context, _, _ string, _ []byte, _ int) error {
	if d.shouldFailCpToContainer {
		return fmt.Errorf("mock cp failure")
	}
	return nil
}

func newMockOrchestrator() *mockOrchestrator {
	return &mockOrchestrator{
		containers:       make(map[string]*mockContainer),
		nextContainerID:  1,
		runtimeState:     state.NewRuntimeState(),
		repoURL:          "https://github.com/test/repo.git",
		lastBuiltImageID: "sha256:latest",
		docker:           &mockDocker{},
	}
}

func (m *mockOrchestrator) StartContainer(_ context.Context, role state.Role, imageID string) (cid, name string, err error) {
	if m.shouldFailStart {
		return "", "", fmt.Errorf("mock start container failure")
	}

	cid = fmt.Sprintf("container-%d", m.nextContainerID)
	name = fmt.Sprintf("test-container-%d", m.nextContainerID)
	m.nextContainerID++

	container := &mockContainer{
		cid:     cid,
		name:    name,
		imageID: imageID,
		role:    role,
		started: time.Now(),
	}

	m.containers[cid] = container
	return cid, name, nil
}

func (m *mockOrchestrator) StopContainer(_ context.Context, cid string) error {
	delete(m.containers, cid)
	return nil
}

func (m *mockOrchestrator) HealthCheck(_ context.Context, _ string) error {
	if m.shouldFailHealth {
		return fmt.Errorf("mock health check failure")
	}
	return nil
}

func (m *mockOrchestrator) GetDocker() runtime.Docker {
	return m.docker
}

func (m *mockOrchestrator) GetRepoURL() string {
	return m.repoURL
}

func (m *mockOrchestrator) GetLastBuiltOrTestedImageID() string {
	return m.lastBuiltImageID
}

func (m *mockOrchestrator) GetRuntimeState() *state.RuntimeState {
	return m.runtimeState
}

func TestContainerSwitch_Success(t *testing.T) {
	// Setup config for testing
	setupTestConfig(t)
	defer cleanupTestConfig()

	ctx := context.Background()
	orchestrator := newMockOrchestrator()

	imageID := "sha256:abcdef123456"

	// Execute container switch
	result, err := ContainerSwitch(ctx, state.RoleTarget, imageID, orchestrator)

	// Verify success
	if err != nil {
		t.Fatalf("ContainerSwitch failed: %v", err)
	}

	if result.Status != "switched" {
		t.Errorf("Expected status 'switched', got '%s'", result.Status)
	}

	if result.ActiveImageID != imageID {
		t.Errorf("Expected active image ID %s, got %s", imageID, result.ActiveImageID)
	}

	// Verify runtime state
	active := orchestrator.runtimeState.GetActive()
	if active == nil {
		t.Fatalf("No active container after switch")
	}

	if active.ImageID != imageID {
		t.Errorf("Expected active container image %s, got %s", imageID, active.ImageID)
	}

	if active.Role != state.RoleTarget {
		t.Errorf("Expected active container role %s, got %s", state.RoleTarget, active.Role)
	}

	// Verify config
	pinnedImageID := config.GetPinnedImageID()
	if pinnedImageID != imageID {
		t.Errorf("Expected pinned image ID %s, got %s", imageID, pinnedImageID)
	}
}

func TestContainerSwitch_Idempotence(t *testing.T) {
	// Setup config for testing
	setupTestConfig(t)
	defer cleanupTestConfig()

	ctx := context.Background()
	orchestrator := newMockOrchestrator()

	imageID := "sha256:abcdef123456"

	// Set up existing state (already on target image)
	existingActive := &state.ActiveContainer{
		Role:    state.RoleTarget,
		CID:     "existing-container",
		ImageID: imageID,
		Name:    "existing-name",
		Started: time.Now().Add(-time.Hour),
	}
	orchestrator.runtimeState.SetActive(existingActive)
	_ = config.SetPinnedImageID(imageID)

	// Execute container switch (should be noop)
	result, err := ContainerSwitch(ctx, state.RoleTarget, imageID, orchestrator)

	// Verify noop
	if err != nil {
		t.Fatalf("ContainerSwitch failed: %v", err)
	}

	if result.Status != "noop" {
		t.Errorf("Expected status 'noop', got '%s'", result.Status)
	}

	// Verify no new containers were created
	if len(orchestrator.containers) > 0 {
		t.Errorf("Expected no new containers, but found %d", len(orchestrator.containers))
	}
}

func TestContainerSwitch_StartContainerFailure(t *testing.T) {
	// Setup config for testing
	setupTestConfig(t)
	defer cleanupTestConfig()

	ctx := context.Background()
	orchestrator := newMockOrchestrator()
	orchestrator.shouldFailStart = true

	imageID := "sha256:abcdef123456"

	// Execute container switch (should fail)
	result, err := ContainerSwitch(ctx, state.RoleTarget, imageID, orchestrator)

	// Verify failure
	if err == nil {
		t.Fatalf("Expected ContainerSwitch to fail, but it succeeded")
	}

	if result != nil {
		t.Errorf("Expected nil result on failure, got %+v", result)
	}

	// Verify no state changes
	active := orchestrator.runtimeState.GetActive()
	if active != nil {
		t.Errorf("Expected no active container, but found %+v", active)
	}
}

func TestContainerSwitch_HealthCheckFailure(t *testing.T) {
	// Setup config for testing
	setupTestConfig(t)
	defer cleanupTestConfig()

	ctx := context.Background()
	orchestrator := newMockOrchestrator()
	orchestrator.shouldFailHealth = true

	imageID := "sha256:abcdef123456"

	// Execute container switch (should fail)
	result, err := ContainerSwitch(ctx, state.RoleTarget, imageID, orchestrator)

	// Verify failure
	if err == nil {
		t.Fatalf("Expected ContainerSwitch to fail, but it succeeded")
	}

	if result != nil {
		t.Errorf("Expected nil result on failure, got %+v", result)
	}

	// Verify cleanup happened (no containers left)
	if len(orchestrator.containers) > 0 {
		t.Errorf("Expected cleanup of failed container, but found %d containers", len(orchestrator.containers))
	}
}

func TestContainerSwitch_PinWriteFailureWithRollback(t *testing.T) {
	// Setup config for testing
	setupTestConfig(t)
	defer cleanupTestConfig()

	ctx := context.Background()
	orchestrator := newMockOrchestrator()

	// Set up existing active container
	existingImageID := "sha256:existing123"
	existingActive := &state.ActiveContainer{
		Role:    state.RoleSafe,
		CID:     "existing-container",
		ImageID: existingImageID,
		Name:    "existing-name",
		Started: time.Now().Add(-time.Hour),
	}
	orchestrator.runtimeState.SetActive(existingActive)
	_ = config.SetPinnedImageID(existingImageID)

	// Force pin write failure by setting invalid container config
	config.UpdateContainer(nil) // This will cause SetPinnedImageID to fail

	newImageID := "sha256:newimage123"

	// Execute container switch (should fail and rollback)
	result, err := ContainerSwitch(ctx, state.RoleTarget, newImageID, orchestrator)

	// Verify failure
	if err == nil {
		t.Fatalf("Expected ContainerSwitch to fail due to pin write failure")
	}

	if result != nil {
		t.Errorf("Expected nil result on failure, got %+v", result)
	}

	// Verify rollback occurred - should have active container with previous image
	active := orchestrator.runtimeState.GetActive()
	if active == nil {
		t.Fatalf("Expected rollback to restore active container")
	}

	if active.ImageID != existingImageID {
		t.Errorf("Expected rollback to previous image %s, got %s", existingImageID, active.ImageID)
	}
}

// Helper functions for test setup

func setupTestConfig(t *testing.T) {
	// Create temporary directory for test config
	tempDir := t.TempDir()

	// Initialize config in temp directory
	if err := config.LoadConfig(tempDir); err != nil {
		t.Fatalf("Failed to initialize test config: %v", err)
	}

	// Update with minimal container config for testing
	containerConfig := &config.ContainerConfig{
		Name: "test-container",
	}
	if err := config.UpdateContainer(containerConfig); err != nil {
		t.Fatalf("Failed to setup test container config: %v", err)
	}
}

func cleanupTestConfig() {
	// Reset pinned image ID
	_ = config.SetPinnedImageID("")
	_ = config.SetSafeImageID("")
}

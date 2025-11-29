package demo

import (
	"context"
	"os/exec"
	"testing"
)

func TestNetworkManager_EnsureNetwork_EmptyName(t *testing.T) {
	m := NewNetworkManager()

	err := m.EnsureNetwork(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty network name")
	}
}

func TestNetworkManager_RemoveNetwork_EmptyName(t *testing.T) {
	m := NewNetworkManager()

	err := m.RemoveNetwork(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty network name")
	}
}

func TestNetworkManager_ConnectContainer_EmptyNetwork(t *testing.T) {
	m := NewNetworkManager()

	err := m.ConnectContainer(context.Background(), "", "container")
	if err == nil {
		t.Error("expected error for empty network name")
	}
}

func TestNetworkManager_ConnectContainer_EmptyContainer(t *testing.T) {
	m := NewNetworkManager()

	err := m.ConnectContainer(context.Background(), "network", "")
	if err == nil {
		t.Error("expected error for empty container name")
	}
}

func TestNetworkManager_DisconnectContainer_EmptyNetwork(t *testing.T) {
	m := NewNetworkManager()

	err := m.DisconnectContainer(context.Background(), "", "container")
	if err == nil {
		t.Error("expected error for empty network name")
	}
}

func TestNetworkManager_DisconnectContainer_EmptyContainer(t *testing.T) {
	m := NewNetworkManager()

	err := m.DisconnectContainer(context.Background(), "network", "")
	if err == nil {
		t.Error("expected error for empty container name")
	}
}

func TestNetworkManager_NetworkExists_EmptyName(t *testing.T) {
	m := NewNetworkManager()

	_, err := m.NetworkExists(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty network name")
	}
}

func TestNetworkManager_IsConnected_EmptyNetwork(t *testing.T) {
	m := NewNetworkManager()

	_, err := m.IsConnected(context.Background(), "", "container")
	if err == nil {
		t.Error("expected error for empty network name")
	}
}

func TestNetworkManager_IsConnected_EmptyContainer(t *testing.T) {
	m := NewNetworkManager()

	_, err := m.IsConnected(context.Background(), "network", "")
	if err == nil {
		t.Error("expected error for empty container name")
	}
}

func TestNetworkManager_ListNetworkContainers_EmptyName(t *testing.T) {
	m := NewNetworkManager()

	_, err := m.ListNetworkContainers(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty network name")
	}
}

// MockedNetworkManager tests with command injection

func TestNetworkManager_EnsureNetwork_AlreadyExists(t *testing.T) {
	callCount := 0
	m := &NetworkManager{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			callCount++
			// First call is network inspect (exists check)
			if callCount == 1 {
				// Return success = network exists
				return exec.CommandContext(ctx, "sh", "-c", "exit 0")
			}
			// Should not reach here
			t.Error("unexpected command call")
			return exec.CommandContext(ctx, "sh", "-c", "exit 1")
		},
	}

	err := m.EnsureNetwork(context.Background(), "test-network")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected 1 call (inspect only), got %d", callCount)
	}
}

func TestNetworkManager_EnsureNetwork_CreatesNew(t *testing.T) {
	callCount := 0
	m := &NetworkManager{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			callCount++
			if callCount == 1 {
				// First call: network inspect - not found
				return exec.CommandContext(ctx, "sh", "-c", "exit 1")
			}
			if callCount == 2 {
				// Second call: network create - success
				return exec.CommandContext(ctx, "sh", "-c", "echo 'network-id'")
			}
			t.Error("unexpected command call")
			return exec.CommandContext(ctx, "sh", "-c", "exit 1")
		},
	}

	err := m.EnsureNetwork(context.Background(), "test-network")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 calls (inspect + create), got %d", callCount)
	}
}

func TestNetworkManager_RemoveNetwork_Success(t *testing.T) {
	m := &NetworkManager{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "exit 0")
		},
	}

	err := m.RemoveNetwork(context.Background(), "test-network")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNetworkManager_RemoveNetwork_NotFound(t *testing.T) {
	m := &NetworkManager{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "echo 'No such network: test-network' >&2; exit 1")
		},
	}

	// Should not return error for non-existent network
	err := m.RemoveNetwork(context.Background(), "test-network")
	if err != nil {
		t.Errorf("expected no error for non-existent network, got: %v", err)
	}
}

func TestNetworkManager_NetworkExists_Found(t *testing.T) {
	m := &NetworkManager{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "exit 0")
		},
	}

	exists, err := m.NetworkExists(context.Background(), "test-network")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("expected network to exist")
	}
}

func TestNetworkManager_NetworkExists_NotFound(t *testing.T) {
	m := &NetworkManager{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "exit 1")
		},
	}

	exists, err := m.NetworkExists(context.Background(), "test-network")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if exists {
		t.Error("expected network to not exist")
	}
}

func TestNetworkManager_IsConnected_True(t *testing.T) {
	m := &NetworkManager{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			// Return container in network
			return exec.CommandContext(ctx, "sh", "-c", "echo 'container1 test-container container2'")
		},
	}

	connected, err := m.IsConnected(context.Background(), "test-network", "test-container")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !connected {
		t.Error("expected container to be connected")
	}
}

func TestNetworkManager_IsConnected_False(t *testing.T) {
	m := &NetworkManager{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			// Return containers not including target
			return exec.CommandContext(ctx, "sh", "-c", "echo 'container1 container2'")
		},
	}

	connected, err := m.IsConnected(context.Background(), "test-network", "test-container")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if connected {
		t.Error("expected container to not be connected")
	}
}

func TestNetworkManager_ListNetworkContainers_Success(t *testing.T) {
	m := &NetworkManager{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "echo 'container1 container2 container3'")
		},
	}

	containers, err := m.ListNetworkContainers(context.Background(), "test-network")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(containers) != 3 {
		t.Errorf("expected 3 containers, got %d", len(containers))
	}
}

func TestNetworkManager_ListNetworkContainers_Empty(t *testing.T) {
	m := &NetworkManager{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "echo ''")
		},
	}

	containers, err := m.ListNetworkContainers(context.Background(), "test-network")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(containers) != 0 {
		t.Errorf("expected 0 containers, got %d", len(containers))
	}
}

func TestNetworkManager_ListNetworkContainers_NetworkNotFound(t *testing.T) {
	m := &NetworkManager{
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "exit 1")
		},
	}

	containers, err := m.ListNetworkContainers(context.Background(), "test-network")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(containers) != 0 {
		t.Errorf("expected empty list for non-existent network, got %d", len(containers))
	}
}

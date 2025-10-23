package coder

import (
	"context"
	"sync"
	"testing"
	"time"

	"orchestrator/pkg/build"
	"orchestrator/pkg/proto"
)

// TestCoderStateNotificationWithBaseStateMachine verifies that real coder agents
// properly send state change notifications through BaseStateMachine.
func TestCoderStateNotificationWithBaseStateMachine(t *testing.T) {
	// Create a test coder with minimal setup
	agentID := "test-coder-notifications"
	workDir := t.TempDir()

	// Create minimal clone manager
	gitRunner := NewDefaultGitRunner()
	cloneManager := NewCloneManager(
		gitRunner,
		workDir,
		"https://github.com/test/repo.git",
		"main",
		".mirrors",
		"coder-*",
	)

	buildService := build.NewBuildService()

	// Create real coder
	coder, err := NewCoder(context.Background(), agentID, workDir, cloneManager, buildService, nil)
	if err != nil {
		t.Fatalf("Failed to create coder: %v", err)
	}

	// Create notification channel
	notificationCh := make(chan *proto.StateChangeNotification, 10)

	// Set up state notification channel
	coder.SetStateNotificationChannel(notificationCh)

	// Track received notifications
	var receivedNotifications []*proto.StateChangeNotification
	var mu sync.Mutex

	// Start notification collector
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case notification, ok := <-notificationCh:
				if !ok {
					return
				}
				mu.Lock()
				receivedNotifications = append(receivedNotifications, notification)
				t.Logf("Received notification: %s %s -> %s", notification.AgentID, notification.FromState, notification.ToState)
				mu.Unlock()
			case <-time.After(2 * time.Second):
				return // Timeout
			}
		}
	}()

	// Initialize coder
	ctx := context.Background()
	if initErr := coder.Initialize(ctx); initErr != nil {
		t.Fatalf("Failed to initialize coder: %v", initErr)
	}

	// Force a state transition using the BaseStateMachine
	// This simulates what happens when a coder finishes work
	initialState := coder.GetCurrentState()
	t.Logf("Initial coder state: %s", initialState)

	// Transition to DONE (this should send a notification)
	err = coder.BaseStateMachine.TransitionTo(ctx, proto.StateDone, map[string]any{
		"reason": "test_completion",
	})
	if err != nil {
		t.Fatalf("Failed to transition to DONE: %v", err)
	}

	// Verify state changed
	currentState := coder.GetCurrentState()
	if currentState != proto.StateDone {
		t.Errorf("Expected state to be DONE, got %s", currentState)
	}

	// Wait a bit for notification processing
	time.Sleep(100 * time.Millisecond)

	// Close notification channel and wait for collector to finish
	close(notificationCh)
	<-done

	// Verify we received the notification
	mu.Lock()
	defer mu.Unlock()

	if len(receivedNotifications) == 0 {
		t.Fatal("Expected to receive at least one state change notification, got none")
	}

	// Find the DONE transition notification
	found := false
	for _, notification := range receivedNotifications {
		if notification.AgentID == agentID && notification.ToState == proto.StateDone {
			found = true
			t.Logf("✅ Found expected DONE notification: %s -> %s", notification.FromState, notification.ToState)
			break
		}
	}

	if !found {
		t.Error("Did not receive expected state change notification for DONE transition")
		for i, notification := range receivedNotifications {
			t.Logf("Notification %d: %s %s -> %s", i, notification.AgentID, notification.FromState, notification.ToState)
		}
	}
}

// TestCoderStateNotificationChannelSetup verifies the SetStateNotificationChannel method.
func TestCoderStateNotificationChannelSetup(t *testing.T) {
	// Create a minimal coder
	agentID := "test-coder-channel-setup"
	workDir := t.TempDir()

	// Create minimal clone manager
	gitRunner := NewDefaultGitRunner()
	cloneManager := NewCloneManager(
		gitRunner,
		workDir,
		"https://github.com/test/repo.git",
		"main",
		".mirrors",
		"coder-*",
	)

	buildService := build.NewBuildService()

	// Create real coder
	coder, err := NewCoder(context.Background(), agentID, workDir, cloneManager, buildService, nil)
	if err != nil {
		t.Fatalf("Failed to create coder: %v", err)
	}

	// Create notification channel
	notificationCh := make(chan *proto.StateChangeNotification, 1)

	// Verify channel is initially not set (accessing private field for test)
	if coder.BaseStateMachine != nil {
		// BaseStateMachine should exist
		t.Log("✅ Coder has BaseStateMachine")
	} else {
		t.Fatal("❌ Coder missing BaseStateMachine")
	}

	// Set notification channel
	coder.SetStateNotificationChannel(notificationCh)
	t.Log("✅ Set state notification channel")

	// Verify it was set by triggering a transition
	ctx := context.Background()
	if initErr := coder.Initialize(ctx); initErr != nil {
		t.Fatalf("Failed to initialize coder: %v", initErr)
	}

	// Try to send a notification
	err = coder.BaseStateMachine.TransitionTo(ctx, proto.StateDone, nil)
	if err != nil {
		t.Fatalf("Failed to transition: %v", err)
	}

	// Check if we received notification (with timeout)
	select {
	case notification := <-notificationCh:
		t.Logf("✅ Received notification: %s %s -> %s", notification.AgentID, notification.FromState, notification.ToState)
	case <-time.After(1 * time.Second):
		t.Error("❌ Did not receive state change notification within timeout")
	}
}

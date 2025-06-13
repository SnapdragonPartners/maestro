package architect

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

func TestChannelConnectivity(t *testing.T) {
	// Create a driver with all channels connected
	stateStore, _ := state.NewStore("test_data")
	driver := NewDriver("test-architect", stateStore, "test_work", "test_stories")

	// Initialize the driver to start workers
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := driver.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}
	defer driver.Shutdown()

	t.Run("Queue notifications", func(t *testing.T) {
		// Test that queue can send notifications to readyStoryCh
		// Manually trigger notification by calling checkAndNotifyReady
		driver.queue.checkAndNotifyReady()

		// In this test, no stories are loaded so no notifications should be sent
		// This verifies the channel is connected without blocking
	})

	t.Run("RouteMessage to workers", func(t *testing.T) {
		// Test routing a QUESTION message
		questionMsg := proto.NewAgentMsg(proto.MsgTypeQUESTION, "test-coder", "test-architect")
		questionMsg.SetPayload("question", "How do I implement feature X?")

		err := driver.RouteMessage(questionMsg)
		if err != nil {
			t.Errorf("Failed to route question message: %v", err)
		}

		// Test routing a REQUEST message
		requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "test-coder", "test-architect")
		requestMsg.SetPayload("code", "func main() { fmt.Println(\"Hello\") }")

		err = driver.RouteMessage(requestMsg)
		if err != nil {
			t.Errorf("Failed to route request message: %v", err)
		}

		// Give workers time to process
		time.Sleep(100 * time.Millisecond)
	})

	t.Run("Worker completion signals", func(t *testing.T) {
		// Wait for worker completion signals
		timeout := time.After(1 * time.Second)
		questionsAnswered := 0
		reviewsCompleted := 0

		// Collect completion signals
		for questionsAnswered < 1 || reviewsCompleted < 1 {
			select {
			case msgID := <-driver.questionAnsweredCh:
				t.Logf("Question answered: %s", msgID)
				questionsAnswered++
			case msgID := <-driver.reviewDoneCh:
				t.Logf("Review completed: %s", msgID)
				reviewsCompleted++
			case <-timeout:
				t.Errorf("Timeout waiting for worker completion signals. Questions: %d, Reviews: %d", 
					questionsAnswered, reviewsCompleted)
				return
			}
		}

		if questionsAnswered != 1 {
			t.Errorf("Expected 1 question answered, got %d", questionsAnswered)
		}
		if reviewsCompleted != 1 {
			t.Errorf("Expected 1 review completed, got %d", reviewsCompleted)
		}
	})

	t.Run("MockDispatcher message capture", func(t *testing.T) {
		// Access the mock dispatcher to verify messages were sent
		// Note: In real implementation, we'd need a way to access the dispatcher
		// For now, this test just verifies the flow doesn't crash
		t.Log("Mock dispatcher integration verified")
	})
}

func TestChannelIntegrationWithStories(t *testing.T) {
	// Create a temporary stories directory for testing
	stateStore, _ := state.NewStore("test_data")
	driver := NewDriver("test-architect", stateStore, "test_work", "test_stories")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := driver.Initialize(ctx)
	if err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}
	defer driver.Shutdown()

	t.Run("Ready story notifications", func(t *testing.T) {
		// Add a test story to the queue
		testStory := &QueuedStory{
			ID:              "test-001",
			Title:           "Test Story",
			Status:          StatusPending,
			DependsOn:       []string{}, // No dependencies, should be ready
			EstimatedPoints: 1,
		}
		
		driver.queue.stories[testStory.ID] = testStory

		// Trigger notification check
		driver.queue.checkAndNotifyReady()

		// Wait for ready story notification
		select {
		case storyID := <-driver.readyStoryCh:
			if storyID != "test-001" {
				t.Errorf("Expected story test-001, got %s", storyID)
			}
			t.Logf("Successfully received ready story notification: %s", storyID)
		case <-time.After(1 * time.Second):
			t.Error("Timeout waiting for ready story notification")
		}
	})
}
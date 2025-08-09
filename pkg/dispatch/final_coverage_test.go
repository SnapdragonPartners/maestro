package dispatch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/proto"
)

// TestReportErrorChannelFull tests the error channel full path.
func TestReportErrorChannelFull(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Fill up the error channel to trigger the default case
	// Error channel capacity is typically small
	for i := 0; i < 10; i++ {
		dispatcher.ReportError(fmt.Sprintf("agent-%d", i), fmt.Errorf("error %d", i), Warn)
	}

	// Now report one more error that should trigger the "channel full" path
	dispatcher.ReportError("overflow-agent", fmt.Errorf("overflow error"), Fatal)
}

// TestMetricsMonitorWithTicker attempts to trigger the ticker case by using a very short ticker interval.
func TestMetricsMonitorWithTicker(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Fill the story channel to trigger high utilization detection
	capacity := cap(dispatcher.storyCh)
	fillCount := capacity * 90 / 100 // 90% utilization to trigger monitoring

loop:
	for i := 0; i < fillCount; i++ {
		msg := proto.NewAgentMsg(proto.MsgTypeSTORY, "orchestrator", "test-agent")
		msg.SetPayload(proto.KeyStoryID, fmt.Sprintf("metrics-story-%d", i))

		select {
		case dispatcher.storyCh <- msg:
			// Message sent
		default:
			// Channel full, stop
			break loop
		}
	}

	// Briefly start the dispatcher to run metrics monitor
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := dispatcher.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}

	// Wait a brief moment for metrics monitor to potentially run
	time.Sleep(10 * time.Millisecond)

	// Stop dispatcher
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer stopCancel()
	dispatcher.Stop(stopCtx)
}

// TestProcessMessageTypeTimeout tests timeout paths in processMessage.
func TestProcessMessageTypeTimeout(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test ERROR message processing
	errorMsg := proto.NewAgentMsg(proto.MsgTypeERROR, "test-agent", "target-agent")
	errorMsg.SetPayload(proto.KeyContent, "Test error message")

	ctx := context.Background()
	dispatcher.processMessage(ctx, errorMsg)

	// Test SHUTDOWN message processing
	shutdownMsg := proto.NewAgentMsg(proto.MsgTypeSHUTDOWN, "orchestrator", "test-agent")
	dispatcher.processMessage(ctx, shutdownMsg)
}

// TestMessageProcessorEdgeCases tests more edge cases in message processing.
func TestMessageProcessorEdgeCases(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Add an agent to process messages for
	agent := &mockChannelReceiver{
		mockAgent: mockAgent{id: "edge-case-agent"},
		agentType: agent.TypeCoder,
	}
	dispatcher.Attach(agent)

	// Start dispatcher
	err := dispatcher.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}

	// Send various message types to exercise different paths
	messages := []*proto.AgentMsg{
		proto.NewAgentMsg(proto.MsgTypeSTORY, "orchestrator", "edge-case-agent"),
		proto.NewAgentMsg(proto.MsgTypeREQUEST, "edge-case-agent", "architect"),
		proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect", "edge-case-agent"),
		proto.NewAgentMsg(proto.MsgTypeERROR, "orchestrator", "edge-case-agent"),
		proto.NewAgentMsg(proto.MsgTypeSHUTDOWN, "orchestrator", "edge-case-agent"),
	}

	// Set required payloads
	messages[0].SetPayload(proto.KeyStoryID, "edge-story")
	messages[0].SetPayload(proto.KeyTitle, "Edge Case Story")
	messages[0].SetPayload(proto.KeyRequirements, "Test requirements")

	messages[1].SetPayload(proto.KeyKind, "question")
	messages[1].SetPayload(proto.KeyContent, "Test question")

	messages[2].SetPayload(proto.KeyKind, "answer")
	messages[2].SetPayload(proto.KeyContent, "Test answer")

	messages[3].SetPayload(proto.KeyContent, "Test error")

	for _, msg := range messages {
		err := dispatcher.DispatchMessage(msg)
		if err != nil {
			t.Logf("Message dispatch result for %s: %v", msg.Type, err)
		}
	}

	// Give time for message processing
	time.Sleep(20 * time.Millisecond)

	// Stop dispatcher
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer stopCancel()
	dispatcher.Stop(stopCtx)
}

// TestSendResponseEdgeCases tests more edge cases in sendResponse.
func TestSendResponseEdgeCases(t *testing.T) {
	dispatcher := createTestDispatcher(t)

	// Test with message that has no kind payload
	responseMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect", "test-agent")
	// Don't set kind payload to test fallback path
	responseMsg.SetPayload(proto.KeyContent, "Response without kind")

	dispatcher.sendResponse(responseMsg)

	// Test with message that has invalid kind payload
	responseMsg2 := proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect", "test-agent-2")
	responseMsg2.SetPayload(proto.KeyKind, 123) // Invalid type (not string)
	responseMsg2.SetPayload(proto.KeyContent, "Response with invalid kind")

	dispatcher.sendResponse(responseMsg2)
}

// TestDispatchMessageStoryChannelFull tests the story channel full path.
func TestDispatchMessageStoryChannelFull(t *testing.T) {
	dispatcher := createTestDispatcher(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Start dispatcher
	err := dispatcher.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}

	// Try to fill the story channel beyond capacity
	capacity := cap(dispatcher.storyCh)

	for i := 0; i <= capacity+5; i++ {
		storyMsg := proto.NewAgentMsg(proto.MsgTypeSTORY, "orchestrator", "test-agent")
		storyMsg.SetPayload(proto.KeyStoryID, fmt.Sprintf("overflow-story-%d", i))
		storyMsg.SetPayload(proto.KeyTitle, "Overflow Test")
		storyMsg.SetPayload(proto.KeyRequirements, "Test story channel overflow")

		err := dispatcher.DispatchMessage(storyMsg)
		if err != nil {
			t.Logf("Story channel overflow at message %d: %v", i, err)
			break
		}
	}

	// Stop dispatcher
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer stopCancel()
	dispatcher.Stop(stopCtx)
}

package eventlog

import (
	"fmt"
	"os"
	"testing"

	"orchestrator/pkg/proto"
)

func ExampleWriter_usage() {
	// Create a temporary directory for this example
	tmpDir, err := os.MkdirTemp("", "eventlog_example")
	if err != nil {
		fmt.Printf("Failed to create temp dir: %v\n", err)
		return
	}
	defer os.RemoveAll(tmpDir)

	fmt.Println("=== Event Log Demo ===")

	// Create event log writer
	writer, err := NewWriter(tmpDir, 24)
	if err != nil {
		fmt.Printf("Failed to create writer: %v\n", err)
		return
	}
	defer writer.Close()

	// Simulate orchestrator workflow with logged events

	// 1. Architect creates a task
	taskMsg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
	taskMsg.SetPayload("story_id", "001")
	taskMsg.SetPayload("content", "Implement health endpoint")
	taskMsg.SetPayload("requirements", []string{"GET /health", "return 200 OK", "JSON response"})
	taskMsg.SetMetadata("priority", "high")

	writer.WriteMessage(taskMsg)
	fmt.Printf("📝 Logged TASK: architect → claude (story 001)\n")

	// 2. Claude processes and returns result
	resultMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, "claude", "architect")
	resultMsg.ParentMsgID = taskMsg.ID
	resultMsg.SetPayload("status", "completed")
	resultMsg.SetPayload("files_created", []string{"health.go", "health_test.go"})
	resultMsg.SetMetadata("execution_time", "2.5s")
	resultMsg.SetMetadata("tokens_used", "450")

	writer.WriteMessage(resultMsg)
	fmt.Printf("📝 Logged RESULT: claude → architect (task completed)\n")

	// 3. Claude encounters an error on next task
	errorMsg := proto.NewAgentMsg(proto.MsgTypeERROR, "claude", "architect")
	errorMsg.SetPayload("error", "API rate limit exceeded")
	errorMsg.SetPayload("retry_after", "60s")
	errorMsg.SetMetadata("error_code", "429")

	writer.WriteMessage(errorMsg)
	fmt.Printf("📝 Logged ERROR: claude → architect (rate limit)\n")

	// 4. Claude asks a question
	questionMsg := proto.NewAgentMsg(proto.MsgTypeQUESTION, "claude", "architect")
	questionMsg.SetPayload("question", "Should I use goroutines for concurrent request handling?")
	questionMsg.SetPayload("context", "Health endpoint implementation")

	writer.WriteMessage(questionMsg)
	fmt.Printf("📝 Logged QUESTION: claude → architect (design question)\n")

	// 5. Orchestrator initiates shutdown
	shutdownMsg := proto.NewAgentMsg(proto.MsgTypeSHUTDOWN, "orchestrator", "all")
	shutdownMsg.SetPayload("reason", "User requested shutdown")
	shutdownMsg.SetPayload("timeout", "30s")

	writer.WriteMessage(shutdownMsg)
	fmt.Printf("📝 Logged SHUTDOWN: orchestrator → all agents\n")

	// Read back all events
	currentLogFile := writer.GetCurrentLogFile()
	messages, err := ReadMessages(currentLogFile)
	if err != nil {
		fmt.Printf("Failed to read messages: %v\n", err)
		return
	}

	fmt.Printf("\n📋 Event Log Summary: %d events recorded\n", len(messages))

	// Show event details
	for i, msg := range messages {
		fmt.Printf("  %d. [%s] %s → %s: %s\n",
			i+1,
			msg.Timestamp.Format("15:04:05"),
			msg.FromAgent,
			msg.ToAgent,
			msg.Type)
	}

	fmt.Printf("\n💾 Log file: %s\n", currentLogFile)
	fmt.Println("=== End Demo ===")
}

func TestEventLogUsage(t *testing.T) {
	ExampleWriter_usage()
}

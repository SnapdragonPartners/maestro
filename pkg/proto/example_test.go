package proto

import (
	"fmt"
	"testing"
)

func ExampleAgentMsg_usage() {
	// Create a STORY message from architect to claude.
	storyMsg := NewAgentMsg(MsgTypeSTORY, "architect", "claude")

	// Build story payload with typed generic payload
	storyPayload := map[string]any{
		"content":      "Implement health endpoint",
		"requirements": []string{"GET /health", "return 200 OK", "JSON response"},
	}
	storyMsg.SetTypedPayload(NewGenericPayload(PayloadKindStory, storyPayload))
	storyMsg.SetMetadata("story_id", "001")
	storyMsg.SetMetadata("priority", "high")
	storyMsg.SetMetadata("estimated_points", "1")

	// Convert to JSON for transmission.
	jsonData, err := storyMsg.ToJSON()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("STORY Message JSON:\n%s\n\n", jsonData)

	// Claude receives and processes the story, then creates a RESULT message.
	resultMsg := NewAgentMsg(MsgTypeRESPONSE, "claude", "architect")
	resultMsg.ParentMsgID = storyMsg.ID

	// Build result payload with typed generic payload
	resultPayload := map[string]any{
		"status": "completed",
		"implementation": `
package main

import (
	"encoding/json"
	"net/http"
)

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}`,
		"tests_passed": true,
	}
	resultMsg.SetTypedPayload(NewGenericPayload(PayloadKindGeneric, resultPayload))
	resultMsg.SetMetadata("execution_time", "2.5s")

	resultJSON, err := resultMsg.ToJSON()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("RESULT Message JSON:\n%s\n", resultJSON)
}

func TestExampleUsage(_ *testing.T) {
	// This test demonstrates the message protocol in action.
	ExampleAgentMsg_usage()
}

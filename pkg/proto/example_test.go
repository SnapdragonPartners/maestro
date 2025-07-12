package proto

import (
	"fmt"
	"testing"
)

func ExampleAgentMsg_usage() {
	// Create a STORY message from architect to claude
	storyMsg := NewAgentMsg(MsgTypeSTORY, "architect", "claude")
	storyMsg.SetPayload("story_id", "001")
	storyMsg.SetPayload("content", "Implement health endpoint")
	storyMsg.SetPayload("requirements", []string{"GET /health", "return 200 OK", "JSON response"})
	storyMsg.SetMetadata("priority", "high")
	storyMsg.SetMetadata("estimated_points", "1")

	// Convert to JSON for transmission
	jsonData, err := storyMsg.ToJSON()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("STORY Message JSON:\n%s\n\n", jsonData)

	// Claude receives and processes the story, then creates a RESULT message
	resultMsg := NewAgentMsg(MsgTypeRESULT, "claude", "architect")
	resultMsg.ParentMsgID = storyMsg.ID
	resultMsg.SetPayload("status", "completed")
	resultMsg.SetPayload("implementation", `
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
}`)
	resultMsg.SetPayload("tests_passed", true)
	resultMsg.SetMetadata("execution_time", "2.5s")

	resultJSON, err := resultMsg.ToJSON()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("RESULT Message JSON:\n%s\n", resultJSON)
}

func TestExampleUsage(t *testing.T) {
	// This test demonstrates the message protocol in action
	ExampleAgentMsg_usage()
}

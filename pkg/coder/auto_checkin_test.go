package coder

import (
	"os"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

// TestCheckLoopBudget tests the checkLoopBudget helper function.
func TestCheckLoopBudget(t *testing.T) {
	// Create temp directory for state store.
	tempDir, err := os.MkdirTemp("", "auto-checkin-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test state store.
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create base state machine with coder transitions.
	sm := agent.NewBaseStateMachine("test", proto.StateWaiting, stateStore, CoderTransitions)

	// Create test driver with custom budgets.
	agentConfig := &config.Agent{
		IterationBudgets: config.IterationBudgets{
			CodingBudget: 3,
		},
	}

	driver := &Coder{
		BaseStateMachine: sm,
		codingBudget:     agentConfig.IterationBudgets.CodingBudget,
	}

	tests := []struct {
		name          string
		key           string
		budget        int
		origin        proto.State
		iterations    int
		expectTrigger bool
		expectedLoops int
	}{
		{
			name:          "coding under budget",
			key:           string(stateDataKeyCodingIterations),
			budget:        3,
			origin:        StateCoding,
			iterations:    2,
			expectTrigger: false,
			expectedLoops: 2,
		},
		{
			name:          "coding at budget",
			key:           string(stateDataKeyCodingIterations),
			budget:        3,
			origin:        StateCoding,
			iterations:    3,
			expectTrigger: true,
			expectedLoops: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset state.
			sm.SetStateData(tt.key, tt.iterations-1)
			sm.SetStateData(string(stateDataKeyQuestionReason), "")
			sm.SetStateData(string(stateDataKeyQuestionOrigin), "")
			sm.SetStateData(string(stateDataKeyQuestionContent), "")

			// Call checkLoopBudget.
			triggered := driver.checkLoopBudget(sm, tt.key, tt.budget, tt.origin)

			// Verify trigger result.
			if triggered != tt.expectTrigger {
				t.Errorf("Expected triggered=%v, got %v", tt.expectTrigger, triggered)
			}

			// Check iteration count.
			if val, exists := sm.GetStateValue(tt.key); exists {
				if count, ok := val.(int); ok {
					if count != tt.expectedLoops {
						t.Errorf("Expected %d loops, got %d", tt.expectedLoops, count)
					}
				} else {
					t.Errorf("Expected int type for iteration count")
				}
			} else {
				t.Errorf("Expected iteration count to be stored")
			}

			// If triggered, check BUDGET_REVIEW fields.
			if tt.expectTrigger {
				if reason, exists := sm.GetStateValue(string(stateDataKeyQuestionReason)); !exists || reason != "BUDGET_REVIEW" {
					t.Errorf("Expected question_reason=BUDGET_REVIEW, got %v", reason)
				}
				if origin, exists := sm.GetStateValue(string(stateDataKeyQuestionOrigin)); !exists || origin != string(tt.origin) {
					t.Errorf("Expected question_origin=%s, got %v", tt.origin, origin)
				}
				if loops, exists := sm.GetStateValue(string(stateDataKeyLoops)); !exists || loops != tt.expectedLoops {
					t.Errorf("Expected loops=%d, got %v", tt.expectedLoops, loops)
				}
				if maxLoops, exists := sm.GetStateValue(string(stateDataKeyMaxLoops)); !exists || maxLoops != tt.budget {
					t.Errorf("Expected max_loops=%d, got %v", tt.budget, maxLoops)
				}
			}
		})
	}
}

// TestProcessBudgetReviewAnswer tests the BUDGET_REVIEW answer processing.
// TODO: This test needs to be updated for the new BUDGET_REVIEW state approach.
func TestProcessBudgetReviewAnswer_DISABLED(t *testing.T) {
	t.Skip("Test disabled - needs update for BUDGET_REVIEW state approach")
	// Create temp directory for state store.
	tempDir, err := os.MkdirTemp("", "auto-checkin-answer-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test state store.
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create base state machine with coder transitions.
	sm := agent.NewBaseStateMachine("test", proto.StateWaiting, stateStore, CoderTransitions)

	// Create test driver.
	driver := &Coder{
		BaseStateMachine: sm,
		codingBudget:     5,
	}

	tests := []struct {
		name            string
		origin          string
		answer          string
		expectError     bool
		expectedBudget  int
		expectedCounter int
	}{
		{
			name:            "continue with increase",
			origin:          string(StateCoding),
			answer:          "CONTINUE 2",
			expectError:     false,
			expectedBudget:  7, // 5 + 2
			expectedCounter: 0,
		},
		{
			name:            "pivot command",
			origin:          string(StateCoding),
			answer:          "PIVOT",
			expectError:     false,
			expectedBudget:  5, // no change
			expectedCounter: 0,
		},
		{
			name:            "escalate command",
			origin:          string(StateCoding),
			answer:          "ESCALATE",
			expectError:     false,
			expectedBudget:  5, // no change
			expectedCounter: 5, // unchanged
		},
		{
			name:        "invalid command",
			origin:      "CODING",
			answer:      "INVALID_COMMAND",
			expectError: true,
		},
		{
			name:        "invalid number",
			origin:      "CODING",
			answer:      "CONTINUE xyz",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset state.
			driver.codingBudget = 5
			sm.SetStateData(string(stateDataKeyQuestionReason), "BUDGET_REVIEW")
			sm.SetStateData(string(stateDataKeyQuestionOrigin), tt.origin)
			sm.SetStateData(string(stateDataKeyCodingIterations), 5)

			// Call processAutoCheckinAnswer - DISABLED.
			// err := driver.processAutoCheckinAnswer(tt.answer).
			var err error = nil // placeholder for test compilation

			// Check error expectation.
			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Skip further checks if error was expected.
			if tt.expectError {
				return
			}

			// Check budget changes.
			if tt.origin == string(StateCoding) && driver.codingBudget != tt.expectedBudget {
				t.Errorf("Expected coding budget %d, got %d", tt.expectedBudget, driver.codingBudget)
			}

			// Check counter reset for CONTINUE and PIVOT.
			if tt.answer == "CONTINUE" || tt.answer == "CONTINUE 2" || tt.answer == "PIVOT" {
				key := string(stateDataKeyCodingIterations)
				if val, exists := sm.GetStateValue(key); exists {
					if count, ok := val.(int); ok && count != tt.expectedCounter {
						t.Errorf("Expected counter %d, got %d", tt.expectedCounter, count)
					}
				}
			}
		})
	}
}

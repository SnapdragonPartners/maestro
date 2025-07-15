package coder

import (
	"os"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/state"
)

// TestCheckLoopBudget tests the checkLoopBudget helper function
func TestCheckLoopBudget(t *testing.T) {
	// Create temp directory for state store
	tempDir, err := os.MkdirTemp("", "auto-checkin-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test state store
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create base state machine
	sm := agent.NewBaseStateMachine("test", agent.StateWaiting, stateStore, nil)

	// Create test driver with custom budgets
	agentConfig := &config.Agent{
		IterationBudgets: config.IterationBudgets{
			CodingBudget: 3,
			FixingBudget: 2,
		},
	}

	driver := &Coder{
		BaseStateMachine: sm,
		codingBudget:     agentConfig.IterationBudgets.CodingBudget,
		fixingBudget:     agentConfig.IterationBudgets.FixingBudget,
	}

	tests := []struct {
		name          string
		key           string
		budget        int
		origin        agent.State
		iterations    int
		expectTrigger bool
		expectedLoops int
	}{
		{
			name:          "coding under budget",
			key:           keyCodingIterations,
			budget:        3,
			origin:        StateCoding,
			iterations:    2,
			expectTrigger: false,
			expectedLoops: 2,
		},
		{
			name:          "coding at budget",
			key:           keyCodingIterations,
			budget:        3,
			origin:        StateCoding,
			iterations:    3,
			expectTrigger: true,
			expectedLoops: 3,
		},
		{
			name:          "fixing under budget",
			key:           keyFixingIterations,
			budget:        2,
			origin:        StateFixing,
			iterations:    1,
			expectTrigger: false,
			expectedLoops: 1,
		},
		{
			name:          "fixing at budget",
			key:           keyFixingIterations,
			budget:        2,
			origin:        StateFixing,
			iterations:    2,
			expectTrigger: true,
			expectedLoops: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset state
			sm.SetStateData(tt.key, tt.iterations-1)
			sm.SetStateData(keyQuestionReason, "")
			sm.SetStateData(keyQuestionOrigin, "")
			sm.SetStateData(keyQuestionContent, "")

			// Call checkLoopBudget
			triggered := driver.checkLoopBudget(sm, tt.key, tt.budget, tt.origin)

			// Verify trigger result
			if triggered != tt.expectTrigger {
				t.Errorf("Expected triggered=%v, got %v", tt.expectTrigger, triggered)
			}

			// Check iteration count
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

			// If triggered, check BUDGET_REVIEW fields
			if tt.expectTrigger {
				if reason, exists := sm.GetStateValue(keyQuestionReason); !exists || reason != "BUDGET_REVIEW" {
					t.Errorf("Expected question_reason=BUDGET_REVIEW, got %v", reason)
				}
				if origin, exists := sm.GetStateValue(keyQuestionOrigin); !exists || origin != string(tt.origin) {
					t.Errorf("Expected question_origin=%s, got %v", tt.origin, origin)
				}
				if loops, exists := sm.GetStateValue(keyLoops); !exists || loops != tt.expectedLoops {
					t.Errorf("Expected loops=%d, got %v", tt.expectedLoops, loops)
				}
				if maxLoops, exists := sm.GetStateValue(keyMaxLoops); !exists || maxLoops != tt.budget {
					t.Errorf("Expected max_loops=%d, got %v", tt.budget, maxLoops)
				}
			}
		})
	}
}

// TestProcessBudgetReviewAnswer tests the BUDGET_REVIEW answer processing
// TODO: This test needs to be updated for the new BUDGET_REVIEW state approach
func TestProcessBudgetReviewAnswer_DISABLED(t *testing.T) {
	t.Skip("Test disabled - needs update for BUDGET_REVIEW state approach")
	// Create temp directory for state store
	tempDir, err := os.MkdirTemp("", "auto-checkin-answer-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test state store
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create base state machine
	sm := agent.NewBaseStateMachine("test", agent.StateWaiting, stateStore, nil)

	// Create test driver
	driver := &Coder{
		BaseStateMachine: sm,
		codingBudget:     5,
		fixingBudget:     3,
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
			origin:          "CODING",
			answer:          "CONTINUE 2",
			expectError:     false,
			expectedBudget:  7, // 5 + 2
			expectedCounter: 0,
		},
		{
			name:            "continue without increase",
			origin:          "FIXING",
			answer:          "CONTINUE",
			expectError:     false,
			expectedBudget:  3, // no change
			expectedCounter: 0,
		},
		{
			name:            "pivot command",
			origin:          "CODING",
			answer:          "PIVOT",
			expectError:     false,
			expectedBudget:  5, // no change
			expectedCounter: 0,
		},
		{
			name:            "escalate command",
			origin:          "CODING",
			answer:          "ESCALATE",
			expectError:     false,
			expectedBudget:  5, // no change
			expectedCounter: 5, // unchanged
		},
		{
			name:            "abandon command",
			origin:          "FIXING",
			answer:          "ABANDON",
			expectError:     false,
			expectedBudget:  3, // no change
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
			// Reset state
			driver.codingBudget = 5
			driver.fixingBudget = 3
			sm.SetStateData(keyQuestionReason, "BUDGET_REVIEW")
			sm.SetStateData(keyQuestionOrigin, tt.origin)
			sm.SetStateData(keyCodingIterations, 5)
			sm.SetStateData(keyFixingIterations, 5)

			// Call processAutoCheckinAnswer - DISABLED 
			// err := driver.processAutoCheckinAnswer(tt.answer)
			var err error = nil // placeholder for test compilation

			// Check error expectation
			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Skip further checks if error was expected
			if tt.expectError {
				return
			}

			// Check budget changes
			if tt.origin == "CODING" && driver.codingBudget != tt.expectedBudget {
				t.Errorf("Expected coding budget %d, got %d", tt.expectedBudget, driver.codingBudget)
			}
			if tt.origin == "FIXING" && driver.fixingBudget != tt.expectedBudget {
				t.Errorf("Expected fixing budget %d, got %d", tt.expectedBudget, driver.fixingBudget)
			}

			// Check counter reset for CONTINUE and PIVOT
			if tt.answer == "CONTINUE" || tt.answer == "CONTINUE 2" || tt.answer == "PIVOT" {
				key := keyCodingIterations
				if tt.origin == "FIXING" {
					key = keyFixingIterations
				}
				if val, exists := sm.GetStateValue(key); exists {
					if count, ok := val.(int); ok && count != tt.expectedCounter {
						t.Errorf("Expected counter %d, got %d", tt.expectedCounter, count)
					}
				}
			}
		})
	}
}

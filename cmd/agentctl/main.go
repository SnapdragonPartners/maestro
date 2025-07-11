package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"orchestrator/pkg/architect"
	"orchestrator/pkg/coder"
	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

// processWithApprovals handles the full workflow including auto-approvals in standalone mode
func processWithApprovals(ctx context.Context, agent *coder.Coder, msg *proto.AgentMsg, isLiveMode bool) (*proto.AgentMsg, error) {
	maxIterations := 10 // Prevent infinite loops

	for i := 0; i < maxIterations; i++ {
		fmt.Fprintf(os.Stderr, "[DEBUG] Iteration %d, processing message type: %s\n", i, msg.Type)
		fmt.Fprintf(os.Stderr, "[DEBUG] Current agent state: %s\n", agent.GetDriver().GetCurrentState())

		// Process the message
		result, err := agent.ProcessMessage(ctx, msg)
		if err != nil {
			return nil, err
		}

		fmt.Fprintf(os.Stderr, "[DEBUG] Got result type: %s, new state: %s\n", result.Type, agent.GetDriver().GetCurrentState())

		// Check if this is a completion message (RESULT with status completed)
		if result.Type == proto.MsgTypeRESULT {
			if status, exists := result.GetPayload("status"); exists {
				if statusStr, ok := status.(string); ok && statusStr == "completed" {
					return result, nil // Task completed successfully
				}
			}
		}

		// Handle REQUEST messages (approval requests) in standalone mode
		if result.Type == proto.MsgTypeREQUEST && isLiveMode {
			if requestType, exists := result.GetPayload("request_type"); exists {
				if requestTypeStr, ok := requestType.(string); ok && requestTypeStr == "approval" {
					// Auto-approve using the internal approval system
					approvalType, _ := result.GetPayload("approval_type")
					approvalTypeStr, _ := approvalType.(string)

					fmt.Fprintf(os.Stderr, "[Auto-approving] %s request\n", approvalTypeStr)

					// Process approval directly through the driver
					if err := agent.GetDriver().ProcessApprovalResult("APPROVED", approvalTypeStr); err != nil {
						return nil, fmt.Errorf("failed to process approval: %w", err)
					}

					// Clear the pending request
					agent.GetDriver().ClearPendingApprovalRequest()

					// Now step the state machine to continue processing
					done, err := agent.GetDriver().Step(ctx)
					if err != nil {
						return nil, fmt.Errorf("failed to step state machine: %w", err)
					}

					if done {
						// Create final result message
						finalResult := proto.NewAgentMsg(proto.MsgTypeRESULT, agent.GetID(), "agentctl")
						finalResult.SetPayload("status", "completed")
						finalResult.SetPayload("current_state", string(agent.GetDriver().GetCurrentState()))
						return finalResult, nil
					}

					// Continue with next iteration without changing the message
					continue
				}
			}
		}

		// In standalone mode, check for pending approvals and auto-approve them
		if isLiveMode {
			if hasPending, _, content, reason, approvalType := agent.GetDriver().GetPendingApprovalRequest(); hasPending {
				fmt.Fprintf(os.Stderr, "[Auto-approving] %s: %s\n", reason, content[:min(100, len(content))])

				// Use the approval type from the pending request
				approvalTypeStr := string(approvalType)

				// Auto-approve
				if err := agent.GetDriver().ProcessApprovalResult("APPROVED", approvalTypeStr); err != nil {
					return nil, fmt.Errorf("failed to process approval: %w", err)
				}

				// Clear the pending request
				agent.GetDriver().ClearPendingApprovalRequest()

				// Continue processing with a dummy continue message
				continueMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, "agentctl", agent.GetID())
				continueMsg.SetPayload("approval_status", "APPROVED")
				continueMsg.SetPayload("approval_type", approvalTypeStr)
				msg = continueMsg
				continue
			}

			// Check for pending questions and auto-answer them
			if hasPending, _, content, reason := agent.GetDriver().GetPendingQuestion(); hasPending {
				fmt.Fprintf(os.Stderr, "[Auto-answering] %s: %s\n", reason, content[:min(100, len(content))])

				// Provide a generic helpful answer
				answer := "Please proceed with your best judgment. Focus on clean, well-documented code that follows best practices."
				if err := agent.GetDriver().ProcessAnswer(answer); err != nil {
					return nil, fmt.Errorf("failed to process answer: %w", err)
				}

				// Clear the pending question
				agent.GetDriver().ClearPendingQuestion()

				// Continue processing with a dummy continue message
				continueMsg := proto.NewAgentMsg(proto.MsgTypeANSWER, "agentctl", agent.GetID())
				continueMsg.SetPayload("answer", answer)
				msg = continueMsg
				continue
			}
		}

		// If we get here with no pending items, return the result
		return result, nil
	}

	return nil, fmt.Errorf("exceeded maximum iterations (%d), possible infinite loop", maxIterations)
}

// Helper functions
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func main() {
	// Parse agent type - default to coder if not specified
	agentType := "coder"
	if len(os.Args) >= 3 && os.Args[1] == "run" {
		if os.Args[2] == "coder" || os.Args[2] == "architect" {
			agentType = os.Args[2]
		} else if os.Args[2] != "--input" {
			fmt.Fprintf(os.Stderr, "Usage: %s run [coder|architect] --input <file.json> [--workdir <dir>] [--mode <mock|live|debug>] [--cleanup]\n", os.Args[0])
			os.Exit(1)
		}
	} else if len(os.Args) < 2 || os.Args[1] != "run" {
		fmt.Fprintf(os.Stderr, "Usage: %s run [coder|architect] --input <file.json> [--workdir <dir>] [--mode <mock|live|debug>] [--cleanup]\n", os.Args[0])
		os.Exit(1)
	}

	var inputFile string
	var workDir string
	var mode string = "" // Will be auto-detected
	var cleanup bool = false

	// Determine starting index for flag parsing
	startIdx := 2
	if len(os.Args) >= 3 && (os.Args[2] == "coder" || os.Args[2] == "architect") {
		startIdx = 3
	}

	// Simple flag parsing
	for i := startIdx; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--input":
			if i+1 < len(os.Args) {
				inputFile = os.Args[i+1]
				i++
			}
		case "--workdir":
			if i+1 < len(os.Args) {
				workDir = os.Args[i+1]
				i++
			}
		case "--mode":
			if i+1 < len(os.Args) {
				mode = os.Args[i+1]
				i++
			}
		case "--cleanup":
			cleanup = true
		}
	}

	if inputFile == "" {
		fmt.Fprintf(os.Stderr, "Error: --input is required\n")
		os.Exit(1)
	}

	// Auto-detect mode if not specified
	if mode == "" {
		if os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("OPENAI_API_KEY") != "" {
			mode = "live"
		} else {
			mode = "mock"
			fmt.Fprintf(os.Stderr, "No API keys found, defaulting to mock mode\n")
		}
	}

	// Set up workspace
	if workDir == "" {
		tmpDir, err := os.MkdirTemp("", "agentctl-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create temp directory: %v\n", err)
			os.Exit(1)
		}
		workDir = tmpDir
		if cleanup {
			defer os.RemoveAll(workDir)
		} else {
			fmt.Fprintf(os.Stderr, "Working directory preserved at: %s\n", workDir)
		}
	}

	// Read and parse input
	inputData, err := os.ReadFile(inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read input file: %v\n", err)
		os.Exit(1)
	}

	var inputMsg proto.AgentMsg
	if err := json.Unmarshal(inputData, &inputMsg); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse input JSON: %v\n", err)
		os.Exit(1)
	}

	// Create coder
	stateStore, err := state.NewStore(filepath.Join(workDir, "state"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create state store: %v\n", err)
		os.Exit(1)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 32000,
		MaxReplyTokens:   4096,
		CompactionBuffer: 1000,
	}

	// Create agent based on type
	switch agentType {
	case "coder":
		var claudeAgent *coder.Coder
		switch mode {
		case "mock":
			claudeAgent, err = coder.NewCoder("agentctl-coder", "standalone-coder", workDir, stateStore, modelConfig)
		case "live":
			apiKey := os.Getenv("ANTHROPIC_API_KEY")
			if apiKey == "" {
				fmt.Fprintf(os.Stderr, "ANTHROPIC_API_KEY environment variable required for live mode\n")
				os.Exit(1)
			}
			claudeAgent, err = coder.NewCoderWithClaude("agentctl-coder", "standalone-coder", workDir, stateStore, modelConfig, apiKey)
		default:
			fmt.Fprintf(os.Stderr, "Invalid mode '%s', must be 'mock', 'live', or 'debug'\n", mode)
			os.Exit(1)
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create coder: %v\n", err)
			os.Exit(1)
		}

		// Process message with approval handling for standalone mode
		ctx := context.Background()
		result, err := processWithApprovals(ctx, claudeAgent, &inputMsg, mode == "live")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to process message: %v\n", err)
			os.Exit(1)
		}

		// Output result
		jsonData, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to marshal result: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(jsonData))

	case "architect":
		switch mode {
		case "mock":
			_ = architect.NewDriver("agentctl-architect", stateStore, workDir, filepath.Join(workDir, "stories"))
		case "live":
			// TODO: Implement live architect mode - requires dispatcher and LLM client setup
			fmt.Fprintf(os.Stderr, "Live mode for architect not yet implemented\n")
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "Invalid mode '%s', must be 'mock', 'live', or 'debug'\n", mode)
			os.Exit(1)
		}

		// TODO: Implement architect processing workflow
		fmt.Fprintf(os.Stderr, "Architect processing not yet implemented\n")
		os.Exit(1)

	default:
		fmt.Fprintf(os.Stderr, "Invalid agent type '%s', must be 'coder' or 'architect'\n", agentType)
		os.Exit(1)
	}
}

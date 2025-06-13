package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"orchestrator/pkg/coder"
	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "run" || os.Args[2] != "claude" {
		fmt.Fprintf(os.Stderr, "Usage: %s run claude --input <file.json> [--workdir <dir>] [--mode <mock|live>]\n", os.Args[0])
		os.Exit(1)
	}

	var inputFile string
	var workDir string
	var mode string = "mock"

	// Simple flag parsing
	for i := 3; i < len(os.Args); i++ {
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
		}
	}

	if inputFile == "" {
		fmt.Fprintf(os.Stderr, "Error: --input is required\n")
		os.Exit(1)
	}

	// Set up workspace
	if workDir == "" {
		tmpDir, err := os.MkdirTemp("", "agentctl-claude-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create temp directory: %v\n", err)
			os.Exit(1)
		}
		workDir = tmpDir
		defer os.RemoveAll(workDir)
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

	var claudeAgent *coder.Coder
	switch mode {
	case "mock":
		claudeAgent, err = coder.NewCoder("agentctl-claude", "standalone-claude", workDir, stateStore, modelConfig)
	case "live":
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			fmt.Fprintf(os.Stderr, "ANTHROPIC_API_KEY environment variable required for live mode\n")
			os.Exit(1)
		}
		claudeAgent, err = coder.NewCoderWithClaude("agentctl-claude", "standalone-claude", workDir, stateStore, modelConfig, apiKey)
	default:
		fmt.Fprintf(os.Stderr, "Invalid mode '%s', must be 'mock' or 'live'\n", mode)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create coder: %v\n", err)
		os.Exit(1)
	}

	// Process message
	ctx := context.Background()
	result, err := claudeAgent.ProcessMessage(ctx, &inputMsg)
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
}
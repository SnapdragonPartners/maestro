package limiter

import (
	"fmt"
	"testing"

	"orchestrator/pkg/config"
)

func ExampleLimiter_usage() {
	// Create test configuration
	cfg := &config.Config{
		Models: map[string]config.ModelCfg{
			"claude": {
				MaxTokensPerMinute: 1000,
				MaxBudgetPerDayUSD: 25.0,
				APIKey:             "test-key",
				Agents: []config.Agent{
					{Name: "claude-1", ID: "001", Type: "coder", WorkDir: "./work/claude1"},
					{Name: "claude-2", ID: "002", Type: "coder", WorkDir: "./work/claude2"},
					{Name: "claude-3", ID: "003", Type: "coder", WorkDir: "./work/claude3"},
				},
			},
			"o3": {
				MaxTokensPerMinute: 500,
				MaxBudgetPerDayUSD: 50.0,
				APIKey:             "test-key-2",
				Agents: []config.Agent{
					{Name: "architect-1", ID: "001", Type: "architect", WorkDir: "./work/arch1"},
				},
			},
		},
	}

	// Create limiter
	limiter := NewLimiter(cfg)
	defer limiter.Close()

	fmt.Println("=== Rate Limiting Demo ===")

	// Check initial status
	tokens, budget, agents, _ := limiter.GetStatus("claude")
	fmt.Printf("Claude initial: %d tokens, $%.2f spent, %d agents\n", tokens, budget, agents)

	// Reserve some tokens for Claude
	fmt.Println("\nReserving 300 tokens for Claude...")
	err := limiter.Reserve("claude", 300)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Println("Success!")
	}

	// Check status after reservation
	tokens, budget, agents, _ = limiter.GetStatus("claude")
	fmt.Printf("Claude after reservation: %d tokens, $%.2f spent, %d agents\n", tokens, budget, agents)

	// Reserve budget
	fmt.Println("\nReserving $15.50 budget for Claude...")
	err = limiter.ReserveBudget("claude", 15.50)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Println("Success!")
	}

	// Check status after budget reservation
	tokens, budget, agents, _ = limiter.GetStatus("claude")
	fmt.Printf("Claude after budget reservation: %d tokens, $%.2f spent, %d agents\n", tokens, budget, agents)

	// Test agent reservation
	fmt.Println("\nReserving 2 agents for Claude...")
	err = limiter.ReserveAgent("claude")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Println("Reserved 1 agent!")
	}

	err = limiter.ReserveAgent("claude")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Println("Reserved 2nd agent!")
	}

	tokens, budget, agents, _ = limiter.GetStatus("claude")
	fmt.Printf("Claude after agent reservations: %d tokens, $%.2f spent, %d agents\n", tokens, budget, agents)

	// Try to exceed rate limit
	fmt.Println("\nTrying to reserve 800 tokens (should fail)...")
	err = limiter.Reserve("claude", 800)
	if err != nil {
		fmt.Printf("Expected error: %v\n", err)
	}

	// Try to exceed budget
	fmt.Println("\nTrying to reserve $15.00 more budget (should fail)...")
	err = limiter.ReserveBudget("claude", 15.00)
	if err != nil {
		fmt.Printf("Expected error: %v\n", err)
	}

	// Test different model
	fmt.Println("\n=== Testing O3 Model ===")
	tokens, budget, agents, _ = limiter.GetStatus("o3")
	fmt.Printf("O3 initial: %d tokens, $%.2f spent, %d agents\n", tokens, budget, agents)

	err = limiter.Reserve("o3", 200)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Println("Reserved 200 tokens for O3!")
	}

	tokens, budget, agents, _ = limiter.GetStatus("o3")
	fmt.Printf("O3 after reservation: %d tokens, $%.2f spent, %d agents\n", tokens, budget, agents)
}

func TestExampleLimiterUsage(t *testing.T) {
	ExampleLimiter_usage()
}

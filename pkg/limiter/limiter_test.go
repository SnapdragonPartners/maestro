package limiter

import (
	"errors"
	"testing"
	"time"

	"orchestrator/pkg/config"
)

func createTestConfig() *config.Config {
	return &config.Config{
		Models: map[string]config.ModelCfg{
			"claude": {
				MaxTokensPerMinute: 100,
				MaxBudgetPerDayUSD: 10.0,
				APIKey:             "test-key",
				Agents: []config.Agent{
					{Name: "test-claude-1", ID: "001", Type: "coder", WorkDir: "./work/test1"},
					{Name: "test-claude-2", ID: "002", Type: "coder", WorkDir: "./work/test2"},
					{Name: "test-claude-3", ID: "003", Type: "coder", WorkDir: "./work/test3"},
				},
			},
			"o3": {
				MaxTokensPerMinute: 50,
				MaxBudgetPerDayUSD: 20.0,
				APIKey:             "test-key-2",
				Agents: []config.Agent{
					{Name: "test-o3-1", ID: "001", Type: "architect", WorkDir: "./work/o3test"},
				},
			},
		},
	}
}

func TestNewLimiter(t *testing.T) {
	cfg := createTestConfig()
	limiter := NewLimiter(cfg)
	defer limiter.Close()

	// Test that model limiters are created.
	tokens, budget, agents, err := limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status for claude: %v", err)
	}

	if tokens != 100 {
		t.Errorf("Expected claude to start with 100 tokens, got %d", tokens)
	}
	if budget != 0 {
		t.Errorf("Expected claude to start with 0 budget, got %f", budget)
	}
	if agents != 0 {
		t.Errorf("Expected claude to start with 0 agents, got %d", agents)
	}

	// Test unknown model.
	_, _, _, err = limiter.GetStatus("unknown")
	if err == nil {
		t.Error("Expected error for unknown model")
	}
}

func TestTokenReservation(t *testing.T) {
	cfg := createTestConfig()
	limiter := NewLimiter(cfg)
	defer limiter.Close()

	// Test successful reservation.
	err := limiter.Reserve("claude", 50)
	if err != nil {
		t.Fatalf("Failed to reserve tokens: %v", err)
	}

	tokens, _, _, err := limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}
	if tokens != 50 {
		t.Errorf("Expected 50 tokens remaining, got %d", tokens)
	}

	// Test rate limit exceeded.
	err = limiter.Reserve("claude", 60)
	if !errors.Is(err, ErrRateLimit) {
		t.Errorf("Expected rate limit error, got %v", err)
	}

	// Test exact limit.
	err = limiter.Reserve("claude", 50)
	if err != nil {
		t.Errorf("Expected successful reservation of remaining tokens, got %v", err)
	}

	tokens, _, _, err = limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}
	if tokens != 0 {
		t.Errorf("Expected 0 tokens remaining, got %d", tokens)
	}
}

func TestBudgetReservation(t *testing.T) {
	cfg := createTestConfig()
	limiter := NewLimiter(cfg)
	defer limiter.Close()

	// Test successful budget reservation.
	err := limiter.ReserveBudget("claude", 5.0)
	if err != nil {
		t.Fatalf("Failed to reserve budget: %v", err)
	}

	_, budget, _, err := limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}
	if budget != 5.0 {
		t.Errorf("Expected 5.0 budget spent, got %f", budget)
	}

	// Test budget exceeded.
	err = limiter.ReserveBudget("claude", 6.0)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Errorf("Expected budget exceeded error, got %v", err)
	}

	// Test exact budget limit.
	err = limiter.ReserveBudget("claude", 5.0)
	if err != nil {
		t.Errorf("Expected successful reservation of remaining budget, got %v", err)
	}

	_, budget, _, err = limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}
	if budget != 10.0 {
		t.Errorf("Expected 10.0 budget spent, got %f", budget)
	}
}

func TestTokenRefill(t *testing.T) {
	cfg := &config.Config{
		Models: map[string]config.ModelCfg{
			"test": {
				MaxTokensPerMinute: 60, // 1 token per second for easy testing
				MaxBudgetPerDayUSD: 10.0,
				Agents: []config.Agent{
					{Name: "test-agent-1", ID: "001", Type: "coder", WorkDir: "./work/test1"},
					{Name: "test-agent-2", ID: "002", Type: "coder", WorkDir: "./work/test2"},
				},
				APIKey: "test-key",
			},
		},
	}

	limiter := NewLimiter(cfg)
	defer limiter.Close()

	// Use all tokens.
	err := limiter.Reserve("test", 60)
	if err != nil {
		t.Fatalf("Failed to reserve initial tokens: %v", err)
	}

	tokens, _, _, err := limiter.GetStatus("test")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}
	if tokens != 0 {
		t.Errorf("Expected 0 tokens, got %d", tokens)
	}

	// Manually advance the lastRefill time by 1 minute to simulate time passage.
	modelLimiter := limiter.models["test"]
	modelLimiter.mu.Lock()
	modelLimiter.lastRefill = modelLimiter.lastRefill.Add(-time.Minute)
	modelLimiter.mu.Unlock()

	// Check that tokens are refilled.
	tokens, _, _, err = limiter.GetStatus("test")
	if err != nil {
		t.Fatalf("Failed to get status after refill: %v", err)
	}
	if tokens != 60 {
		t.Errorf("Expected 60 tokens after refill, got %d", tokens)
	}
}

func TestDailyReset(t *testing.T) {
	cfg := createTestConfig()
	limiter := NewLimiter(cfg)
	defer limiter.Close()

	// Use some tokens and budget.
	err := limiter.Reserve("claude", 50)
	if err != nil {
		t.Fatalf("Failed to reserve tokens: %v", err)
	}

	err = limiter.ReserveBudget("claude", 8.0)
	if err != nil {
		t.Fatalf("Failed to reserve budget: %v", err)
	}

	tokens, budget, agents, err := limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}
	if tokens != 50 || budget != 8.0 {
		t.Errorf("Expected 50 tokens and 8.0 budget, got %d tokens and %f budget", tokens, budget)
	}
	if agents != 0 {
		t.Errorf("Expected 0 agents, got %d", agents)
	}

	// Trigger daily reset.
	limiter.ResetDaily()

	tokens, budget, agents, err = limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status after reset: %v", err)
	}
	if tokens != 100 {
		t.Errorf("Expected 100 tokens after reset, got %d", tokens)
	}
	if budget != 0 {
		t.Errorf("Expected 0 budget after reset, got %f", budget)
	}
	if agents != 0 {
		t.Errorf("Expected 0 agents after reset, got %d", agents)
	}
}

func TestMultipleModels(t *testing.T) {
	cfg := createTestConfig()
	limiter := NewLimiter(cfg)
	defer limiter.Close()

	// Test claude.
	err := limiter.Reserve("claude", 30)
	if err != nil {
		t.Fatalf("Failed to reserve claude tokens: %v", err)
	}

	err = limiter.ReserveBudget("claude", 3.0)
	if err != nil {
		t.Fatalf("Failed to reserve claude budget: %v", err)
	}

	// Test o3
	err = limiter.Reserve("o3", 20)
	if err != nil {
		t.Fatalf("Failed to reserve o3 tokens: %v", err)
	}

	err = limiter.ReserveBudget("o3", 15.0)
	if err != nil {
		t.Fatalf("Failed to reserve o3 budget: %v", err)
	}

	// Check claude status.
	claudeTokens, claudeBudget, _, err := limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get claude status: %v", err)
	}
	if claudeTokens != 70 || claudeBudget != 3.0 {
		t.Errorf("Expected claude: 70 tokens, 3.0 budget; got %d tokens, %f budget", claudeTokens, claudeBudget)
	}

	// Check o3 status.
	o3Tokens, o3Budget, _, err := limiter.GetStatus("o3")
	if err != nil {
		t.Fatalf("Failed to get o3 status: %v", err)
	}
	if o3Tokens != 30 || o3Budget != 15.0 {
		t.Errorf("Expected o3: 30 tokens, 15.0 budget; got %d tokens, %f budget", o3Tokens, o3Budget)
	}
}

func TestConcurrentAccess(t *testing.T) {
	cfg := createTestConfig()
	limiter := NewLimiter(cfg)
	defer limiter.Close()

	// Test concurrent token reservations.
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			err := limiter.Reserve("claude", 10)
			if err != nil && !errors.Is(err, ErrRateLimit) {
				t.Errorf("Unexpected error: %v", err)
			}
			done <- true
		}()
	}

	// Wait for all goroutines to complete.
	for i := 0; i < 10; i++ {
		<-done
	}

	// Check final state.
	tokens, _, _, err := limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get final status: %v", err)
	}

	// Should have 0 tokens (100 total, 10 reservations of 10 each)
	if tokens != 0 {
		t.Errorf("Expected 0 tokens remaining, got %d", tokens)
	}
}

func TestUnknownModel(t *testing.T) {
	cfg := createTestConfig()
	limiter := NewLimiter(cfg)
	defer limiter.Close()

	err := limiter.Reserve("unknown", 10)
	if err == nil {
		t.Error("Expected error for unknown model")
	}

	err = limiter.ReserveBudget("unknown", 1.0)
	if err == nil {
		t.Error("Expected error for unknown model")
	}
}

package ratelimit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestTokenBucketRefill verifies that tokens refill at the correct rate.
func TestTokenBucketRefill(t *testing.T) {
	cfg := Config{
		TokensPerMinute: 6000, // 600 tokens per refill (every 6 seconds)
		MaxConcurrency:  5,
	}

	limiter := NewTokenBucketLimiter("test-provider", cfg, 3*time.Minute)

	// Start with 90% capacity = 5400 tokens
	stats := limiter.GetStats()
	if stats.AvailableTokens != 5400 {
		t.Errorf("Initial tokens = %d, want 5400", stats.AvailableTokens)
	}

	// Consume some tokens
	ctx := context.Background()
	release, err := limiter.Acquire(ctx, 3000, "test-agent")
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer release()

	// Should have 2400 tokens left
	stats = limiter.GetStats()
	if stats.AvailableTokens != 2400 {
		t.Errorf("After acquire, tokens = %d, want 2400", stats.AvailableTokens)
	}

	// Manually trigger refill
	limiter.refill()

	// Should have 2400 + 600 = 3000 tokens
	stats = limiter.GetStats()
	if stats.AvailableTokens != 3000 {
		t.Errorf("After refill, tokens = %d, want 3000", stats.AvailableTokens)
	}
}

// TestTokenBucketCapacity verifies that tokens don't exceed max capacity.
func TestTokenBucketCapacity(t *testing.T) {
	cfg := Config{
		TokensPerMinute: 1000, // Max capacity = 900 (90%)
		MaxConcurrency:  5,
	}

	limiter := NewTokenBucketLimiter("test-provider", cfg, 3*time.Minute)

	// Start with full bucket (900 tokens)
	stats := limiter.GetStats()
	if stats.AvailableTokens != 900 {
		t.Errorf("Initial tokens = %d, want 900", stats.AvailableTokens)
	}

	// Try to refill again - should stay at 900
	limiter.refill()

	stats = limiter.GetStats()
	if stats.AvailableTokens != 900 {
		t.Errorf("After refill at capacity, tokens = %d, want 900", stats.AvailableTokens)
	}
}

// TestAtomicAcquisition verifies that token and concurrency acquisition is atomic.
func TestAtomicAcquisition(t *testing.T) {
	cfg := Config{
		TokensPerMinute: 10000, // 9000 max capacity
		MaxConcurrency:  2,     // Only 2 concurrent slots
	}

	limiter := NewTokenBucketLimiter("test-provider", cfg, 3*time.Minute)
	ctx := context.Background()

	// Acquire both concurrency slots
	release1, err := limiter.Acquire(ctx, 1000, "agent-1")
	if err != nil {
		t.Fatalf("First acquire error = %v", err)
	}
	defer release1()

	release2, err := limiter.Acquire(ctx, 1000, "agent-2")
	if err != nil {
		t.Fatalf("Second acquire error = %v", err)
	}
	defer release2()

	// Verify both slots are taken
	stats := limiter.GetStats()
	if stats.ActiveRequests != 2 {
		t.Errorf("ActiveRequests = %d, want 2", stats.ActiveRequests)
	}

	// Try to acquire more tokens (should block since no slots available)
	// Even though we have plenty of tokens (7000 remaining)
	ctx2, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	_, err = limiter.Acquire(ctx2, 1000, "agent-3")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected context.DeadlineExceeded, got %v", err)
	}

	// Verify we didn't consume tokens (atomic acquisition failed)
	stats = limiter.GetStats()
	if stats.AvailableTokens != 7000 {
		t.Errorf("Tokens after failed acquire = %d, want 7000", stats.AvailableTokens)
	}
}

// TestConcurrencyLimiting verifies concurrency slot management.
func TestConcurrencyLimiting(t *testing.T) {
	cfg := Config{
		TokensPerMinute: 100000, // Plenty of tokens
		MaxConcurrency:  3,
	}

	limiter := NewTokenBucketLimiter("test-provider", cfg, 3*time.Minute)
	ctx := context.Background()

	// Acquire all 3 slots
	var releases []func()
	for i := 0; i < 3; i++ {
		release, err := limiter.Acquire(ctx, 100, "test-agent")
		if err != nil {
			t.Fatalf("Acquire %d error = %v", i, err)
		}
		releases = append(releases, release)
	}

	// Verify all slots taken
	stats := limiter.GetStats()
	if stats.ActiveRequests != 3 {
		t.Errorf("ActiveRequests = %d, want 3", stats.ActiveRequests)
	}

	// Try to acquire one more (should block)
	ctx2, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	_, err := limiter.Acquire(ctx2, 100, "test-agent")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected timeout, got %v", err)
	}

	// Release one slot
	releases[0]()

	// Should now be able to acquire
	release4, err := limiter.Acquire(ctx, 100, "test-agent")
	if err != nil {
		t.Fatalf("Acquire after release error = %v", err)
	}
	defer release4()

	// Clean up
	for i := 1; i < len(releases); i++ {
		releases[i]()
	}
}

// TestStaleAcquisitionCleanup verifies that stale acquisitions are auto-released.
func TestStaleAcquisitionCleanup(t *testing.T) {
	cfg := Config{
		TokensPerMinute: 100000,
		MaxConcurrency:  2,
	}

	// Use very short timeout for testing
	limiter := NewTokenBucketLimiter("test-provider", cfg, 100*time.Millisecond)
	ctx := context.Background()

	// Acquire both slots
	release1, err := limiter.Acquire(ctx, 100, "agent-1")
	if err != nil {
		t.Fatalf("First acquire error = %v", err)
	}
	defer release1() // Clean up

	_, err = limiter.Acquire(ctx, 100, "agent-2")
	if err != nil {
		t.Fatalf("Second acquire error = %v", err)
	}
	// Deliberately NOT calling release to simulate slot leak

	// Wait for acquisitions to become stale
	time.Sleep(150 * time.Millisecond)

	// Try to acquire - this should trigger cleanup
	ctx2, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	release3, err := limiter.Acquire(ctx2, 100, "agent-3")
	if err != nil {
		t.Fatalf("Acquire after stale cleanup error = %v", err)
	}
	defer release3()

	// Verify cleanup happened (should have active requests)
	stats := limiter.GetStats()
	if stats.ActiveRequests == 0 {
		t.Error("Expected active requests after cleanup")
	}
}

// TestConcurrentAcquisitions verifies thread safety under concurrent load.
func TestConcurrentAcquisitions(t *testing.T) {
	cfg := Config{
		TokensPerMinute: 60000, // 54000 max capacity
		MaxConcurrency:  10,
	}

	limiter := NewTokenBucketLimiter("test-provider", cfg, 3*time.Minute)
	ctx := context.Background()

	// Launch 20 goroutines trying to acquire concurrently
	const numGoroutines = 20
	const tokensPerRequest = 1000

	var wg sync.WaitGroup
	var successCount atomic.Int32
	var failCount atomic.Int32

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()

			// Use short timeout to avoid blocking forever
			ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()

			release, err := limiter.Acquire(ctx2, tokensPerRequest, "test-agent")
			if err != nil {
				failCount.Add(1)
				return
			}
			defer release()

			successCount.Add(1)

			// Hold slot briefly
			time.Sleep(10 * time.Millisecond)
		}(i)
	}

	wg.Wait()

	// Should have some successes and some failures (due to limits)
	if successCount.Load() == 0 {
		t.Error("Expected some successful acquisitions")
	}

	// Verify limiter state is consistent
	stats := limiter.GetStats()
	if stats.ActiveRequests != 0 {
		t.Errorf("ActiveRequests after all releases = %d, want 0", stats.ActiveRequests)
	}

	// Tokens should be reduced but not negative
	if stats.AvailableTokens < 0 || stats.AvailableTokens > stats.MaxCapacity {
		t.Errorf("AvailableTokens = %d, want 0 <= tokens <= %d", stats.AvailableTokens, stats.MaxCapacity)
	}
}

// TestMetricsTracking verifies that congestion metrics are tracked correctly.
func TestMetricsTracking(t *testing.T) {
	cfg := Config{
		TokensPerMinute: 1000, // 900 max capacity
		MaxConcurrency:  2,
	}

	limiter := NewTokenBucketLimiter("test-provider", cfg, 3*time.Minute)
	ctx := context.Background()

	// Acquire all tokens
	release1, err := limiter.Acquire(ctx, 900, "agent-1")
	if err != nil {
		t.Fatalf("Acquire error = %v", err)
	}
	defer release1()

	// Try to acquire more tokens (should hit token limit)
	ctx2, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	_, err = limiter.Acquire(ctx2, 100, "agent-2")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected timeout, got %v", err)
	}

	// Check that token limit hit was recorded
	stats := limiter.GetStats()
	if stats.TokenLimitHits != 1 {
		t.Errorf("TokenLimitHits = %d, want 1", stats.TokenLimitHits)
	}

	// Now test concurrency hits
	limiter2 := NewTokenBucketLimiter("test-provider-2", cfg, 3*time.Minute)

	// Acquire both concurrency slots
	rel1, _ := limiter2.Acquire(context.Background(), 100, "agent-1")
	defer rel1()
	rel2, _ := limiter2.Acquire(context.Background(), 100, "agent-2")
	defer rel2()

	// Try to acquire one more (should hit concurrency limit)
	ctx3, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()

	_, err = limiter2.Acquire(ctx3, 100, "agent-3")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected timeout, got %v", err)
	}

	// Check that concurrency limit hit was recorded
	stats2 := limiter2.GetStats()
	if stats2.ConcurrencyHits != 1 {
		t.Errorf("ConcurrencyHits = %d, want 1", stats2.ConcurrencyHits)
	}
}

// TestProviderLimiterMap verifies multi-provider management.
func TestProviderLimiterMap(t *testing.T) {
	configs := map[string]Config{
		"anthropic": {
			TokensPerMinute: 10000,
			MaxConcurrency:  5,
		},
		"openai_official": {
			TokensPerMinute: 5000,
			MaxConcurrency:  3,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	limiterMap := NewProviderLimiterMap(ctx, configs, 3*time.Minute)
	defer limiterMap.Stop()

	// Verify limiters exist for both providers
	anthropicLimiter, err := limiterMap.GetLimiter("claude-sonnet-4")
	if err != nil {
		t.Fatalf("GetLimiter(claude-sonnet-4) error = %v", err)
	}

	openaiLimiter, err := limiterMap.GetLimiter("o3-mini")
	if err != nil {
		t.Fatalf("GetLimiter(o3-mini) error = %v", err)
	}

	// Verify they're different limiters with correct config
	stats1 := anthropicLimiter.GetStats()
	stats2 := openaiLimiter.GetStats()

	if stats1.MaxCapacity != 9000 { // 90% of 10000
		t.Errorf("Anthropic max capacity = %d, want 9000", stats1.MaxCapacity)
	}

	if stats2.MaxCapacity != 4500 { // 90% of 5000
		t.Errorf("OpenAI Official max capacity = %d, want 4500", stats2.MaxCapacity)
	}

	// Verify GetAllStats works
	allStats := limiterMap.GetAllStats()
	if len(allStats) != 2 {
		t.Errorf("GetAllStats() returned %d providers, want 2", len(allStats))
	}

	if _, exists := allStats["anthropic"]; !exists {
		t.Error("GetAllStats() missing anthropic provider")
	}

	if _, exists := allStats["openai_official"]; !exists {
		t.Error("GetAllStats() missing openai_official provider")
	}
}

// TestRefillTimer verifies background refill goroutine.
func TestRefillTimer(t *testing.T) {
	cfg := Config{
		TokensPerMinute: 6000, // 600 tokens per refill
		MaxConcurrency:  5,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	limiter := NewTokenBucketLimiter("test-provider", cfg, 3*time.Minute)

	// Consume some tokens
	release, err := limiter.Acquire(context.Background(), 3000, "test-agent")
	if err != nil {
		t.Fatalf("Acquire error = %v", err)
	}
	defer release()

	initialTokens := limiter.GetStats().AvailableTokens

	// Start refill timer
	limiter.startRefillTimer(ctx)

	// Wait for at least one refill (6 seconds + buffer)
	time.Sleep(7 * time.Second)

	// Should have more tokens now
	finalTokens := limiter.GetStats().AvailableTokens
	if finalTokens <= initialTokens {
		t.Errorf("Expected tokens to increase after refill timer, got %d <= %d", finalTokens, initialTokens)
	}

	// Cancel context and verify timer stops (no way to directly verify, but check no panic)
	cancel()
	time.Sleep(100 * time.Millisecond)
}

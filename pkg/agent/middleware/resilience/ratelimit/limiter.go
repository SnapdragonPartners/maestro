// Package ratelimit provides rate limiting functionality for LLM clients.
package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/utils"
)

// Note: The buffer factor (0.9) is defined in config.RateLimitBufferFactor
// to ensure consistency between config validation and limiter capacity calculation.

// Limiter defines the interface for rate limiting implementations.
type Limiter interface {
	// Acquire attempts to atomically acquire tokens and a concurrency slot.
	// Returns a release function that must be called to return the concurrency slot.
	// Blocks until both resources are available or context is cancelled.
	Acquire(ctx context.Context, tokens int, agentID string) (releaseFunc func(), err error)

	// GetStats returns current limiter statistics.
	GetStats() LimiterStats
}

// TokenEstimator estimates the number of tokens needed for a request.
type TokenEstimator interface {
	// EstimatePrompt estimates the number of prompt tokens for a request.
	EstimatePrompt(req llm.CompletionRequest) int
}

// Config defines rate limiting configuration for a provider.
type Config struct {
	TokensPerMinute int `json:"tokens_per_minute"` // Rate limit in tokens per minute
	MaxConcurrency  int `json:"max_concurrency"`   // Maximum concurrent requests
}

// DefaultTokenEstimator provides token estimation using TikToken.
type DefaultTokenEstimator struct{}

// NewDefaultTokenEstimator creates a new default token estimator.
func NewDefaultTokenEstimator() TokenEstimator {
	return &DefaultTokenEstimator{}
}

// EstimatePrompt estimates prompt tokens using TikToken-based counting.
//
//nolint:gocritic // 80 bytes is reasonable for token estimation
func (e *DefaultTokenEstimator) EstimatePrompt(req llm.CompletionRequest) int {
	var promptText string
	for i := range req.Messages {
		promptText += req.Messages[i].Content + "\n"
	}
	return utils.CountTokensSimple(promptText)
}

// acquisition tracks a single concurrency slot acquisition for cleanup purposes.
type acquisition struct {
	timestamp time.Time
	agentID   string
}

// TokenBucketLimiter implements rate limiting using a token bucket algorithm
// combined with concurrency limiting (semaphore).
//
//nolint:govet // fieldalignment: Struct layout optimized for readability over memory
type TokenBucketLimiter struct {
	mu sync.Mutex

	// Provider identification
	provider string

	// Token bucket state
	availableTokens int // Current tokens available
	tokensPerRefill int // Tokens added every refill (tokens_per_minute / 10)
	maxCapacity     int // Maximum bucket capacity (tokens_per_minute * RateLimitBufferFactor)

	// Concurrency limiting
	activeRequests int            // Current active requests
	maxConcurrency int            // Maximum concurrent requests
	acquisitions   []*acquisition // Track active acquisitions for cleanup
	releaseTimeout time.Duration  // How long before auto-releasing stale acquisitions

	// Metrics
	tokenLimitHits  int64 // Times we had to wait for tokens
	concurrencyHits int64 // Times we had to wait for a slot
}

// LimiterStats represents current rate limiter statistics.
type LimiterStats struct {
	Provider            string `json:"provider"`
	AvailableTokens     int    `json:"available_tokens"`
	MaxCapacity         int    `json:"max_capacity"`
	ActiveRequests      int    `json:"active_requests"`
	MaxConcurrency      int    `json:"max_concurrency"`
	TokenLimitHits      int64  `json:"token_limit_hits"`
	ConcurrencyHits     int64  `json:"concurrency_hits"`
	TrackedAcquisitions int    `json:"tracked_acquisitions"` // For debugging
}

// NewTokenBucketLimiter creates a new token bucket rate limiter for a provider.
func NewTokenBucketLimiter(provider string, cfg Config, requestTimeout time.Duration) *TokenBucketLimiter {
	// Calculate capacity with safety buffer (accounts for token estimation inaccuracies)
	maxCapacity := int(float64(cfg.TokensPerMinute) * config.RateLimitBufferFactor)

	// Refill every 6 seconds (divide by 10 for per-minute rate)
	tokensPerRefill := cfg.TokensPerMinute / 10

	return &TokenBucketLimiter{
		provider:        provider,
		availableTokens: maxCapacity, // Start with full bucket
		tokensPerRefill: tokensPerRefill,
		maxCapacity:     maxCapacity,
		activeRequests:  0,
		maxConcurrency:  cfg.MaxConcurrency,
		acquisitions:    make([]*acquisition, 0),
		releaseTimeout:  requestTimeout * 2, // 2x request timeout for stale detection
		tokenLimitHits:  0,
		concurrencyHits: 0,
	}
}

// Acquire atomically acquires both tokens and a concurrency slot.
// Returns a release function that MUST be called (via defer) to return the slot.
// Blocks until both resources are available, context is cancelled, or timeout is reached.
//
// The timeout is calculated as: agent_count × 1 minute. Rationale:
//   - Agents use LLM serially (one request at a time per agent).
//   - Bucket refills to full capacity over ~1 minute (10 refills × 6 seconds).
//   - Worst case FIFO: each agent ahead drains bucket, you wait for their refill cycle.
//   - If waiting longer than this, something is fundamentally wrong (config error or impossible request).
func (l *TokenBucketLimiter) Acquire(ctx context.Context, tokens int, agentID string) (func(), error) {
	firstAttempt := true
	startTime := time.Now()

	// Calculate maximum wait time: agent_count × 1 minute
	// This provides a safety net for impossible requests or configuration errors
	maxWait := time.Duration(config.GetTotalAgentCount()) * time.Minute

	for {
		l.mu.Lock()

		// If we're at capacity, opportunistically check for stale acquisitions
		if l.activeRequests >= l.maxConcurrency {
			l.cleanStaleAcquisitions()
		}

		// Check both conditions atomically under same lock
		hasTokens := l.availableTokens >= tokens
		hasSlot := l.activeRequests < l.maxConcurrency

		if hasTokens && hasSlot {
			// Acquire both resources atomically
			l.availableTokens -= tokens
			l.activeRequests++

			// Track this acquisition for stale cleanup
			acq := &acquisition{
				timestamp: time.Now(),
				agentID:   agentID,
			}
			l.acquisitions = append(l.acquisitions, acq)

			// Create release function that captures the acquisition
			releaseFunc := func() {
				l.release(acq)
			}

			l.mu.Unlock()
			return releaseFunc, nil
		}

		// Check for timeout before waiting
		elapsed := time.Since(startTime)
		if elapsed > maxWait {
			l.mu.Unlock()
			return nil, fmt.Errorf("rate limit acquisition timeout after %v "+
				"(requested %d tokens, max capacity %d, provider: %s, agent: %s)",
				elapsed.Round(time.Second), tokens, l.maxCapacity, l.provider, agentID)
		}

		// Can't acquire - record what blocked us (only on first attempt to avoid log spam)
		if firstAttempt {
			if !hasTokens {
				l.tokenLimitHits++
				logx.Infof("RATELIMIT: %s token limit hit, waiting for refill (need %d, have %d, agent: %s)",
					l.provider, tokens, l.availableTokens, agentID)
			}
			if !hasSlot {
				l.concurrencyHits++
				logx.Infof("RATELIMIT: %s concurrency limit hit, waiting for slot (active: %d/%d, agent: %s)",
					l.provider, l.activeRequests, l.maxConcurrency, agentID)
			}
			firstAttempt = false
		}

		l.mu.Unlock()

		// Wait briefly then retry
		select {
		case <-ctx.Done():
			return nil, ctx.Err() //nolint:wrapcheck // Context error propagated as-is
		case <-time.After(100 * time.Millisecond):
			continue
		}
	}
}

// release returns a concurrency slot (tokens are already consumed and not refunded).
func (l *TokenBucketLimiter) release(acq *acquisition) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Remove from acquisitions list
	for i, a := range l.acquisitions {
		if a == acq {
			l.acquisitions = append(l.acquisitions[:i], l.acquisitions[i+1:]...)
			break
		}
	}

	l.activeRequests--
}

// cleanStaleAcquisitions removes acquisitions that have exceeded the release timeout.
// Called under lock when concurrency appears full.
func (l *TokenBucketLimiter) cleanStaleAcquisitions() {
	now := time.Now()
	cleaned := 0

	// Filter out stale acquisitions
	validAcquisitions := make([]*acquisition, 0, len(l.acquisitions))
	for _, acq := range l.acquisitions {
		if now.Sub(acq.timestamp) > l.releaseTimeout {
			cleaned++
			l.activeRequests--
			_ = logx.Errorf("RATELIMIT: Force-released stale concurrency slot after %v (provider: %s, agent: %s)",
				l.releaseTimeout, l.provider, acq.agentID)
		} else {
			validAcquisitions = append(validAcquisitions, acq)
		}
	}
	l.acquisitions = validAcquisitions

	if cleaned > 0 {
		logx.Warnf("RATELIMIT: Cleaned %d stale concurrency slots for provider %s", cleaned, l.provider)
	}
}

// startRefillTimer starts a background goroutine that refills tokens every 6 seconds.
// Stops when context is cancelled.
func (l *TokenBucketLimiter) startRefillTimer(ctx context.Context) {
	ticker := time.NewTicker(6 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				l.refill()
			}
		}
	}()
}

// refill adds tokens to the bucket up to max capacity.
func (l *TokenBucketLimiter) refill() {
	l.mu.Lock()
	defer l.mu.Unlock()

	oldTokens := l.availableTokens
	l.availableTokens += l.tokensPerRefill

	// Cap at maximum capacity
	if l.availableTokens > l.maxCapacity {
		l.availableTokens = l.maxCapacity
	}

	// Debug logging for refill (can be removed if too verbose)
	if l.availableTokens != oldTokens {
		logx.Debugf("RATELIMIT: %s bucket refilled: %d -> %d tokens (max: %d)",
			l.provider, oldTokens, l.availableTokens, l.maxCapacity)
	}
}

// GetStats returns current limiter statistics (thread-safe).
func (l *TokenBucketLimiter) GetStats() LimiterStats {
	l.mu.Lock()
	defer l.mu.Unlock()

	return LimiterStats{
		Provider:            l.provider,
		AvailableTokens:     l.availableTokens,
		MaxCapacity:         l.maxCapacity,
		ActiveRequests:      l.activeRequests,
		MaxConcurrency:      l.maxConcurrency,
		TokenLimitHits:      l.tokenLimitHits,
		ConcurrencyHits:     l.concurrencyHits,
		TrackedAcquisitions: len(l.acquisitions),
	}
}

// ProviderLimiterMap manages rate limiters for different API providers.
type ProviderLimiterMap struct {
	limiters map[string]*TokenBucketLimiter
	ctx      context.Context //nolint:containedctx // Required for refill timer lifecycle management
	cancel   context.CancelFunc
}

// NewProviderLimiterMap creates a new provider limiter map with real token bucket limiters.
func NewProviderLimiterMap(ctx context.Context, configs map[string]Config, requestTimeout time.Duration) *ProviderLimiterMap {
	// Create cancellable context for limiter lifecycle
	ctx, cancel := context.WithCancel(ctx)

	limiters := make(map[string]*TokenBucketLimiter)
	for provider, cfg := range configs {
		limiter := NewTokenBucketLimiter(provider, cfg, requestTimeout)
		limiter.startRefillTimer(ctx) // Start background refill
		limiters[provider] = limiter
	}

	return &ProviderLimiterMap{
		limiters: limiters,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Stop cancels all refill timers and cleans up resources.
func (p *ProviderLimiterMap) Stop() {
	p.cancel()
}

// GetLimiter returns the rate limiter for a specific model.
func (p *ProviderLimiterMap) GetLimiter(modelName string) (Limiter, error) {
	provider, err := config.GetModelProvider(modelName)
	if err != nil {
		return nil, fmt.Errorf("cannot determine provider for model %s: %w", modelName, err)
	}

	limiter, exists := p.limiters[provider]
	if !exists {
		return nil, fmt.Errorf("no rate limiter configured for provider %s", provider)
	}

	return limiter, nil
}

// GetAllStats returns statistics for all provider limiters.
func (p *ProviderLimiterMap) GetAllStats() map[string]LimiterStats {
	stats := make(map[string]LimiterStats)
	for provider, limiter := range p.limiters {
		stats[provider] = limiter.GetStats()
	}
	return stats
}

// Package limiter provides rate limiting and budget enforcement for LLM API calls with token bucket algorithms.
package limiter

import (
	"fmt"
	"sync"
	"time"

	"orchestrator/pkg/config"
)

// Limiter manages rate limiting and budget enforcement across multiple LLM models.
type Limiter struct {
	models     map[string]*ModelLimiter
	resetTimer *time.Timer
	mu         sync.RWMutex
}

// ModelLimiter enforces token, budget, and concurrency limits for a specific LLM model.
//
//nolint:govet // Struct layout optimization not critical for this use case
type ModelLimiter struct {
	maxBudgetPerDayUSD float64
	currentBudgetUSD   float64
	lastRefill         time.Time
	mu                 sync.Mutex
	name               string
	maxTokensPerMinute int
	maxAgents          int
	currentTokens      int
	currentAgents      int
}

var (
	// ErrRateLimit is returned when token rate limits are exceeded.
	ErrRateLimit = fmt.Errorf("rate limit exceeded")
	// ErrBudgetExceeded is returned when daily budget limits are exceeded.
	ErrBudgetExceeded = fmt.Errorf("daily budget exceeded")
	// ErrAgentLimit is returned when agent limits are exceeded.
	ErrAgentLimit = fmt.Errorf("agent limit exceeded")
)

// NewLimiter creates a new rate limiter configured with the provided model limits.
func NewLimiter(cfg *config.Config) *Limiter {
	l := &Limiter{
		models: make(map[string]*ModelLimiter),
	}

	// Initialize model limiters.
	for i := range cfg.Orchestrator.Models {
		model := &cfg.Orchestrator.Models[i]
		l.models[model.Name] = &ModelLimiter{
			name:               model.Name,
			maxTokensPerMinute: model.MaxTPM,
			maxBudgetPerDayUSD: model.DailyBudget,
			maxAgents:          model.MaxConnections, // Use max connections as agent limit
			currentTokens:      model.MaxTPM,         // Start with full bucket
			currentBudgetUSD:   0,                    // Start with no spend
			currentAgents:      0,                    // Start with no active agents
			lastRefill:         time.Now(),
		}
	}

	// Schedule daily reset at midnight.
	l.scheduleDailyReset()

	return l
}

// Reserve attempts to reserve the specified number of tokens for the given model.
func (l *Limiter) Reserve(model string, tokens int) error {
	l.mu.RLock()
	modelLimiter, exists := l.models[model]
	l.mu.RUnlock()

	if !exists {
		return fmt.Errorf("model %s not configured", model)
	}

	return modelLimiter.Reserve(tokens)
}

// ReserveBudget reserves budget for a model operation.
func (l *Limiter) ReserveBudget(model string, costUSD float64) error {
	l.mu.RLock()
	modelLimiter, exists := l.models[model]
	l.mu.RUnlock()

	if !exists {
		return fmt.Errorf("model %s not configured", model)
	}

	return modelLimiter.ReserveBudget(costUSD)
}

// ReserveAgent reserves an agent slot for a model.
func (l *Limiter) ReserveAgent(model string) error {
	l.mu.RLock()
	modelLimiter, exists := l.models[model]
	l.mu.RUnlock()

	if !exists {
		return fmt.Errorf("model %s not configured", model)
	}

	return modelLimiter.ReserveAgent()
}

// ReleaseAgent releases an agent slot for a model.
func (l *Limiter) ReleaseAgent(model string) error {
	l.mu.RLock()
	modelLimiter, exists := l.models[model]
	l.mu.RUnlock()

	if !exists {
		return fmt.Errorf("model %s not configured", model)
	}

	return modelLimiter.ReleaseAgent()
}

// GetStatus returns the current status for a model's limits.
func (l *Limiter) GetStatus(model string) (tokens int, budget float64, agents int, err error) {
	l.mu.RLock()
	modelLimiter, exists := l.models[model]
	l.mu.RUnlock()

	if !exists {
		return 0, 0, 0, fmt.Errorf("model %s not configured", model)
	}

	return modelLimiter.GetStatus()
}

// ResetDaily resets daily limits for all models.
func (l *Limiter) ResetDaily() {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, modelLimiter := range l.models {
		modelLimiter.ResetDaily()
	}
}

// Close stops the limiter and releases resources.
func (l *Limiter) Close() {
	if l.resetTimer != nil {
		l.resetTimer.Stop()
	}
}

// Reserve reserves tokens from the rate limit bucket.
func (ml *ModelLimiter) Reserve(tokens int) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	// Refill tokens based on time elapsed.
	ml.refillTokens()

	if ml.currentTokens < tokens {
		return ErrRateLimit
	}

	ml.currentTokens -= tokens
	return nil
}

// ReserveBudget reserves budget from the daily limit.
func (ml *ModelLimiter) ReserveBudget(costUSD float64) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	if ml.currentBudgetUSD+costUSD > ml.maxBudgetPerDayUSD {
		return ErrBudgetExceeded
	}

	ml.currentBudgetUSD += costUSD
	return nil
}

// ReserveAgent reserves an agent slot.
func (ml *ModelLimiter) ReserveAgent() error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	if ml.currentAgents >= ml.maxAgents {
		return ErrAgentLimit
	}

	ml.currentAgents++
	return nil
}

// ReleaseAgent releases an agent slot.
func (ml *ModelLimiter) ReleaseAgent() error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	if ml.currentAgents <= 0 {
		return fmt.Errorf("no agents to release for model %s", ml.name)
	}

	ml.currentAgents--
	return nil
}

// GetStatus returns the current status of the model limiter.
func (ml *ModelLimiter) GetStatus() (tokens int, budget float64, agents int, err error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	ml.refillTokens()
	return ml.currentTokens, ml.currentBudgetUSD, ml.currentAgents, nil
}

// ResetDaily resets the daily budget and agent limits for this model.
func (ml *ModelLimiter) ResetDaily() {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	ml.currentBudgetUSD = 0
	ml.currentTokens = ml.maxTokensPerMinute // Reset to full bucket
	ml.currentAgents = 0                     // Reset active agents
	ml.lastRefill = time.Now()
}

func (ml *ModelLimiter) refillTokens() {
	now := time.Now()
	elapsed := now.Sub(ml.lastRefill)

	if elapsed >= time.Minute {
		// Refill tokens for each minute that has passed.
		minutes := int(elapsed / time.Minute)
		refillAmount := minutes * ml.maxTokensPerMinute

		// Cap at maximum.
		ml.currentTokens += refillAmount
		if ml.currentTokens > ml.maxTokensPerMinute {
			ml.currentTokens = ml.maxTokensPerMinute
		}

		// Update refill time to the last complete minute.
		ml.lastRefill = ml.lastRefill.Add(time.Duration(minutes) * time.Minute)
	}
}

func (l *Limiter) scheduleDailyReset() {
	now := time.Now()

	// Calculate next midnight in local time.
	nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	timeUntilMidnight := time.Until(nextMidnight)

	l.resetTimer = time.AfterFunc(timeUntilMidnight, func() {
		l.ResetDaily()

		// Schedule the next reset (24 hours from now)
		l.resetTimer = time.AfterFunc(24*time.Hour, func() {
			l.scheduleDailyReset() // Reschedule for next day
		})
	})
}

package limiter

import (
	"fmt"
	"sync"
	"time"

	"orchestrator/pkg/config"
)

type Limiter struct {
	models     map[string]*ModelLimiter
	mu         sync.RWMutex
	resetTimer *time.Timer
}

type ModelLimiter struct {
	name               string
	maxTokensPerMinute int
	maxBudgetPerDayUSD float64
	maxAgents          int
	currentTokens      int
	currentBudgetUSD   float64
	currentAgents      int
	lastRefill         time.Time
	mu                 sync.Mutex
}

var (
	ErrRateLimit      = fmt.Errorf("rate limit exceeded")
	ErrBudgetExceeded = fmt.Errorf("daily budget exceeded")
	ErrAgentLimit     = fmt.Errorf("agent limit exceeded")
)

func NewLimiter(cfg *config.Config) *Limiter {
	l := &Limiter{
		models: make(map[string]*ModelLimiter),
	}

	// Initialize model limiters
	for name, modelCfg := range cfg.Models {
		l.models[name] = &ModelLimiter{
			name:               name,
			maxTokensPerMinute: modelCfg.MaxTokensPerMinute,
			maxBudgetPerDayUSD: modelCfg.MaxBudgetPerDayUSD,
			maxAgents:          len(modelCfg.Agents), // Use number of configured agents
			currentTokens:      modelCfg.MaxTokensPerMinute, // Start with full bucket
			currentBudgetUSD:   0,                           // Start with no spend
			currentAgents:      0,                           // Start with no active agents
			lastRefill:         time.Now(),
		}
	}

	// Schedule daily reset at midnight
	l.scheduleDailyReset()

	return l
}

func (l *Limiter) Reserve(model string, tokens int) error {
	l.mu.RLock()
	modelLimiter, exists := l.models[model]
	l.mu.RUnlock()

	if !exists {
		return fmt.Errorf("model %s not configured", model)
	}

	return modelLimiter.Reserve(tokens)
}

func (l *Limiter) ReserveBudget(model string, costUSD float64) error {
	l.mu.RLock()
	modelLimiter, exists := l.models[model]
	l.mu.RUnlock()

	if !exists {
		return fmt.Errorf("model %s not configured", model)
	}

	return modelLimiter.ReserveBudget(costUSD)
}

func (l *Limiter) ReserveAgent(model string) error {
	l.mu.RLock()
	modelLimiter, exists := l.models[model]
	l.mu.RUnlock()

	if !exists {
		return fmt.Errorf("model %s not configured", model)
	}

	return modelLimiter.ReserveAgent()
}

func (l *Limiter) ReleaseAgent(model string) error {
	l.mu.RLock()
	modelLimiter, exists := l.models[model]
	l.mu.RUnlock()

	if !exists {
		return fmt.Errorf("model %s not configured", model)
	}

	return modelLimiter.ReleaseAgent()
}

func (l *Limiter) GetStatus(model string) (tokens int, budget float64, agents int, err error) {
	l.mu.RLock()
	modelLimiter, exists := l.models[model]
	l.mu.RUnlock()

	if !exists {
		return 0, 0, 0, fmt.Errorf("model %s not configured", model)
	}

	return modelLimiter.GetStatus()
}

func (l *Limiter) ResetDaily() {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, modelLimiter := range l.models {
		modelLimiter.ResetDaily()
	}
}

func (l *Limiter) Close() {
	if l.resetTimer != nil {
		l.resetTimer.Stop()
	}
}

func (ml *ModelLimiter) Reserve(tokens int) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	// Refill tokens based on time elapsed
	ml.refillTokens()

	if ml.currentTokens < tokens {
		return ErrRateLimit
	}

	ml.currentTokens -= tokens
	return nil
}

func (ml *ModelLimiter) ReserveBudget(costUSD float64) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	if ml.currentBudgetUSD+costUSD > ml.maxBudgetPerDayUSD {
		return ErrBudgetExceeded
	}

	ml.currentBudgetUSD += costUSD
	return nil
}

func (ml *ModelLimiter) ReserveAgent() error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	if ml.currentAgents >= ml.maxAgents {
		return ErrAgentLimit
	}

	ml.currentAgents++
	return nil
}

func (ml *ModelLimiter) ReleaseAgent() error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	if ml.currentAgents <= 0 {
		return fmt.Errorf("no agents to release for model %s", ml.name)
	}

	ml.currentAgents--
	return nil
}

func (ml *ModelLimiter) GetStatus() (tokens int, budget float64, agents int, err error) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	ml.refillTokens()
	return ml.currentTokens, ml.currentBudgetUSD, ml.currentAgents, nil
}

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
		// Refill tokens for each minute that has passed
		minutes := int(elapsed / time.Minute)
		refillAmount := minutes * ml.maxTokensPerMinute

		// Cap at maximum
		ml.currentTokens += refillAmount
		if ml.currentTokens > ml.maxTokensPerMinute {
			ml.currentTokens = ml.maxTokensPerMinute
		}

		// Update refill time to the last complete minute
		ml.lastRefill = ml.lastRefill.Add(time.Duration(minutes) * time.Minute)
	}
}

func (l *Limiter) scheduleDailyReset() {
	now := time.Now()

	// Calculate next midnight in local time
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

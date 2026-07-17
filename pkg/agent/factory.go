// Package agent provides the LLM client factory.
//
// All LLM I/O goes through the maestro-llms toolkit via the llmadapter seam
// (see docs/MAESTRO_LLMS_MIGRATION.md). The legacy in-tree provider clients
// and resilience middleware were removed at cut-over; the chain — metrics
// (outermost) → validation → retry → [timeout] → circuit → [rate limit] →
// provider — plus the app-side SUSPEND boundary and empty-response validator
// are composed in factory_llms.go (see buildMaestroLLMsClient for the exact
// order and rationale).
package agent

import (
	"context"
	"fmt"
	"path/filepath"

	mrl "github.com/SnapdragonPartners/maestro-llms/llms/ratelimit"

	"orchestrator/pkg/agent/internal/llmadapter"
	"orchestrator/pkg/agent/middleware/metrics"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

// RateLimitStat is the Maestro-side, web-UI-facing rate-limiter snapshot
// shape (ownership stays in Maestro — see migration spec §9). It is mapped
// from the toolkit's provider-neutral ratelimit.LimiterSnapshot.
type RateLimitStat struct {
	Provider        string `json:"provider"`
	AvailableTokens int    `json:"available_tokens"`
	MaxCapacity     int    `json:"max_capacity"`
	ActiveRequests  int    `json:"active_requests"`
	MaxConcurrency  int    `json:"max_concurrency"`
	TokenLimitHits  int64  `json:"token_limit_hits"`
	ConcurrencyHits int64  `json:"concurrency_hits"`
}

// LLMClientFactory creates LLM clients backed by the maestro-llms toolkit.
type LLMClientFactory struct {
	// mllmsLimiters holds one shared limiter per provider, created once so
	// all agents on a provider share a single token bucket (a per-client
	// limiter would give every agent the full budget — PR #220 review).
	mllmsLimiters   map[string]*mrl.InMemoryLimiter
	metricsRecorder metrics.Recorder
	config          config.Config
}

// NewLLMClientFactory creates a new LLM client factory with the given configuration.
func NewLLMClientFactory(cfg *config.Config) (*LLMClientFactory, error) {
	logger := logx.NewLogger("factory")

	var recorder metrics.Recorder
	if cfg.Agents != nil && cfg.Agents.Metrics.Enabled {
		logger.Info("📊 Using internal metrics recorder")
		recorder = metrics.NewInternalRecorder()
		// P-1 usage surface: fan out every LLM call to a durable usage log
		// so external instrumentation (the golden-story benchmark runner)
		// can stream usage; the internal aggregates are untouched.
		usagePath := filepath.Join(config.GetProjectDir(), ".maestro", metrics.UsageLogFileName)
		if usageRecorder, usageErr := metrics.NewUsageLogRecorder(usagePath, recorder); usageErr == nil {
			logger.Info("📊 Usage log v%d at %s", metrics.UsageSurfaceVersion, usagePath)
			recorder = usageRecorder
		} else {
			logger.Warn("Usage log unavailable (%v); continuing with in-memory metrics only", usageErr)
		}
	} else {
		logger.Info("📊 Using no-op metrics recorder")
		recorder = metrics.Nop()
	}

	// One shared maestro-llms limiter per provider, created once.
	rl := cfg.Agents.Resilience.RateLimit
	limits := map[string]config.ProviderLimits{
		config.ProviderAnthropic: rl.Anthropic,
		config.ProviderOpenAI:    rl.OpenAI,
		config.ProviderGoogle:    rl.Google,
		config.ProviderOllama:    rl.Ollama,
	}
	mllmsLimiters := make(map[string]*mrl.InMemoryLimiter, len(limits))
	for provider, pl := range limits {
		mllmsLimiters[provider] = mrl.NewInMemoryLimiter(mrl.Config{
			TokensPerMinute: pl.TokensPerMinute,
			MaxConcurrency:  pl.MaxConcurrency,
		})
	}

	return &LLMClientFactory{
		config:          *cfg,
		metricsRecorder: recorder,
		mllmsLimiters:   mllmsLimiters,
	}, nil
}

// Stop cleans up factory resources. The maestro-llms in-memory limiter is
// goroutine-free (lazy token bucket), so there is nothing to tear down; the
// method is retained for caller API stability.
func (f *LLMClientFactory) Stop() {}

// GetRateLimitStats returns a point-in-time snapshot per provider for the
// web UI congestion display.
func (f *LLMClientFactory) GetRateLimitStats(ctx context.Context) map[string]RateLimitStat {
	out := make(map[string]RateLimitStat, len(f.mllmsLimiters))
	logger := logx.NewLogger("factory")
	for provider, lim := range f.mllmsLimiters {
		// lim is the concrete *mrl.InMemoryLimiter, which has Stats() —
		// no capability assertion needed (compile-time guaranteed).
		snap, err := lim.Stats(ctx)
		if err != nil {
			// Surface rather than silently drop the provider's section.
			logger.Warn("rate-limit stats unavailable for provider %s: %v", provider, err)
			continue
		}
		out[provider] = RateLimitStat{
			Provider:        provider,
			AvailableTokens: snap.AvailableTokens,
			MaxCapacity:     snap.MaxCapacity,
			ActiveRequests:  snap.ActiveRequests,
			MaxConcurrency:  snap.MaxConcurrency,
			TokenLimitHits:  snap.TokenWaitHits,
			ConcurrencyHits: snap.SlotWaitHits,
		}
	}
	return out
}

// CreateClient creates an LLM client for the specified agent type.
// The API key is automatically retrieved from environment variables based on the model's provider.
func (f *LLMClientFactory) CreateClient(agentType Type) (LLMClient, error) {
	return f.CreateClientWithContext(agentType, nil, nil)
}

// CreateClientWithContext creates an LLM client with StateProvider and logger for enhanced metrics.
func (f *LLMClientFactory) CreateClientWithContext(agentType Type, stateProvider metrics.StateProvider, logger *logx.Logger) (LLMClient, error) {
	var modelName string
	switch agentType {
	case TypeCoder:
		modelName = config.GetEffectiveCoderModel()
	case TypeArchitect:
		modelName = config.GetEffectiveArchitectModel()
	case TypePM:
		modelName = config.GetEffectivePMModel()
	default:
		return nil, fmt.Errorf("unsupported agent type: %s", agentType)
	}

	provider, err := config.GetModelProvider(modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to determine provider for model %s: %w", modelName, err)
	}
	apiKey, err := config.GetAPIKey(provider)
	if err != nil {
		return nil, fmt.Errorf("failed to get API key for provider %s: %w", provider, err)
	}
	return f.buildMaestroLLMsClient(modelName, provider, apiKey, agentType.String(), stateProvider, logger)
}

// CreateRawClient creates a bare LLM client with no middleware for the given
// provider and API key. Used for lightweight validation (e.g. preflight key
// checking) where the full chain is not needed.
func CreateRawClient(provider, apiKey, model string) (LLMClient, error) {
	c, err := llmadapter.New(provider, apiKey, model)
	if err != nil {
		return nil, fmt.Errorf("failed to create maestro-llms client: %w", err)
	}
	return c, nil
}

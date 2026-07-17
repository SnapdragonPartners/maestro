package agent

// Phase 2 of the maestro-llms migration (docs/MAESTRO_LLMS_MIGRATION.md):
// middleware + observability for the flag-on path. This file owns the three
// app-side pieces the toolkit deliberately does not carry:
//
//   - metricsObserver — reimplements Maestro's Recorder semantics (cost via
//     config.CalculateCost, story/agent attribution) as a toolkit Observer
//     (§5 M2 / X4).
//   - suspendBoundary — maps the toolkit's typed terminal errors
//     (*middleware.CircuitOpenError, exhausted retryable *llms.ProviderError /
//     *llms.LimitError) back onto Maestro's existing
//     llmerrors.IsServiceUnavailable SUSPEND contract (§5 M4).
//   - buildMaestroLLMsClient — hand-composes the chain with metrics OUTERMOST
//     so one aggregate Event per logical call still observes validation /
//     limiter / circuit-open / retry-exhaustion, matching Maestro's current
//     metrics semantics (§5 M2; the RecommendedChat innermost default would
//     hide those).

import (
	"context"
	"errors"
	"fmt"

	mllms "github.com/SnapdragonPartners/maestro-llms/llms"
	mmw "github.com/SnapdragonPartners/maestro-llms/llms/middleware"

	"orchestrator/pkg/agent/internal/llmadapter"
	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/llmerrors"
	"orchestrator/pkg/agent/middleware/metrics"
	"orchestrator/pkg/agent/middleware/validation"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

// metricsObserver adapts a toolkit middleware.Event to Maestro's Recorder plus
// the detailed per-call log line. Concurrency-safe: it only reads immutable
// fields and calls a concurrency-safe Recorder. stateProvider may be nil
// (the CreateClient path passes none).
type metricsObserver struct {
	recorder      metrics.Recorder
	stateProvider metrics.StateProvider
	logger        *logx.Logger
}

// Observe implements middleware.Observer.
//
//nolint:gocritic // hugeParam: signature is fixed by the middleware.Observer interface.
func (o *metricsObserver) Observe(ev mmw.Event) {
	success := ev.Err == nil
	// X4: usage is now reliably populated by the toolkit; the old middleware
	// could only estimate via tokenizer counting.
	promptTokens := ev.Usage.InputTokens
	// Per maestro-llms ADR-0016 (v0.7.1), OutputTokens is visible output only;
	// billing math must read BillableOutputTokens or reasoning tokens are
	// undercounted. Fall back for paths that only populate OutputTokens
	// (e.g. pre-call estimates).
	completionTokens := ev.Usage.BillableOutputTokens
	if completionTokens == 0 {
		completionTokens = ev.Usage.OutputTokens + ev.Usage.ReasoningTokens
	}

	var cost float64
	if success && (promptTokens > 0 || completionTokens > 0) {
		if c, err := config.CalculateCost(ev.Model, promptTokens, completionTokens); err == nil {
			cost = c
		} else if o.logger != nil {
			o.logger.Warn("Failed to calculate cost for model %s: %v", ev.Model, err)
		}
	}

	var storyID, agentID, state string
	if o.stateProvider != nil {
		storyID = o.stateProvider.GetStoryID()
		agentID = o.stateProvider.GetID()
		state = string(o.stateProvider.GetCurrentState())
	}

	o.recorder.ObserveRequest(storyID, agentID, ev.Model, promptTokens, completionTokens, cost, success)

	if o.logger == nil {
		return
	}
	if success {
		o.logger.Info("LLM call to model '%s': latency %.3gs, request tokens: %d, response tokens: %d, total tokens: %d, cost $%.6f (agent: %s, story: %s, state: %s)",
			ev.Model, ev.Latency.Seconds(), promptTokens, completionTokens, promptTokens+completionTokens, cost, agentID, storyID, state)
	} else {
		o.logger.Error("LLM call to model '%s' failed: latency %.3gs, error: %s (agent: %s, story: %s, state: %s)",
			ev.Model, ev.Latency.Seconds(), ev.Err.Error(), agentID, storyID, state)
	}
}

// suspendBoundary maps the toolkit's typed terminal errors back onto Maestro's
// llmerrors.IsServiceUnavailable SUSPEND contract so existing handlers
// (pkg/pm/working.go, pkg/coder/planning.go, …) keep working unchanged.
// Implemented as a concrete llm.LLMClient (not llm.WrapClient) so phase-6's
// deletion of pkg/agent/llm/chain.go does not affect it.
type suspendBoundary struct {
	inner llm.LLMClient
}

func (s *suspendBoundary) GetModelName() string { return s.inner.GetModelName() }

//nolint:gocritic // hugeParam: signature is fixed by the llm.LLMClient interface.
func (s *suspendBoundary) Stream(ctx context.Context, in llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
	ch, err := s.inner.Stream(ctx, in)
	return ch, mapSuspend(err)
}

//nolint:gocritic // hugeParam: signature is fixed by the llm.LLMClient interface.
func (s *suspendBoundary) Complete(ctx context.Context, in llm.CompletionRequest) (llm.CompletionResponse, error) {
	resp, err := s.inner.Complete(ctx, in)
	return resp, mapSuspend(err)
}

// mapSuspend converts toolkit terminal errors to a ServiceUnavailable error
// (§5 M4). A retryable ProviderError/LimitError that survived the retry
// middleware means the provider is genuinely down — escalate to SUSPEND.
// context.Canceled passes through unchanged (§5 X5).
func mapSuspend(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	var coe *mmw.CircuitOpenError
	if errors.As(err, &coe) {
		return llmerrors.NewServiceUnavailableError(err, 0)
	}
	var pe *mllms.ProviderError
	var le *mllms.LimitError
	if (errors.As(err, &pe) || errors.As(err, &le)) && mllms.Retryable(err) {
		return llmerrors.NewServiceUnavailableError(err, 0)
	}
	return err
}

// buildMaestroLLMsClient constructs the flag-on client: toolkit provider →
// hand-composed middleware chain → adapter → suspend boundary.
//
// Chain order (ChainChat: first arg outermost): metrics → validation → retry
// → per-attempt timeout → circuit → rate limit → provider. This is the
// spec's recommended order with metrics relocated from innermost to outermost
// so a single aggregate Event still observes outer rejections (§5 M2). Note
// the deliberate tradeoff: latency now folds in retry backoff and per-attempt
// granularity is lost — that matches Maestro's *current* metrics semantics.
func (f *LLMClientFactory) buildMaestroLLMsClient(modelName, provider, apiKey, agentTypeStr string, stateProvider metrics.StateProvider, logger *logx.Logger) (LLMClient, error) {
	base, err := llmadapter.NewChatClient(provider, apiKey, modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to create maestro-llms client: %w", err)
	}

	res := f.config.Agents.Resilience
	retryCfg := mmw.RetryConfig{
		MaxAttempts:   res.Retry.MaxAttempts,
		InitialDelay:  res.Retry.InitialDelay,
		MaxDelay:      res.Retry.MaxDelay,
		BackoffFactor: res.Retry.BackoffFactor,
	}
	if res.Retry.Jitter {
		retryCfg.Jitter = 0.1
	}
	circuitCfg := mmw.CircuitConfig{
		FailureThreshold: res.CircuitBreaker.FailureThreshold,
		SuccessThreshold: res.CircuitBreaker.SuccessThreshold,
		OpenTimeout:      res.CircuitBreaker.Timeout,
	}

	// Shared per-provider limiter (built once in NewLLMClientFactory) so all
	// agents on a provider draw from one token bucket — not one per client.
	limiter := f.mllmsLimiters[provider]

	obs := &metricsObserver{recorder: f.metricsRecorder, stateProvider: stateProvider, logger: logger}

	// Order (ChainChat: first arg outermost): metrics → validation → retry →
	// [per-attempt timeout] → circuit → [rate limit] → provider. Timeout and
	// rate limit are conditional; built in order so no index juggling.
	mws := []mmw.ChatMiddleware{
		mmw.MetricsChat(obs),    // outermost: one aggregate Event incl. rejections (§5 M2)
		mmw.ValidationChat(),    // structural; agent-aware empty-response is the app-side wrapper below
		mmw.RetryChat(retryCfg), // §5 M1: retries iff llms.Retryable
	}
	if t := res.Timeout; t > 0 {
		mws = append(mws, mmw.TimeoutChat(t)) // per-attempt, between retry and circuit
	}
	mws = append(mws, mmw.CircuitChat(circuitCfg))
	if limiter != nil {
		mws = append(mws, mmw.RateLimitChat(limiter, mmw.DefaultEstimator{}))
	}

	chain := mmw.ChainChat(base, mws...)
	adapter := llmadapter.Wrap(chain, modelName)

	// §5 M3: agent-aware empty-response/pause-turn handling stays app-side
	// (the toolkit passes empty responses through). It must wrap at the
	// llm.LLMClient level — it mutates req.Messages with guidance and retries
	// — so it sits OUTSIDE the adapter, INSIDE the suspend boundary (an
	// empty-response error is not a provider-down signal).
	validator := validation.NewEmptyResponseValidator(validationAgentType(agentTypeStr))
	validated := validator.Wrap(adapter)

	return &suspendBoundary{inner: validated}, nil
}

// validationAgentType maps the agent-type string to the validator's agent
// type. PM responds with text like the architect (no forced tool calls);
// unknown defaults to coder (the stricter "must use tools" policy).
func validationAgentType(agentTypeStr string) validation.AgentType {
	switch Type(agentTypeStr) {
	case TypeArchitect, TypePM:
		return validation.AgentTypeArchitect
	case TypeCoder:
		return validation.AgentTypeCoder
	default:
		return validation.AgentTypeCoder
	}
}

// compile-time guards.
var (
	_ mmw.Observer  = (*metricsObserver)(nil)
	_ llm.LLMClient = (*suspendBoundary)(nil)
)

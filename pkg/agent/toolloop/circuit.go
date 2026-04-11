package toolloop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
)

// defaultMaxConsecutiveFailures is used when MaxConsecutiveFailures is 0.
const defaultMaxConsecutiveFailures = 3

// ToolCircuitBreakerConfig configures per-tool failure detection within a toolloop Run.
//
//nolint:govet // Function pointer logically grouped with threshold
type ToolCircuitBreakerConfig struct {
	// MaxConsecutiveFailures before the breaker trips for a given fingerprint.
	// Zero value defaults to 3.
	MaxConsecutiveFailures int

	// OnTrip is called when a tool's circuit breaker trips (optional).
	// Receives the tool name, human-readable label, and failure count.
	OnTrip func(toolName, label string, count int)
}

// classifyToolResult checks tool execution results for both Go errors and semantic
// failures (JSON "success": false). Returns whether the result represents a failure
// and a human-readable error detail string.
//
// Only "success": false is treated as a semantic failure. Non-zero exit_code alone
// is NOT a failure — the shell tool explicitly returns non-zero exit codes as normal
// data (grep, test -f, git diff --quiet all use non-zero exits for non-error conditions).
func classifyToolResult(execResult *tools.ExecResult, execErr error) (isFailure bool, errorDetail string) {
	// Go error is always a failure
	if execErr != nil {
		return true, execErr.Error()
	}

	if execResult == nil || execResult.Content == "" {
		return false, ""
	}

	// Try to parse content as JSON and check for semantic failure indicators.
	var result map[string]any
	if err := json.Unmarshal([]byte(execResult.Content), &result); err != nil {
		// Not JSON — treat as success (plain text tool output).
		return false, ""
	}

	// Check "success": false — the canonical semantic failure signal.
	// Tools like file_edit, read_file, list_files, build, test, lint all use this.
	if success, ok := result["success"]; ok {
		if successBool, ok := success.(bool); ok && !successBool {
			detail := "success: false"
			if errMsg, ok := result["error"].(string); ok && errMsg != "" {
				detail = errMsg
			}
			return true, detail
		}
	}

	return false, ""
}

// toolErrorTracker tracks per-tool failure patterns within a single toolloop Run.
type toolErrorTracker struct {
	config  *ToolCircuitBreakerConfig
	counts  map[string]int    // full fingerprint → consecutive failure count
	lastErr map[string]string // callKey → last error message (for synthetic response)
	tripped map[string]bool   // callKeys that have tripped
	logger  *logx.Logger
}

func newToolErrorTracker(cfg *ToolCircuitBreakerConfig, logger *logx.Logger) *toolErrorTracker {
	threshold := cfg.MaxConsecutiveFailures
	if threshold <= 0 {
		threshold = defaultMaxConsecutiveFailures
	}
	return &toolErrorTracker{
		config:  &ToolCircuitBreakerConfig{MaxConsecutiveFailures: threshold, OnTrip: cfg.OnTrip},
		counts:  make(map[string]int),
		lastErr: make(map[string]string),
		tripped: make(map[string]bool),
		logger:  logger,
	}
}

// callKey builds a key from tool name + hashed params that identifies "what we're
// trying to do" independent of the error. Used for checking tripped state.
func callKey(toolName string, params map[string]any) string {
	return toolName + ":" + hashParams(params)
}

// fullFingerprint builds a breaker-state key from tool name, params, and error.
// Different errors on the same call produce different fingerprints, so the counter
// only accumulates when the same call fails the same way repeatedly.
func fullFingerprint(toolName string, params map[string]any, errorDetail string) string {
	return callKey(toolName, params) + ":" + firstLine(errorDetail, 100)
}

// checkTripped returns true if any fingerprint for this tool+params is tripped.
func (t *toolErrorTracker) checkTripped(toolName string, params map[string]any) (tripped bool, lastError string) {
	key := callKey(toolName, params)
	if t.tripped[key] {
		return true, t.lastErr[key]
	}
	return false, ""
}

// recordFailure increments the counter for this specific fingerprint (tool+params+error).
// When the error changes for the same callKey, old fingerprints are cleared so only
// truly consecutive same-error failures accumulate toward the threshold.
func (t *toolErrorTracker) recordFailure(toolName string, params map[string]any, errorDetail string) {
	fp := fullFingerprint(toolName, params, errorDetail)
	key := callKey(toolName, params)

	// Clear other fingerprints for this callKey — if the error changed, the old
	// error's count must not persist (A,A,B,A should NOT trip A at 3).
	keyPrefix := key + ":"
	for k := range t.counts {
		if strings.HasPrefix(k, keyPrefix) && k != fp {
			delete(t.counts, k)
		}
	}

	t.counts[fp]++
	t.lastErr[key] = errorDetail

	if t.counts[fp] >= t.config.MaxConsecutiveFailures && !t.tripped[key] {
		t.tripped[key] = true
		label := displayLabel(toolName, params)
		t.logger.Warn("🔌 Circuit breaker tripped for %s after %d consecutive failures", label, t.counts[fp])
		if t.config.OnTrip != nil {
			t.config.OnTrip(toolName, label, t.counts[fp])
		}
	}
}

// recordSuccess resets failure tracking for the specific tool+params combination.
// A successful shell(cmd=pwd) does NOT clear failures for shell(cmd=make test).
func (t *toolErrorTracker) recordSuccess(toolName string, params map[string]any) {
	key := callKey(toolName, params)
	keyPrefix := key + ":"
	for k := range t.counts {
		if strings.HasPrefix(k, keyPrefix) {
			delete(t.counts, k)
		}
	}
	delete(t.tripped, key)
	delete(t.lastErr, key)
}

// hashParams produces a short hex hash of the canonicalized (sorted-key) JSON
// representation of tool parameters. This ensures map iteration order doesn't
// affect the fingerprint.
func hashParams(params map[string]any) string {
	if len(params) == 0 {
		return "empty"
	}
	// Sort keys for deterministic ordering
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build canonical representation
	ordered := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		ordered = append(ordered, k, params[k])
	}
	b, err := json.Marshal(ordered)
	if err != nil {
		return "unhashable"
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8]) // 16 hex chars
}

// displayLabel builds a human-readable label for logging, using the first
// significant parameter (cmd, command, path) if present.
func displayLabel(toolName string, params map[string]any) string {
	for _, key := range []string{"cmd", "command", "path"} {
		if val, ok := params[key]; ok {
			return fmt.Sprintf("%s(%s=%v)", toolName, key, val)
		}
	}
	return toolName
}

// firstLine returns the first line of s, truncated to maxLen characters.
func firstLine(s string, maxLen int) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return s
}

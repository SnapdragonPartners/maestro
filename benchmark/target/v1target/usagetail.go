package v1target

// Tailing the P-1 usage surface: v1-as-patched appends one JSONL line per
// LLM call to .maestro/usage.jsonl (versioned header). The adapter streams
// deltas through ReportUsage so the engine cancels at the cap, and the log
// totals become the canonical tokens/cost/llm_calls.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/target"
)

// usageSurfaceVersion is the P-1 log format this adapter speaks. It must
// match both the version advertised by `maestro -version` (validated in
// Describe, pre-run) and the log header (validated at first read).
//
// v2 records the provider, the five token axes separately, one instant plus
// an exact duration, a nullable cost and the failure text
// (docs/v2/phase_2/design_slice_import.md, D9). It is a replacement schema:
// prompt_tokens and completion_tokens no longer exist.
const usageSurfaceVersion = 2

// usageErrorFileName is the sentinel v1 writes next to the usage log on
// its first append/sync failure (pkg/agent/middleware/metrics
// UsageErrorFileName). Its presence means streamed usage is undercounting
// — fatal for the run regardless of a validated header.
const usageErrorFileName = "usage.error"

// usageTail incrementally reads the usage log across poll ticks.
type usageTail struct {
	report    func(target.UsageDelta)
	path      string
	errPath   string
	offset    int64
	calls     int64
	tokens    int64
	costUSD   float64
	validated bool
}

type usageHeader struct {
	UsageSurfaceVersion int `json:"usage_surface_version"`
}

// usageLine is one surface-v2 entry.
//
// The token axes and the cost are pointers because their ABSENCE is
// meaningful: a failed call carries no measurement (the target's toolkit
// reports none), and an unpriced model carries no cost. Decoding either into
// a bare zero would turn "not measured" into "measured as nothing", which is
// the confusion this surface version exists to end.
type usageLine struct {
	FinishedAt       *time.Time `json:"finished_at"`
	InputTokens      *int64     `json:"input_tokens"`
	OutputTokens     *int64     `json:"output_tokens"`
	ReasoningTokens  *int64     `json:"reasoning_tokens"`
	CacheReadTokens  *int64     `json:"cache_read_tokens"`
	CacheWriteTokens *int64     `json:"cache_write_tokens"`
	CostUSD          *float64   `json:"cost_usd"`
	Provider         string     `json:"provider"`
	Model            string     `json:"model"`
	Error            string     `json:"error"`
	LatencyNS        int64      `json:"latency_ns"`
	Success          bool       `json:"success"`
}

// budgetTokens is the cap-relevant total for one line: input plus visible
// output plus reasoning, exactly what v1's prompt+completion came to given
// that its completion field was BillableOutputTokens. A failed line
// contributes nothing, as its five zeros did before.
//
// Every axis is validated INDIVIDUALLY before it is summed. The engine's
// usageTracker guards against a negative delta, but it only ever sees this
// total: a line carrying {input: 1000000, output: -999999} would reach it as
// a delta of 1, sail past the guard, and under-account the attempt by two
// million tokens with the cap unenforced and nothing logged. The guard is not
// weak; it is downstream of the arithmetic that destroys the evidence.
func (l *usageLine) budgetTokens() (int64, error) {
	if l.InputTokens == nil {
		return 0, nil // failed call: no measurement, contributes nothing
	}
	var total int64
	for _, axis := range [...]struct {
		value *int64
		name  string
	}{
		{l.InputTokens, "input_tokens"}, {l.OutputTokens, "output_tokens"},
		{l.ReasoningTokens, "reasoning_tokens"}, {l.CacheReadTokens, "cache_read_tokens"},
		{l.CacheWriteTokens, "cache_write_tokens"},
	} {
		if axis.value == nil {
			return 0, fmt.Errorf("%s is missing from a measured call: availability is all-or-nothing", axis.name)
		}
		if *axis.value < 0 {
			return 0, fmt.Errorf("%s is %d: a negative axis can hide inside a nonnegative total", axis.name, *axis.value)
		}
		// Cache reads and writes are validated but NOT summed: adding them
		// would change what a declared cap means.
		if axis.name == "cache_read_tokens" || axis.name == "cache_write_tokens" {
			continue
		}
		if *axis.value > math.MaxInt64-total {
			return 0, fmt.Errorf("token total overflows int64 at %s", axis.name)
		}
		total += *axis.value
	}
	return total, nil
}

// validate enforces the surface's value and coherence rules on a line read
// from disk.
//
// The writer enforces the same rules, and that is not a reason to skip them
// here: the writer's guarantee is a fact about the process that produced the
// file, not about the bytes being read now.
func (l *usageLine) validate() error {
	if err := l.validateIdentity(); err != nil {
		return err
	}
	if err := l.validateOutcome(); err != nil {
		return err
	}
	return l.validateCost()
}

// validateIdentity checks the fields every line carries whatever happened.
func (l *usageLine) validateIdentity() error {
	switch {
	case strings.TrimSpace(l.Provider) == "":
		return fmt.Errorf("provider is blank")
	case strings.TrimSpace(l.Model) == "":
		return fmt.Errorf("model is blank")
	case l.FinishedAt == nil || l.FinishedAt.IsZero():
		// The zero time is year 1, so it would sort before every window a
		// query could ask about rather than failing visibly.
		return fmt.Errorf("finished_at is missing or the zero time")
	case l.LatencyNS < 0:
		// started_at is derived by subtracting this, so a negative duration
		// describes a call that ended before it began.
		return fmt.Errorf("latency_ns is %d", l.LatencyNS)
	}
	return nil
}

// validateOutcome checks the success/error/measurement triangle.
func (l *usageLine) validateOutcome() error {
	switch {
	case l.Success && l.Error != "":
		return fmt.Errorf("a successful call carries error %q", l.Error)
	case !l.Success && strings.TrimSpace(l.Error) == "":
		return fmt.Errorf("a failed call carries no error text")
	case l.Success && l.InputTokens == nil:
		return fmt.Errorf("a successful call carries no token measurement")
	case !l.Success && l.InputTokens != nil:
		return fmt.Errorf("a failed call carries token counts the provider never reported")
	}
	return nil
}

// validateCost checks the optional cost.
func (l *usageLine) validateCost() error {
	if l.CostUSD == nil {
		return nil
	}
	cost := *l.CostUSD
	if math.IsNaN(cost) || math.IsInf(cost, 0) {
		return fmt.Errorf("cost_usd is not finite")
	}
	if cost < 0 {
		return fmt.Errorf("cost_usd is %v", cost)
	}
	return nil
}

// advance reads any new complete lines, validates the header on first
// contact, streams deltas, and accumulates totals. A missing file is not
// an error (v1 may not have started yet); a bad header is fatal — the
// run half of the P-1 capability handshake.
func (u *usageTail) advance() error {
	if u.errPath != "" {
		raw, sentinelErr := os.ReadFile(u.errPath)
		switch {
		case sentinelErr == nil:
			return fmt.Errorf("target reported usage log write failure (streamed usage is undercounting): %s", strings.TrimSpace(string(raw)))
		case !os.IsNotExist(sentinelErr):
			// Any read error other than "does not exist" (permission,
			// I/O, it's-a-directory) is itself fatal: we cannot rule out
			// an undercounting sentinel we simply failed to read.
			return fmt.Errorf("usage error sentinel unreadable (%s): %w", u.errPath, sentinelErr)
		}
	}
	file, err := os.Open(u.path)
	if err != nil {
		return nil //nolint:nilerr // absent log = target not started; the pre-run handshake guarantees it will appear
	}
	defer file.Close() //nolint:errcheck // read-only tail
	if _, err := file.Seek(u.offset, 0); err != nil {
		return fmt.Errorf("usage log seek: %w", err)
	}
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return nil // incomplete tail line or EOF; next tick continues
		}
		u.offset += int64(len(line))
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !u.validated {
			var header usageHeader
			if err := json.Unmarshal([]byte(trimmed), &header); err != nil || header.UsageSurfaceVersion != usageSurfaceVersion {
				return fmt.Errorf("usage log header mismatch: want usage_surface_version %d, got %q", usageSurfaceVersion, trimmed)
			}
			u.validated = true
			continue
		}
		if err := u.consume(trimmed); err != nil {
			return err
		}
	}
}

// consume validates one entry line and folds it into the totals.
//
// A malformed line means the accounting is no longer trustworthy, which is
// precisely the condition the usage.error sentinel exists to make loud. The
// engine's usageTracker answers a bad delta with a bare return -- correct for
// a budget guard, which must not be walked backwards -- but that is the wrong
// response one layer up, where these totals become the run record's canonical
// figures. Fail the attempt rather than complete it with quietly wrong
// numbers, and report nothing from a line that did not validate.
func (u *usageTail) consume(trimmed string) error {
	var entry usageLine
	if err := json.Unmarshal([]byte(trimmed), &entry); err != nil {
		return fmt.Errorf("usage log line: %w", err)
	}
	if err := entry.validate(); err != nil {
		return fmt.Errorf("usage log line %d: %w", u.calls+1, err)
	}
	tokens, err := entry.budgetTokens()
	if err != nil {
		return fmt.Errorf("usage log line %d: %w", u.calls+1, err)
	}
	u.calls++
	if err := u.accumulate(tokens, entry.CostUSD); err != nil {
		return fmt.Errorf("usage log line %d: %w", u.calls, err)
	}
	if u.report != nil {
		var cost float64
		if entry.CostUSD != nil {
			cost = *entry.CostUSD
		}
		u.report(target.UsageDelta{Tokens: tokens, CostUSD: cost})
	}
	return nil
}

// accumulate adds one validated line to the running totals.
//
// The totals this maintains are, per this file's own header, "the canonical
// tokens/cost/llm_calls" for the run record. The engine's usageTracker
// carefully saturates its copy against overflow and non-finite sums; these
// had no such guard, so a total the tracker refused could still reach the
// record. Same protection, same reason.
func (u *usageTail) accumulate(tokens int64, cost *float64) error {
	if tokens > math.MaxInt64-u.tokens {
		return fmt.Errorf("accumulated token total overflows int64")
	}
	u.tokens += tokens
	if cost == nil {
		return nil // unpriced call: real tokens, no cost to add
	}
	sum := u.costUSD + *cost
	if math.IsNaN(sum) || math.IsInf(sum, 0) {
		return fmt.Errorf("accumulated cost is no longer finite")
	}
	u.costUSD = sum
	return nil
}

// verifyAdvertisedSurface is the pre-run half of the handshake: the target
// binary must advertise the expected usage-surface version in its -version
// output. A missing or mismatched advertisement is a target-identity
// error, never a silent downgrade.
func verifyAdvertisedSurface(versionOut string) error {
	want := fmt.Sprintf("usage-surface: v%d", usageSurfaceVersion)
	for _, line := range strings.Split(versionOut, "\n") {
		if strings.TrimSpace(line) == want {
			return nil
		}
	}
	return fmt.Errorf("target does not advertise %q in -version output: not a v1-as-patched build with the P-1 usage surface", want)
}

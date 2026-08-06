package v1target

// Tailing the P-1 usage surface: v1-as-patched appends one JSONL line per
// LLM call to .maestro/usage.jsonl (versioned header). The adapter streams
// deltas through ReportUsage so the engine cancels at the cap, and the log
// totals become the canonical tokens/cost/llm_calls.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
// Every field is a pointer, including the ones that are always written.
//
// Presence and value are different questions, and a plain value type can only
// answer the second. A missing `success` would decode as false and be read as
// a failure that happened; a missing `latency_ns` would decode as zero and be
// read as an instantaneous call. Both are lines this reader must refuse, and
// neither is distinguishable after the zero value has been substituted.
type usageLine struct {
	FinishedAt       *time.Time `json:"finished_at"`
	InputTokens      *int64     `json:"input_tokens"`
	OutputTokens     *int64     `json:"output_tokens"`
	ReasoningTokens  *int64     `json:"reasoning_tokens"`
	CacheReadTokens  *int64     `json:"cache_read_tokens"`
	CacheWriteTokens *int64     `json:"cache_write_tokens"`
	CostUSD          *float64   `json:"cost_usd"`
	Provider         *string    `json:"provider"`
	Model            *string    `json:"model"`
	StoryID          *string    `json:"story_id"`
	AgentID          *string    `json:"agent_id"`
	Error            *string    `json:"error"`
	LatencyNS        *int64     `json:"latency_ns"`
	Success          *bool      `json:"success"`
}

// decodeUsageLine parses one entry STRICTLY.
//
// Unknown keys are refused rather than ignored. A line carrying a field this
// build does not know is a line written by a surface this build does not
// speak, and the header version is supposed to be what catches that -- so a
// disagreement here means the header lied, which is not a condition to read
// past. Trailing content after the object is refused for the same reason.
func decodeUsageLine(trimmed string) (usageLine, error) {
	var entry usageLine
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entry); err != nil {
		return entry, fmt.Errorf("usage log line: %w", err)
	}
	// Exhaustion is proven by DECODING again and requiring io.EOF, not by
	// asking Decoder.More().
	//
	// More() answers "is there another element in the array or object I am
	// currently inside?", and at the top level there is no such container: it
	// peeks, sees a closing `]` or `}`, and answers false. So a line like
	// `{...}]` reported no trailing content and was accepted -- the one form
	// of trailing garbage that a check written to catch trailing garbage let
	// through. A second Decode has no such blind spot: anything other than a
	// clean EOF is content this line should not contain.
	var rest json.RawMessage
	if err := decoder.Decode(&rest); !errors.Is(err, io.EOF) {
		return entry, fmt.Errorf("usage log line: trailing content after the entry object")
	}
	return entry, nil
}

// checkUsageHeader validates the log's first line STRICTLY.
//
// Unknown fields and trailing content are refused for the same reason a line
// refuses them, and this is the place it matters most: the header decides
// how every line below it is read. json.Unmarshal ignored unknown keys and
// this reader accepted `{...}]`, while the importer -- the other reader of
// this surface -- refused both, so the two disagreed about which files are
// readable at all.
func checkUsageHeader(trimmed string) error {
	var header usageHeader
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&header); err != nil {
		return fmt.Errorf("usage log header unreadable: want usage_surface_version %d, got %q: %w",
			usageSurfaceVersion, trimmed, err)
	}
	var rest json.RawMessage
	if err := decoder.Decode(&rest); !errors.Is(err, io.EOF) {
		return fmt.Errorf("usage log header carries content after the object: %q", trimmed)
	}
	if header.UsageSurfaceVersion != usageSurfaceVersion {
		return fmt.Errorf("usage log header mismatch: want usage_surface_version %d, got %q",
			usageSurfaceVersion, trimmed)
	}
	return nil
}

// tokenAxes returns the line's five axes in a fixed order, for the rules that
// treat availability as a property of the whole measurement.
func (l *usageLine) tokenAxes() [5]struct {
	value *int64
	name  string
} {
	return [5]struct {
		value *int64
		name  string
	}{
		{l.InputTokens, "input_tokens"}, {l.OutputTokens, "output_tokens"},
		{l.ReasoningTokens, "reasoning_tokens"}, {l.CacheReadTokens, "cache_read_tokens"},
		{l.CacheWriteTokens, "cache_write_tokens"},
	}
}

// measured reports whether the line carries a complete token measurement, and
// refuses a partial one.
//
// Testing InputTokens alone was not enough: a line carrying only
// output_tokens read as "no measurement" and its stray axis was never
// examined. Availability is a property of the observation, so all five decide
// it together.
func (l *usageLine) measured() (bool, error) {
	present := 0
	for _, axis := range l.tokenAxes() {
		if axis.value != nil {
			present++
		}
	}
	switch present {
	case 0:
		return false, nil
	case len(l.tokenAxes()):
		return true, nil
	default:
		return false, fmt.Errorf("%d of %d token axes are present; availability is all-or-nothing, "+
			"and a missing axis would read as zero in every total", present, len(l.tokenAxes()))
	}
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
	measured, err := l.measured()
	if err != nil {
		return 0, err
	}
	if !measured {
		return 0, nil // failed call: no measurement, contributes nothing
	}
	var total int64
	for _, axis := range l.tokenAxes() {
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
//
// Presence is checked before value throughout: an absent required field is a
// different fault from a bad one, and reporting it as a bad value would name
// a zero the writer never wrote.
func (l *usageLine) validateIdentity() error {
	switch {
	case l.Provider == nil:
		return fmt.Errorf("provider is missing")
	case strings.TrimSpace(*l.Provider) == "":
		return fmt.Errorf("provider is blank")
	case l.Model == nil:
		return fmt.Errorf("model is missing")
	case strings.TrimSpace(*l.Model) == "":
		return fmt.Errorf("model is blank")
	case l.FinishedAt == nil:
		return fmt.Errorf("finished_at is missing")
	case l.FinishedAt.IsZero():
		// The zero time is year 1, so it would sort before every window a
		// query could ask about rather than failing visibly.
		return fmt.Errorf("finished_at is the zero time")
	case l.LatencyNS == nil:
		// Absent would decode as zero and read as an instantaneous call.
		return fmt.Errorf("latency_ns is missing")
	case *l.LatencyNS < 0:
		// started_at is derived by subtracting this, so a negative duration
		// describes a call that ended before it began.
		return fmt.Errorf("latency_ns is %d", *l.LatencyNS)
	}
	return nil
}

// validateOutcome checks the success/error/measurement triangle.
func (l *usageLine) validateOutcome() error {
	if l.Success == nil {
		// Absent would decode as false: a line that never said how it ended
		// would be read as a failure that did.
		return fmt.Errorf("success is missing")
	}
	measured, err := l.measured()
	if err != nil {
		return err
	}
	success := *l.Success
	// An empty error string is a THIRD state, distinct from absent and from
	// present-and-meaningful, and it must not read as either. A failure
	// carrying "" says nothing about what went wrong; a success carrying ""
	// wrote a field the schema says a success does not have.
	switch {
	case success && l.Error != nil:
		return fmt.Errorf("a successful call carries an error field (%q)", *l.Error)
	case !success && l.Error == nil:
		return fmt.Errorf("a failed call carries no error text")
	case !success && strings.TrimSpace(*l.Error) == "":
		return fmt.Errorf("a failed call carries a blank error text")
	case success && !measured:
		return fmt.Errorf("a successful call carries no token measurement")
	case !success && measured:
		return fmt.Errorf("a failed call carries token counts the provider never reported")
	case !success && l.CostUSD != nil:
		// Cost is computed FROM the token counts a failed call does not
		// have, so the producer never prices one.
		return fmt.Errorf("a failed call carries cost %v, computed from tokens nobody measured", *l.CostUSD)
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
			if err := checkUsageHeader(trimmed); err != nil {
				return err
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
	entry, decodeErr := decodeUsageLine(trimmed)
	if decodeErr != nil {
		return decodeErr
	}
	if err := entry.validate(); err != nil {
		return fmt.Errorf("usage log line %d: %w", u.calls+1, err)
	}
	tokens, err := entry.budgetTokens()
	if err != nil {
		return fmt.Errorf("usage log line %d: %w", u.calls+1, err)
	}
	// Every proposed total is computed and proven BEFORE any of them is
	// committed. Incrementing calls and tokens first left a rejected line
	// half-applied: two individually finite costs can overflow cumulatively,
	// so the cost check could fail after the token total had already absorbed
	// the line it was rejecting. A line is either wholly accounted or wholly
	// refused.
	nextTokens, nextCost, err := u.proposedTotals(tokens, entry.CostUSD)
	if err != nil {
		return fmt.Errorf("usage log line %d: %w", u.calls+1, err)
	}
	u.calls++
	u.tokens = nextTokens
	u.costUSD = nextCost
	if u.report != nil {
		var cost float64
		if entry.CostUSD != nil {
			cost = *entry.CostUSD
		}
		u.report(target.UsageDelta{Tokens: tokens, CostUSD: cost})
	}
	return nil
}

// proposedTotals computes what the running totals WOULD become, without
// changing them.
//
// The totals it feeds are, per this file's own header, "the canonical
// tokens/cost/llm_calls" for the run record. The engine's usageTracker
// carefully saturates its copy against overflow and non-finite sums; these
// had no such guard, so a total the tracker refused could still reach the
// record. Same protection, same reason -- and computed rather than applied,
// so a rejection leaves nothing behind.
func (u *usageTail) proposedTotals(tokens int64, cost *float64) (int64, float64, error) {
	if tokens > math.MaxInt64-u.tokens {
		return 0, 0, fmt.Errorf("accumulated token total overflows int64")
	}
	nextTokens := u.tokens + tokens
	if cost == nil {
		return nextTokens, u.costUSD, nil // unpriced call: real tokens, no cost to add
	}
	// Finite addends can still sum to an infinity, which is why this checks
	// the SUM and not the addend the line carried.
	nextCost := u.costUSD + *cost
	if math.IsNaN(nextCost) || math.IsInf(nextCost, 0) {
		return 0, 0, fmt.Errorf("accumulated cost is no longer finite")
	}
	return nextTokens, nextCost, nil
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

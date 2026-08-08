package benchmarkimport

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UsageSurfaceVersion is the usage-log contract this build reads.
//
// It mirrors metrics.UsageSurfaceVersion, and the mirror is the same
// deliberate one the record contract makes (see the package comment): this
// package reads FILES. The header carries the version, so a log written at
// another version is recognised rather than mis-parsed — which is the whole
// reason the header exists, and why design D9 gates the legacy path on it.
const UsageSurfaceVersion = 2

// usageLogFileName is the log's name inside an attempt's evidence directory.
// The adapter copies it there from the project's .maestro directory.
const usageLogFileName = "usage.jsonl"

// MaxUsageLineBytes bounds one usage line, matching the record reader's
// limit and the writer's own.
//
// One number shared by every component that touches this surface: a line the
// writer is willing to emit must be one both readers are willing to read, or
// a legal call becomes an unreadable log. It is exported so the two-sided
// corpus can assert the three agree rather than trusting that they do.
const MaxUsageLineBytes = 16 * 1024 * 1024

// usageHeader is the log's first line.
type usageHeader struct {
	UsageSurfaceVersion *int `json:"usage_surface_version"`
}

// UsageLine is one LLM call as it appears on disk.
//
// EVERY field is a pointer, including the ones always written. Presence and
// value are different questions and a plain value type can only answer the
// second: a missing `success` would decode as false and read as a failure
// that happened, and a missing `latency_ns` would decode as zero and read as
// an instantaneous call. Both are lines this reader must refuse, and neither
// is distinguishable once the zero value has been substituted.
type UsageLine struct {
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

// UsageLog is one attempt's per-call surface, or the reason there is none.
//
// Absence is a first-class outcome rather than an error. A surface-v1 suite
// in benchmark/runs/ imports normally and simply cannot yield calls — its
// axes cannot be honestly split (design D9) — and an attempt whose evidence
// was pruned is in the same position. What must not happen is a silent zero:
// "this attempt made no calls" and "this attempt's calls are not knowable"
// are different facts, and only the second needs the reason below.
type UsageLog struct {
	// Reason is empty exactly when the lines are usable. When it is set,
	// Lines is empty and this is what the import summary and the suite
	// report record in place of calls.
	Reason string
	Lines  []UsageLine
}

// Available reports whether this attempt's calls can be imported.
func (u *UsageLog) Available() bool { return u.Reason == "" }

// LegacyUsageSurfaceVersion is the one older surface whose logs are read as
// an absence rather than refused.
//
// v1 is a REVIEWED case: it folds reasoning into a completion count, so its
// axes cannot be split after the fact, and every suite in benchmark/runs/
// today is one (design D9). No other version has been examined — a v3 log
// was written by a contract this build has never seen, and treating it as
// "no calls" would silently discard measurements that exist. Unknown is
// refused; only this one is legacy.
const LegacyUsageSurfaceVersion = 1

// The reasons an attempt yields no calls. Each is a different fact about the
// store, and an operator reading a report needs to tell them apart.
const (
	usageNoEvidence   = "the attempt has no evidence directory in this store"
	usageNoLog        = "the attempt's evidence carries no usage log"
	usageEmptyLog     = "the usage log is empty, so it carries no surface version"
	usageLegacyFormat = "the usage log is surface v%d; only v%d records the axes a call row needs"
)

// DecodeUsageLine parses one line STRICTLY.
//
// Unknown keys are refused rather than ignored, for the same reason the
// record reader refuses them: a line carrying a field this build does not
// know was written by a surface this build does not speak, and the header
// version is what is supposed to catch that — so a disagreement here means
// the header lied, which is not a condition to read past. Exhaustion is
// proven by decoding a SECOND value and requiring io.EOF, never by
// Decoder.More, which answers a question about the container it is inside
// and so reports false for the trailing `]` or `}` that most looks like an
// ending.
func DecodeUsageLine(line string) (UsageLine, error) {
	var entry UsageLine
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entry); err != nil {
		return entry, fmt.Errorf("usage line: %w", err)
	}
	var rest json.RawMessage
	if err := decoder.Decode(&rest); !isEOF(err) {
		return entry, errors.New("usage line: content follows the entry object")
	}
	return entry, nil
}

// tokenAxes returns the five axes in a fixed order.
func (l *UsageLine) tokenAxes() [5]struct {
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

// Measured reports whether the line carries a COMPLETE token measurement,
// and refuses a partial one.
//
// Availability is a property of the observation, so all five axes decide it
// together. Testing one axis alone would read a line carrying only
// output_tokens as "no measurement" and never examine the stray axis, which
// then reads as zero in every total that follows.
func (l *UsageLine) Measured() (bool, error) {
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

// Validate enforces the surface's value and coherence rules on a line read
// from disk.
//
// The writer enforces the same rules and that is not a reason to skip them:
// the writer's guarantee is a fact about the process that produced the file,
// not about the bytes being read now — which may have been truncated, hand
// edited, or written by a different build entirely.
func (l *UsageLine) Validate() error {
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
// Presence before value throughout: an absent required field is a different
// fault from a bad one, and reporting it as a bad value would name a zero
// the writer never wrote.
func (l *UsageLine) validateIdentity() error {
	switch {
	case l.Provider == nil:
		return errors.New("provider is missing")
	case strings.TrimSpace(*l.Provider) == "":
		// A blank provider is the omission design D9 exists to prevent,
		// arriving through a different door.
		return errors.New("provider is blank")
	case l.Model == nil:
		return errors.New("model is missing")
	case strings.TrimSpace(*l.Model) == "":
		return errors.New("model is blank")
	case l.FinishedAt == nil:
		return errors.New("finished_at is missing")
	case l.FinishedAt.IsZero():
		// The zero time is year 1, so it sorts before every window a query
		// could ask about rather than failing visibly.
		return errors.New("finished_at is the zero time")
	case l.LatencyNS == nil:
		return errors.New("latency_ns is missing")
	case *l.LatencyNS < 0:
		// started_at is DERIVED by subtracting this, so a negative duration
		// describes a call that ended before it began — which the row's own
		// ordering then refuses at the far end of the import.
		return fmt.Errorf("latency_ns is %d", *l.LatencyNS)
	}
	return nil
}

// validateOutcome checks the success/error/measurement triangle, then the
// axes of whatever measurement survived it.
func (l *UsageLine) validateOutcome() error {
	if l.Success == nil {
		return errors.New("success is missing")
	}
	measured, err := l.Measured()
	if err != nil {
		return err
	}
	if err := l.validateTriangle(measured); err != nil {
		return err
	}
	if !measured {
		return nil
	}
	for _, axis := range l.tokenAxes() {
		// Per axis, never on the sum. A negative axis hides inside a
		// nonnegative total: {input: 1000000, output: -999999} sums to 1 and
		// under-accounts the call by two million tokens.
		if *axis.value < 0 {
			return fmt.Errorf("%s is %d", axis.name, *axis.value)
		}
	}
	return l.validateTokenRange()
}

// validateTriangle checks success against the error text and the presence of
// a measurement.
//
// An empty error string is a THIRD state, distinct from absent and from
// present-and-meaningful, and must not read as either.
func (l *UsageLine) validateTriangle(measured bool) error {
	switch {
	case *l.Success && l.Error != nil:
		return fmt.Errorf("a successful call carries an error field (%q)", *l.Error)
	case !*l.Success && l.Error == nil:
		return errors.New("a failed call carries no error text")
	case !*l.Success && strings.TrimSpace(*l.Error) == "":
		return errors.New("a failed call carries a blank error text")
	case *l.Success && !measured:
		return errors.New("a successful call carries no token measurement")
	case !*l.Success && measured:
		// The toolkit populates usage only when the error is nil, so counts
		// on a failed call are a measurement nobody made.
		return errors.New("a failed call carries token counts the provider never reported")
	case !*l.Success && l.CostUSD != nil:
		// Cost is computed FROM the token counts, which a failed call does
		// not have, so the producer never prices one. A cost here is the
		// same fabrication as the counts, one column over.
		return fmt.Errorf("a failed call carries cost %v, which was computed from tokens nobody measured",
			*l.CostUSD)
	}
	return nil
}

// validateTokenRange refuses a measurement whose axes cannot be summed.
//
// A wrapped sum is a small positive number that looks entirely ordinary, so
// the overflow has to be caught while the axes are still separate. The total
// is the budget one, so the two readers of this surface agree about which
// lines are READABLE: a file the tail refused mid-run must not import
// cleanly afterwards.
func (l *UsageLine) validateTokenRange() error {
	_, err := l.budgetTokens()
	return err
}

// validateCost checks the optional cost.
func (l *UsageLine) validateCost() error {
	if l.CostUSD == nil {
		return nil // unpriced: absent means not knowable, never free
	}
	cost := *l.CostUSD
	switch {
	case math.IsNaN(cost) || math.IsInf(cost, 0):
		// Non-finite propagates through every sum it touches, and the
		// plane's numeric column cannot store it at all.
		return errors.New("cost_usd is not finite")
	case cost < 0:
		return fmt.Errorf("cost_usd is %v", cost)
	}
	return nil
}

// UsageTotals is what the log says about the attempt as a whole.
//
// Recomputed here by the SAME arithmetic the budget tail used to produce the
// record's canonical figures — file order, budget axes only, float64 cost —
// because the point is to compare them, and a different summation would
// disagree by rounding alone.
type UsageTotals struct {
	Cost   float64
	Calls  int64
	Tokens int64
}

// Totals recomputes the canonical figures from the lines.
func (u *UsageLog) Totals() (UsageTotals, error) {
	var totals UsageTotals
	for index := range u.Lines {
		line := &u.Lines[index]
		tokens, err := line.budgetTokens()
		if err != nil {
			return UsageTotals{}, fmt.Errorf("call %d: %w", index+1, err)
		}
		if tokens > math.MaxInt64-totals.Tokens {
			return UsageTotals{}, fmt.Errorf("call %d: the accumulated token total overflows int64", index+1)
		}
		totals.Calls++
		totals.Tokens += tokens
		if line.CostUSD != nil {
			// Finite addends can still sum to an infinity, so the SUM is
			// what is checked rather than the value the line carried.
			totals.Cost += *line.CostUSD
			if math.IsNaN(totals.Cost) || math.IsInf(totals.Cost, 0) {
				return UsageTotals{}, fmt.Errorf("call %d: the accumulated cost is no longer finite", index+1)
			}
		}
	}
	return totals, nil
}

// budgetTokens is one line's contribution to the cap-relevant total: input
// plus visible output plus reasoning. Cache reads and writes are recorded
// and NOT added, because adding them would change what a declared cap meant.
// A failed line contributes nothing, having measured nothing.
func (l *UsageLine) budgetTokens() (int64, error) {
	measured, err := l.Measured()
	if err != nil || !measured {
		return 0, err
	}
	var total int64
	for _, axis := range l.tokenAxes() {
		if axis.name == "cache_read_tokens" || axis.name == "cache_write_tokens" {
			continue
		}
		if *axis.value > math.MaxInt64-total {
			return 0, fmt.Errorf("the token axes overflow int64 at %s", axis.name)
		}
		total += *axis.value
	}
	return total, nil
}

// Reconcile checks the log against the record's own canonical metrics.
//
// The record and the log are TWO ACCOUNTS OF ONE ATTEMPT written by the same
// process: the tail streams the log, and its running totals are what
// `llm_calls`, `tokens_total` and `cost_usd` are set from (run.go's metrics).
// So they cannot legitimately disagree, and if they do, one of them has been
// edited, truncated or rewritten since the run — in which case importing
// both would put two contradicting authoritative accounts in the plane, the
// per-call rows saying one thing and the metric events beside them another.
//
// An ACCEPTED attempt must additionally have MEASURED its calls and tokens.
// It ran to completion, so its metrics are the target's own observation, and
// a validated log always produces measured counts there — silence beside a
// readable log would mean per-call rows with no canonical total to agree
// with.
//
// The requirement stops at accepted, deliberately, because a FAILED attempt
// legitimately has both. When the target errors, the engine synthesizes
// `unavailable` for every supported metric and then overlays only the
// streamed tracker totals — `tokens_total` and `cost_usd`, never
// `llm_calls` (engine/attempt.go, overlayStreamedUsage) — while evidence,
// including the usage log, is still exported. So a target-error attempt
// carrying real calls beside `llm_calls: unavailable` is the ordinary
// shape of that path, and requiring measurement there would refuse every
// one of them.
//
// cost_usd is never required: a local config's cost is `unavailable` by
// item 5.1 rather than the log's zero passed through.
func (u *UsageLog) Reconcile(record *Record) error {
	if !u.Available() {
		return nil // nothing was read, so there is nothing to reconcile
	}
	totals, err := u.Totals()
	if err != nil {
		return err
	}
	checks := []struct {
		key      string
		computed float64
		// required marks a metric the record MUST have measured when a
		// readable log sits beside it.
		required bool
	}{
		// An accepted attempt ran to completion, so its metrics come from
		// the target's own observation, where a validated log always yields
		// measured counts. Silence there is a contradiction: per-call rows
		// with no canonical total to agree with.
		{"llm_calls", float64(totals.Calls), record.Verdict == verdictAccepted},
		{"tokens_total", float64(totals.Tokens), record.Verdict == verdictAccepted},
		// cost_usd is NEVER required. A local config's cost is `unavailable`
		// by contract (item 5.1) rather than the log's zero passed through,
		// and that is a legitimate silence beside a log full of calls.
		{"cost_usd", totals.Cost, false},
	}
	for index := range checks {
		check := &checks[index]
		metric, present := record.Metrics[check.key]
		measured := present && metric.Status == statusValue && metric.Value != nil
		if !measured {
			if check.required {
				return fmt.Errorf("%w: the record's usage log accounts for %v %s, but the record "+
					"itself declines to measure it; an accepted attempt's metrics come from the run "+
					"that wrote this log, so it cannot have both",
					ErrIncoherent, check.computed, check.key)
			}
			continue
		}
		if *metric.Value != check.computed {
			return fmt.Errorf("%w: the record reports %s = %v while its usage log accounts for %v; "+
				"both were written by the same run, so one of them has changed since",
				ErrIncoherent, check.key, *metric.Value, check.computed)
		}
	}
	return nil
}

// StartedAt derives the instant the orchestrator began asking.
//
// The recorded latency is the WHOLE LOGICAL CALL — Maestro places the metrics
// middleware outermost, so it folds in validation, every retry and its
// backoff, per-attempt timeouts, circuit behaviour and rate-limit waiting
// (design D9). So this interval can be far longer than any single provider
// round trip, and per-attempt latency is not recoverable from this surface at
// all. It is nonetheless the right start for a call row: cost-over-time and
// concurrency questions want the span the orchestrator was actually engaged.
func (l *UsageLine) StartedAt() time.Time {
	return l.FinishedAt.Add(-time.Duration(*l.LatencyNS))
}

// ReadUsageLog loads one attempt's calls, or reports why there are none.
//
// The log is found by walking the store's own layout, exactly as evidence is
// (design D8): the record's pointers are absolute paths from the machine that
// ran the attempt, and the store is portable while those paths are not.
// EvidenceDir has already proved containment and refused symlinks for the
// directory; the file itself is checked here for the same reason, since a
// containment check on a directory says nothing about a link inside it.
func (s *Suite) ReadUsageLog(runID string) (*UsageLog, error) {
	dir, present, err := s.EvidenceDir(runID)
	if err != nil {
		return nil, err
	}
	if !present {
		return &UsageLog{Reason: usageNoEvidence}, nil
	}
	path := filepath.Join(dir, usageLogFileName)
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return &UsageLog{Reason: usageNoLog}, nil
	case err != nil:
		return nil, fmt.Errorf("inspect usage log %s: %w", path, err)
	case info.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("%w: the usage log (%s) is a symbolic link; calls are read from the "+
			"store's own layout, and a link can attribute one attempt's calls to another even when "+
			"its target is inside the store", ErrIncoherent, path)
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%w: the usage log (%s) is not a regular file", ErrIncoherent, path)
	}
	file, err := os.Open(path) //nolint:gosec // runID is pattern-checked and contained by EvidenceDir
	if err != nil {
		return nil, fmt.Errorf("open usage log %s: %w", path, err)
	}
	defer file.Close() //nolint:errcheck // read-only
	return readUsageLines(file, path)
}

// readUsageLines parses the header and then every entry.
//
// COMPLETE LINES ONLY, which is the tail's protocol and must be this
// reader's too. The tail consumes a line only once its newline has arrived,
// so a torn final write is never counted in the run record's canonical
// totals. A reader that accepted the partial line would import a call the
// record does not know about — and the reconciliation below would then be
// comparing against a set the two sides disagree on. bufio.Scanner returns a
// final unterminated token, which is exactly the wrong behaviour here, so
// the framing is done with a Reader instead.
func readUsageLines(source io.Reader, path string) (*UsageLog, error) {
	reader := bufio.NewReaderSize(source, 64*1024)
	log := &UsageLog{}
	header := false
	for line := 1; ; line++ {
		text, err := readCompleteLine(reader, path)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if text == "" {
			continue
		}
		if !header {
			version, headerErr := decodeUsageHeader(text, path)
			if headerErr != nil {
				return nil, headerErr
			}
			header = true
			if version != UsageSurfaceVersion {
				return legacyOrRefused(version, path)
			}
			continue
		}
		entry, decodeErr := DecodeUsageLine(text)
		if decodeErr != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, line, decodeErr)
		}
		if validateErr := entry.Validate(); validateErr != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, line, validateErr)
		}
		log.Lines = append(log.Lines, entry)
	}
	if !header {
		// A file with no complete header line states no surface version, so
		// nothing in it can be read as any version. Reported rather than
		// refused: an attempt whose log was truncated to nothing still
		// imports, without calls.
		return &UsageLog{Reason: usageEmptyLog}, nil
	}
	return log, nil
}

// legacyOrRefused classifies a log this build cannot read as v2.
func legacyOrRefused(version int, path string) (*UsageLog, error) {
	if version == LegacyUsageSurfaceVersion {
		// A recorded absence, not a failure. Historical suites are v1 and
		// import normally; what they cannot do is yield call rows.
		return &UsageLog{Reason: fmt.Sprintf(usageLegacyFormat, version, UsageSurfaceVersion)}, nil
	}
	return nil, fmt.Errorf("%w: usage log %s declares surface v%d, which this build has never seen; "+
		"v%d is read and v%d is the one known legacy format, and treating an unknown contract as "+
		"'no calls' would discard measurements that are there",
		ErrIncoherent, path, version, UsageSurfaceVersion, LegacyUsageSurfaceVersion)
}

// readCompleteLine returns the next NEWLINE-TERMINATED line, trimmed.
//
// A trailing fragment is discarded with io.EOF rather than returned: the
// writer appends whole lines, so a fragment is a write that did not finish,
// and the tail already excluded it from everything the record says.
//
// The size cap is applied WHILE READING, not after. ReadString allocates
// until it finds a newline, so a hostile or torn log holding one enormous
// unterminated run of bytes would be read into memory in full and only then
// measured — the check would report the problem after doing the damage it
// exists to prevent. ReadSlice returns what fits in the buffer and says
// there is more, so the total is bounded as it accumulates.
func readCompleteLine(reader *bufio.Reader, path string) (string, error) {
	var line strings.Builder
	for {
		chunk, err := reader.ReadSlice('\n')
		if line.Len()+len(chunk) > MaxUsageLineBytes {
			return "", fmt.Errorf("%w: usage log %s carries a line over %d bytes",
				ErrIncoherent, path, MaxUsageLineBytes)
		}
		line.Write(chunk)
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue // more of this line follows
		case errors.Is(err, io.EOF):
			// Whatever accumulated here reached no newline, so it is a write
			// that did not finish. Dropped, exactly as the tail drops it.
			return "", io.EOF
		case err != nil:
			return "", fmt.Errorf("read usage log %s: %w", path, err)
		}
		return strings.TrimSpace(line.String()), nil
	}
}

// decodeUsageHeader reads the first line's surface version.
//
// A header that does not parse is REFUSED rather than treated as legacy.
// Legacy is a version this build knows it cannot use; an unparseable first
// line is a file whose contents nothing has established, and reading past it
// would be guessing at bytes that decide whether every line below means what
// it appears to.
func decodeUsageHeader(text, path string) (int, error) {
	var header usageHeader
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&header); err != nil {
		return 0, fmt.Errorf("%w: usage log %s has no readable header: %w", ErrIncoherent, path, err)
	}
	// Exhaustion proven the same way every other line in this package proves
	// it. The header decides how every line below it is read, so it is the
	// last place to accept trailing content.
	var rest json.RawMessage
	if err := decoder.Decode(&rest); !isEOF(err) {
		return 0, fmt.Errorf("%w: usage log %s carries content after its header", ErrIncoherent, path)
	}
	if header.UsageSurfaceVersion == nil {
		return 0, fmt.Errorf("%w: usage log %s has a header naming no surface version", ErrIncoherent, path)
	}
	return *header.UsageSurfaceVersion, nil
}

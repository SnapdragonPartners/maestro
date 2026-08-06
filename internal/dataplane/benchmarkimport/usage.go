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

// maxUsageLineBytes bounds one line, matching the record reader's limit.
const maxUsageLineBytes = 16 * 1024 * 1024

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
	}
	return nil
}

// validateTokenRange refuses a measurement whose axes cannot be summed.
//
// A wrapped sum is a small positive number that looks entirely ordinary, so
// the overflow has to be caught while the axes are still separate.
//
// The total is input + output + reasoning, which is the budget total the
// engine's cap was enforced against — cache reads and writes are validated
// but not added, because adding them would change what a declared cap meant
// (design D9). This importer never sums the axes itself, since the plane
// stores each in its own column, so the check is here for a different reason
// than the tail's: the two readers of this surface must not disagree about
// which lines are READABLE. A file the tail refused mid-run must not import
// cleanly afterwards.
func (l *UsageLine) validateTokenRange() error {
	var total int64
	for _, axis := range l.tokenAxes() {
		if axis.name == "cache_read_tokens" || axis.name == "cache_write_tokens" {
			continue
		}
		if *axis.value > math.MaxInt64-total {
			return fmt.Errorf("the token axes overflow int64 at %s", axis.name)
		}
		total += *axis.value
	}
	return nil
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
func readUsageLines(source io.Reader, path string) (*UsageLog, error) {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 0, 64*1024), maxUsageLineBytes)

	log := &UsageLog{}
	header := false
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if !header {
			version, err := decodeUsageHeader(text, path)
			if err != nil {
				return nil, err
			}
			header = true
			if version != UsageSurfaceVersion {
				// A recorded absence, not a failure. Historical suites are
				// v1 and import normally; what they cannot do is yield call
				// rows, because the axes cannot be honestly split.
				return &UsageLog{Reason: fmt.Sprintf(usageLegacyFormat, version, UsageSurfaceVersion)}, nil
			}
			continue
		}
		entry, err := DecodeUsageLine(text)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, line, err)
		}
		if err := entry.Validate(); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, line, err)
		}
		log.Lines = append(log.Lines, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan usage log %s: %w", path, err)
	}
	if !header {
		// A file with no header states no surface version, so nothing in it
		// can be read as any version. Reported rather than refused: an
		// attempt whose log was truncated to nothing still imports.
		return &UsageLog{Reason: usageEmptyLog}, nil
	}
	return log, nil
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
	if header.UsageSurfaceVersion == nil {
		return 0, fmt.Errorf("%w: usage log %s has a header naming no surface version", ErrIncoherent, path)
	}
	return *header.UsageSurfaceVersion, nil
}

package metrics

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"orchestrator/pkg/logx"
)

// UsageSurfaceVersion identifies the durable per-LLM-call usage log format
// — the P-1 benchmark usage surface (docs/v2/phase_1/design_adapter_v1.md).
// The benchmark runner validates this version pre-run (advertised via
// maestro -version) and against the log header at run time; bump it on any
// format change.
//
// v2 (docs/v2/phase_2/design_slice_import.md, D9) is a REPLACEMENT schema,
// not v1 plus fields: it records the provider, the five token axes apart
// rather than folding reasoning into a completion count, one instant plus an
// exact duration rather than three overlapping timestamps, a nullable cost so
// unpriced and free stay distinct, and the failure text. The v1 fields
// prompt_tokens and completion_tokens are gone rather than kept beside their
// replacements.
const UsageSurfaceVersion = 2

// UsageLogFileName is the log's location under the project .maestro dir.
const UsageLogFileName = "usage.jsonl"

// MaxUsageLineBytes bounds one line of the log.
//
// One number shared by every component that touches this surface: the budget
// tail and the importer both refuse a longer line, so a line this writer
// emits past the cap makes the whole file unreadable rather than just that
// call. Bounded here so the disagreement cannot arise.
const MaxUsageLineBytes = 16 * 1024 * 1024

// maxUsageErrorBytes bounds the one field that can grow without limit.
//
// A provider error can carry an entire response body, and the failure text is
// a DIAGNOSTIC: the first kilobytes say what went wrong and the rest is
// noise. Truncating it keeps the call counted, which is what accounting
// needs; refusing the line would drop the call from every total instead. The
// marker makes the truncation visible rather than silent.
const maxUsageErrorBytes = 8 * 1024

// truncateError bounds a failure diagnostic, marking it when it is cut.
func truncateError(text string) string {
	if len(text) <= maxUsageErrorBytes {
		return text
	}
	return text[:maxUsageErrorBytes] + "… [truncated]"
}

// UsageErrorFileName is the sentinel written next to the usage log on the
// first append/sync failure. External instrumentation (the benchmark
// adapter) treats its presence as fatal for the run: a stalled log means
// streamed usage is undercounting, which must not pass silently.
const UsageErrorFileName = "usage.error"

// UsageHeader is the log's first line.
type UsageHeader struct {
	UsageSurfaceVersion int `json:"usage_surface_version"`
}

// UsageEntry is one LLM call, surface v2.
//
// Failed calls are recorded, because the call happened and its failure is a
// fact worth keeping. What a failed call does NOT carry is a token
// measurement: maestro-llms populates usage only when the error is nil, so
// the five axes are absent rather than zero. ADR 0025 says failed-attempt
// costs count, and they cannot be counted from this surface -- see issue #311.
//
// The token pointers are what make "absent" expressible. A non-nil pointer to
// zero is a real measurement of nothing; nil is no measurement.
type UsageEntry struct {
	FinishedAt time.Time `json:"finished_at"`

	InputTokens      *int64   `json:"input_tokens,omitempty"`
	OutputTokens     *int64   `json:"output_tokens,omitempty"`
	ReasoningTokens  *int64   `json:"reasoning_tokens,omitempty"`
	CacheReadTokens  *int64   `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int64   `json:"cache_write_tokens,omitempty"`
	CostUSD          *float64 `json:"cost_usd,omitempty"`

	Provider string `json:"provider"`
	Model    string `json:"model"`
	StoryID  string `json:"story_id,omitempty"`
	AgentID  string `json:"agent_id,omitempty"`
	Error    string `json:"error,omitempty"`

	// LatencyNS is nanoseconds, not milliseconds: the source is a
	// time.Duration, and milliseconds would round it so that started_at could
	// not be recovered from what was written.
	LatencyNS int64 `json:"latency_ns"`

	Success bool `json:"success"`
}

// entryFor renders an observation as a log entry. The observation is assumed
// valid; ObserveCall validates before calling this.
func entryFor(observation *Observation) UsageEntry {
	entry := UsageEntry{
		FinishedAt: observation.FinishedAt.UTC(),
		Provider:   observation.Provider,
		Model:      observation.Model,
		StoryID:    observation.StoryID,
		AgentID:    observation.AgentID,
		Error:      truncateError(observation.Error),
		CostUSD:    observation.Cost,
		LatencyNS:  observation.Latency.Nanoseconds(),
		Success:    observation.Success,
	}
	if axes := observation.Tokens; axes != nil {
		input, output, reasoning := axes.Input, axes.Output, axes.Reasoning
		cacheRead, cacheWrite := axes.CacheRead, axes.CacheWrite
		entry.InputTokens = &input
		entry.OutputTokens = &output
		entry.ReasoningTokens = &reasoning
		entry.CacheReadTokens = &cacheRead
		entry.CacheWriteTokens = &cacheWrite
	}
	return entry
}

// UsageLogRecorder is a fan-out Recorder: every observation goes to the
// wrapped recorder (the InternalRecorder singleton, whose story aggregates
// handleWorkAccepted still reads) AND to an append-only JSONL usage log.
type UsageLogRecorder struct {
	inner    Recorder
	writeErr error // first append/sync failure, sticky; see Err()
	file     *os.File
	onFatal  func(error) // escalation when the failure cannot be signaled; default aborts the process
	path     string
	mu       sync.Mutex
}

// fatalUsageAbort terminates the process. It is the default escalation when
// usage accounting has failed AND the failure cannot be signaled via the
// sentinel (a correlated filesystem failure breaks both writes). Process
// death is the one failure channel independent of the usage log's disk, so
// external instrumentation sees the target die rather than silently accept
// an under-counted run. Overridable in tests.
func fatalUsageAbort(err error) {
	_ = logx.Errorf("FATAL: usage accounting integrity lost and could not be signaled; aborting: %v", err)
	os.Exit(1)
}

// ErrSurfaceVersionMismatch reports an existing usage log written by a
// different surface version. Distinguished because the operator response is
// specific and is not "retry": see checkExistingHeader.
var ErrSurfaceVersionMismatch = errors.New("usage log was written by a different usage-surface version")

// NewUsageLogRecorder opens (creating if needed) the usage log at path and
// returns the fan-out recorder. A header line is written when the file is
// new or empty; an existing file's header is VALIDATED before anything is
// appended beneath it.
func NewUsageLogRecorder(path string, inner Recorder) (*UsageLogRecorder, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("usage log dir: %w", err)
	}
	// O_RDWR rather than O_WRONLY so the header can be read back. O_APPEND
	// still governs writes -- they go to the end regardless of where reading
	// left the offset -- so reading the header cannot displace an append.
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open usage log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close() //nolint:errcheck // error path
		return nil, fmt.Errorf("stat usage log: %w", err)
	}
	recorder := &UsageLogRecorder{inner: inner, file: file, path: path, onFatal: fatalUsageAbort}
	if info.Size() > 0 {
		if err := checkExistingHeader(file, path); err != nil {
			_ = file.Close() //nolint:errcheck // error path
			return nil, err
		}
		return recorder, nil
	}
	if writeErr := recorder.writeLine(UsageHeader{UsageSurfaceVersion: UsageSurfaceVersion}); writeErr != nil {
		_ = file.Close() //nolint:errcheck // error path
		return nil, writeErr
	}
	return recorder, nil
}

// decodeUsageHeader parses the log's first line strictly: unknown keys and
// trailing content are refused, and exhaustion is proven by decoding a second
// value and requiring io.EOF. Decoder.More would not do -- it answers a
// question about the container it is inside, so it reports false for the
// trailing `]` that most looks like an ending.
func decodeUsageHeader(line string) (UsageHeader, error) {
	var header UsageHeader
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&header); err != nil {
		return header, fmt.Errorf("decode header: %w", err)
	}
	var rest json.RawMessage
	if err := decoder.Decode(&rest); !errors.Is(err, io.EOF) {
		return header, errors.New("content follows the header object")
	}
	return header, nil
}

// checkExistingHeader refuses to append beneath a header this build did not
// write.
//
// Without it, a surface version bump silently produces a file whose header
// says v1 and whose later lines are v2. Every reader trusts the header, parses
// the new lines as the old shape, and mis-totals -- which is exactly the
// undercounting UsageErrorFileName exists to make impossible, arriving by a
// route that never touches the sentinel.
//
// It REFUSES rather than rotating. Renaming a file that another process
// already holds open leaves that process appending to an unlinked inode, so
// its calls vanish from the log the benchmark adapter is tailing: the
// mitigation would cause the loss it was meant to prevent, and ADR 0027
// forbids recovery that removes another actor's in-progress work. The file is
// left untouched, and the remedy is to move it aside AFTER every writer on
// this project directory has stopped -- doing it under a running target
// reproduces the same unlinked-inode loss.
func checkExistingHeader(file *os.File, path string) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek usage log %s: %w", path, err)
	}
	reader := bufio.NewReader(file)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read usage log header %s: %w", path, err)
	}
	// EOF before the delimiter means the header line was never terminated,
	// and that must be refused even when what is there parses perfectly.
	// Appending after an unterminated line concatenates the next entry onto
	// it, producing one line holding two JSON objects -- so a header that is
	// valid but unfinished is the most dangerous case, not the safest: the
	// version check would pass and the corruption would land on the line
	// after it.
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %s ends without a newline after its header line, so appending "+
			"would concatenate the next entry onto it. Move the file aside once every writer on "+
			"this directory has stopped", ErrSurfaceVersionMismatch, path)
	}
	// STRICTLY, and for the same reason both readers do it: the header
	// decides how every line beneath it is read. An unknown key or trailing
	// content means the file was written by a contract this build does not
	// speak, and appending v2 lines beneath it produces a file the budget
	// tail and the importer both refuse -- so accepting it here would write
	// a log nobody can read, which is the undercounting this check exists to
	// prevent, arriving one door over.
	header, headerErr := decodeUsageHeader(strings.TrimSpace(line))
	if headerErr != nil {
		return fmt.Errorf("%w: %s has an unreadable header line (%w); this build writes v%d. "+
			"Move the file aside once every writer on this directory has stopped",
			ErrSurfaceVersionMismatch, path, headerErr, UsageSurfaceVersion)
	}
	if header.UsageSurfaceVersion != UsageSurfaceVersion {
		return fmt.Errorf("%w: %s is v%d and this build writes v%d. "+
			"Move the file aside once every writer on this directory has stopped",
			ErrSurfaceVersionMismatch, path, header.UsageSurfaceVersion, UsageSurfaceVersion)
	}
	return nil
}

// ObserveCall implements Recorder: fan out to the wrapped recorder and
// append one usage line. Log write failures never disturb the wrapped
// recorder or the calling agent, but they are surfaced: logged at ERROR on
// first occurrence and retained (sticky) for Err().
//
// An observation that fails validation takes the SAME path as a failed write,
// and deliberately so. Both mean the durable account of this call is missing,
// and a run whose accounting is incomplete must not be silently accepted --
// which is the whole reason the sentinel and its escalation exist. Dropping an
// invalid observation quietly would be the one outcome neither a reader nor an
// operator could detect.
// Validation happens BEFORE the fan-out, not after. The wrapped recorder is
// a consumer like the log is: a negative axis, an overflowing tuple or a
// non-finite cost would otherwise be folded into the internal aggregates and
// stay there, corrupting story totals that no sentinel describes, while only
// the durable path reported the problem.
func (u *UsageLogRecorder) ObserveCall(observation *Observation) {
	if err := observation.Validate(); err != nil {
		u.recordWriteErr(err)
		return
	}
	u.inner.ObserveCall(observation)
	if err := u.writeLine(entryFor(observation)); err != nil {
		u.recordWriteErr(err)
	}
}

// recordWriteErr retains the first write failure, logs it once, and drops
// the machine-observable sentinel (UsageErrorFileName) next to the log so
// external instrumentation streaming the log (the benchmark adapter) fails
// the run rather than silently under-counting.
func (u *UsageLogRecorder) recordWriteErr(err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.writeErr != nil {
		return
	}
	u.writeErr = err
	_ = logx.Errorf("usage log write failed — streamed usage is no longer being recorded: %v", err)
	// The sentinel is a different write path (a new small file), so it
	// usually survives whatever broke the log append and gives the adapter
	// a machine-observable signal. If it ALSO fails, the failure is
	// correlated (e.g. disk full breaks every write on this filesystem):
	// escalate to the one channel that does not depend on that disk —
	// terminating the process — so the run cannot be accepted with silent
	// undercounting.
	sentinel := filepath.Join(filepath.Dir(u.path), UsageErrorFileName)
	if writeErr := os.WriteFile(sentinel, []byte(err.Error()+"\n"), 0o644); writeErr != nil {
		_ = logx.Errorf("usage error sentinel write also failed (%v); escalating to process abort", writeErr)
		u.onFatal(fmt.Errorf("usage accounting failed and could not be signaled: append=%w; sentinel=%w", err, writeErr))
	}
}

// Err returns the first usage-log write failure, or nil.
func (u *UsageLogRecorder) Err() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.writeErr
}

func (u *UsageLogRecorder) writeLine(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal usage line: %w", err)
	}
	// A line past the shared cap is one both readers refuse, so emitting it
	// would produce a log nobody can read — every call in the file lost, not
	// just this one. Refusing takes the same path as a failed write, which
	// raises the sentinel and fails the run loudly: an accounting problem is
	// exactly what that mechanism exists to surface. The realistic cause is a
	// pathological error text, which is why entryFor bounds that first.
	if len(raw)+1 > MaxUsageLineBytes {
		return fmt.Errorf("usage line is %d bytes, over the %d-byte limit every reader of this "+
			"surface enforces", len(raw)+1, MaxUsageLineBytes)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, err := u.file.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("write usage line: %w", err)
	}
	if err := u.file.Sync(); err != nil {
		return fmt.Errorf("sync usage log: %w", err)
	}
	return nil
}

// Close closes the underlying log file.
func (u *UsageLogRecorder) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if err := u.file.Close(); err != nil {
		return fmt.Errorf("close usage log: %w", err)
	}
	return nil
}

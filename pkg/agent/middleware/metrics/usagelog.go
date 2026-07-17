package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"orchestrator/pkg/logx"
)

// UsageSurfaceVersion identifies the durable per-LLM-call usage log format
// — the P-1 benchmark usage surface (docs/v2/phase_1/design_adapter_v1.md).
// The benchmark runner validates this version pre-run (advertised via
// maestro -version) and against the log header at run time; bump it on any
// format change.
const UsageSurfaceVersion = 1

// UsageLogFileName is the log's location under the project .maestro dir.
const UsageLogFileName = "usage.jsonl"

// UsageErrorFileName is the sentinel written next to the usage log on the
// first append/sync failure. External instrumentation (the benchmark
// adapter) treats its presence as fatal for the run: a stalled log means
// streamed usage is undercounting, which must not pass silently.
const UsageErrorFileName = "usage.error"

// UsageHeader is the log's first line.
type UsageHeader struct {
	UsageSurfaceVersion int `json:"usage_surface_version"`
}

// UsageEntry is one LLM call. Failed calls are recorded too: their tokens
// were spent, and failed-attempt costs count (ADR 0025).
type UsageEntry struct {
	Timestamp        time.Time `json:"ts"`
	StoryID          string    `json:"story_id,omitempty"`
	AgentID          string    `json:"agent_id,omitempty"`
	Model            string    `json:"model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	CostUSD          float64   `json:"cost_usd"`
	Success          bool      `json:"success"`
}

// UsageLogRecorder is a fan-out Recorder: every observation goes to the
// wrapped recorder (the InternalRecorder singleton, whose story aggregates
// handleWorkAccepted still reads) AND to an append-only JSONL usage log.
type UsageLogRecorder struct {
	inner    Recorder
	writeErr error // first append/sync failure, sticky; see Err()
	file     *os.File
	path     string
	mu       sync.Mutex
}

// NewUsageLogRecorder opens (creating if needed) the usage log at path and
// returns the fan-out recorder. A header line is written when the file is
// new or empty.
func NewUsageLogRecorder(path string, inner Recorder) (*UsageLogRecorder, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("usage log dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open usage log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close() //nolint:errcheck // error path
		return nil, fmt.Errorf("stat usage log: %w", err)
	}
	recorder := &UsageLogRecorder{inner: inner, file: file, path: path}
	if info.Size() == 0 {
		if writeErr := recorder.writeLine(UsageHeader{UsageSurfaceVersion: UsageSurfaceVersion}); writeErr != nil {
			_ = file.Close() //nolint:errcheck // error path
			return nil, writeErr
		}
	}
	return recorder, nil
}

// ObserveRequest implements Recorder: fan out to the wrapped recorder and
// append one usage line. Log write failures never disturb the wrapped
// recorder or the calling agent, but they are surfaced: logged at ERROR on
// first occurrence and retained (sticky) for Err().
func (u *UsageLogRecorder) ObserveRequest(
	storyID, agentID, model string,
	promptTokens, completionTokens int,
	cost float64,
	success bool,
) {
	u.inner.ObserveRequest(storyID, agentID, model, promptTokens, completionTokens, cost, success)
	if err := u.writeLine(UsageEntry{
		Timestamp:        time.Now().UTC(),
		StoryID:          storyID,
		AgentID:          agentID,
		Model:            model,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CostUSD:          cost,
		Success:          success,
	}); err != nil {
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
	// Best effort: the sentinel is a different write path (new small file)
	// and usually survives whatever broke the log append; if it fails too,
	// the ERROR log above is the last line of defense.
	sentinel := filepath.Join(filepath.Dir(u.path), UsageErrorFileName)
	if writeErr := os.WriteFile(sentinel, []byte(err.Error()+"\n"), 0o644); writeErr != nil {
		_ = logx.Errorf("usage error sentinel write failed: %v", writeErr)
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

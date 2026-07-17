package v1target

// Tailing the P-1 usage surface: v1-as-patched appends one JSONL line per
// LLM call to .maestro/usage.jsonl (versioned header). The adapter streams
// deltas through ReportUsage so the engine cancels at the cap, and the log
// totals become the canonical tokens/cost/llm_calls.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/SnapdragonPartners/maestro/benchmark/target"
)

// usageSurfaceVersion is the P-1 log format this adapter speaks. It must
// match both the version advertised by `maestro -version` (validated in
// Describe, pre-run) and the log header (validated at first read).
const usageSurfaceVersion = 1

// usageTail incrementally reads the usage log across poll ticks.
type usageTail struct {
	report    func(target.UsageDelta)
	path      string
	offset    int64
	calls     int64
	tokens    int64
	costUSD   float64
	validated bool
}

type usageHeader struct {
	UsageSurfaceVersion int `json:"usage_surface_version"`
}

type usageLine struct {
	Model            string  `json:"model"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	Success          bool    `json:"success"`
}

// advance reads any new complete lines, validates the header on first
// contact, streams deltas, and accumulates totals. A missing file is not
// an error (v1 may not have started yet); a bad header is fatal — the
// run half of the P-1 capability handshake.
func (u *usageTail) advance() error {
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
		var entry usageLine
		if err := json.Unmarshal([]byte(trimmed), &entry); err != nil {
			return fmt.Errorf("usage log line: %w", err)
		}
		tokens := entry.PromptTokens + entry.CompletionTokens
		u.calls++
		u.tokens += tokens
		u.costUSD += entry.CostUSD
		if u.report != nil {
			u.report(target.UsageDelta{Tokens: tokens, CostUSD: entry.CostUSD})
		}
	}
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

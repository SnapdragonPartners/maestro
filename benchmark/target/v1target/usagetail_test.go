package v1target

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/target"
)

func TestUsageTailIncrementalStreaming(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	var deltas []target.UsageDelta
	tail := &usageTail{path: path, report: func(d target.UsageDelta) { deltas = append(deltas, d) }}

	// Absent file: not an error, nothing consumed.
	if err := tail.advance(); err != nil || tail.validated {
		t.Fatalf("absent log must be a no-op: %v %v", err, tail.validated)
	}

	write := func(content string) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if _, err := f.WriteString(content); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}

	// tokens = input + output + reasoning, which is exactly what v1's
	// prompt+completion came to (its completion field was
	// BillableOutputTokens = output + reasoning). Cache reads are recorded
	// and NOT counted: adding them would change what a declared cap means.
	write(v2Header + "\n" + v2Line(`"input_tokens":100,"output_tokens":40,`+
		`"reasoning_tokens":10,"cache_read_tokens":7,"cache_write_tokens":3,"cost_usd":0.01,"success":true`) + "\n")
	if err := tail.advance(); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if !tail.validated || tail.calls != 1 || tail.tokens != 150 || len(deltas) != 1 || deltas[0].Tokens != 150 {
		t.Fatalf("first read wrong: %+v deltas=%+v", tail, deltas)
	}

	// A partial (unterminated) line is left for the next tick.
	write(`{"provider":"p","model":"m",`)
	if err := tail.advance(); err != nil {
		t.Fatalf("advance partial: %v", err)
	}
	if tail.calls != 1 {
		t.Fatalf("partial lines must not be consumed: %+v", tail)
	}
	// A failed call: an error text, no token measurement, and so no
	// contribution to the totals. Under v1 this line carried five zeros and
	// was indistinguishable from a call that genuinely used nothing.
	write(`"finished_at":"2026-08-04T00:00:00Z","latency_ns":1000,` +
		`"error":"provider down","success":false}` + "\n")
	if err := tail.advance(); err != nil {
		t.Fatalf("advance completed: %v", err)
	}
	if tail.calls != 2 || tail.tokens != 150 || len(deltas) != 2 || deltas[1].Tokens != 0 {
		t.Fatalf("a failed line is counted as a call but contributes no tokens: %+v deltas=%+v", tail, deltas)
	}
}

const v2Header = `{"usage_surface_version":2}`

// v2Line wraps the varying fields in the ones every valid line carries.
func v2Line(fields string) string {
	return `{"provider":"anthropic","model":"m","finished_at":"2026-08-04T00:00:00Z",` +
		`"latency_ns":1500000000,` + fields + `}`
}

// The live accounting bypass: usageTracker guards against a negative delta,
// but it only ever sees the SUM. A line whose axes are individually absurd
// but whose total is a small positive number walks straight past it, and the
// attempt under-accounts by however much the negative axis hid.
func TestUsageTailRejectsNegativeAxisHiddenInPositiveTotal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	line := v2Line(`"input_tokens":1000000,"output_tokens":-999999,"reasoning_tokens":0,` +
		`"cache_read_tokens":0,"cache_write_tokens":0,"success":true`)
	if err := os.WriteFile(path, []byte(v2Header+"\n"+line+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var deltas []target.UsageDelta
	tail := &usageTail{path: path, report: func(d target.UsageDelta) { deltas = append(deltas, d) }}
	err := tail.advance()
	if err == nil {
		t.Fatalf("a negative axis must fail the attempt; it summed to %d and was reported as %+v",
			tail.tokens, deltas)
	}
	if !strings.Contains(err.Error(), "output_tokens") {
		t.Fatalf("the error must name the offending axis, got: %v", err)
	}
	if len(deltas) != 0 {
		t.Fatalf("nothing may be reported from a line that failed validation: %+v", deltas)
	}
}

// Every other value rule, each broken on its own.
func TestUsageTailRejectsInvalidValues(t *testing.T) {
	valid := `"input_tokens":10,"output_tokens":5,"reasoning_tokens":0,` +
		`"cache_read_tokens":0,"cache_write_tokens":0,"success":true`
	cases := map[string]string{
		"blank provider":            `{"provider":"","model":"m","finished_at":"2026-08-04T00:00:00Z","latency_ns":1,` + valid + `}`,
		"blank model":               `{"provider":"p","model":" ","finished_at":"2026-08-04T00:00:00Z","latency_ns":1,` + valid + `}`,
		"zero finished_at":          `{"provider":"p","model":"m","finished_at":"0001-01-01T00:00:00Z","latency_ns":1,` + valid + `}`,
		"missing finished_at":       `{"provider":"p","model":"m","latency_ns":1,` + valid + `}`,
		"negative latency":          `{"provider":"p","model":"m","finished_at":"2026-08-04T00:00:00Z","latency_ns":-5,` + valid + `}`,
		"negative cost":             v2Line(valid + `,"cost_usd":-0.5`),
		"success with error text":   v2Line(valid + `,"error":"boom"`),
		"failure with no error":     v2Line(`"success":false`),
		"failure carrying tokens":   v2Line(`"input_tokens":1,"output_tokens":0,"reasoning_tokens":0,"cache_read_tokens":0,"cache_write_tokens":0,"success":false,"error":"x"`),
		"measured but axis missing": v2Line(`"input_tokens":10,"output_tokens":5,"success":true`),
		// Presence is not value. Each of these decodes into a plausible zero
		// value and would be read as a fact the writer never stated.
		"success missing entirely": v2Line(`"error":"provider down"`),
		"latency_ns missing":       `{"provider":"p","model":"m","finished_at":"2026-08-04T00:00:00Z",` + valid + `}`,
		"success with empty error": v2Line(valid + `,"error":""`),
		// A stray axis with input_tokens absent: checking only input_tokens
		// read this as "no measurement" and never looked at the rest.
		"only output_tokens present": v2Line(`"output_tokens":5,"success":false,"error":"x"`),
		// Strict decoding: a key this build does not know means the header
		// version lied about what wrote the file.
		"unknown key": v2Line(valid + `,"prompt_tokens":42`),
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "usage.jsonl")
			if err := os.WriteFile(path, []byte(v2Header+"\n"+line+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			tail := &usageTail{path: path}
			if err := tail.advance(); err == nil {
				t.Fatalf("must fail the attempt rather than accounting it: %+v", tail)
			}
		})
	}
	// The control: the shape these are derived from must be accepted, or
	// every case above could be failing for an unrelated reason.
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	if err := os.WriteFile(path, []byte(v2Header+"\n"+v2Line(valid)+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tail := &usageTail{path: path}
	if err := tail.advance(); err != nil {
		t.Fatalf("the control line must be accepted: %v", err)
	}
	if tail.tokens != 15 {
		t.Fatalf("control totals wrong: %d", tail.tokens)
	}
}

// A rejected line must leave the totals exactly as it found them.
//
// Two individually finite costs can sum to +Inf, so the cost check can only
// fail AFTER the token total has been computed — and if the totals are
// committed as they are computed, the refused line has already been counted
// in calls and tokens by the time the refusal happens.
func TestUsageTailRejectedLineLeavesTotalsUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	measured := `"input_tokens":10,"output_tokens":5,"reasoning_tokens":0,` +
		`"cache_read_tokens":0,"cache_write_tokens":0,"success":true`
	// Two finite costs whose SUM overflows float64.
	huge := v2Line(measured + `,"cost_usd":1e308`)
	if err := os.WriteFile(path, []byte(v2Header+"\n"+huge+"\n"+huge+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tail := &usageTail{path: path}
	if err := tail.advance(); err == nil {
		t.Fatal("a cumulative cost overflow must fail the attempt")
	}
	// The first line was accepted; the second was refused and must have
	// contributed nothing at all — not its call, not its tokens, not its cost.
	if tail.calls != 1 {
		t.Errorf("calls = %d, want 1: the refused line was counted", tail.calls)
	}
	if tail.tokens != 15 {
		t.Errorf("tokens = %d, want 15: the refused line's tokens were absorbed", tail.tokens)
	}
	if tail.costUSD != 1e308 {
		t.Errorf("cost = %v, want 1e308: the refused line moved the cost total", tail.costUSD)
	}
}

// The tail's own totals are the record's canonical figures, so they need the
// same overflow and non-finite protection usageTracker gives its copy.
func TestUsageTailRefusesOverflowingTotals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	huge := `"input_tokens":9223372036854775807,"output_tokens":1,"reasoning_tokens":0,` +
		`"cache_read_tokens":0,"cache_write_tokens":0,"success":true`
	if err := os.WriteFile(path, []byte(v2Header+"\n"+v2Line(huge)+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tail := &usageTail{path: path}
	if err := tail.advance(); err == nil {
		t.Fatalf("an overflowing total must fail rather than wrap to a small number: %d", tail.tokens)
	}
}

func TestUsageTailHeaderMismatchIsFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	if err := os.WriteFile(path, []byte(`{"usage_surface_version":99}`+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tail := &usageTail{path: path}
	if err := tail.advance(); err == nil || !strings.Contains(err.Error(), "header mismatch") {
		t.Fatalf("wrong surface version must be fatal, got %v", err)
	}
}

func TestVerifyAdvertisedSurface(t *testing.T) {
	good := "maestro v1\n  commit: abc\n  usage-surface: v2\n"
	if err := verifyAdvertisedSurface(good); err != nil {
		t.Fatalf("advertised surface must verify: %v", err)
	}
	for name, out := range map[string]string{
		"missing": "maestro v1\n  commit: abc\n",
		// A target still on the old surface must fail the handshake rather
		// than have its lines parsed as though they were the new shape.
		"older": "maestro v1\n  usage-surface: v1\n",
		"newer": "maestro v1\n  usage-surface: v3\n",
	} {
		if err := verifyAdvertisedSurface(out); err == nil {
			t.Fatalf("%s advertisement must fail the pre-run handshake", name)
		}
	}
}

// TestUsageTailUnreadableSentinelIsFatal pins the Codex round-3 read-side
// hardening: a sentinel that exists but cannot be read (here, a directory
// at the sentinel path) must fail the run, not be mistaken for "no error
// reported".
func TestUsageTailUnreadableSentinelIsFatal(t *testing.T) {
	dir := t.TempDir()
	errPath := filepath.Join(dir, "usage.error")
	// A directory at the sentinel path makes ReadFile fail with a non
	// not-exist error.
	if err := os.Mkdir(errPath, 0o755); err != nil {
		t.Fatalf("mkdir sentinel: %v", err)
	}
	tail := &usageTail{path: filepath.Join(dir, "usage.jsonl"), errPath: errPath, validated: true}
	err := tail.advance()
	if err == nil || !strings.Contains(err.Error(), "sentinel unreadable") {
		t.Fatalf("unreadable sentinel must be fatal, got: %v", err)
	}
}

// TestUsageTailAbsentSentinelIsFine confirms the common case: no sentinel
// present is not an error.
func TestUsageTailAbsentSentinelIsFine(t *testing.T) {
	dir := t.TempDir()
	tail := &usageTail{path: filepath.Join(dir, "usage.jsonl"), errPath: filepath.Join(dir, "usage.error")}
	if err := tail.advance(); err != nil {
		t.Fatalf("absent sentinel must be a no-op, got: %v", err)
	}
}

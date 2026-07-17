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

	write(`{"usage_surface_version":1}` + "\n" +
		`{"model":"m","prompt_tokens":100,"completion_tokens":50,"cost_usd":0.01,"success":true}` + "\n")
	if err := tail.advance(); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if !tail.validated || tail.calls != 1 || tail.tokens != 150 || len(deltas) != 1 || deltas[0].Tokens != 150 {
		t.Fatalf("first read wrong: %+v deltas=%+v", tail, deltas)
	}

	// A partial (unterminated) line is left for the next tick.
	write(`{"model":"m","prompt_tokens":10,`)
	if err := tail.advance(); err != nil {
		t.Fatalf("advance partial: %v", err)
	}
	if tail.calls != 1 {
		t.Fatalf("partial lines must not be consumed: %+v", tail)
	}
	write(`"completion_tokens":5,"cost_usd":0.002,"success":false}` + "\n")
	if err := tail.advance(); err != nil {
		t.Fatalf("advance completed: %v", err)
	}
	if tail.calls != 2 || tail.tokens != 165 || len(deltas) != 2 {
		t.Fatalf("completed line must be consumed exactly once: %+v", tail)
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
	good := "maestro v1\n  commit: abc\n  usage-surface: v1\n"
	if err := verifyAdvertisedSurface(good); err != nil {
		t.Fatalf("advertised surface must verify: %v", err)
	}
	for name, out := range map[string]string{
		"missing": "maestro v1\n  commit: abc\n",
		"wrong":   "maestro v1\n  usage-surface: v2\n",
	} {
		if err := verifyAdvertisedSurface(out); err == nil {
			t.Fatalf("%s advertisement must fail the pre-run handshake", name)
		}
	}
}

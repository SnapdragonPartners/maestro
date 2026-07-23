package main

// ENGINE-OWNED BEHAVIOURAL ORACLE for the flag-instance-name story. The engine
// materialises this file into the bound solution and runs
//
//	go test -run TestOracleInstanceName .
//
// It is never authored by, nor visible to, the agent.
//
// What it proves that a grep or a newServer-direct test cannot: the full
// main→server wiring. It builds and runs the REAL binary with a NON-DEFAULT
// -instance-name, then asserts the value appears in X-Instance-Name on three
// responses served by different handlers — the root page, the JSON API, and a
// path under the static file server. So a main that declares the flag but
// passes a hardcoded value, or a header applied to only one route, or a header
// set per-handler instead of on every response, all fail. The static-server
// path is the discriminating one: "/" is a catch-all in net/http's ServeMux, so
// an arbitrary unknown path still reaches the "/" handler and would carry a
// per-handler header; only a path routed to a DIFFERENT handler exposes a
// header that was set per-handler rather than by wrapping the whole mux.
//
// Hermetic by construction. main() os.Exit(1)s when zero providers are
// detected, and every provider detector makes a live ListModels call, so the
// binary cannot reach newServer offline on its own. This oracle stands up a
// fake Ollama /api/tags endpoint serving one model and launches the binary with
// a CLEAN environment whose only provider signal is OLLAMA_HOST pointing at that
// fake — no network, no API keys, fully deterministic. This realises the
// story's stated "self-contained, no external dependency API" intent, which the
// original shell oracle claimed but could not deliver.

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestOracleInstanceName(t *testing.T) {
	const want = "oracle-xyz"

	// Fake Ollama: one model on GET /api/tags, so detectAvailable returns a
	// single option and main proceeds to serve rather than os.Exit(1).
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"placeholder","modified_at":"2024-01-01T00:00:00Z"}]}`))
	}))
	defer ollama.Close()

	// Build the solution binary. `go build` excludes _test.go files, so this
	// oracle is not compiled into the server it launches.
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "chat")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build solution binary: %v\n%s", err, out)
	}

	// Reserve a loopback address and hand it to the binary. The brief
	// close/reopen window is an acceptable TOCTOU for a local oracle. Using the
	// listener's own address string avoids a *net.TCPAddr assertion.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	// Clean env: only OLLAMA_HOST (the fake) plus the essentials a process
	// needs. No ambient provider keys leak in, so detection is deterministic.
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "-addr="+addr, "-instance-name="+want)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"OLLAMA_HOST=" + ollama.URL,
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start binary: %v", err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	base := "http://" + addr
	waitListening(t, base, 10*time.Second)

	// Every response the app serves must carry the header. Three handlers: the
	// root page, the JSON API, and a miss under the static file server. The
	// last is the discriminating probe — it is served by a different handler
	// than "/", so a header set per-handler (rather than by wrapping the whole
	// mux) is absent here even though "/" carries it.
	for _, path := range []string{"/", "/api/providers", "/static/zz-oracle-nonexistent"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		got := resp.Header.Get("X-Instance-Name")
		_ = resp.Body.Close()
		if got != want {
			t.Fatalf("GET %s: X-Instance-Name=%q, want %q — the header must be on every response, including 404", path, got, want)
		}
	}
}

// waitListening polls the root route until the server answers or the timeout
// elapses.
func waitListening(t *testing.T, base string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(base + "/"); err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server at %s never came up within %s", base, timeout)
}

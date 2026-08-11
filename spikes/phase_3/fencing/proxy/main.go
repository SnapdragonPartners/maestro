// Command proxy is a constrained Docker API proxy that enforces fencing-domain
// membership at creation.
//
// It is the mediating boundary ADR 0029 §7 names as one of the three ways a
// fencing domain can exist. Three jobs, and the reproducer needs all three:
//
//  1. STAMP    Rewrite every container-create so the domain label is present,
//     overwriting whatever the caller supplied. A caller cannot opt out.
//  2. CONTAIN  Reject creates that would hand the new container its own
//     unmediated route to the daemon. Stamping alone is not containment: a
//     correctly labelled child holding /var/run/docker.sock can create an
//     unlabeled grandchild, and the domain is broken in two hops.
//  3. REVOKE   Provide a checked revocation barrier. Killing the proxy is not a
//     barrier — creates already accepted may still be completing, and a
//     caller cannot distinguish "revoked" from "crashed". /spike/revoke stops
//     accepting creates, waits for in-flight creates to finish, and
//     acknowledges with the count it drained.
//
// Env: DOMAIN_LABEL, DOMAIN_VALUE, LISTEN (default :2375), SOCK
// (default /var/run/docker.sock).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// createPath matches /containers/create with or without a version prefix.
var createPath = regexp.MustCompile(`^(/v[0-9.]+)?/containers/create$`)

// gate holds the revocation state and the in-flight create count. Both are
// needed for a barrier: refusing new work without draining accepted work would
// acknowledge a quiet daemon rather than a closed one.
type gate struct {
	mu       sync.Mutex
	revoked  bool
	inflight int
	drained  int
}

// enter admits a create unless creation has been revoked.
func (g *gate) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.revoked {
		return false
	}
	g.inflight++
	return true
}

func (g *gate) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inflight--
	g.drained++
}

// revoke closes creation and waits for accepted creates to complete. The
// returned count is what the caller may treat as drained.
func (g *gate) revoke(timeout time.Duration) (int, error) {
	g.mu.Lock()
	g.revoked = true
	g.mu.Unlock()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		n, drained := g.inflight, g.drained
		g.mu.Unlock()
		if n == 0 {
			return drained, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	g.mu.Lock()
	n := g.inflight
	g.mu.Unlock()
	return 0, fmt.Errorf("%d creates still in flight after %s", n, timeout)
}

func main() {
	label := env("DOMAIN_LABEL", "")
	value := env("DOMAIN_VALUE", "")
	listen := env("LISTEN", ":2375")
	sock := env("SOCK", "/var/run/docker.sock")
	if label == "" || value == "" {
		log.Fatal("DOMAIN_LABEL and DOMAIN_VALUE are required")
	}

	g := &gate{}
	target, _ := url.Parse("http://docker")
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.Transport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/spike/revoke", func(w http.ResponseWriter, r *http.Request) {
		drained, err := g.revoke(30 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusGatewayTimeout)
			_ = json.NewEncoder(w).Encode(map[string]any{"revoked": false, "error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"revoked": true, "drained": drained})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !createPath.MatchString(r.URL.Path) {
			rp.ServeHTTP(w, r)
			return
		}
		if !g.enter() {
			http.Error(w, "proxy: creation revoked for this fencing domain", http.StatusForbidden)
			return
		}
		defer g.leave()

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "proxy: "+err.Error(), http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		var cfg map[string]any
		if err := json.Unmarshal(body, &cfg); err != nil {
			http.Error(w, "proxy: "+err.Error(), http.StatusBadRequest)
			return
		}
		if why := escapes(cfg); why != "" {
			http.Error(w, "proxy: refused, would escape the fencing domain: "+why,
				http.StatusForbidden)
			return
		}
		stamp(cfg, label, value)

		rewritten, err := json.Marshal(cfg)
		if err != nil {
			http.Error(w, "proxy: "+err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(rewritten))
		r.ContentLength = int64(len(rewritten))
		r.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
		rp.ServeHTTP(w, r)
	})

	log.Printf("enforcing %s=%s on %s -> %s", label, value, listen, sock)
	log.Fatal(http.ListenAndServe(listen, mux))
}

// stamp overwrites rather than defaults: a caller must not be able to opt out by
// supplying its own value for the key.
func stamp(cfg map[string]any, label, value string) {
	labels, ok := cfg["Labels"].(map[string]any)
	if !ok {
		labels = map[string]any{}
	}
	labels[label] = value
	cfg["Labels"] = labels
}

// escapes reports why a create would give the new container its own route out of
// the domain, or "" if it would not.
//
// Stamping without this is not containment. A labelled child holding the daemon
// socket can create an unlabeled grandchild through the raw socket, so the domain
// survives one hop and fails at two. A private daemon would close the same gap by
// construction; this is the mediation form of it.
func escapes(cfg map[string]any) string {
	hc, ok := cfg["HostConfig"].(map[string]any)
	if !ok {
		return ""
	}

	// Bind strings: "src:dst[:opts]".
	if binds, ok := hc["Binds"].([]any); ok {
		for _, b := range binds {
			s, _ := b.(string)
			if src, _, found := strings.Cut(s, ":"); found && isDaemonSocket(src) {
				return "bind mounts the daemon socket (" + s + ")"
			}
		}
	}
	// Mounts: [{Source, Target, ...}].
	if mounts, ok := hc["Mounts"].([]any); ok {
		for _, m := range mounts {
			mm, _ := m.(map[string]any)
			src, _ := mm["Source"].(string)
			if isDaemonSocket(src) {
				return "mounts the daemon socket (" + src + ")"
			}
		}
	}
	// Equivalent routes out of the domain.
	if priv, ok := hc["Privileged"].(bool); ok && priv {
		return "privileged"
	}
	if pid, ok := hc["PidMode"].(string); ok && (pid == "host" || strings.HasPrefix(pid, "container:")) {
		return "shares a PID namespace (" + pid + ")"
	}
	if netMode, ok := hc["NetworkMode"].(string); ok && netMode == "host" {
		return "host network"
	}
	return ""
}

func isDaemonSocket(p string) bool {
	return strings.HasSuffix(p, "docker.sock") || strings.HasSuffix(p, "containerd.sock")
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

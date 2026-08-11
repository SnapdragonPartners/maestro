// Command proxy is a constrained Docker API proxy that enforces fencing-domain
// membership at creation.
//
// It is the mediating boundary ADR 0029 §7 names as one of the three ways a
// fencing domain can exist. It listens on TCP, forwards to the real Docker
// socket, and rewrites every container-create request so the domain label is
// present whether or not the caller supplied it. A caller that omits the label —
// or supplies a different one — still lands inside the domain.
//
// Killing this process is what "revoke the ability to create" means in the
// reproducer: the holder's only route to the daemon disappears.
//
// Env: DOMAIN_LABEL, DOMAIN_VALUE, LISTEN (default :2375), SOCK
// (default /var/run/docker.sock).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strconv"
)

// createPath matches /containers/create with or without a version prefix.
var createPath = regexp.MustCompile(`^(/v[0-9.]+)?/containers/create$`)

func main() {
	label := env("DOMAIN_LABEL", "")
	value := env("DOMAIN_VALUE", "")
	listen := env("LISTEN", ":2375")
	sock := env("SOCK", "/var/run/docker.sock")
	if label == "" || value == "" {
		log.Fatal("DOMAIN_LABEL and DOMAIN_VALUE are required")
	}

	target, _ := url.Parse("http://docker")
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.Transport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}

	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && createPath.MatchString(r.URL.Path) {
			if err := stamp(r, label, value); err != nil {
				http.Error(w, "proxy: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		rp.ServeHTTP(w, r)
	})

	log.Printf("enforcing %s=%s on %s -> %s", label, value, listen, sock)
	log.Fatal(http.ListenAndServe(listen, mux))
}

// stamp rewrites the create request body so the domain label is present. It
// overwrites rather than defaults: a caller must not be able to opt out by
// supplying its own value for the key.
func stamp(r *http.Request, label, value string) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	_ = r.Body.Close()

	var cfg map[string]any
	if err := json.Unmarshal(body, &cfg); err != nil {
		return err
	}

	labels, ok := cfg["Labels"].(map[string]any)
	if !ok {
		labels = map[string]any{}
	}
	labels[label] = value
	cfg["Labels"] = labels

	rewritten, err := json.Marshal(cfg)
	if err != nil {
		return err
	}

	r.Body = io.NopCloser(bytes.NewReader(rewritten))
	r.ContentLength = int64(len(rewritten))
	r.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	return nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

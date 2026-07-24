package main

// ENGINE-OWNED BEHAVIOURAL ORACLE for the app-healthz-endpoint story. The
// engine materialises this test into the bound solution and runs
//
//	go test -count=1 -run TestOracleHealthz .
//
// The agent never sees it. The story's grep checks prove a test exists that
// mentions httptest, newServer and status — a broken route with a test merely
// naming those strings satisfies all of them. So the engine drives a real
// request through the application's OWN router (newServer) and asserts the
// response contract: 200 with a JSON body whose "status" is "ok", with no
// provider configured (newServer(nil, ...)). `-count=1` defeats Go's test
// cache. The companion scratch-mode mutation check verifies the agent's OWN
// test is meaningful.

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOracleHealthz(t *testing.T) {
	srv := httptest.NewServer(newServer(nil, log.New(io.Discard, "", 0)))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf(`body["status"] = %v, want "ok"`, body["status"])
	}
}

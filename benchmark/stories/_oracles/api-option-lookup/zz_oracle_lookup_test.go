package main

// ENGINE-OWNED BEHAVIOURAL ORACLE for the api-option-lookup story. The engine
// materialises this test into the bound solution and runs
//
//	go test -count=1 -run TestOracleFindOption .
//
// The agent never sees it. The story's grep checks prove the agent produced a
// test file that mentions findOption; they cannot prove the CONTRACT holds —
// four vacuous subtests plus `ProviderName` in a comment satisfy every one. So
// the engine brings its own test, exercising all four specified behaviours
// directly against the agent's findOption. It verifies the IMPLEMENTATION; the
// companion scratch-mode mutation check verifies the AGENT'S OWN tests.

import "testing"

func TestOracleFindOption(t *testing.T) {
	opts := []ModelOption{
		{ID: "anthropic/m1", ProviderName: "anthropic", Model: "m1"},
		{ID: "vllm/a", ProviderName: "vllm", Model: "a"},
		{ID: "vllm/b", ProviderName: "vllm", Model: "b"},
	}
	// 1. Exact ID match wins.
	if got, ok := findOption(opts, "vllm/a"); !ok || got.ID != "vllm/a" {
		t.Fatalf("exact ID must win: got %+v ok=%v", got, ok)
	}
	// 2. A unique provider name resolves.
	if got, ok := findOption(opts, "anthropic"); !ok || got.ID != "anthropic/m1" {
		t.Fatalf("unique provider name must resolve: got %+v ok=%v", got, ok)
	}
	// 3. An ambiguous provider name is not found.
	if got, ok := findOption(opts, "vllm"); ok {
		t.Fatalf("ambiguous provider name must not resolve: got %+v", got)
	}
	// 4. An unknown id is not found.
	if _, ok := findOption(opts, "nope"); ok {
		t.Fatal("unknown id must not resolve")
	}
}

package config

import "testing"

// TestBenchmarkArchitectModelIsRegistered guards the trap that an unlisted model
// is not an error: GetModelInfo falls through to a zero-cost default with
// InputCPM/OutputCPM of 0.0, so an unregistered model reports $0 spend and
// silently disables budget enforcement rather than failing loudly.
//
// provider_routing_test.go is NOT a substitute. It asserts provider routing,
// which ProviderPatterns satisfies by prefix inference ("gpt-4.1" -> "gpt" ->
// OpenAI) even with the KnownModels entry deleted. Only the known flag and the
// pricing fields distinguish a registered model from an inferred one.
//
// Mutation-verified 2026-08-08: deleting the "gpt-4.1" entry from KnownModels
// fails this test on the known flag and on every pricing assertion below.
func TestBenchmarkArchitectModelIsRegistered(t *testing.T) {
	// Values verified against developers.openai.com on 2026-08-08.
	const (
		model         = "gpt-4.1"
		wantInputCPM  = 2.0
		wantOutputCPM = 8.0
		wantContext   = 1047576
		wantOutput    = 32768
	)

	info, known := GetModelInfo(model)
	if !known {
		t.Fatalf("%s is absent from KnownModels: cost would report $0 and budget "+
			"enforcement would be silently disabled", model)
	}
	if info.Provider != ProviderOpenAI {
		t.Errorf("Provider = %q, want %q", info.Provider, ProviderOpenAI)
	}
	if info.InputCPM != wantInputCPM {
		t.Errorf("InputCPM = %v, want %v", info.InputCPM, wantInputCPM)
	}
	if info.OutputCPM != wantOutputCPM {
		t.Errorf("OutputCPM = %v, want %v", info.OutputCPM, wantOutputCPM)
	}
	if info.MaxContextTokens != wantContext {
		t.Errorf("MaxContextTokens = %d, want %d", info.MaxContextTokens, wantContext)
	}
	if info.MaxOutputTokens != wantOutput {
		t.Errorf("MaxOutputTokens = %d, want %d", info.MaxOutputTokens, wantOutput)
	}

	// The defect this guards is priced work reported as free, so assert the
	// consequence through the function that actually computes spend — the CPM
	// fields above are the inputs to it, not the outcome.
	const wantCost = 10.0 // 1M prompt @ $2/M + 1M completion @ $8/M
	cost, err := CalculateCost(model, 1_000_000, 1_000_000)
	if err != nil {
		t.Fatalf("CalculateCost(%s): %v", model, err)
	}
	if cost != wantCost {
		t.Errorf("CalculateCost(%s, 1M, 1M) = %v, want %v", model, cost, wantCost)
	}
	if cost == 0 {
		t.Errorf("%s prices to zero: priced calls would be recorded as $0 spend, "+
			"and the per-story budget cap would never trigger", model)
	}
}

// TestUnknownModelPricesToZero pins the fallback the test above guards against,
// so the trap stays visible if GetModelInfo's default ever changes.
func TestUnknownModelPricesToZero(t *testing.T) {
	info, known := GetModelInfo("gpt-not-a-real-model")
	if known {
		t.Fatal("sentinel model unexpectedly present in KnownModels")
	}
	if info.Provider != ProviderOpenAI {
		t.Errorf("Provider = %q, want %q from prefix inference", info.Provider, ProviderOpenAI)
	}
	if info.InputCPM != 0 || info.OutputCPM != 0 {
		t.Errorf("unknown-model fallback now prices at in=%v out=%v; the "+
			"registration guard above may no longer be load-bearing",
			info.InputCPM, info.OutputCPM)
	}
}

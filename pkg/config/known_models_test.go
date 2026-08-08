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
	// consequence directly rather than only the fields that produce it.
	if info.InputCPM == 0 || info.OutputCPM == 0 {
		t.Errorf("%s prices to zero (in=%v out=%v): priced calls would be "+
			"recorded as $0 spend", model, info.InputCPM, info.OutputCPM)
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

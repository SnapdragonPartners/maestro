package preflight

import (
	"context"

	"orchestrator/pkg/config"
)

// ValidatorFunc validates API keys for the given config and returns check results.
// The default implementation calls ValidateRequiredKeys with real API calls.
// Tests can replace this to inject deterministic validation results.
type ValidatorFunc func(ctx context.Context, cfg *config.Config) []KeyCheckResult

// validatorFunc is the current validator implementation. Tests inject mocks here.
var validatorFunc ValidatorFunc //nolint:gochecknoglobals // injectable seam for tests

// SetValidatorFunc replaces the validator used by EvaluateSetupReadiness.
// Pass nil to restore the default (real API validation).
func SetValidatorFunc(fn ValidatorFunc) {
	validatorFunc = fn
}

// ReadinessResult describes whether all required API keys are accessible and valid.
//
//nolint:govet // fieldalignment: logical grouping preferred over memory optimization
type ReadinessResult struct {
	// Ready is true when all required keys are accessible and validated successfully.
	Ready bool `json:"ready"`

	// AllPresent is true when all required keys are accessible (env or decrypted secrets).
	AllPresent bool `json:"all_present"`

	// KeyInfo lists presence status for each required key.
	KeyInfo []ProviderKeyInfo `json:"key_info"`

	// ValidationErrors holds results for keys that are present but rejected (unauthorized/forbidden).
	ValidationErrors []KeyCheckResult `json:"validation_errors,omitempty"`

	// Warnings holds results for transient failures (unreachable, generic error) that don't block startup.
	Warnings []KeyCheckResult `json:"warnings,omitempty"`
}

// EvaluateSetupReadiness checks whether all required API keys are accessible
// and valid for the current configuration.
//
// Flow:
//  1. Determine required providers from config
//  2. Check key presence via GetSystemSecret (handles env + decrypted secrets)
//  3. If any keys missing → not ready (missing-key setup flow)
//  4. If all present → validate with real API calls (or injected validator)
//  5. Classify results: unauthorized/forbidden → blocking; unreachable/error → warning
func EvaluateSetupReadiness(ctx context.Context, cfg *config.Config) *ReadinessResult {
	result := &ReadinessResult{}

	// Step 1-2: Check which required keys are accessible
	keyInfo, allPresent := CheckRequiredAPIKeys(cfg)
	result.KeyInfo = keyInfo
	result.AllPresent = allPresent

	if !allPresent {
		// Can't validate what we don't have — report missing keys
		result.Ready = false
		return result
	}

	// Step 3: All required keys are present — validate them
	validate := validatorFunc
	if validate == nil {
		validate = ValidateRequiredKeys
	}
	validationResults := validate(ctx, cfg)

	// Step 4: Classify results
	hasBlockingError := false
	for i := range validationResults {
		r := &validationResults[i]
		switch r.Status {
		case KeyStatusValid:
			// OK — nothing to do
		case KeyStatusUnauthorized, KeyStatusForbidden:
			// Blocking — key is wrong
			hasBlockingError = true
			result.ValidationErrors = append(result.ValidationErrors, *r)
		case KeyStatusMissing:
			// Shouldn't happen (allPresent was true) but handle defensively
			hasBlockingError = true
			result.ValidationErrors = append(result.ValidationErrors, *r)
		default:
			// Unreachable, generic error, etc. — warn but don't block
			result.Warnings = append(result.Warnings, *r)
		}
	}

	result.Ready = !hasBlockingError
	return result
}

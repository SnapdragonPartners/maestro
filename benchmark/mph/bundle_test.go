package mph_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/mph"
)

const pairedPath = "testdata/paired.toml"

func pairedTOML(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(pairedPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(raw)
}

func writeBundle(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bundle.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	return path
}

func TestLoadFileValid(t *testing.T) {
	loaded, err := mph.LoadFile(pairedPath)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	b := loaded.Bundle
	if b.Name != "paired-default" || b.Harness.Adapter != "v1-as-patched" {
		t.Fatalf("unexpected bundle: %+v", b)
	}
	if b.Model.Roles["reviewer"] != "openai:model-reviewer" {
		t.Fatalf("role routing not decoded: %+v", b.Model)
	}
	if !strings.HasPrefix(loaded.Hash, "sha256:") {
		t.Fatalf("hash %q must carry the algorithm prefix", loaded.Hash)
	}
}

func TestIdentityIsCanonicalNotBytes(t *testing.T) {
	a, err := mph.LoadFile(pairedPath)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	b, err := mph.LoadFile("testdata/paired_reformatted.toml")
	if err != nil {
		t.Fatalf("load reformatted bundle: %v", err)
	}
	if a.Hash != b.Hash {
		t.Fatalf("semantically identical bundles must share identity: %q vs %q", a.Hash, b.Hash)
	}
}

func TestIdentityTracksContent(t *testing.T) {
	a, err := mph.LoadFile(pairedPath)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	edited := strings.Replace(pairedTOML(t), `default = "anthropic:model-main"`, `default = "anthropic:model-next"`, 1)
	b, err := mph.LoadFile(writeBundle(t, edited))
	if err != nil {
		t.Fatalf("load edited bundle: %v", err)
	}
	if a.Hash == b.Hash {
		t.Fatalf("a model change must change identity")
	}
}

func TestLoadFileRejections(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(string) string
		wantErr string
	}{
		{"unknown key", func(s string) string { return s + "\nsurprise = true\n" }, "unknown keys"},
		{"bad name", func(s string) string {
			return strings.Replace(s, `name = "paired-default"`, `name = "Paired Default"`, 1)
		}, "kebab-case"},
		{"missing model", func(s string) string {
			return strings.Replace(s, `default = "anthropic:model-main"`, `default = ""`, 1)
		}, "model.default"},
		{"missing pack", func(s string) string {
			return strings.Replace(s, `pack = "v1-embedded"`, `pack = ""`, 1)
		}, "prompt.pack"},
		{"unprefixed declared hash", func(s string) string {
			return strings.Replace(s, `pack = "v1-embedded"`, "pack = \"v1-embedded\"\nhash = \"deadbeef\"", 1)
		}, "prompt.hash"},
		{"missing adapter", func(s string) string {
			return strings.Replace(s, `adapter = "v1-as-patched"`, `adapter = ""`, 1)
		}, "harness.adapter"},
		{"cap below expectation", func(s string) string {
			return strings.Replace(s, "max_cost_usd_per_run = 6.0", "max_cost_usd_per_run = 1.0", 1)
		}, "max_cost_usd_per_run"},
		{"suite cap below run cap", func(s string) string {
			return strings.Replace(s, "max_cost_usd_per_suite = 60.0", "max_cost_usd_per_suite = 2.0", 1)
		}, "max_cost_usd_per_suite"},
		{"zero expectations", func(s string) string {
			return strings.Replace(s, "expected_tokens_per_run = 500000", "expected_tokens_per_run = 0", 1)
		}, "declared"},
		{"nan cap", func(s string) string {
			return strings.Replace(s, "max_cost_usd_per_run = 6.0", "max_cost_usd_per_run = nan", 1)
		}, "finite"},
		{"infinite cap", func(s string) string {
			return strings.Replace(s, "max_cost_usd_per_suite = 60.0", "max_cost_usd_per_suite = inf", 1)
		}, "finite"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mph.LoadFile(writeBundle(t, tc.mutate(pairedTOML(t))))
			if err == nil {
				t.Fatalf("expected load error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadDirRejectsDuplicateNames(t *testing.T) {
	// testdata holds two formatting variants of the same bundle name, which
	// is exactly the collision LoadDir must refuse.
	_, err := mph.LoadDir("testdata")
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate names must fail, got %v", err)
	}
}

// TestLocalBundleValidation pins the dimension-keyed budget validation
// (item 5.1): a local config is budgeted in tokens and must declare positive
// token caps with zero USD caps; a hosted config must not declare token caps.
func TestLocalBundleValidation(t *testing.T) {
	base := func() *mph.Bundle {
		return &mph.Bundle{
			SchemaVersion: mph.SchemaVersion,
			Name:          "paired-local-test",
			Model:         mph.ModelRouting{Default: "qwen3-coder:30b"},
			Prompt:        mph.PromptRef{Pack: "v1-embedded"},
			Harness:       mph.HarnessSettings{Adapter: "v1-as-patched"},
			Local:         true,
			Budget: mph.DeclaredBudget{
				ExpectedTokensPerRun: 500_000,
				MaxTokensPerRun:      2_000_000,
				MaxTokensPerSuite:    8_000_000,
			},
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("valid local bundle must pass: %v", err)
	}
	b := base()
	b.Budget.MaxCostUSDPerRun = 5
	if err := b.Validate(); err == nil || !strings.Contains(err.Error(), "USD caps") {
		t.Fatalf("local + USD caps must be rejected, got %v", err)
	}
	b = base()
	b.Budget.MaxTokensPerRun = 0
	if err := b.Validate(); err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("local without token caps must be rejected, got %v", err)
	}
	b = base()
	b.Local = false
	b.Budget = mph.DeclaredBudget{
		ExpectedTokensPerRun: 1, ExpectedCostUSDPerRun: 1, MaxCostUSDPerRun: 2, MaxCostUSDPerSuite: 3,
		MaxTokensPerRun: 100,
	}
	if err := b.Validate(); err == nil || !strings.Contains(err.Error(), "token caps") {
		t.Fatalf("hosted + token caps must be rejected, got %v", err)
	}
}

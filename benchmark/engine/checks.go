package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/SnapdragonPartners/maestro/benchmark/internal/gitx"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/story"
)

// runShell executes a validator/check command via sh -c in dir; exit 0
// passes. Output is captured as the result detail.
func runShell(ctx context.Context, dir, name, command string) runrecord.CheckResult {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	detail := truncateDetail(strings.TrimSpace(string(out)))
	if err != nil {
		if detail != "" {
			detail += "; "
		}
		detail += err.Error()
		return runrecord.CheckResult{Name: name, Passed: false, Detail: detail}
	}
	return runrecord.CheckResult{Name: name, Passed: true, Detail: detail}
}

// runValidators executes every story validator in the solution workspace.
func runValidators(ctx context.Context, dir string, validators []story.Validator) []runrecord.CheckResult {
	out := make([]runrecord.CheckResult, 0, len(validators))
	for i := range validators {
		out = append(out, runShell(ctx, dir, validators[i].Name, validators[i].Command))
	}
	return out
}

// runChecks executes every deterministic story check at the solution.
func runChecks(ctx context.Context, dir string, def *story.Definition, pin, solution string) []runrecord.CheckResult {
	out := make([]runrecord.CheckResult, 0, len(def.Checks))
	for i := range def.Checks {
		check := &def.Checks[i]
		var result runrecord.CheckResult
		switch check.Type {
		case story.CheckCommand:
			result = runShell(ctx, dir, check.Name, check.Command)
		case story.CheckFilesChangedWithin:
			result = checkFilesChangedWithin(ctx, dir, check.Name, def.Expectations.AllowedPaths, pin, solution)
		case story.CheckFileContains:
			result = checkFileContains(dir, check)
		default:
			// Unknown types cannot load past story validation; belt and
			// suspenders for hand-built definitions.
			result = runrecord.CheckResult{Name: check.Name, Passed: false, Detail: fmt.Sprintf("unknown check type %q", check.Type)}
		}
		out = append(out, result)
	}
	return out
}

// checkFilesChangedWithin passes when every changed path between the pin
// and the solution commit is inside the story's allowed paths.
func checkFilesChangedWithin(ctx context.Context, dir, name string, allowed []string, pin, solution string) runrecord.CheckResult {
	changed, err := gitx.DiffNames(ctx, dir, pin, solution)
	if err != nil {
		return runrecord.CheckResult{Name: name, Passed: false, Detail: truncateDetail(err.Error())}
	}
	var outside []string
	for _, path := range changed {
		if !pathAllowed(path, allowed) {
			outside = append(outside, path)
		}
	}
	if len(outside) > 0 {
		return runrecord.CheckResult{Name: name, Passed: false, Detail: truncateDetail("changed outside allowed paths: " + strings.Join(outside, ", "))}
	}
	return runrecord.CheckResult{Name: name, Passed: true, Detail: fmt.Sprintf("%d files changed, all allowed", len(changed))}
}

// pathAllowed reports whether path equals an allowed entry or lives under
// an allowed directory entry.
func pathAllowed(path string, allowed []string) bool {
	for _, entry := range allowed {
		if path == entry {
			return true
		}
		prefix := strings.TrimSuffix(entry, "/") + "/"
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// checkFileContains passes when the file at the check's path contains its
// substring at the validated solution state.
func checkFileContains(dir string, check *story.Check) runrecord.CheckResult {
	raw, err := os.ReadFile(filepath.Join(dir, check.Path))
	if err != nil {
		return runrecord.CheckResult{Name: check.Name, Passed: false, Detail: truncateDetail(err.Error())}
	}
	if !strings.Contains(string(raw), check.Contains) {
		return runrecord.CheckResult{Name: check.Name, Passed: false, Detail: fmt.Sprintf("%s does not contain %q", check.Path, check.Contains)}
	}
	return runrecord.CheckResult{Name: check.Name, Passed: true}
}

// evidenceCoverage returns the expected evidence kinds and required
// artifacts not covered by the observation's evidence.
func evidenceCoverage(def *story.Definition, evidence []runrecord.EvidencePointer) []string {
	kinds := make(map[string]bool, len(evidence))
	for i := range evidence {
		kinds[evidence[i].Kind] = true
	}
	var missing []string
	for _, want := range def.Expectations.EvidenceShape {
		if !kinds[want] {
			missing = append(missing, "evidence:"+want)
		}
	}
	for _, want := range def.Expectations.RequiredArtifacts {
		if !kinds[want] {
			missing = append(missing, "artifact:"+want)
		}
	}
	return missing
}

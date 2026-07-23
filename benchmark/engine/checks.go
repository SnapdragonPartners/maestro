package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/internal/gitx"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
	"github.com/SnapdragonPartners/maestro/benchmark/story"
)

// runShell executes a validator/check command via sh -c in dir; exit 0
// passes. Output is captured as the result detail. The command runs in its
// own process group and the whole group is killed on context expiry —
// otherwise an orphaned grandchild holding the output pipe would block
// CombinedOutput past the wall-clock cap (observed on Linux CI).
func runShell(ctx context.Context, dir, name, command string) runrecord.CheckResult {
	result, _ := runShellFull(ctx, dir, name, command)
	return result
}

// runShellFull is runShell also returning the full captured output for
// evidence export.
func runShellFull(ctx context.Context, dir, name, command string) (runrecord.CheckResult, string) {
	return runProcess(ctx, dir, nil, name, "sh", "-c", command)
}

// runProcess executes argv in dir (with the given extra environment appended
// to the parent's, or the parent's alone when env is nil) and reports pass on
// exit 0. Like runShellFull it runs in its own process group and kills the
// whole group on context expiry, so an orphaned grandchild holding the output
// pipe cannot block CombinedOutput past the wall-clock cap. Oracle checks use
// this directly (no shell) so nothing re-introduces the truncation/quoting
// hazards the oracle change exists to remove.
func runProcess(ctx context.Context, dir string, env []string, name string, argv ...string) (runrecord.CheckResult, string) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // argv is story-authored, hashed into identity
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) //nolint:wrapcheck // exec.Cmd.Cancel contract
	}
	cmd.WaitDelay = 5 * time.Second // backstop if the pipe is still held
	out, err := cmd.CombinedOutput()
	full := strings.TrimSpace(string(out))
	detail := truncateDetail(full)
	if err != nil {
		if detail != "" {
			detail += "; "
		}
		detail += err.Error()
		full += "\n" + err.Error()
		return runrecord.CheckResult{Name: name, Passed: false, Detail: detail}, full
	}
	return runrecord.CheckResult{Name: name, Passed: true, Detail: detail}, full
}

// runValidators executes every story validator in the solution workspace,
// returning results plus each validator's full captured output (the record
// keeps truncated details; evidence files keep everything).
func runValidators(ctx context.Context, dir string, validators []story.Validator) ([]runrecord.CheckResult, []string) {
	results := make([]runrecord.CheckResult, 0, len(validators))
	outputs := make([]string, 0, len(validators))
	for i := range validators {
		result, output := runShellFull(ctx, dir, validators[i].Name, validators[i].Command)
		results = append(results, result)
		outputs = append(outputs, output)
	}
	return results, outputs
}

// runChecks executes every deterministic story check at the solution. loaded
// carries the retained oracle-asset bytes an oracle check materialises; solution
// is the immutable commit a scratch-mode oracle checks out.
func runChecks(ctx context.Context, dir string, loaded *story.Loaded, pin, solution string) []runrecord.CheckResult {
	def := loaded.Definition
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
		case story.CheckOracle:
			result = runOracle(ctx, dir, check, loaded.OracleAssets, solution)
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

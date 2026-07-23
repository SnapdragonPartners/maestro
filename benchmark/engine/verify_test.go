package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/SnapdragonPartners/maestro/benchmark/story"
)

// gitInit lays down a one-commit git repo with the given files and returns the
// dir and its HEAD commit. Verify needs a real commit for the pin/solution
// diff in files_changed_within.
func gitInit(t *testing.T, files map[string]string) (dir, head string) {
	t.Helper()
	dir = t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "t"},
		{"config", "user.email", "t@t"},
		{"add", "-A"},
		{"commit", "-q", "-m", "base"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, string(out[:len(out)-1])
}

func verifyStory(validators []story.Validator, checks []story.Check) *story.Loaded {
	return &story.Loaded{Definition: &story.Definition{
		Validators: validators,
		Checks:     checks,
	}}
}

// TestVerifyAllPass exercises the single executor: a passing validator and
// check yield OK, and the validator's full output is surfaced on
// ValidatorOutputs (the field the evidence contract depends on).
func TestVerifyAllPass(t *testing.T) {
	dir, head := gitInit(t, map[string]string{"x.txt": "hi"})
	loaded := verifyStory(
		[]story.Validator{{Name: "echo", Command: "echo hello-from-validator"}},
		[]story.Check{{Name: "present", Type: story.CheckCommand, Command: "test -f x.txt"}},
	)
	res := Verify(context.Background(), dir, loaded, head, head)
	if !res.OK {
		t.Fatalf("expected OK; validators=%+v checks=%+v", res.Validators, res.Checks)
	}
	if len(res.ValidatorOutputs) != 1 {
		t.Fatalf("ValidatorOutputs len = %d, want 1", len(res.ValidatorOutputs))
	}
	// The FULL output must survive — evidence writing depends on it, and it is
	// exactly what a truncated CheckResult would drop.
	if got := res.ValidatorOutputs[0]; !contains(got, "hello-from-validator") {
		t.Errorf("validator output %q lost its content", got)
	}
}

// TestVerifyPreservesFullValidatorOutput proves ValidatorOutputs carries the
// UNtruncated output, distinct from the truncated CheckResult.Detail — the
// exact property the test-output evidence contract depends on. The validator
// emits more than detailLimit with a tail sentinel: the sentinel must survive
// in the full output and in the evidence file, and must be absent from Detail.
func TestVerifyPreservesFullValidatorOutput(t *testing.T) {
	const sentinel = "_TAIL_SENTINEL_"
	dir, head := gitInit(t, map[string]string{"x.txt": "hi"})
	// (detailLimit + 100) 'A's, then the sentinel on its own line — so the
	// sentinel sits well beyond the truncation point.
	cmd := "head -c " + strconv.Itoa(detailLimit+100) + " /dev/zero | tr '\\0' 'A'; echo " + sentinel
	loaded := verifyStory(
		[]story.Validator{{Name: "big", Command: cmd}},
		nil,
	)
	res := Verify(context.Background(), dir, loaded, head, head)

	if len(res.ValidatorOutputs) != 1 {
		t.Fatalf("ValidatorOutputs len = %d, want 1", len(res.ValidatorOutputs))
	}
	full := res.ValidatorOutputs[0]
	if !contains(full, sentinel) {
		t.Fatal("full validator output lost the tail sentinel — it was truncated")
	}
	if detail := res.Validators[0].Detail; contains(detail, sentinel) {
		t.Fatalf("Detail retained the tail sentinel; it should be truncated at detailLimit (len=%d)", len(detail))
	}

	// The evidence file (the attempt->writeValidatorEvidence path) must persist
	// the FULL output, not the truncated detail.
	evDir := t.TempDir()
	pointers := writeValidatorEvidence(evDir, loaded.Definition.Validators, res.ValidatorOutputs, func(string, ...any) {})
	if len(pointers) != 1 {
		t.Fatalf("evidence pointers = %d, want 1", len(pointers))
	}
	body, err := os.ReadFile(pointers[0].Location)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(body), sentinel) {
		t.Fatal("evidence file lost the tail sentinel — writeValidatorEvidence did not persist the full output")
	}
}

// TestVerifyValidatorFailureFailsOverall proves a failing validator alone
// flips OK, independent of the checks.
func TestVerifyValidatorFailureFailsOverall(t *testing.T) {
	dir, head := gitInit(t, map[string]string{"x.txt": "hi"})
	loaded := verifyStory(
		[]story.Validator{{Name: "false", Command: "exit 3"}},
		[]story.Check{{Name: "present", Type: story.CheckCommand, Command: "true"}},
	)
	res := Verify(context.Background(), dir, loaded, head, head)
	if res.OK {
		t.Fatal("a failing validator must fail overall")
	}
	if res.Validators[0].Passed {
		t.Error("validator recorded as passed despite non-zero exit")
	}
}

// TestVerifyCheckFailureFailsOverall proves a failing check alone flips OK.
func TestVerifyCheckFailureFailsOverall(t *testing.T) {
	dir, head := gitInit(t, map[string]string{"x.txt": "hi"})
	loaded := verifyStory(
		[]story.Validator{{Name: "true", Command: "true"}},
		[]story.Check{{Name: "missing", Type: story.CheckCommand, Command: "test -f nope.txt"}},
	)
	res := Verify(context.Background(), dir, loaded, head, head)
	if res.OK {
		t.Fatal("a failing check must fail overall")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return len(sub) == 0
}

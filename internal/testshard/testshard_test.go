package testshard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// writePackage lays out a package directory from name→source pairs.
func writePackage(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

const tagged = "//go:build integration\n\npackage p\n\nimport \"testing\"\n\n"
const untagged = "package p\n\nimport \"testing\"\n\n"

// TestAssignsEveryTaggedTestToItsShard is the happy path, and checks the
// two properties CI consumes: the pattern selects exactly the shard's tests
// (a name that is another's prefix must not leak across shards) and the
// count is the number of top-level tests.
func TestAssignsEveryTaggedTestToItsShard(t *testing.T) {
	dir := writePackage(t, map[string]string{
		"a_integration_test.go": tagged +
			"// TestRestore restores.\n//ci:shard 0\nfunc TestRestore(t *testing.T) {}\n\n" +
			"//ci:shard 1\nfunc TestRestoreTwice(t *testing.T) {}\n",
		"b_integration_test.go": tagged +
			"//ci:shard 0\nfunc TestBackup(t *testing.T) {}\n\n" +
			"//ci:shard\t2\nfunc TestReset(t *testing.T) {}\n" +
			"func helper(t *testing.T) {}\n" +
			"func TestMain(m *testing.M) {}\n",
		"unit_test.go":       untagged + "func TestUnit(t *testing.T) {}\n",
		"excluded_test.go":   "//go:build !integration\n\npackage p\n\nimport \"testing\"\n\nfunc TestNever(t *testing.T) {}\n",
		"notatest.go":        "package p\n",
		"README_test.go.bak": "not go",
	})
	assignment, err := Discover(dir, "integration")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := [][]string{{"TestBackup", "TestRestore"}, {"TestRestoreTwice"}, {"TestReset"}}
	if len(assignment.Shards) != len(want) {
		t.Fatalf("shards = %v, want %v", assignment.Shards, want)
	}
	for id, names := range want {
		if strings.Join(assignment.Shards[id], ",") != strings.Join(names, ",") {
			t.Errorf("shard %d = %v, want %v", id, assignment.Shards[id], names)
		}
		if assignment.Count(id) != len(names) {
			t.Errorf("Count(%d) = %d, want %d", id, assignment.Count(id), len(names))
		}
	}

	pattern := regexp.MustCompile(assignment.RunPattern(0))
	for name, selected := range map[string]bool{
		"TestBackup": true, "TestRestore": true,
		"TestRestoreTwice": false, "TestReset": false, "TestUnit": false, "TestNever": false, "TestBackupX": false,
	} {
		if pattern.MatchString(name) != selected {
			t.Errorf("pattern %q matches %s = %v, want %v", pattern, name, !selected, selected)
		}
	}
}

// TestRefusesEveryWayAnAssignmentGoesWrong plants each defect the rules
// exist for and checks the error names the offender. Each case is one
// file, so a rule that stopped firing shows up as its own failure.
func TestRefusesEveryWayAnAssignmentGoesWrong(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "tagged test with no directive",
			source: tagged + "//ci:shard 0\nfunc TestA(t *testing.T) {}\n\nfunc TestOrphan(t *testing.T) {}\n",
			want:   "TestOrphan names no shard",
		},
		{
			name:   "tagged test in two shards",
			source: tagged + "//ci:shard 0\n//ci:shard 1\nfunc TestTwice(t *testing.T) {}\n\n//ci:shard 1\nfunc TestB(t *testing.T) {}\n",
			want:   "TestTwice names 2 shards",
		},
		{
			name:   "directive on a unit test",
			source: untagged + "//ci:shard 0\nfunc TestUnit(t *testing.T) {}\n",
			want:   "TestUnit names a shard but is not an integration-only test",
		},
		{
			name:   "directive detached by a blank line",
			source: tagged + "//ci:shard 0\n\nfunc TestDetached(t *testing.T) {}\n",
			want:   "is not in a test's doc comment",
		},
		{
			name:   "directive on a helper",
			source: tagged + "//ci:shard 0\nfunc TestA(t *testing.T) {}\n\n//ci:shard 0\nfunc helper() {}\n",
			want:   "is not in a test's doc comment",
		},
		{
			name:   "empty shard between two used ones",
			source: tagged + "//ci:shard 0\nfunc TestA(t *testing.T) {}\n\n//ci:shard 2\nfunc TestC(t *testing.T) {}\n",
			want:   "shard 1 is empty",
		},
		{
			name:   "shards not starting at zero",
			source: tagged + "//ci:shard 1\nfunc TestA(t *testing.T) {}\n",
			want:   "shard 0 is empty",
		},
		{
			name:   "malformed directive: no id",
			source: tagged + "//ci:shard\nfunc TestA(t *testing.T) {}\n",
			want:   "malformed directive",
		},
		{
			name:   "malformed directive: negative id",
			source: tagged + "//ci:shard -1\nfunc TestA(t *testing.T) {}\n",
			want:   "malformed directive",
		},
		{
			name:   "malformed directive: not a number",
			source: tagged + "//ci:shard two\nfunc TestA(t *testing.T) {}\n",
			want:   "malformed directive",
		},
		{
			name:   "no integration-only tests at all",
			source: untagged + "func TestUnit(t *testing.T) {}\n",
			want:   "nothing to shard",
		},
		{
			name:   "constraint on a tag this tool cannot evaluate",
			source: "//go:build integration && linux\n\npackage p\n\nimport \"testing\"\n\n//ci:shard 0\nfunc TestA(t *testing.T) {}\n",
			want:   "build constraint mentions linux",
		},
		{
			name:   "file that does not parse",
			source: tagged + "func TestA(t *testing.T) {\n",
			want:   "expected '}'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writePackage(t, map[string]string{"x_test.go": tc.source})
			_, err := Discover(dir, "integration")
			if err == nil {
				t.Fatalf("Discover accepted the package; want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestReportsEveryProblemAtOnce: a guard that stops at the first
// unassigned test costs a review round per test. The error carries all of
// them.
func TestReportsEveryProblemAtOnce(t *testing.T) {
	dir := writePackage(t, map[string]string{
		"x_test.go": tagged + "func TestA(t *testing.T) {}\n\nfunc TestB(t *testing.T) {}\n",
		"y_test.go": tagged + "//ci:shard 0\nfunc TestC(t *testing.T) {}\n\nfunc TestD(t *testing.T) {}\n",
	})
	_, err := Discover(dir, "integration")
	if err == nil {
		t.Fatal("Discover accepted a package with three unassigned tests")
	}
	for _, name := range []string{"TestA", "TestB", "TestD"} {
		if !strings.Contains(err.Error(), name+" names no shard") {
			t.Errorf("error does not name %s:\n%v", name, err)
		}
	}
}

// TestIsTestFollowsGoTestsRule pins the name rule to `go test`'s: `Testable`
// is not a test, `Test_x` and `Test` are, and TestMain is never one.
func TestIsTestFollowsGoTestsRule(t *testing.T) {
	dir := writePackage(t, map[string]string{
		"x_test.go": tagged +
			"//ci:shard 0\nfunc Test(t *testing.T) {}\n\n" +
			"//ci:shard 0\nfunc Test_x(t *testing.T) {}\n\n" +
			"//ci:shard 0\nfunc TestÜ(t *testing.T) {}\n\n" +
			"func Testable(t *testing.T) {}\n\n" +
			"func TestMain(m *testing.M) {}\n",
	})
	assignment, err := Discover(dir, "integration")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got := strings.Join(assignment.Shards[0], ","); got != "Test,Test_x,TestÜ" {
		t.Errorf("shard 0 = %q, want Test,Test_x,TestÜ", got)
	}
}

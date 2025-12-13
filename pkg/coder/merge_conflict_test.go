package coder

import (
	"strings"
	"testing"
)

// TestBuildConflictResolutionMessage tests the message formatting for conflict resolution.
func TestBuildConflictResolutionMessage(t *testing.T) {
	coder := &Coder{}

	testCases := []struct {
		name           string
		info           *MergeConflictInfo
		expectedParts  []string // Parts that should be present in the message
		unexpectedPart string   // Part that should NOT be present (optional)
	}{
		{
			name: "RebaseConflict",
			info: &MergeConflictInfo{
				Kind:             FailureRebaseConflict,
				ErrorOutput:      "CONFLICT (content): Merge conflict in main.go",
				ConflictingFiles: []string{"main.go", "util.go"},
				GitStatus:        "UU main.go\nUU util.go",
				MidRebase:        true,
				AttemptNumber:    1,
				MaxAttempts:      3,
			},
			expectedParts: []string{
				"Rebase conflict",
				"main.go",
				"util.go",
				"git rebase --continue",
				"Attempt 1 of 3",
				"shell",
			},
		},
		{
			name: "MergeConflict",
			info: &MergeConflictInfo{
				Kind:             FailureMergeConflict,
				ErrorOutput:      "Automatic merge failed",
				ConflictingFiles: []string{"config.yaml"},
				GitStatus:        "UU config.yaml",
				MidRebase:        false,
				AttemptNumber:    2,
				MaxAttempts:      3,
			},
			expectedParts: []string{
				"Merge conflict",
				"config.yaml",
				"Attempt 2 of 3",
			},
		},
		{
			name: "PushRejected",
			info: &MergeConflictInfo{
				Kind:             FailurePushRejected,
				ErrorOutput:      "! [rejected] main -> main (non-fast-forward)",
				ConflictingFiles: nil,
				GitStatus:        "nothing to commit",
				MidRebase:        false,
				AttemptNumber:    1,
				MaxAttempts:      3,
			},
			expectedParts: []string{
				"Push rejected",
				"non-fast-forward",
			},
		},
		{
			name: "LastAttempt",
			info: &MergeConflictInfo{
				Kind:             FailureRebaseConflict,
				ErrorOutput:      "conflict",
				ConflictingFiles: []string{"file.go"},
				GitStatus:        "UU file.go",
				MidRebase:        true,
				AttemptNumber:    3,
				MaxAttempts:      3,
			},
			expectedParts: []string{
				"Attempt 3 of 3",
				"last attempt",
				"ask_question",
			},
		},
		{
			name: "AuthError",
			info: &MergeConflictInfo{
				Kind:             FailureAuthError,
				ErrorOutput:      "Authentication failed",
				ConflictingFiles: nil,
				GitStatus:        "",
				MidRebase:        false,
				AttemptNumber:    1,
				MaxAttempts:      3,
			},
			expectedParts: []string{
				"Authentication error",
				"infrastructure issue",
			},
		},
		{
			name: "BinaryFileGuidance",
			info: &MergeConflictInfo{
				Kind:             FailureRebaseConflict,
				ErrorOutput:      "conflict",
				ConflictingFiles: []string{"image.png"},
				GitStatus:        "UU image.png",
				MidRebase:        true,
				AttemptNumber:    1,
				MaxAttempts:      3,
			},
			expectedParts: []string{
				"binary files",
				"--ours",
				"--theirs",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg := coder.buildConflictResolutionMessage(tc.info)

			for _, expected := range tc.expectedParts {
				if !strings.Contains(strings.ToLower(msg), strings.ToLower(expected)) {
					t.Errorf("Expected message to contain '%s', but it didn't.\nMessage:\n%s", expected, msg)
				}
			}

			if tc.unexpectedPart != "" {
				if strings.Contains(strings.ToLower(msg), strings.ToLower(tc.unexpectedPart)) {
					t.Errorf("Expected message to NOT contain '%s', but it did.\nMessage:\n%s", tc.unexpectedPart, msg)
				}
			}
		})
	}
}

// TestRebaseConflictError tests the error interface implementation.
func TestRebaseConflictError(t *testing.T) {
	err := &RebaseConflictError{
		ErrorOutput:      "CONFLICT in file.go",
		ConflictingFiles: []string{"file.go", "other.go"},
		GitStatus:        "UU file.go\nUU other.go",
	}

	// Test Error() method
	errMsg := err.Error()
	if !strings.Contains(errMsg, "CONFLICT in file.go") {
		t.Errorf("Error message should contain the error output, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "manual resolution") {
		t.Errorf("Error message should mention manual resolution, got: %s", errMsg)
	}
}

// TestMergeFailureKindValues tests that all failure kinds have expected string values.
func TestMergeFailureKindValues(t *testing.T) {
	testCases := []struct {
		kind     MergeFailureKind
		expected string
	}{
		{FailureRebaseConflict, "rebase_conflict"},
		{FailureMergeConflict, "merge_conflict"},
		{FailurePushRejected, "push_rejected"},
		{FailureAuthError, "auth_error"},
		{FailureUnknown, "unknown"},
	}

	for _, tc := range testCases {
		t.Run(string(tc.kind), func(t *testing.T) {
			if string(tc.kind) != tc.expected {
				t.Errorf("Expected %s, got %s", tc.expected, string(tc.kind))
			}
		})
	}
}

// TestMergeAttemptConstants tests the iteration limit constants.
func TestMergeAttemptConstants(t *testing.T) {
	// Verify constants have reasonable values
	if MaxStuckAttempts < 1 || MaxStuckAttempts > 5 {
		t.Errorf("MaxStuckAttempts should be between 1 and 5, got %d", MaxStuckAttempts)
	}

	if MaxTotalAttempts < MaxStuckAttempts {
		t.Errorf("MaxTotalAttempts (%d) should be >= MaxStuckAttempts (%d)",
			MaxTotalAttempts, MaxStuckAttempts)
	}

	if MaxTotalAttempts > 10 {
		t.Errorf("MaxTotalAttempts should be <= 10 to prevent excessive retries, got %d", MaxTotalAttempts)
	}
}

// TestStateKeyConstants tests that state keys are properly defined.
func TestMergeStateKeyConstants(t *testing.T) {
	// Verify keys are non-empty and unique
	keys := []string{
		KeyMergeAttemptCount,
		KeyMergeStuckAttempts,
		KeyLastRemoteHEAD,
	}

	seen := make(map[string]bool)
	for _, key := range keys {
		if key == "" {
			t.Error("State key should not be empty")
		}
		if seen[key] {
			t.Errorf("Duplicate state key: %s", key)
		}
		seen[key] = true
	}
}

// TestGitWorkspaceStateStruct tests the GitWorkspaceState struct defaults.
func TestGitWorkspaceStateStruct(t *testing.T) {
	// Test zero value
	var state GitWorkspaceState

	if state.MidRebase {
		t.Error("MidRebase should default to false")
	}
	if state.MidMerge {
		t.Error("MidMerge should default to false")
	}
	if state.IndexLocked {
		t.Error("IndexLocked should default to false")
	}
	if state.HasConflicts {
		t.Error("HasConflicts should default to false")
	}
	if state.HasUncommitted {
		t.Error("HasUncommitted should default to false")
	}
	if len(state.ConflictingFiles) != 0 {
		t.Error("ConflictingFiles should default to empty slice")
	}
}

// TestParseGitStatusOutput tests parsing of git status --porcelain output.
// This is a unit test that doesn't require git - it just tests the parsing logic.
func TestParseGitStatusOutput(t *testing.T) {
	testCases := []struct {
		name           string
		statusOutput   string
		expectConflict bool
		expectUncommit bool
		conflictFiles  []string
	}{
		{
			name:           "NoChanges",
			statusOutput:   "",
			expectConflict: false,
			expectUncommit: false,
			conflictFiles:  nil,
		},
		{
			name:           "UntrackedFiles",
			statusOutput:   "?? newfile.txt\n?? another.txt",
			expectConflict: false,
			expectUncommit: false,
			conflictFiles:  nil,
		},
		{
			name:           "ModifiedFile",
			statusOutput:   " M modified.go",
			expectConflict: false,
			expectUncommit: true,
			conflictFiles:  nil,
		},
		{
			name:           "StagedFile",
			statusOutput:   "M  staged.go",
			expectConflict: false,
			expectUncommit: true,
			conflictFiles:  nil,
		},
		{
			name:           "ConflictUU",
			statusOutput:   "UU conflict.go",
			expectConflict: true,
			expectUncommit: false,
			conflictFiles:  []string{"conflict.go"},
		},
		{
			name:           "ConflictAA",
			statusOutput:   "AA both_added.go",
			expectConflict: true,
			expectUncommit: false,
			conflictFiles:  []string{"both_added.go"},
		},
		{
			name:           "ConflictDD",
			statusOutput:   "DD both_deleted.go",
			expectConflict: true,
			expectUncommit: false,
			conflictFiles:  []string{"both_deleted.go"},
		},
		{
			name:           "MixedStatus",
			statusOutput:   "UU conflict.go\n M modified.go\n?? untracked.go",
			expectConflict: true,
			expectUncommit: true,
			conflictFiles:  []string{"conflict.go"},
		},
		{
			name:           "MultipleConflicts",
			statusOutput:   "UU file1.go\nUU file2.go\nAA file3.go",
			expectConflict: true,
			expectUncommit: false,
			conflictFiles:  []string{"file1.go", "file2.go", "file3.go"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Parse the status output using the same logic as detectGitWorkspaceState
			state := parseGitStatusOutput(tc.statusOutput)

			if state.HasConflicts != tc.expectConflict {
				t.Errorf("HasConflicts: expected %v, got %v", tc.expectConflict, state.HasConflicts)
			}

			if state.HasUncommitted != tc.expectUncommit {
				t.Errorf("HasUncommitted: expected %v, got %v", tc.expectUncommit, state.HasUncommitted)
			}

			if len(state.ConflictingFiles) != len(tc.conflictFiles) {
				t.Errorf("ConflictingFiles count: expected %d, got %d (%v)",
					len(tc.conflictFiles), len(state.ConflictingFiles), state.ConflictingFiles)
			}

			for i, expected := range tc.conflictFiles {
				if i >= len(state.ConflictingFiles) {
					break
				}
				if state.ConflictingFiles[i] != expected {
					t.Errorf("ConflictingFiles[%d]: expected %s, got %s",
						i, expected, state.ConflictingFiles[i])
				}
			}
		})
	}
}

// parseGitStatusOutput is a helper that extracts the parsing logic for testing.
// This mirrors the logic in detectGitWorkspaceState.
func parseGitStatusOutput(output string) *GitWorkspaceState {
	state := &GitWorkspaceState{
		GitStatusOutput: output,
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if len(line) < 3 {
			continue
		}
		statusCode := line[:2]
		fileName := strings.TrimSpace(line[3:])

		// UU = unmerged, both modified (conflict)
		// AA = unmerged, both added (conflict)
		// DD = unmerged, both deleted (conflict)
		// AU/UA/DU/UD = other unmerged states
		if strings.Contains(statusCode, "U") ||
			statusCode == "AA" || statusCode == "DD" {
			state.HasConflicts = true
			state.ConflictingFiles = append(state.ConflictingFiles, fileName)
		} else if statusCode != "??" && statusCode != "!!" {
			// Any other status (M, A, D, R, C) means uncommitted changes
			state.HasUncommitted = true
		}
	}

	return state
}

// TestIterationLimitLogic tests the smart iteration limit logic.
// The logic mirrors handlePrepareMerge: MaxStuckAttempts=2, MaxTotalAttempts=3
// stuckAttempts is incremented AFTER checking (same as in code), so:
//   - stuckAttempts starts at 0 on first entry
//   - After incrementing, stuckAttempts >= 2 triggers stuck failure
//   - attemptCount >= 3 triggers total failure
func TestIterationLimitLogic(t *testing.T) {
	testCases := []struct {
		name           string
		attemptCount   int // Already incremented in test (as code does)
		stuckAttempts  int // Value BEFORE this iteration's increment
		lastRemoteHEAD string
		currentHEAD    string
		expectFail     bool
		expectReason   string
	}{
		{
			name:           "FirstAttempt",
			attemptCount:   1,
			stuckAttempts:  0,
			lastRemoteHEAD: "", // No last HEAD on first attempt
			currentHEAD:    "abc123",
			expectFail:     false,
		},
		{
			name:           "SecondAttemptSameHEAD",
			attemptCount:   2,
			stuckAttempts:  0, // Was 0 after first attempt (no last HEAD to compare)
			lastRemoteHEAD: "abc123",
			currentHEAD:    "abc123",
			expectFail:     false, // After increment, stuck=1 which is < MaxStuckAttempts(2)
		},
		{
			name:           "ThirdAttemptSameHEAD_StuckLimitReached",
			attemptCount:   3,
			stuckAttempts:  1, // After second attempt same HEAD
			lastRemoteHEAD: "abc123",
			currentHEAD:    "abc123",
			expectFail:     true,    // After increment, stuck=2 >= MaxStuckAttempts(2), AND total=3 >= MaxTotalAttempts(3)
			expectReason:   "stuck", // Stuck check comes first
		},
		{
			name:           "HEADChanged_ResetStuckCounter",
			attemptCount:   2,
			stuckAttempts:  1,
			lastRemoteHEAD: "abc123",
			currentHEAD:    "def456", // HEAD changed - stuck counter resets to 0
			expectFail:     false,
		},
		{
			name:           "TotalLimitReached_EvenWithHEADChange",
			attemptCount:   3, // >= MaxTotalAttempts
			stuckAttempts:  1,
			lastRemoteHEAD: "abc123",
			currentHEAD:    "def456", // HEAD changed, but total limit still reached
			expectFail:     true,
			expectReason:   "total",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Simulate the logic from handlePrepareMerge
			stuckAttempts := tc.stuckAttempts

			// Update stuck counter based on HEAD comparison
			if tc.currentHEAD != "" && tc.lastRemoteHEAD != "" {
				if tc.currentHEAD == tc.lastRemoteHEAD {
					stuckAttempts++
				} else {
					stuckAttempts = 0
				}
			}

			// Check limits - same order as handlePrepareMerge (stuck first, then total)
			shouldFail := false
			failReason := ""

			// Stuck check comes FIRST in the real code
			if stuckAttempts >= MaxStuckAttempts {
				shouldFail = true
				failReason = "stuck"
			} else if tc.attemptCount >= MaxTotalAttempts {
				// Total check only if not already failing due to stuck
				shouldFail = true
				failReason = "total"
			}

			if shouldFail != tc.expectFail {
				t.Errorf("Expected fail=%v, got %v (stuckAttempts=%d, attemptCount=%d)",
					tc.expectFail, shouldFail, stuckAttempts, tc.attemptCount)
			}

			if tc.expectFail && failReason != tc.expectReason {
				t.Errorf("Expected fail reason '%s', got '%s'", tc.expectReason, failReason)
			}
		})
	}
}

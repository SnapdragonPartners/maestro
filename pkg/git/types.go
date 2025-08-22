package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	execpkg "orchestrator/pkg/exec"
)

// MergeResult represents the result of a git merge operation.
type MergeResult struct {
	Status       string
	ConflictInfo string
	MergeCommit  string
}

// WorkDoneResult contains detailed information about repository state
type WorkDoneResult struct {
	HasWork   bool     // true if any work was done (changes or commits)
	Reasons   []string // human-readable reasons why HasWork is true
	Unstaged  bool     // has unstaged changes
	Staged    bool     // has staged changes
	Untracked bool     // has untracked files
	Ahead     bool     // has commits ahead of base branch
	Err       error    // any error encountered
}

// GitExecutor interface for running git commands (allows container-based execution)
type GitExecutor interface {
	Run(ctx context.Context, cmd []string, opts *execpkg.Opts) (execpkg.Result, error)
}

// No longer needed - using execpkg.Opts directly

// CheckWorkDone returns comprehensive info about whether work was done on this branch.
// baseBranch: the branch we branched from (e.g., "main", "master")
// workDir: directory to run git commands in (only used for direct exec, ignored with executor)
// executor: optional executor for container-based git commands (if nil, uses direct exec)
func CheckWorkDone(ctx context.Context, baseBranch, workDir string, executor GitExecutor) *WorkDoneResult {
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	res := &WorkDoneResult{Reasons: []string{}}

	// Define execution helpers based on whether we have an executor
	var run func(args ...string) ([]byte, error)
	var runOK func(args ...string) error

	if executor != nil {
		// Use executor (container-based execution)
		run = func(args ...string) ([]byte, error) {
			gitArgs := append([]string{"git"}, args...)
			opts := &execpkg.Opts{
				WorkDir: workDir,
				Timeout: 10 * time.Second,
			}
			result, err := executor.Run(ctx, gitArgs, opts)
			if err != nil {
				return nil, err
			}
			// Extract stdout from Result struct
			return []byte(result.Stdout), nil
		}
		runOK = func(args ...string) error {
			gitArgs := append([]string{"git"}, args...)
			opts := &execpkg.Opts{
				WorkDir: workDir,
				Timeout: 10 * time.Second,
			}
			_, err := executor.Run(ctx, gitArgs, opts)
			return err
		}
	} else {
		// Direct execution (host-based)
		run = func(args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, "git", args...)
			if workDir != "" {
				cmd.Dir = workDir
			}
			return cmd.Output()
		}
		runOK = func(args ...string) error {
			cmd := exec.CommandContext(ctx, "git", args...)
			if workDir != "" {
				cmd.Dir = workDir
			}
			return cmd.Run()
		}
	}

	// Sanity: are we inside a repo?
	if err := runOK("rev-parse", "--is-inside-work-tree"); err != nil {
		res.Err = fmt.Errorf("not a git repo (rev-parse): %w", err)
		return res
	}

	// Determine if repo has any commit yet (brand-new repos will fail here).
	hasHead := (runOK("rev-parse", "--verify", "HEAD") == nil)

	// 1) Unstaged changes
	if err := runOK("diff", "--quiet"); err != nil {
		res.Unstaged, res.HasWork = true, true
		res.Reasons = append(res.Reasons, "unstaged changes")
	}

	// 2) Staged changes
	if err := runOK("diff", "--cached", "--quiet"); err != nil {
		res.Staged, res.HasWork = true, true
		res.Reasons = append(res.Reasons, "staged changes")
	}

	// 3) Untracked files (honors .gitignore)
	if out, err := run("ls-files", "--others", "--exclude-standard"); err == nil && len(bytes.TrimSpace(out)) > 0 {
		res.Untracked, res.HasWork = true, true
		res.Reasons = append(res.Reasons, "untracked files")
	}

	// 4) Commits ahead of base (work since branching)
	// Handle brand-new repos (no HEAD): commits ahead can't exist yet.
	if hasHead {
		// Resolve a compare target:
		//   try <baseBranch>, else try remotes/origin/<baseBranch>.
		target := baseBranch
		if err := runOK("rev-parse", "--verify", target); err != nil {
			originRef := "refs/remotes/origin/" + baseBranch
			if err2 := runOK("rev-parse", "--verify", originRef); err2 == nil {
				target = "origin/" + baseBranch
			} else {
				// Neither local nor origin/<base> exists. We can't compare; don't fail hard.
				res.Err = fmt.Errorf("base branch %q not found locally or on origin", baseBranch)
				target = "" // skip ahead check
			}
		}

		if target != "" {
			// Optional: Use merge-base to count commits since the branch point,
			// which is more stable if base moved.
			mb, err := run("merge-base", target, "HEAD")
			if err == nil {
				basePoint := strings.TrimSpace(string(mb))
				if basePoint != "" {
					out, err := run("rev-list", basePoint+"..HEAD")
					if err == nil && len(bytes.TrimSpace(out)) > 0 {
						res.Ahead, res.HasWork = true, true
						res.Reasons = append(res.Reasons, fmt.Sprintf("commits ahead of %s", target))
					}
				}
			} else {
				// Fallback: simple range
				out, err := run("rev-list", target+"..HEAD")
				if err == nil && len(bytes.TrimSpace(out)) > 0 {
					res.Ahead, res.HasWork = true, true
					res.Reasons = append(res.Reasons, fmt.Sprintf("commits ahead of %s", target))
				}
			}
		}
	}

	return res
}

// RepoDirty is the simple boolean version for existing call sites.
// Returns true iff any work exists (changes or commits not in base).
func RepoDirty(ctx context.Context, baseBranch, workDir string, executor GitExecutor) bool {
	return CheckWorkDone(ctx, baseBranch, workDir, executor).HasWork
}

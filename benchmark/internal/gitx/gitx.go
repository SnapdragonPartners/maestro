// Package gitx wraps the git CLI operations the engine and test targets
// need: clone/checkout/pin verification, ref resolution, ancestry checks,
// working-tree snapshots, and remote ref listing/cleanup.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Run executes git with args in dir and returns trimmed combined output.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, text)
	}
	return text, nil
}

// Clone clones url into dir.
func Clone(ctx context.Context, url, dir string) error {
	_, err := Run(ctx, ".", "clone", "--quiet", url, dir)
	return err
}

// Checkout checks out ref (detached for commits) in dir.
func Checkout(ctx context.Context, dir, ref string) error {
	_, err := Run(ctx, dir, "checkout", "--quiet", "--detach", ref)
	return err
}

// Head returns the current commit hash of dir.
func Head(ctx context.Context, dir string) (string, error) {
	return Run(ctx, dir, "rev-parse", "HEAD")
}

// IsAncestor reports whether ancestor is an ancestor of descendant in dir.
func IsAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", ancestor, descendant)
	cmd.Dir = dir
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor: %w", err)
}

// ResolveRemoteBranch fetches branch from origin into dir and returns the
// commit it points at.
func ResolveRemoteBranch(ctx context.Context, dir, branch string) (string, error) {
	if _, err := Run(ctx, dir, "fetch", "--quiet", "origin", branch); err != nil {
		return "", err
	}
	return Run(ctx, dir, "rev-parse", "FETCH_HEAD")
}

// CleanUntracked removes untracked and ignored files from dir so a
// detached validation checkout sees exactly the solution commit and
// nothing the target left lying around.
func CleanUntracked(ctx context.Context, dir string) error {
	_, err := Run(ctx, dir, "clean", "-fdx", "--quiet")
	return err
}

// SnapshotCommit stages the entire working tree of dir — including ignored
// files (--force) — and commits it (allowing empty), returning the
// resulting commit hash. Used to bind in-place solutions immutably:
// anything less than the full tree would let validators see state not
// represented by the recorded solution commit.
func SnapshotCommit(ctx context.Context, dir string) (string, error) {
	if _, err := Run(ctx, dir, "add", "-A", "--force"); err != nil {
		return "", err
	}
	_, err := Run(ctx, dir,
		"-c", "user.name=golden-runner", "-c", "user.email=runner@invalid",
		"commit", "--quiet", "--allow-empty", "-m", "golden-runner solution snapshot")
	if err != nil {
		return "", err
	}
	return Head(ctx, dir)
}

// LsRemoteHeads lists remote head refs of repoURL matching pattern.
func LsRemoteHeads(ctx context.Context, dir, repoURL, pattern string) ([]string, error) {
	out, err := Run(ctx, dir, "ls-remote", "--heads", repoURL, pattern)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	lines := strings.Split(out, "\n")
	refs := make([]string, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			refs = append(refs, fields[1])
		}
	}
	return refs, nil
}

// DiffNames returns the file paths changed between two commits in dir.
func DiffNames(ctx context.Context, dir, from, to string) ([]string, error) {
	out, err := Run(ctx, dir, "diff", "--name-only", from+".."+to)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// CommitAllOnBranch creates branch at HEAD, stages everything, commits, and
// returns the commit hash (test-target helper).
func CommitAllOnBranch(ctx context.Context, dir, branch, message string) (string, error) {
	if _, err := Run(ctx, dir, "checkout", "--quiet", "-b", branch); err != nil {
		return "", err
	}
	if _, err := Run(ctx, dir, "add", "-A"); err != nil {
		return "", err
	}
	_, err := Run(ctx, dir,
		"-c", "user.name=golden-runner", "-c", "user.email=runner@invalid",
		"commit", "--quiet", "--allow-empty", "-m", message)
	if err != nil {
		return "", err
	}
	return Head(ctx, dir)
}

// Push pushes refspec to origin from dir.
func Push(ctx context.Context, dir, refspec string) error {
	_, err := Run(ctx, dir, "push", "--quiet", "origin", refspec)
	return err
}

// DeleteRemoteBranch removes branch from origin; missing branches are not
// an error.
func DeleteRemoteBranch(ctx context.Context, dir, branch string) error {
	_, err := Run(ctx, dir, "push", "--quiet", "origin", "--delete", branch)
	if err != nil && strings.Contains(err.Error(), "remote ref does not exist") {
		return nil
	}
	return err
}

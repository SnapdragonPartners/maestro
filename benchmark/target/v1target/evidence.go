package v1target

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/SnapdragonPartners/maestro/benchmark/internal/gitx"
	"github.com/SnapdragonPartners/maestro/benchmark/runrecord"
)

// exportEvidence copies everything durable into the evidence dir before any
// teardown: PR metadata, the diff, a WAL-consistent DB snapshot, and v1's
// log tree. Export failures degrade (fewer pointers), never abort — the
// evidence that exists is still evidence.
func (r *v1Run) exportEvidence(ctx context.Context, projectDir, dbPath string, prs []prInfo) []runrecord.EvidencePointer {
	var out []runrecord.EvidencePointer
	dir := r.spec.EvidenceDir

	if len(prs) > 0 {
		path := filepath.Join(dir, "pr.json")
		if raw, err := json.MarshalIndent(prs, "", "  "); err == nil {
			if err := os.WriteFile(path, raw, 0o644); err == nil {
				out = append(out, runrecord.EvidencePointer{Kind: "pr", Location: path})
			}
		}
	}
	if diffPath, ok := r.exportDiff(ctx, dir); ok {
		out = append(out, runrecord.EvidencePointer{Kind: "diff", Location: diffPath})
	}
	if _, err := os.Stat(dbPath); err == nil {
		snap := filepath.Join(dir, "maestro.db")
		if err := snapshotDB(ctx, dbPath, snap); err == nil {
			out = append(out, runrecord.EvidencePointer{Kind: "db", Location: snap})
		}
	}
	if logs := filepath.Join(projectDir, ".maestro", "logs"); dirExists(logs) {
		dest := filepath.Join(dir, "logs")
		if err := copyTree(logs, dest); err == nil {
			out = append(out, runrecord.EvidencePointer{Kind: "log", Location: dest})
		}
	}
	out = append(out, runrecord.EvidencePointer{Kind: "log", Location: filepath.Join(dir, "maestro-launch.log")})
	return out
}

// exportDiff writes the pin-to-solution diff. The solution branch must be
// imported first; when it is absent the diff is skipped.
func (r *v1Run) exportDiff(ctx context.Context, dir string) (string, bool) {
	branch := r.spec.BranchNamespace + solutionLeaf
	head, err := gitx.Run(ctx, r.spec.WorkspaceDir, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		return "", false
	}
	diff, err := gitx.Run(ctx, r.spec.WorkspaceDir, "diff", r.spec.Story.Fixture.Commit+".."+head)
	if err != nil {
		return "", false
	}
	path := filepath.Join(dir, "diff.patch")
	if err := os.WriteFile(path, []byte(diff+"\n"), 0o644); err != nil {
		return "", false
	}
	return path, true
}

// importSolution fetches the throwaway repo's merged main into the engine
// workspace as the run's local solution branch (the engine resolves local
// refs first).
func (r *v1Run) importSolution(ctx context.Context) (string, error) {
	branch := r.spec.BranchNamespace + solutionLeaf
	if _, err := gitx.Run(ctx, r.spec.WorkspaceDir, "fetch", "--quiet", r.authURL,
		"refs/heads/main:refs/heads/"+branch); err != nil {
		return "", fmt.Errorf("import solution: %w", err)
	}
	return branch, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// copyTree copies a directory tree (regular files only).
func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error { //nolint:wrapcheck // walk callback
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("relativize %s: %w", path, err)
		}
		targetPath := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", path, err)
		}
		defer in.Close() //nolint:errcheck // read side
		outFile, err := os.Create(targetPath)
		if err != nil {
			return fmt.Errorf("create %s: %w", targetPath, err)
		}
		if _, err := io.Copy(outFile, in); err != nil {
			_ = outFile.Close() //nolint:errcheck // error path
			return fmt.Errorf("copy %s: %w", path, err)
		}
		return outFile.Close()
	})
}

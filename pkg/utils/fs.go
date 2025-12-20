package utils

import (
	"fmt"
	"os"
	"path/filepath"
)

// CleanDirectoryContents removes all contents of a directory without removing the directory itself.
// This preserves the directory inode, which is critical for Docker bind mounts on macOS.
// When a directory is deleted and recreated, Docker bind mounts become stale because they
// track the original inode.
func CleanDirectoryContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// Directory doesn't exist yet, nothing to clean
			return nil
		}
		return fmt.Errorf("failed to read directory: %w", err)
	}

	for _, entry := range entries {
		entryPath := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(entryPath); err != nil {
			return fmt.Errorf("failed to remove %s: %w", entryPath, err)
		}
	}

	return nil
}

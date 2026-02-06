package utils

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCleanDirectoryContents_RemovesContents(t *testing.T) {
	dir := t.TempDir()

	// Create files and subdirectories
	if err := os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file2.go"), []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "nested.txt"), []byte("nested"), 0644); err != nil {
		t.Fatal(err)
	}

	// Verify contents exist
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries before cleanup, got %d", len(entries))
	}

	// Clean directory contents
	cleanErr := CleanDirectoryContents(dir)
	if cleanErr != nil {
		t.Fatalf("CleanDirectoryContents failed: %v", cleanErr)
	}

	// Directory should still exist
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("directory should still exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("path should be a directory")
	}

	// Contents should be empty
	entries, err = os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after cleanup, got %d", len(entries))
	}
}

func TestCleanDirectoryContents_PreservesInode(t *testing.T) {
	dir := t.TempDir()

	// Create some content
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	// Record inode before cleanup
	infoBefore, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	statBefore, ok := infoBefore.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("syscall.Stat_t not available on this platform")
	}
	inodeBefore := statBefore.Ino

	// Clean directory contents
	cleanErr := CleanDirectoryContents(dir)
	if cleanErr != nil {
		t.Fatalf("CleanDirectoryContents failed: %v", cleanErr)
	}

	// Record inode after cleanup
	infoAfter, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	statAfter, ok := infoAfter.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("syscall.Stat_t should be available (worked before)")
	}
	inodeAfter := statAfter.Ino

	// Inode MUST be preserved (critical for Docker bind mounts on macOS)
	if inodeBefore != inodeAfter {
		t.Errorf("directory inode changed: before=%d after=%d (breaks Docker bind mounts)", inodeBefore, inodeAfter)
	}
}

func TestCleanDirectoryContents_NonExistentDir(t *testing.T) {
	// Calling on a non-existent directory should not error
	err := CleanDirectoryContents("/tmp/nonexistent-dir-for-test-" + t.Name())
	if err != nil {
		t.Errorf("expected no error for non-existent directory, got: %v", err)
	}
}

func TestCleanDirectoryContents_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	// Empty directory should clean successfully
	err := CleanDirectoryContents(dir)
	if err != nil {
		t.Errorf("expected no error for empty directory, got: %v", err)
	}

	// Should still exist
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("directory should still exist: %v", err)
	}
}

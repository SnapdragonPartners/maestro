// Package workspace provides workspace verification and validation functionality.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// MaxFileSize is the hard limit for file sizes (100MB - GitHub's push limit).
	MaxFileSize = 100 * 1024 * 1024 // 100MB

	// WarnFileSize is the soft warning threshold (50MB).
	WarnFileSize = 50 * 1024 * 1024 // 50MB
)

// BinarySizeCheckResult contains the results of binary size checking.
//
//nolint:govet // Logical field grouping preferred over memory optimization
type BinarySizeCheckResult struct {
	LargeFiles    []LargeFileInfo `json:"large_files"`    // Files ≥ 50MB
	OversizeFiles []LargeFileInfo `json:"oversize_files"` // Files ≥ 100MB (violations)
	TotalSize     int64           `json:"total_size"`     // Total size of all checked files
	CheckedCount  int             `json:"checked_count"`  // Number of files checked
}

// LargeFileInfo contains information about a large file.
type LargeFileInfo struct {
	Path string `json:"path"` // Relative path from project root
	Size int64  `json:"size"` // Size in bytes
}

// CheckBinarySizes performs binary size checking on a workspace directory.
// It scans for files that exceed size thresholds and reports violations.
func CheckBinarySizes(projectDir string) (*BinarySizeCheckResult, error) {
	result := &BinarySizeCheckResult{
		LargeFiles:    []LargeFileInfo{},
		OversizeFiles: []LargeFileInfo{},
	}

	// Walk the directory tree, but skip certain directories to avoid false positives
	err := filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Skip files we can't read but continue processing
			return filepath.SkipDir // Skip the problematic directory/file
		}

		// Skip directories
		if info.IsDir() {
			// Skip common directories that shouldn't be checked
			dirName := info.Name()
			if shouldSkipDirectory(dirName) {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip certain file types that are expected to be large
		if shouldSkipFile(path) {
			return nil
		}

		result.CheckedCount++
		result.TotalSize += info.Size()

		// Get relative path for reporting
		relPath, pathErr := filepath.Rel(projectDir, path)
		if pathErr != nil {
			relPath = path // Fallback to absolute path
		}

		fileInfo := LargeFileInfo{
			Path: relPath,
			Size: info.Size(),
		}

		// Check for oversize files (hard fail)
		if info.Size() >= MaxFileSize {
			result.OversizeFiles = append(result.OversizeFiles, fileInfo)
		} else if info.Size() >= WarnFileSize {
			// Check for large files (warning)
			result.LargeFiles = append(result.LargeFiles, fileInfo)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk directory: %w", err)
	}

	return result, nil
}

// shouldSkipDirectory determines if a directory should be skipped during size checking.
func shouldSkipDirectory(dirName string) bool {
	skipDirs := []string{
		".git",
		".mirrors",
		"node_modules",
		"vendor",
		"target",        // Rust build directory
		"build",         // Generic build directory
		"dist",          // Distribution directory
		"__pycache__",   // Python cache
		".pytest_cache", // Pytest cache
		".venv",         // Python virtual environment
		"venv",          // Python virtual environment
		".env",          // Environment directory
		"tmp",           // Temporary files
		"temp",          // Temporary files
		"logs",          // Log files
		".maestro",      // Maestro configuration (should be small anyway)
		"bin",           // Compiled binaries (expected to be large but necessary)
		".docker",       // Docker build context
		"docker-build",  // Docker build artifacts
	}

	for _, skip := range skipDirs {
		if dirName == skip {
			return true
		}
	}

	// Skip hidden directories that start with dot (except specific ones we check)
	if strings.HasPrefix(dirName, ".") && dirName != ".github" {
		return true
	}

	return false
}

// shouldSkipFile determines if a file should be skipped during size checking.
func shouldSkipFile(filePath string) bool {
	fileName := filepath.Base(filePath)
	ext := strings.ToLower(filepath.Ext(fileName))

	// Skip common large file types that are expected/legitimate
	skipExtensions := []string{
		".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", // Archives
		".iso", ".img", ".dmg", // Disk images
		".mp4", ".avi", ".mov", ".mkv", // Videos
		".mp3", ".wav", ".flac", // Audio
		".jpg", ".jpeg", ".png", ".gif", ".bmp", // Images (though large ones might still be worth flagging)
		".pdf",    // PDFs can be large legitimately
		".db",     // Database files
		".sqlite", // SQLite databases
		".log",    // Log files
	}

	for _, skipExt := range skipExtensions {
		if ext == skipExt {
			return true
		}
	}

	// Skip specific filenames
	skipFiles := []string{
		"package-lock.json", // npm lock files can be large
		"yarn.lock",         // Yarn lock files
		"go.sum",            // Go checksum files
		"Cargo.lock",        // Rust lock files
	}

	for _, skipFile := range skipFiles {
		if fileName == skipFile {
			return true
		}
	}

	return false
}

// FormatFileSize formats a file size in bytes to a human-readable string.
func FormatFileSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

// GenerateBinarySizeReport generates a human-readable report of binary size check results.
func GenerateBinarySizeReport(result *BinarySizeCheckResult) string {
	var report strings.Builder

	report.WriteString("# Binary Size Check Report\n\n")
	report.WriteString(fmt.Sprintf("**Files Checked:** %d\n", result.CheckedCount))
	report.WriteString(fmt.Sprintf("**Total Size:** %s\n\n", FormatFileSize(result.TotalSize)))

	if len(result.OversizeFiles) > 0 {
		report.WriteString("## ❌ Oversize Files (≥100MB) - **BLOCKING**\n\n")
		report.WriteString("These files exceed GitHub's push limit and must be addressed:\n\n")
		for _, file := range result.OversizeFiles {
			report.WriteString(fmt.Sprintf("- `%s` - %s\n", file.Path, FormatFileSize(file.Size)))
		}
		report.WriteString("\n**Action Required:** Move to Git LFS or remove these files.\n\n")
	}

	if len(result.LargeFiles) > 0 {
		report.WriteString("## ⚠️ Large Files (50-99MB) - **WARNING**\n\n")
		report.WriteString("Consider using Git LFS for these files:\n\n")
		for _, file := range result.LargeFiles {
			report.WriteString(fmt.Sprintf("- `%s` - %s\n", file.Path, FormatFileSize(file.Size)))
		}
		report.WriteString("\n**Recommendation:** Use `git lfs track` for these file patterns.\n\n")
	}

	if len(result.OversizeFiles) == 0 && len(result.LargeFiles) == 0 {
		report.WriteString("✅ **All files are within size limits.**\n\n")
	}

	return report.String()
}

// HasViolations returns true if there are any hard violations (oversize files).
func (r *BinarySizeCheckResult) HasViolations() bool {
	return len(r.OversizeFiles) > 0
}

// HasWarnings returns true if there are any warnings (large files).
func (r *BinarySizeCheckResult) HasWarnings() bool {
	return len(r.LargeFiles) > 0
}

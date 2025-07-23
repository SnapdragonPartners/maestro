//nolint:cyclop // Utility script, complexity acceptable
package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		fmt.Println("Usage: go run scripts/fix-godot-simple.go [--dry-run]")
		fmt.Println("Fix godot linting issues by adding periods to comments that need them.")
		fmt.Println("  --dry-run    Show what would be changed without making changes")
		return
	}

	dryRun := false
	if len(os.Args) > 1 && os.Args[1] == "--dry-run" {
		dryRun = true
		fmt.Println("DRY RUN MODE - showing what would be changed")
	}

	totalFiles := 0
	totalFixed := 0

	err := filepath.Walk(".", func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip non-Go files.
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Skip directories we don't want to process.
		if strings.Contains(path, "vendor/") ||
			strings.Contains(path, ".git/") ||
			strings.Contains(path, "node_modules/") {
			return nil
		}

		fixed, err := processFile(path, dryRun)
		if err != nil {
			log.Printf("Error processing %s: %v", path, err)
			return nil // Continue processing other files
		}

		if fixed > 0 {
			totalFiles++
			totalFixed += fixed
			if dryRun {
				fmt.Printf("Would fix %d comments in %s\n", fixed, path)
			} else {
				fmt.Printf("Fixed %d comments in %s\n", fixed, path)
			}
		}

		return nil
	})

	if err != nil {
		log.Fatal(err)
	}

	if dryRun {
		fmt.Printf("\nDry run complete: Would fix %d comments in %d files\n", totalFixed, totalFiles)
	} else {
		fmt.Printf("\nFixed %d comments in %d files\n", totalFixed, totalFiles)
	}
}

func processFile(filename string, dryRun bool) (int, error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, fmt.Errorf("failed to open file %s: %w", filename, err)
	}
	defer func() {
		_ = file.Close()
	}()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scanner error reading file %s: %w", filename, err)
	}

	fixedCount := 0
	modified := false

	for i, line := range lines {
		if fixed := fixCommentLine(line); fixed != line {
			if !dryRun {
				lines[i] = fixed
				modified = true
			}
			fixedCount++
			if dryRun {
				fmt.Printf("%s:%d: %s -> %s\n", filename, i+1, strings.TrimSpace(line), strings.TrimSpace(fixed))
			}
		}
	}

	// Write back the modified content if not in dry run mode.
	if modified && !dryRun {
		file, err := os.Create(filename)
		if err != nil {
			return 0, fmt.Errorf("failed to create file %s: %w", filename, err)
		}
		defer func() {
			_ = file.Close()
		}()

		writer := bufio.NewWriter(file)
		for i, line := range lines {
			if i > 0 {
				_, _ = writer.WriteString("\n")
			}
			_, _ = writer.WriteString(line)
		}
		_ = writer.Flush()
	}

	return fixedCount, nil
}

func fixCommentLine(line string) string {
	// Regex to match comments that start with // and capture the prefix and content.
	commentRegex := regexp.MustCompile(`^(\s*//\s*)(.+)$`)
	matches := commentRegex.FindStringSubmatch(line)

	if matches == nil {
		return line // Not a comment line
	}

	prefix := matches[1]  // "// " or similar with any indentation
	content := matches[2] // The actual comment content

	// Trim any trailing whitespace from content.
	content = strings.TrimRightFunc(content, func(r rune) bool {
		return r == ' ' || r == '\t'
	})

	// Skip if already ends with punctuation.
	if regexp.MustCompile(`[.!?:;]$`).MatchString(content) {
		return line
	}

	// Skip special comments that shouldn't get periods.
	specialPatterns := []string{
		`^\s*//\s*\+build`,      // Build tags
		`^\s*//go:`,             // Go directives
		`^\s*//nolint`,          // Lint directives
		`^\s*//\s*TODO`,         // TODO comments
		`^\s*//\s*FIXME`,        // FIXME comments
		`^\s*//\s*NOTE`,         // NOTE comments
		`^\s*//\s*XXX`,          // XXX comments
		`^\s*//\s*HACK`,         // HACK comments
		`^\s*//\s*BUG`,          // BUG comments
		`^\s*//\s*-+\s*$`,       // Separator lines with dashes
		`^\s*//\s*=+\s*$`,       // Separator lines with equals
		`^\s*//\s*\*+\s*$`,      // Separator lines with stars
		`^\s*//\s*https?://`,    // URLs
		`^\s*//\s*[A-Z_]+:\s*$`, // Labels like "WARNING:", "ERROR:" that end with colon
	}

	for _, pattern := range specialPatterns {
		if regexp.MustCompile(pattern).MatchString(line) {
			return line
		}
	}

	// Skip very short comments (likely abbreviations, single words, etc.)
	trimmedContent := strings.TrimSpace(content)
	if len(trimmedContent) < 8 {
		return line
	}

	// Skip comments that look like code or function signatures.
	if strings.Contains(content, "(") && strings.Contains(content, ")") ||
		strings.Contains(content, "{") && strings.Contains(content, "}") ||
		strings.Contains(content, "=") ||
		strings.HasPrefix(trimmedContent, "func ") ||
		strings.HasPrefix(trimmedContent, "var ") ||
		strings.HasPrefix(trimmedContent, "const ") ||
		strings.HasPrefix(trimmedContent, "type ") {
		return line
	}

	// Add period to the comment.
	return prefix + content + "."
}

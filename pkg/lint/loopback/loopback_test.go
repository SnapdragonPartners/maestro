package loopback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanEnvFile(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	linter := NewLinter(tmpDir)

	tests := []struct {
		name           string
		content        string
		expectFindings int
		expectPatterns []string
	}{
		{
			name:           "localhost in DATABASE_URL",
			content:        "DATABASE_URL=postgres://user:pass@localhost:5432/db\n",
			expectFindings: 1,
			expectPatterns: []string{"localhost"},
		},
		{
			name:           "127.0.0.1 in REDIS_URL",
			content:        "REDIS_URL=redis://127.0.0.1:6379\n",
			expectFindings: 1,
			expectPatterns: []string{"127.0.0.1"},
		},
		{
			name:           "IPv6 loopback",
			content:        "API_HOST=::1\n",
			expectFindings: 1,
			expectPatterns: []string{"::1"},
		},
		{
			name:           "multiple loopback references",
			content:        "DB_HOST=localhost\nCACHE_HOST=127.0.0.1\n",
			expectFindings: 2,
			expectPatterns: []string{"localhost", "127.0.0.1"},
		},
		{
			name:           "nolint suppression",
			content:        "DEBUG_HOST=localhost # nolint:localhost (for local debugging)\n",
			expectFindings: 0,
			expectPatterns: nil,
		},
		{
			name:           "nolint without space before hash - should still flag",
			content:        "DEBUG_HOST=localhost#nolint:localhost\n",
			expectFindings: 1,
			expectPatterns: []string{"localhost"},
		},
		{
			name:           "compose service name - no finding",
			content:        "DATABASE_URL=postgres://user:pass@db:5432/mydb\n",
			expectFindings: 0,
			expectPatterns: nil,
		},
		{
			name:           "full line comment - ignored",
			content:        "# This is a comment about localhost\nDB_HOST=postgres\n",
			expectFindings: 0,
			expectPatterns: nil,
		},
		{
			name:           "blank lines and comments",
			content:        "\n# comment\n\nDB_HOST=mydb\n",
			expectFindings: 0,
			expectPatterns: nil,
		},
		{
			name:           "non-assignment line - ignored",
			content:        "This line has localhost but no assignment\nDB_HOST=db\n",
			expectFindings: 0,
			expectPatterns: nil,
		},
		{
			name:           "0.0.0.0 is NOT flagged (common bind address)",
			content:        "BIND_ADDR=0.0.0.0:8080\n",
			expectFindings: 0,
			expectPatterns: nil,
		},
		{
			name:           "127.0.1.1 is flagged",
			content:        "HOST=127.0.1.1\n",
			expectFindings: 1,
			expectPatterns: []string{"127.0.1.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Write test .env file
			envPath := filepath.Join(tmpDir, ".env")
			if err := os.WriteFile(envPath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write test file: %v", err)
			}

			findings, err := linter.scanEnvFile(envPath, ".env")
			if err != nil {
				t.Fatalf("scanEnvFile failed: %v", err)
			}

			if len(findings) != tt.expectFindings {
				t.Errorf("expected %d findings, got %d", tt.expectFindings, len(findings))
				for _, f := range findings {
					t.Logf("  finding: %s:%d pattern=%s", f.File, f.Line, f.Pattern)
				}
			}

			// Check expected patterns
			for i, pattern := range tt.expectPatterns {
				if i < len(findings) && findings[i].Pattern != pattern {
					t.Errorf("finding %d: expected pattern %q, got %q", i, pattern, findings[i].Pattern)
				}
			}
		})
	}
}

func TestScanComposeFile(t *testing.T) {
	tmpDir := t.TempDir()
	linter := NewLinter(tmpDir)

	tests := []struct {
		name           string
		content        string
		expectFindings int
	}{
		{
			name: "localhost in environment map",
			content: `
services:
  app:
    image: myapp
    environment:
      DATABASE_URL: postgres://user:pass@localhost:5432/db
`,
			expectFindings: 1,
		},
		{
			name: "service name in environment - no finding",
			content: `
services:
  app:
    image: myapp
    environment:
      DATABASE_URL: postgres://user:pass@db:5432/mydb
  db:
    image: postgres
`,
			expectFindings: 0,
		},
		{
			name: "localhost in environment list format",
			content: `
services:
  app:
    image: myapp
    environment:
      - DATABASE_URL=postgres://user:pass@localhost:5432/db
`,
			expectFindings: 1,
		},
		{
			name: "multiple services with loopback",
			content: `
services:
  app:
    environment:
      DB_HOST: 127.0.0.1
  worker:
    environment:
      CACHE_HOST: localhost
`,
			expectFindings: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			composePath := filepath.Join(tmpDir, "compose.yml")
			if err := os.WriteFile(composePath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write test file: %v", err)
			}

			findings, err := linter.scanComposeFile(composePath, "compose.yml")
			if err != nil {
				t.Fatalf("scanComposeFile failed: %v", err)
			}

			if len(findings) != tt.expectFindings {
				t.Errorf("expected %d findings, got %d", tt.expectFindings, len(findings))
				for _, f := range findings {
					t.Logf("  finding: %s pattern=%s content=%s", f.File, f.Pattern, f.Content)
				}
			}
		})
	}
}

func TestExtractComposeServices(t *testing.T) {
	tmpDir := t.TempDir()
	linter := NewLinter(tmpDir)

	content := `
services:
  app:
    image: myapp
  db:
    image: postgres
  redis:
    image: redis
`

	composePath := filepath.Join(tmpDir, "compose.yml")
	if err := os.WriteFile(composePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	services, err := linter.extractComposeServices(composePath)
	if err != nil {
		t.Fatalf("extractComposeServices failed: %v", err)
	}

	if len(services) != 3 {
		t.Errorf("expected 3 services, got %d: %v", len(services), services)
	}

	// Check that all expected services are present (order may vary)
	expected := map[string]bool{"app": false, "db": false, "redis": false}
	for _, s := range services {
		if _, ok := expected[s]; ok {
			expected[s] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("expected service %q not found", name)
		}
	}
}

func TestIsEnvFile(t *testing.T) {
	linter := NewLinter("/tmp")

	tests := []struct {
		path   string
		expect bool
	}{
		{".env", true},
		{".env.local", true},
		{".env.production", true},
		{"app.env", true},
		{"config.env", true},
		{"Dockerfile", false},
		{"main.go", false},
		{"README.md", false},
		{"env", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := linter.isEnvFile(tt.path)
			if result != tt.expect {
				t.Errorf("isEnvFile(%q) = %v, want %v", tt.path, result, tt.expect)
			}
		})
	}
}

func TestIsComposeFile(t *testing.T) {
	linter := NewLinter("/tmp")

	tests := []struct {
		path   string
		expect bool
	}{
		{"compose.yml", true},
		{"compose.yaml", true},
		{"docker-compose.yml", true},
		{"docker-compose.yaml", true},
		{"docker-compose.override.yml", true},
		{"docker-compose.dev.yml", true},
		{".maestro/compose.yml", true},
		{".maestro/compose.yaml", true},
		{"Dockerfile", false},
		{"main.go", false},
		{".env", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := linter.isComposeFile(tt.path)
			if result != tt.expect {
				t.Errorf("isComposeFile(%q) = %v, want %v", tt.path, result, tt.expect)
			}
		})
	}
}

func TestScanChangedFiles(t *testing.T) {
	tmpDir := t.TempDir()
	linter := NewLinter(tmpDir)

	// Create test files
	envContent := "DATABASE_URL=postgres://user:pass@localhost:5432/db\n"
	envPath := filepath.Join(tmpDir, ".env")
	if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
		t.Fatalf("failed to write .env: %v", err)
	}

	composeContent := `
services:
  db:
    image: postgres
  app:
    environment:
      REDIS_URL: redis://127.0.0.1:6379
`
	composePath := filepath.Join(tmpDir, "compose.yml")
	if err := os.WriteFile(composePath, []byte(composeContent), 0644); err != nil {
		t.Fatalf("failed to write compose.yml: %v", err)
	}

	// Scan both files
	changedFiles := []string{".env", "compose.yml"}
	result, err := linter.ScanChangedFiles(changedFiles)
	if err != nil {
		t.Fatalf("ScanChangedFiles failed: %v", err)
	}

	// Should find 2 issues: localhost in .env, 127.0.0.1 in compose
	if len(result.Findings) != 2 {
		t.Errorf("expected 2 findings, got %d", len(result.Findings))
		for _, f := range result.Findings {
			t.Logf("  finding: %s:%d pattern=%s", f.File, f.Line, f.Pattern)
		}
	}

	// Should extract compose services
	if len(result.ComposeServices) < 2 {
		t.Errorf("expected at least 2 compose services, got %d: %v", len(result.ComposeServices), result.ComposeServices)
	}

	// Should list scanned files
	if len(result.ScannedFiles) != 2 {
		t.Errorf("expected 2 scanned files, got %d: %v", len(result.ScannedFiles), result.ScannedFiles)
	}
}

func TestResultFormatError(t *testing.T) {
	result := &Result{
		Findings: []Finding{
			{File: ".env", Line: 3, Content: "DB_HOST=localhost", Pattern: "localhost"},
		},
		ComposeServices: []string{"db", "redis", "app"},
	}

	formatted := result.FormatError()

	// Check that error message contains key information
	if !strings.Contains(formatted, ".env:3") {
		t.Error("formatted error should contain file:line")
	}
	if !strings.Contains(formatted, "localhost") {
		t.Error("formatted error should contain the pattern")
	}
	if !strings.Contains(formatted, "db, redis, app") {
		t.Error("formatted error should list compose services")
	}
	if !strings.Contains(formatted, "nolint:localhost") {
		t.Error("formatted error should mention nolint suppression")
	}
}

func TestResultHasFindings(t *testing.T) {
	empty := &Result{}
	if empty.HasFindings() {
		t.Error("empty result should not have findings")
	}

	withFindings := &Result{
		Findings: []Finding{{File: ".env", Line: 1, Pattern: "localhost"}},
	}
	if !withFindings.HasFindings() {
		t.Error("result with findings should report HasFindings=true")
	}
}

func TestScanChangedFilesNoRelevantFiles(t *testing.T) {
	tmpDir := t.TempDir()
	linter := NewLinter(tmpDir)

	// No .env or compose files in the list
	changedFiles := []string{"main.go", "README.md", "Dockerfile"}
	result, err := linter.ScanChangedFiles(changedFiles)
	if err != nil {
		t.Fatalf("ScanChangedFiles failed: %v", err)
	}

	if result.HasFindings() {
		t.Error("should have no findings when no relevant files changed")
	}

	if len(result.ScannedFiles) != 0 {
		t.Errorf("should have scanned 0 files, got %d", len(result.ScannedFiles))
	}
}

package webui

import (
	"archive/zip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/pkg/config"
	"orchestrator/pkg/issueservice"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/version"
)

func TestComputeIssueHMAC(t *testing.T) {
	// Known test vector: HMAC-SHA256("test-installation-id", "dev-issue-key")
	result := issueservice.ComputeHMACWithKey("test-installation-id", "dev-issue-key")
	if result == "" {
		t.Fatal("HMAC should not be empty")
	}
	// Verify it's a valid hex string of expected length (SHA-256 = 64 hex chars)
	if len(result) != 64 {
		t.Errorf("HMAC hex length = %d, want 64", len(result))
	}
	// Verify deterministic
	result2 := issueservice.ComputeHMACWithKey("test-installation-id", "dev-issue-key")
	if result != result2 {
		t.Error("HMAC should be deterministic")
	}
	// Different input should produce different output
	result3 := issueservice.ComputeHMACWithKey("different-id", "dev-issue-key")
	if result == result3 {
		t.Error("Different inputs should produce different HMACs")
	}
}

func TestHandleIssueSubmit_Validation(t *testing.T) {
	server := &Server{
		logger: logx.NewLogger("webui-test"),
	}

	tests := []struct {
		name       string
		method     string
		body       string
		wantStatus int
	}{
		{
			name:       "wrong method",
			method:     http.MethodGet,
			body:       "",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "empty description",
			method:     http.MethodPost,
			body:       `{"description":"","include_diagnostics":false}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "description too long",
			method:     http.MethodPost,
			body:       `{"description":"` + strings.Repeat("x", 10001) + `","include_diagnostics":false}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid JSON",
			method:     http.MethodPost,
			body:       `{invalid`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/issues/submit", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.handleIssueSubmit(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d; body: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

func TestHandleIssueSubmit_HappyPath(t *testing.T) {
	// Set up config with installation ID
	tmpDir := t.TempDir()
	maestroDir := filepath.Join(tmpDir, ".maestro")
	os.MkdirAll(maestroDir, 0o755)
	configJSON := `{"schema_version":"1.0","installation_id":"test-uuid-1234"}`
	os.WriteFile(filepath.Join(maestroDir, "config.json"), []byte(configJSON), 0o644)
	if err := config.LoadConfig(tmpDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Create a fake issue service
	var receivedFields map[string]string
	fakeService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse multipart form
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		receivedFields = make(map[string]string)
		for k, v := range r.MultipartForm.Value {
			if len(v) > 0 {
				receivedFields[k] = v[0]
			}
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"id": "MAE-0001", "message": "Issue submitted successfully."})
	}))
	defer fakeService.Close()

	// Override issue service URL
	t.Setenv("MAESTRO_ISSUE_SERVICE_URL", fakeService.URL)

	server := &Server{
		logger:          logx.NewLogger("webui-test"),
		workDir:         tmpDir,
		issueHTTPClient: fakeService.Client(),
	}

	body := `{"description":"Something broke","include_diagnostics":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleIssueSubmit(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}

	// Verify the fields sent to the service
	if receivedFields["installation_id"] != "test-uuid-1234" {
		t.Errorf("installation_id = %q, want %q", receivedFields["installation_id"], "test-uuid-1234")
	}
	if receivedFields["description"] != "Something broke" {
		t.Errorf("description = %q, want %q", receivedFields["description"], "Something broke")
	}
	expectedSig := issueservice.ComputeHMACWithKey("test-uuid-1234", version.IssueReportingKey)
	if receivedFields["signature"] != expectedSig {
		t.Errorf("signature = %q, want %q", receivedFields["signature"], expectedSig)
	}
	if receivedFields["maestro_version"] != version.Version {
		t.Errorf("maestro_version = %q, want %q", receivedFields["maestro_version"], version.Version)
	}
}

func TestHandleIssueSubmit_RateLimit(t *testing.T) {
	// Set up config
	tmpDir := t.TempDir()
	maestroDir := filepath.Join(tmpDir, ".maestro")
	os.MkdirAll(maestroDir, 0o755)
	configJSON := `{"schema_version":"1.0","installation_id":"test-uuid-rate"}`
	os.WriteFile(filepath.Join(maestroDir, "config.json"), []byte(configJSON), 0o644)
	if err := config.LoadConfig(tmpDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Fake service returns 429
	fakeService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{"error": "rate limit exceeded"})
	}))
	defer fakeService.Close()
	t.Setenv("MAESTRO_ISSUE_SERVICE_URL", fakeService.URL)

	server := &Server{
		logger:          logx.NewLogger("webui-test"),
		workDir:         tmpDir,
		issueHTTPClient: fakeService.Client(),
	}

	body := `{"description":"rate limit test","include_diagnostics":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/issues/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleIssueSubmit(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429; body: %s", w.Code, w.Body.String())
	}
}

func TestBuildDiagnosticsZip(t *testing.T) {
	// Create a realistic directory structure
	workDir := t.TempDir()
	maestroDir := filepath.Join(workDir, ".maestro")
	logsDir := filepath.Join(maestroDir, "logs")
	os.MkdirAll(logsDir, 0o755)

	// Create test files
	os.WriteFile(filepath.Join(maestroDir, "config.json"), []byte(`{"test":"config"}`), 0o644)
	os.WriteFile(filepath.Join(maestroDir, "maestro.db"), []byte("fake-db-content"), 0o644)
	os.WriteFile(filepath.Join(maestroDir, "maestro.db-wal"), []byte("wal-content"), 0o644)
	os.WriteFile(filepath.Join(logsDir, "maestro.log"), []byte("log line 1\nlog line 2\n"), 0o644)

	// Files that should NOT appear in the zip
	os.WriteFile(filepath.Join(maestroDir, "secrets.json.enc"), []byte("secret!"), 0o644)
	os.WriteFile(filepath.Join(maestroDir, ".password-verifier.json"), []byte("verifier!"), 0o644)

	zipPath, err := buildDiagnosticsZip(workDir, "test-install-id", "test issue description")
	if err != nil {
		t.Fatalf("buildDiagnosticsZip: %v", err)
	}
	defer os.Remove(zipPath)

	// Open and inspect the zip
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer reader.Close()

	fileNames := make(map[string]bool)
	for _, f := range reader.File {
		fileNames[f.Name] = true
	}

	// Verify required files are present
	requiredFiles := []string{"config.json", "maestro.db", "logs/maestro.log", "manifest.json"}
	for _, name := range requiredFiles {
		if !fileNames[name] {
			t.Errorf("expected file %q in zip, not found", name)
		}
	}

	// WAL/SHM files are optional (checkpoint may consume them)
	// but if present, they should be included

	// Verify excluded files are NOT present
	excludedFiles := []string{"secrets.json.enc", ".password-verifier.json"}
	for _, name := range excludedFiles {
		if fileNames[name] {
			t.Errorf("file %q should NOT be in zip", name)
		}
	}

	// Verify manifest content
	for _, f := range reader.File {
		if f.Name == "manifest.json" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open manifest: %v", err)
			}
			data, _ := io.ReadAll(rc)
			rc.Close()

			var manifest diagnosticsManifest
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatalf("unmarshal manifest: %v", err)
			}
			if manifest.InstallationID != "test-install-id" {
				t.Errorf("manifest installation_id = %q, want %q", manifest.InstallationID, "test-install-id")
			}
			if manifest.Description != "test issue description" {
				t.Errorf("manifest description = %q, want %q", manifest.Description, "test issue description")
			}
			if manifest.MaestroVersion != version.Version {
				t.Errorf("manifest version = %q, want %q", manifest.MaestroVersion, version.Version)
			}
			if len(manifest.Files) < 3 {
				t.Errorf("manifest has %d files, want at least 3", len(manifest.Files))
			}
		}
	}
}

func TestEnsureInstallationID(t *testing.T) {
	tmpDir := t.TempDir()
	maestroDir := filepath.Join(tmpDir, ".maestro")
	os.MkdirAll(maestroDir, 0o755)
	os.WriteFile(filepath.Join(maestroDir, "config.json"), []byte(`{"schema_version":"1.0"}`), 0o644)

	if err := config.LoadConfig(tmpDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// First call should generate
	if err := config.EnsureInstallationID(); err != nil {
		t.Fatalf("EnsureInstallationID: %v", err)
	}

	cfg, err := config.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg.InstallationID == "" {
		t.Fatal("InstallationID should be set after EnsureInstallationID")
	}

	firstID := cfg.InstallationID

	// Second call should be idempotent
	if ensureErr := config.EnsureInstallationID(); ensureErr != nil {
		t.Fatalf("EnsureInstallationID (second call): %v", ensureErr)
	}

	cfg2, _ := config.GetConfig()
	if cfg2.InstallationID != firstID {
		t.Errorf("InstallationID changed: %q -> %q", firstID, cfg2.InstallationID)
	}

	// Verify persisted to disk
	data, readErr := os.ReadFile(filepath.Join(maestroDir, "config.json"))
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if !strings.Contains(string(data), firstID) {
		t.Error("InstallationID not found in persisted config.json")
	}
}

package webui

import (
	"archive/zip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // SQLite driver for WAL checkpoint

	"orchestrator/pkg/config"
	"orchestrator/pkg/version"
)

const (
	defaultIssueServiceURL = "https://issues.maestroappfactory.ai"
	maxDescriptionLength   = 10000
	maxRequestBodyBytes    = 64 * 1024 // 64 KB — generous for a 10K-char description + JSON overhead
	maxResponseBodyBytes   = 4 * 1024  // 4 KB — issue service responses are small JSON
	issueSubmitTimeout     = 30 * time.Second
)

// getIssueServiceURL returns the issue service URL, allowing env var override.
func getIssueServiceURL() string {
	if url := os.Getenv("MAESTRO_ISSUE_SERVICE_URL"); url != "" {
		return url
	}
	return defaultIssueServiceURL
}

// issueSubmitRequest is the JSON body from the web UI.
type issueSubmitRequest struct {
	Description        string `json:"description"`
	IncludeDiagnostics bool   `json:"include_diagnostics"`
}

// handleIssueSubmit handles POST /api/issues/submit.
func (s *Server) handleIssueSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body size to prevent abuse
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	var req issueSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Description == "" {
		writeJSONError(w, "Description is required", http.StatusBadRequest)
		return
	}
	if len(req.Description) > maxDescriptionLength {
		writeJSONError(w, fmt.Sprintf("Description exceeds maximum length of %d characters", maxDescriptionLength), http.StatusBadRequest)
		return
	}

	// Read installation ID from config
	cfg, err := config.GetConfig()
	if err != nil {
		s.logger.Error("Failed to get config for issue submit: %v", err)
		writeJSONError(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	installationID := cfg.InstallationID
	if installationID == "" {
		writeJSONError(w, "Installation ID not configured", http.StatusInternalServerError)
		return
	}

	// Compute HMAC signature: HMAC-SHA256(installation_id, issue_reporting_key)
	signature := computeIssueHMAC(installationID, version.IssueReportingKey)

	// Build diagnostics zip if requested
	var diagnosticsPath string
	if req.IncludeDiagnostics {
		diagnosticsPath, err = buildDiagnosticsZip(s.workDir, installationID)
		if err != nil {
			s.logger.Error("Failed to build diagnostics zip: %v", err)
			// Non-fatal: submit without diagnostics
			diagnosticsPath = ""
		}
	}
	if diagnosticsPath != "" {
		defer func() { _ = os.Remove(diagnosticsPath) }()
	}

	// Forward to issue service
	serviceURL := getIssueServiceURL()
	s.logger.Info("Submitting issue to %s (installation_id=%s, diagnostics=%v)", serviceURL, installationID, diagnosticsPath != "")
	resp, err := s.postToIssueService(r.Context(), installationID, signature, req.Description, diagnosticsPath)
	if err != nil {
		s.logger.Error("Failed to post issue to service: %v", err)
		writeJSONError(w, "Failed to submit issue: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Forward the response status and body (limited read to prevent memory issues)
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if readErr != nil {
		s.logger.Error("Failed to read issue service response: %v", readErr)
		writeJSONError(w, "Failed to read issue service response", http.StatusInternalServerError)
		return
	}
	s.logger.Debug("Issue service responded: status=%d", resp.StatusCode)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody) //nolint:errcheck
}

// computeIssueHMAC computes hex(HMAC-SHA256(message, key)) where key is the shared secret.
func computeIssueHMAC(message, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

// postToIssueService sends the issue as multipart/form-data to the issue service.
func (s *Server) postToIssueService(ctx context.Context, installationID, signature, description, diagnosticsPath string) (*http.Response, error) {
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Write multipart form data in a goroutine
	errCh := make(chan error, 1)
	go func() {
		var writeErr error
		defer func() {
			// Close writer first (finalizes multipart boundary), then pipe
			_ = writer.Close()
			if writeErr != nil {
				_ = pw.CloseWithError(writeErr)
			} else {
				_ = pw.Close()
			}
		}()

		if writeErr = writer.WriteField("installation_id", installationID); writeErr != nil {
			errCh <- writeErr
			return
		}
		if writeErr = writer.WriteField("signature", signature); writeErr != nil {
			errCh <- writeErr
			return
		}
		if writeErr = writer.WriteField("maestro_version", version.Version); writeErr != nil {
			errCh <- writeErr
			return
		}
		if writeErr = writer.WriteField("description", description); writeErr != nil {
			errCh <- writeErr
			return
		}

		// Attach diagnostics zip if available
		if diagnosticsPath != "" {
			f, openErr := os.Open(diagnosticsPath)
			if openErr != nil {
				writeErr = fmt.Errorf("open diagnostics zip: %w", openErr)
				errCh <- writeErr
				return
			}
			defer func() { _ = f.Close() }()

			part, partErr := writer.CreateFormFile("diagnostics", "diagnostics.zip")
			if partErr != nil {
				writeErr = partErr
				errCh <- writeErr
				return
			}
			if _, cpErr := io.Copy(part, f); cpErr != nil {
				writeErr = fmt.Errorf("copy diagnostics: %w", cpErr)
				errCh <- writeErr
				return
			}
		}

		errCh <- nil
	}()

	serviceURL := getIssueServiceURL() + "/api/v1/issues"
	httpClient := s.getIssueHTTPClient()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serviceURL, pr)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := httpClient.Do(req)
	if err != nil {
		// Unblock the writer goroutine so it can exit
		_ = pr.CloseWithError(err)
		<-errCh
		return nil, fmt.Errorf("send request: %w", err)
	}

	// Check for write errors
	if writeErr := <-errCh; writeErr != nil {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("write multipart: %w", writeErr)
	}

	return resp, nil
}

// getIssueHTTPClient returns the HTTP client for issue service requests.
func (s *Server) getIssueHTTPClient() *http.Client {
	if s.issueHTTPClient != nil {
		return s.issueHTTPClient
	}
	return &http.Client{Timeout: issueSubmitTimeout}
}

// diagnosticsManifest is written as an entry in the diagnostics zip.
type diagnosticsManifest struct {
	MaestroVersion string         `json:"maestro_version"`
	InstallationID string         `json:"installation_id"`
	CreatedAt      string         `json:"created_at"`
	Files          []manifestFile `json:"files"`
}

type manifestFile struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
}

// buildDiagnosticsZip creates a temporary zip file with diagnostic data.
// Caller is responsible for removing the returned file path.
func buildDiagnosticsZip(workDir, installationID string) (string, error) {
	maestroDir := filepath.Join(workDir, config.ProjectConfigDir)

	// Create temp file for the zip
	tmpFile, err := os.CreateTemp("", "maestro-diagnostics-*.zip")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// cleanupOnError closes and removes the temp file on failure.
	cleanupOnError := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}

	zipWriter := zip.NewWriter(tmpFile)

	// Checkpoint the WAL before copying the database, but only if the DB exists
	dbPath := filepath.Join(maestroDir, config.DatabaseFilename)
	if _, statErr := os.Stat(dbPath); statErr == nil {
		checkpointWAL(dbPath)
	}

	// Files to include (relative to .maestro/)
	includeFiles := []string{
		"config.json",
		config.DatabaseFilename,
		config.DatabaseFilename + "-wal",
		config.DatabaseFilename + "-shm",
		filepath.Join("logs", "maestro.log"),
	}

	manifestFiles := make([]manifestFile, 0, len(includeFiles))

	for _, relPath := range includeFiles {
		absPath := filepath.Join(maestroDir, relPath)
		info, statErr := os.Stat(absPath)
		if statErr != nil {
			// Skip files that don't exist (WAL/SHM may not exist)
			continue
		}

		if zipErr := addFileToZip(zipWriter, absPath, relPath); zipErr != nil {
			cleanupOnError()
			return "", fmt.Errorf("add %s to zip: %w", relPath, zipErr)
		}

		manifestFiles = append(manifestFiles, manifestFile{
			Name:      relPath,
			SizeBytes: info.Size(),
		})
	}

	// Write manifest as the last entry (we needed file sizes first)
	manifest := diagnosticsManifest{
		MaestroVersion: version.Version,
		InstallationID: installationID,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		Files:          manifestFiles,
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		cleanupOnError()
		return "", fmt.Errorf("marshal manifest: %w", err)
	}

	mw, err := zipWriter.Create("manifest.json")
	if err != nil {
		cleanupOnError()
		return "", fmt.Errorf("create manifest entry: %w", err)
	}
	if _, err := mw.Write(manifestJSON); err != nil {
		cleanupOnError()
		return "", fmt.Errorf("write manifest: %w", err)
	}

	if err := zipWriter.Close(); err != nil {
		cleanupOnError()
		return "", fmt.Errorf("close zip: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temp file: %w", err)
	}

	return tmpPath, nil
}

// addFileToZip adds a single file to the zip archive.
func addFileToZip(zw *zip.Writer, absPath, relPath string) error {
	f, err := os.Open(absPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", relPath, err)
	}
	defer func() { _ = f.Close() }()

	w, err := zw.Create(relPath)
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", relPath, err)
	}

	if _, err = io.Copy(w, f); err != nil {
		return fmt.Errorf("write %s to zip: %w", relPath, err)
	}
	return nil
}

// checkpointWAL runs PRAGMA wal_checkpoint(PASSIVE) on the database to flush pending WAL data.
func checkpointWAL(dbPath string) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return
	}
	defer func() { _ = db.Close() }()
	_, _ = db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
}

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message}) //nolint:errcheck
}

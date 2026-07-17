package v1target

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SnapdragonPartners/maestro/benchmark/internal/gitx"
	"github.com/SnapdragonPartners/maestro/benchmark/internal/safe"
)

// Gitea constants — the SWE-EVO mechanics (v1 pkg/benchmark/gitea.go),
// reimplemented over the docker CLI and Gitea HTTP API so this module never
// imports orchestrator code.
const (
	// giteaImage is pinned by DIGEST: a mutable tag would let two nominally
	// identical benchmark runs execute different forge code with no
	// identity change. The digest is bound into the harness hash.
	giteaImage     = "gitea/gitea:1.25@sha256:fee0e5e55da6d2d11186bf39023a772fe63d9deffc0a83283e3d8e5d11c2716a"
	giteaContainer = "golden-runner-gitea"
	giteaVolume    = "golden-runner-gitea-data"
	giteaOrg       = "golden"
	giteaAdmin     = "golden-admin"
	giteaReadyWait = 2 * time.Minute
)

// giteaManager owns the shared Gitea container (one per runner process) and
// the per-run throwaway repos v1 targets are pointed at.
type giteaManager struct {
	httpClient *http.Client
	baseURL    string
	token      string
	password   string
	mu         sync.Mutex
	port       int
	running    bool
}

func newGiteaManager() *giteaManager {
	return &giteaManager{httpClient: &http.Client{Timeout: 30 * time.Second}}
}

// isRunning reads the running flag under the manager's own mutex.
func (g *giteaManager) isRunning() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.running
}

// ensureRunning starts a fresh Gitea container with known credentials.
// Always recreates: stale admin state from a prior run causes token 401s.
func (g *giteaManager) ensureRunning(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.running {
		return nil
	}
	_ = dockerRun(ctx, "rm", "-f", giteaContainer)  //nolint:errcheck // may not exist
	_ = dockerRun(ctx, "volume", "rm", giteaVolume) //nolint:errcheck // may not exist

	port, err := freePort()
	if err != nil {
		return err
	}
	g.port = port
	g.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	if err := dockerRun(ctx, "run", "-d", "--name", giteaContainer,
		"-p", fmt.Sprintf("127.0.0.1:%d:3000", port),
		"-v", giteaVolume+":/data",
		"-e", "GITEA__security__INSTALL_LOCK=true",
		"-e", "GITEA__server__ROOT_URL="+g.baseURL+"/",
		giteaImage); err != nil {
		return fmt.Errorf("start gitea container: %w", err)
	}
	if err := g.waitReady(ctx); err != nil {
		return err
	}
	if err := g.setupAdmin(ctx); err != nil {
		return err
	}
	g.running = true
	return nil
}

func (g *giteaManager) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(giteaReadyWait)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return fmt.Errorf("gitea readiness: %w", ctx.Err())
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+"/api/v1/version", http.NoBody)
		if reqErr != nil {
			return fmt.Errorf("readiness request: %w", reqErr)
		}
		resp, err := g.httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close() //nolint:errcheck // readiness probe
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("gitea not ready within %s", giteaReadyWait)
}

// setupAdmin creates the admin user (via docker exec), an API token, and
// the organization.
func (g *giteaManager) setupAdmin(ctx context.Context) error {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("password entropy: %w", err)
	}
	g.password = "Aa1-" + hex.EncodeToString(raw)

	// gitea admin must run as the git user; retry while internal migrations
	// finish (the HTTP probe can pass slightly before the CLI is usable).
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		lastErr = dockerRun(ctx, "exec", "-u", "git", giteaContainer,
			"gitea", "admin", "user", "create",
			"--username", giteaAdmin, "--password", g.password,
			"--email", giteaAdmin+"@invalid.local",
			"--admin", "--must-change-password=false")
		if lastErr == nil {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if lastErr != nil {
		return fmt.Errorf("create gitea admin: %w", lastErr)
	}

	token, err := g.createToken(ctx)
	if err != nil {
		return err
	}
	g.token = token
	return g.createOrg(ctx)
}

func (g *giteaManager) createToken(ctx context.Context) (string, error) {
	body := `{"name":"golden-runner","scopes":["all"]}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/v1/users/%s/tokens", g.baseURL, giteaAdmin),
		strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	req.SetBasicAuth(giteaAdmin, g.password)
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create token: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body
	if resp.StatusCode != http.StatusCreated {
		payload, _ := io.ReadAll(resp.Body) //nolint:errcheck // diagnostic only
		return "", fmt.Errorf("create token: status %d (%s)", resp.StatusCode, string(payload))
	}
	var parsed struct {
		Token string `json:"sha1"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	return parsed.Token, nil
}

func (g *giteaManager) createOrg(ctx context.Context) error {
	status, payload, err := g.api(ctx, http.MethodPost, "/api/v1/orgs",
		map[string]any{"username": giteaOrg})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("create org: status %d (%s)", status, payload)
	}
	return nil
}

// api performs an authenticated JSON API call, returning status and body.
func (g *giteaManager) api(ctx context.Context, method, path string, body any) (int, string, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, "", fmt.Errorf("marshal api body: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, reader)
	if err != nil {
		return 0, "", fmt.Errorf("api request: %w", err)
	}
	req.Header.Set("Authorization", "token "+g.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("api %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()             //nolint:errcheck // response body
	payload, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort body
	return resp.StatusCode, string(payload), nil
}

// createSeededRepo creates the per-run repo and seeds it with the fixture
// pin's history from the engine workspace. Returns the unauthenticated and
// authenticated clone URLs.
func (g *giteaManager) createSeededRepo(ctx context.Context, runID, workspaceDir, pin string) (cloneURL, authURL string, err error) {
	repoName := sanitizeRepoName(runID)
	status, payload, err := g.api(ctx, http.MethodPost,
		fmt.Sprintf("/api/v1/orgs/%s/repos", giteaOrg),
		map[string]any{"name": repoName, "private": false, "default_branch": "main"})
	if err != nil {
		return "", "", err
	}
	if status != http.StatusCreated {
		return "", "", fmt.Errorf("create repo %s: status %d (%s)", repoName, status, payload)
	}
	cloneURL = fmt.Sprintf("%s/%s/%s.git", g.baseURL, giteaOrg, repoName)
	authURL = g.authenticated(cloneURL)
	if _, err := gitx.Run(ctx, workspaceDir, "push", "--quiet", authURL, pin+":refs/heads/main"); err != nil {
		return "", "", fmt.Errorf("seed repo: %w", err)
	}
	return cloneURL, authURL, nil
}

func (g *giteaManager) deleteRepo(ctx context.Context, runID string) error {
	repoName := sanitizeRepoName(runID)
	status, payload, err := g.api(ctx, http.MethodDelete,
		fmt.Sprintf("/api/v1/repos/%s/%s", giteaOrg, repoName), nil)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && status != http.StatusNotFound {
		return fmt.Errorf("delete repo %s: status %d (%s)", repoName, status, payload)
	}
	return nil
}

// prInfo is the exported PR metadata (evidence).
type prInfo struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	State     string `json:"state"`
	MergedAt  string `json:"merged_at,omitempty"`
	MergeSHA  string `json:"merge_commit_sha,omitempty"`
	CreatedAt string `json:"created_at"`
	Number    int64  `json:"number"`
	Merged    bool   `json:"merged"`
}

// listPRs returns every PR of the run's repo (all states).
func (g *giteaManager) listPRs(ctx context.Context, runID string) ([]prInfo, error) {
	repoName := sanitizeRepoName(runID)
	status, payload, err := g.api(ctx, http.MethodGet,
		fmt.Sprintf("/api/v1/repos/%s/%s/pulls?state=all&limit=50", giteaOrg, repoName), nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("list prs: status %d (%s)", status, payload)
	}
	var prs []prInfo
	if err := json.Unmarshal([]byte(payload), &prs); err != nil {
		return nil, fmt.Errorf("decode prs: %w", err)
	}
	return prs, nil
}

// writeForgeState writes v1's forge_state.json binding into the project dir
// (shape from v1 pkg/forge state).
func (g *giteaManager) writeForgeState(projectDir, runID string) error {
	state := map[string]any{
		"provider":       "gitea",
		"url":            g.baseURL,
		"token":          g.token,
		"owner":          giteaOrg,
		"repo_name":      sanitizeRepoName(runID),
		"container_name": giteaContainer,
		"port":           g.port,
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal forge state: %w", err)
	}
	path := filepath.Join(projectDir, "forge_state.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write forge state: %w", err)
	}
	return nil
}

func (g *giteaManager) authenticated(cloneURL string) string {
	return strings.Replace(cloneURL, "http://", fmt.Sprintf("http://%s:%s@", giteaAdmin, g.token), 1)
}

// teardown removes the shared container and volume. It is idempotent and
// always attempts removal (partial startups are teardown-eligible too);
// running flips false only once removal succeeded, preserving
// retryability.
func (g *giteaManager) teardown(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	rmErr := dockerRemoveIfExists(ctx, "rm", "-f", giteaContainer)
	volErr := dockerRemoveIfExists(ctx, "volume", "rm", giteaVolume)
	if err := errors.Join(rmErr, volErr); err != nil {
		return fmt.Errorf("gitea teardown: %w", err)
	}
	g.running = false
	return nil
}

// dockerRemoveIfExists treats "no such object" as success — removal of
// something absent is the desired end state.
func dockerRemoveIfExists(ctx context.Context, args ...string) error {
	err := dockerRun(ctx, args...)
	if err != nil && (strings.Contains(err.Error(), "No such") || strings.Contains(err.Error(), "no such")) {
		return nil
	}
	return err
}

// sweepSessionContainers force-removes every container labeled with the v1
// session and verifies none remain; leftovers are a cleanup failure (the
// engine records the attempt invalid).
func sweepSessionContainers(ctx context.Context, sessionID string) error {
	filter := fmt.Sprintf("label=%s=%s", sessionLabel, sessionID)
	list := func() ([]string, error) {
		out, err := exec.CommandContext(ctx, "docker", "ps", "-aq", "--filter", filter).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("list session containers: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		ids := strings.Fields(strings.TrimSpace(string(out)))
		return ids, nil
	}
	ids, err := list()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if rmErr := dockerRun(ctx, "rm", "-f", id); rmErr != nil {
			return fmt.Errorf("remove session container %s: %w", id, rmErr)
		}
	}
	remaining, err := list()
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		return fmt.Errorf("session containers left behind after sweep: %s", strings.Join(remaining, ", "))
	}
	return nil
}

func dockerRun(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// dockerAvailable reports whether the docker CLI is usable.
func dockerAvailable(ctx context.Context) bool {
	return exec.CommandContext(ctx, "docker", "info").Run() == nil
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("free port: %w", err)
	}
	addr, ok := safe.As[*net.TCPAddr](listener.Addr())
	if !ok {
		_ = listener.Close() //nolint:errcheck // error path
		return 0, fmt.Errorf("free port: unexpected addr type %T", listener.Addr())
	}
	port := addr.Port
	if err := listener.Close(); err != nil {
		return 0, fmt.Errorf("free port close: %w", err)
	}
	return port, nil
}

// sanitizeRepoName keeps run IDs valid as Gitea repo names.
func sanitizeRepoName(runID string) string {
	out := make([]rune, 0, len(runID))
	for _, r := range runID {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			out = append(out, r)
		} else {
			out = append(out, '-')
		}
	}
	name := string(out)
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

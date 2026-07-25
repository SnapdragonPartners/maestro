package stack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/internal/dataplane/paths"
)

// DefaultComposeFile is the Compose file's path relative to the repository
// root, which is where make targets run.
const DefaultComposeFile = "deploy/dataplane/compose.yaml"

// readyTimeout bounds the wait for both services. The first start runs
// initdb, which dominates it; steady-state startup is a few seconds.
const readyTimeout = 3 * time.Minute

// ErrNotReady reports a stack that did not become healthy in time.
var ErrNotReady = errors.New("data plane did not become ready")

// Up brings the stack up and waits until both services are usable.
//
// It is idempotent: the "one command from a clean checkout" criterion and
// the everyday inner-loop command are the same command, so re-running it
// on a healthy stack must be a no-op rather than a restart.
func Up(ctx context.Context, c *Config, composeFile string) error {
	if err := c.Roots.Ensure(); err != nil {
		return fmt.Errorf("prepare storage roots: %w", err)
	}
	if err := c.Roots.EnsureServiceDataDirs(paths.ServicePostgres, paths.ServiceMinIO); err != nil {
		return fmt.Errorf("prepare service data directories: %w", err)
	}

	rootKey, keyErr := paths.EnsureKey(c.Roots.Config)
	if keyErr != nil {
		return fmt.Errorf("ensure root-of-trust key: %w", keyErr)
	}
	if bootErr := paths.WriteBootstrap(c.Roots.Config, c.Bootstrap()); bootErr != nil {
		return fmt.Errorf("write bootstrap pointer: %w", bootErr)
	}

	env, envErr := c.composeEnv(rootKey)
	if envErr != nil {
		return envErr
	}
	if err := compose(ctx, composeFile, env, "up", "-d", "--wait=false"); err != nil {
		return err
	}
	return waitReady(ctx, c, composeFile, env)
}

// Down stops the stack and leaves the data root untouched.
func Down(ctx context.Context, c *Config, composeFile string) error {
	env, err := c.composeEnv(placeholderKey())
	if err != nil {
		return err
	}
	return compose(ctx, composeFile, env, "down")
}

// Reset stops the stack and deletes the contents of every service data
// directory. This is the only destructive operation here, and it is the
// caller's job to have confirmed it.
//
// The directories themselves are preserved rather than removed and
// recreated: they are bind-mount sources, and on macOS a recreated
// directory has a new inode, which leaves any existing mount pointing at
// the old one (the same hazard CLAUDE.md records for workspaces).
func Reset(ctx context.Context, c *Config, composeFile string) error {
	if err := Down(ctx, c, composeFile); err != nil {
		return err
	}
	for _, service := range []paths.Service{paths.ServicePostgres, paths.ServiceMinIO} {
		dir, err := c.Roots.ServiceDataDir(service)
		if err != nil {
			return fmt.Errorf("locate %s data directory: %w", service, err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read %s: %w", dir, err)
		}
		for _, entry := range entries {
			target := filepath.Join(dir, entry.Name())
			if err := os.RemoveAll(target); err != nil {
				return fmt.Errorf("remove %s: %w", target, err)
			}
		}
	}
	return nil
}

// ImagePinsFile is the name of the digest pin file, which lives beside the
// Compose file so the two travel together.
const ImagePinsFile = "images.env"

// loadImagePins reads the digest pins beside the Compose file.
//
// They are loaded here rather than passed to Compose as an --env-file so
// that a missing or malformed pin is our error with our message. Compose's
// own failure mode is to substitute a blank string and then complain that
// a service "has neither an image nor a build context", which names
// neither the file nor the variable.
func loadImagePins(composeFile string) ([]string, error) {
	path := filepath.Join(filepath.Dir(composeFile), ImagePinsFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read image pins %s: %w", path, err)
	}

	lines := strings.Split(string(raw), "\n")
	pins := make([]string, 0, len(lines))
	required := map[string]bool{"MAESTRO_PG_IMAGE": false, "MAESTRO_MINIO_IMAGE": false}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, found := strings.Cut(trimmed, "=")
		if !found {
			return nil, fmt.Errorf("%s: malformed line %q", path, trimmed)
		}
		if !strings.Contains(value, "@sha256:") {
			return nil, fmt.Errorf("%s: %s is not pinned by digest (ADR 0026)", path, key)
		}
		if _, known := required[key]; known {
			required[key] = true
		}
		pins = append(pins, trimmed)
	}
	for key, present := range required {
		if !present {
			return nil, fmt.Errorf("%s does not define %s", path, key)
		}
	}
	return pins, nil
}

// compose runs a docker compose subcommand against the data-plane project.
func compose(ctx context.Context, composeFile string, env []string, args ...string) error {
	pins, err := loadImagePins(composeFile)
	if err != nil {
		return err
	}
	full := append([]string{"compose", "--project-name", ProjectName, "--file", composeFile}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Env = append(append(os.Environ(), env...), pins...)

	// Combined output: Compose reports the real cause (a port clash, an
	// unwritable mount) on stderr, and losing it turns a diagnosable
	// failure into "exit status 1".
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

// waitReady blocks until Postgres reports healthy and MinIO answers its
// liveness endpoint, or the deadline passes.
func waitReady(ctx context.Context, c *Config, composeFile string, env []string) error {
	deadline := time.Now().Add(readyTimeout)
	var lastErr error

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return fmt.Errorf("waiting for data plane: %w", ctx.Err())
		}
		pgErr := postgresHealthy(ctx, composeFile, env)
		minioErr := minioLive(ctx, c)
		if pgErr == nil && minioErr == nil {
			return nil
		}
		lastErr = errors.Join(pgErr, minioErr)

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for data plane: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("%w within %s: %w", ErrNotReady, readyTimeout, lastErr)
}

// composePS is the subset of `docker compose ps --format json` this needs.
type composePS struct {
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

// postgresHealthy reads the container's own healthcheck verdict.
//
// The check runs inside the container because pg_isready ships there and
// speaks the protocol; a host-side TCP dial would report success as soon
// as the port is bound, which during a cold initdb is long before the
// database can answer.
func postgresHealthy(ctx context.Context, composeFile string, env []string) error {
	pins, err := loadImagePins(composeFile)
	if err != nil {
		return err
	}
	full := []string{"compose", "--project-name", ProjectName, "--file", composeFile, "ps", "--format", "json"}
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Env = append(append(os.Environ(), env...), pins...)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("docker compose ps: %w", err)
	}

	// Compose emits one JSON object per line, not an array.
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var entry composePS
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return fmt.Errorf("parse compose ps output: %w", err)
		}
		if entry.Service != "postgres" {
			continue
		}
		if entry.Health == "healthy" {
			return nil
		}
		return fmt.Errorf("postgres is %s (health: %q)", entry.State, entry.Health)
	}
	return errors.New("postgres container not found")
}

// minioLive probes the published port from the host.
//
// Host-side rather than a container healthcheck: this image's minimal base
// has changed its available tooling across releases, so a healthcheck
// shelling out to curl or mc is one pin-bump from silently breaking. This
// also tests the path callers actually use — the published port.
func minioLive(ctx context.Context, c *Config) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/minio/health/live", c.MinIOPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("build minio health request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("minio not answering: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("minio health returned %s", resp.Status)
	}
	return nil
}

// placeholderKey supplies non-empty key material for operations that must
// render the Compose environment but never touch credentials.
//
// Down and Reset must work even when the key file is missing or
// unreadable — that is exactly when an operator needs to tear the stack
// down. Compose still requires every variable to be set, so the values are
// filled with a constant that is never used to authenticate: the
// containers are being stopped, not started.
func placeholderKey() []byte {
	return []byte("teardown-placeholder-not-a-credential")
}

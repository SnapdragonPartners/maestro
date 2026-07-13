package testkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// SkipDockerTestsEnv is the environment variable that, when set to "1", turns
// Docker-daemon-down failures into explicit test skips. The default is
// failure: a silent skip would let the pre-push hook pass without its Docker
// coverage.
const SkipDockerTestsEnv = "MAESTRO_SKIP_DOCKER_TESTS"

const dockerProbeTimeout = 3 * time.Second

//nolint:gochecknoglobals // Intentional package-level cache: probe the daemon once per test process.
var (
	dockerProbeOnce sync.Once
	dockerProbeErr  error
)

// DockerDaemonError reports whether the Docker daemon is reachable, probing at
// most once per process. It returns nil when the daemon responds. Note that
// `docker info --format` prints client-side output even when the daemon is
// down, so the probe asks for the server version specifically.
func DockerDaemonError() error {
	dockerProbeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), dockerProbeTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Output()
		if err != nil {
			// Output() stores the CLI's stderr on ExitError; surface it, or the
			// error reads as a bare "exit status 1" instead of the real cause
			// (socket missing, DOCKER_HOST unreachable, ...).
			detail := err.Error()
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
				detail = strings.TrimSpace(string(exitErr.Stderr))
			}
			dockerProbeErr = fmt.Errorf("docker daemon not reachable: %s", detail)
			return
		}
		if strings.TrimSpace(string(out)) == "" {
			dockerProbeErr = fmt.Errorf("docker daemon not reachable: probe returned no server version")
		}
	})
	return dockerProbeErr
}

// RequireDocker fails the calling test immediately with an actionable message
// when the Docker daemon is not running, instead of letting Docker-dependent
// tests die slowly on mount or exec timeouts with misleading errors. Setting
// MAESTRO_SKIP_DOCKER_TESTS=1 turns the failure into an explicit skip.
func RequireDocker(tb testing.TB) {
	tb.Helper()
	err := DockerDaemonError()
	if err == nil {
		return
	}
	if os.Getenv(SkipDockerTestsEnv) == "1" {
		tb.Skipf("skipping Docker-dependent test (%s=1): %v", SkipDockerTestsEnv, err)
	}
	tb.Fatalf("%v — start Docker Desktop, or set %s=1 to skip Docker-dependent tests", err, SkipDockerTestsEnv)
}

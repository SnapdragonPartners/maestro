//go:build integration

package integration

import (
	"fmt"
	"os"
	"testing"

	"orchestrator/pkg/testkit"
)

// TestMain fails the whole package fast when the Docker daemon is down. Most
// tests in this package depend on Docker; without this preflight each one
// burns a multi-second mount/exec timeout before failing with a misleading
// error (see issue #241, observed when macOS quit Docker Desktop for an OS
// update). MAESTRO_SKIP_DOCKER_TESTS=1 skips the package explicitly instead.
func TestMain(m *testing.M) {
	if err := testkit.DockerDaemonError(); err != nil {
		if os.Getenv(testkit.SkipDockerTestsEnv) == "1" {
			fmt.Fprintf(os.Stderr, "skipping integration tests (%s=1): %v\n", testkit.SkipDockerTestsEnv, err)
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "integration tests require Docker: %v\nstart Docker Desktop, or set %s=1 to skip Docker-dependent tests\n", err, testkit.SkipDockerTestsEnv)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

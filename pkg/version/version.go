// Package version provides build version information for the maestro orchestrator.
// These variables are set at build time via ldflags by goreleaser.
package version

// Build information variables - set by goreleaser via ldflags.
// Example: go build -ldflags "-X orchestrator/pkg/version.Version=v1.2.3".
//
//nolint:gochecknoglobals // These must be package-level vars for ldflags injection.
var (
	// Version is the semantic version (e.g., "v1.2.3" or "dev" for development builds).
	Version = "dev"

	// Commit is the git commit SHA of the build.
	Commit = "none"

	// Date is the build date in ISO format.
	Date = "unknown"
)

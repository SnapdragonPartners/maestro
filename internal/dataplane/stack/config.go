// Package stack brings the local data plane up and down.
//
// It is the only supported entry point to the Compose file: it resolves
// the storage roots, pre-creates and verifies the bind-mount sources,
// derives credentials from the root-of-trust key, writes the bootstrap
// pointer, and only then invokes Compose. Running Compose by hand fails on
// unset variables, deliberately — the invariants live here, not in YAML.
package stack

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"

	"orchestrator/internal/dataplane/paths"
	"orchestrator/internal/dataplane/secret"
)

// Default host ports. Deliberately not 5432/9000: a developer's own
// Postgres on 5432 would collide, and silently connecting to the wrong
// database is a bad failure. High ports keep the default working on a
// machine that already runs the usual services.
const (
	DefaultPGPort           = 55432
	DefaultMinIOPort        = 59000
	DefaultMinIOConsolePort = 59001

	DefaultDatabase = "maestro"
	DefaultUser     = "maestro"
	DefaultBucket   = "maestro"

	// DefaultProjectName isolates this stack from v1's containers, so a
	// `docker compose down` in one context can never reach the other.
	//
	// It is a DEFAULT rather than a constant because Compose selects
	// containers by project identity alone: a second config pointed at
	// different roots and different ports still reaches these containers
	// unless its project name differs too. Integration tests for the
	// destructive verbs are only isolated when they override it.
	DefaultProjectName = "maestro-dataplane"

	// ComponentLabel marks every data-plane container. It is deliberately
	// NOT a `com.maestro.session` label: the benchmark adapter sweeps by
	// that label on teardown and would destroy the data plane mid-run.
	ComponentLabel = "com.maestro.component=dataplane"
)

// Environment variables that override the defaults, for a machine where
// the chosen ports are themselves taken.
const (
	EnvPGPort           = "MAESTRO_PG_PORT"
	EnvMinIOPort        = "MAESTRO_MINIO_PORT"
	EnvMinIOConsolePort = "MAESTRO_MINIO_CONSOLE_PORT"

	// EnvProjectName overrides the Compose project.
	//
	// It exists for the destructive integration tests, which must act on a
	// stack of their own, and it is an ENVIRONMENT override specifically so
	// a subprocess inherits it — the killed-process cases run
	// `dataplanectl` as a child and would otherwise operate on the
	// developer's plane.
	EnvProjectName = "MAESTRO_PROJECT_NAME"
)

// ErrInvalidProjectName reports a project override Compose would reject or
// that could escape the argument it is substituted into.
var ErrInvalidProjectName = errors.New("invalid compose project name")

// projectNamePattern is Compose's own rule: lowercase letters, digits,
// dashes and underscores, starting with a letter or digit.
//
//nolint:gochecknoglobals // Immutable compiled pattern.
var projectNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Config is the resolved description of a local data plane.
type Config struct {
	Roots            paths.Roots
	ProjectName      string
	Database         string
	User             string
	Bucket           string
	PGPort           int
	MinIOPort        int
	MinIOConsolePort int
}

// NewConfig builds the configuration for the given roots, applying any
// port overrides from the environment.
func NewConfig(roots paths.Roots) (*Config, error) {
	c := &Config{
		Roots:            roots,
		ProjectName:      DefaultProjectName,
		Database:         DefaultDatabase,
		User:             DefaultUser,
		Bucket:           DefaultBucket,
		PGPort:           DefaultPGPort,
		MinIOPort:        DefaultMinIOPort,
		MinIOConsolePort: DefaultMinIOConsolePort,
	}

	for _, override := range []struct {
		port *int
		env  string
	}{
		{&c.PGPort, EnvPGPort},
		{&c.MinIOPort, EnvMinIOPort},
		{&c.MinIOConsolePort, EnvMinIOConsolePort},
	} {
		if err := applyPortOverride(override.env, override.port); err != nil {
			return nil, err
		}
	}

	if err := applyProjectOverride(&c.ProjectName); err != nil {
		return nil, err
	}
	if err := c.validatePorts(); err != nil {
		return nil, err
	}
	return c, nil
}

// applyProjectOverride reads the project-name override, validating it
// against Compose's own naming rule.
//
// Validated rather than trusted because this value becomes a command-line
// argument selecting which containers an operation destroys. An
// unvalidated one could carry a leading dash and be read as a flag, or
// characters Compose silently mangles into a project the caller did not
// mean — and the operation reaching for it is `down`.
func applyProjectOverride(name *string) error {
	raw, set := os.LookupEnv(EnvProjectName)
	if !set {
		return nil
	}
	if !projectNamePattern.MatchString(raw) {
		return fmt.Errorf("%w: %s=%q must match %s", ErrInvalidProjectName, EnvProjectName, raw, projectNamePattern)
	}
	*name = raw
	return nil
}

func applyPortOverride(env string, port *int) error {
	raw := os.Getenv(env)
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("%s=%q is not a number: %w", env, raw, err)
	}
	*port = value
	return nil
}

// validatePorts rejects out-of-range and duplicate ports. Duplicates
// matter because two services binding one port fails deep inside Compose
// with a message that does not name the cause.
func (c *Config) validatePorts() error {
	named := map[string]int{
		EnvPGPort:           c.PGPort,
		EnvMinIOPort:        c.MinIOPort,
		EnvMinIOConsolePort: c.MinIOConsolePort,
	}
	seen := map[int]string{}
	for name, port := range named {
		if port < 1 || port > 65535 {
			return fmt.Errorf("%s must be between 1 and 65535, got %d", name, port)
		}
		if prior, clash := seen[port]; clash {
			return fmt.Errorf("%s and %s are both %d; every service needs its own port", prior, name, port)
		}
		seen[port] = name
	}
	return nil
}

// DSN renders the Postgres connection string for this stack.
//
// The password is derived, never stored, so this is assembled at the point
// of use and never written anywhere — the bootstrap pointer deliberately
// carries no credential.
func (c *Config) DSN(rootKey []byte) (string, error) {
	return c.DSNFor(rootKey, c.Database)
}

// DSNFor renders a connection string for an arbitrary database on this
// stack.
//
// Exists for tests, which must never run destructive migrations against the
// canonical database: a down-migration harness pointed at `maestro` drops
// every table the developer is working with. Tests create a disposable
// database and point here.
func (c *Config) DSNFor(rootKey []byte, database string) (string, error) {
	password, err := secret.Derive(rootKey, secret.ContextPostgresPassword)
	if err != nil {
		return "", fmt.Errorf("derive postgres password: %w", err)
	}
	// url.UserPassword escapes the credential; the derived value is
	// base64url and needs none, but relying on that would make the
	// derivation encoding load-bearing for connection-string correctness.
	dsn := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.User, password),
		Host:     net.JoinHostPort("127.0.0.1", strconv.Itoa(c.PGPort)),
		Path:     "/" + database,
		RawQuery: "sslmode=disable",
	}
	return dsn.String(), nil
}

// Bootstrap renders the pointer describing this stack.
func (c *Config) Bootstrap() *paths.Bootstrap {
	return &paths.Bootstrap{
		Postgres: paths.Postgres{
			Host:     "127.0.0.1",
			Port:     c.PGPort,
			Database: c.Database,
			User:     c.User,
		},
		Objects: paths.ObjectStore{
			Endpoint: fmt.Sprintf("http://127.0.0.1:%d", c.MinIOPort),
			Bucket:   c.Bucket,
		},
		RootOfTrust: paths.RootOfTrust{
			Kind: paths.RootOfTrustKeyFile,
			Path: c.Roots.KeyPath(),
		},
	}
}

// composeEnv renders the variables the Compose file requires.
//
// Credentials are derived here and passed to Compose in the environment,
// never written to disk: the bootstrap pointer must stay secret-free, and
// a generated .env file would be a second copy of a credential sitting in
// the working tree.
func (c *Config) composeEnv(rootKey []byte) ([]string, error) {
	pgDir, err := c.Roots.ServiceDataDir(paths.ServicePostgres)
	if err != nil {
		return nil, fmt.Errorf("locate postgres data directory: %w", err)
	}
	minioDir, err := c.Roots.ServiceDataDir(paths.ServiceMinIO)
	if err != nil {
		return nil, fmt.Errorf("locate minio data directory: %w", err)
	}

	pgPassword, err := secret.Derive(rootKey, secret.ContextPostgresPassword)
	if err != nil {
		return nil, fmt.Errorf("derive postgres password: %w", err)
	}
	minioUser, err := secret.Derive(rootKey, secret.ContextObjectAccessKey)
	if err != nil {
		return nil, fmt.Errorf("derive object access key: %w", err)
	}
	minioPassword, err := secret.Derive(rootKey, secret.ContextObjectSecretKey)
	if err != nil {
		return nil, fmt.Errorf("derive object secret key: %w", err)
	}

	return []string{
		// Design D2a: the containers run as the invoking user, which is who
		// owns the pre-created bind-mount sources. Passed explicitly rather
		// than relying on $UID, which is not exported by every shell.
		"MAESTRO_UID=" + strconv.Itoa(os.Getuid()),
		"MAESTRO_GID=" + strconv.Itoa(os.Getgid()),

		"MAESTRO_PG_DATA_DIR=" + pgDir,
		"MAESTRO_MINIO_DATA_DIR=" + minioDir,

		"MAESTRO_PG_PORT=" + strconv.Itoa(c.PGPort),
		"MAESTRO_MINIO_PORT=" + strconv.Itoa(c.MinIOPort),
		"MAESTRO_MINIO_CONSOLE_PORT=" + strconv.Itoa(c.MinIOConsolePort),

		"MAESTRO_PG_DATABASE=" + c.Database,
		"MAESTRO_PG_USER=" + c.User,
		"MAESTRO_PG_PASSWORD=" + pgPassword,
		"MAESTRO_MINIO_ROOT_USER=" + minioUser,
		"MAESTRO_MINIO_ROOT_PASSWORD=" + minioPassword,
	}, nil
}

// Package stack brings the local data plane up and down.
//
// It is the only supported entry point to the Compose file: it resolves
// the storage roots, pre-creates and verifies the bind-mount sources,
// derives credentials from the root-of-trust key, writes the bootstrap
// pointer, and only then invokes Compose. Running Compose by hand fails on
// unset variables, deliberately — the invariants live here, not in YAML.
package stack

import (
	"fmt"
	"os"
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

	// ProjectName isolates this stack from v1's containers, so a
	// `docker compose down` in one context can never reach the other.
	ProjectName = "maestro-dataplane"

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
)

// Config is the resolved description of a local data plane.
type Config struct {
	Roots            paths.Roots
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

	if err := c.validatePorts(); err != nil {
		return nil, err
	}
	return c, nil
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

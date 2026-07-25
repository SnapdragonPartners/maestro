package paths

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleBootstrap() *Bootstrap {
	return &Bootstrap{
		Postgres:    Postgres{Host: "127.0.0.1", Port: 55432, Database: "maestro", User: "maestro"},
		Objects:     ObjectStore{Endpoint: "http://127.0.0.1:59000", Bucket: "maestro"},
		RootOfTrust: RootOfTrust{Kind: "key_file", Path: "/cfg/root-of-trust.key"},
	}
}

func TestBootstrapRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := sampleBootstrap()

	if err := WriteBootstrap(root, want); err != nil {
		t.Fatalf("WriteBootstrap: %v", err)
	}
	got, err := ReadBootstrap(root)
	if err != nil {
		t.Fatalf("ReadBootstrap: %v", err)
	}

	want.SchemaVersion = BootstrapSchemaVersion
	if got != *want {
		t.Errorf("round trip mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

// The pointer is written before the data plane exists and is copied around
// by operators; it must never carry key material or a password.
func TestBootstrapCarriesNoSecret(t *testing.T) {
	root := t.TempDir()
	if err := WriteBootstrap(root, sampleBootstrap()); err != nil {
		t.Fatalf("WriteBootstrap: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, BootstrapFileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Assert on the decoded key set rather than grepping the text, so a
	// future field named "secret_backend" cannot trip a substring check
	// while a genuinely secret-bearing field slips past one.
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("decode: %v", err)
	}
	allowed := map[string]bool{"schema_version": true, "postgres": true, "objects": true, "root_of_trust": true}
	for k := range generic {
		if !allowed[k] {
			t.Errorf("unexpected top-level field %q: the bootstrap pointer must not grow secret-bearing fields without review", k)
		}
	}

	var pg map[string]json.RawMessage
	if err := json.Unmarshal(generic["postgres"], &pg); err != nil {
		t.Fatalf("decode postgres: %v", err)
	}
	for _, forbidden := range []string{"password", "passwd", "secret", "key"} {
		if _, ok := pg[forbidden]; ok {
			t.Errorf("postgres pointer carries %q; the password is derived from the root-of-trust key, never stored", forbidden)
		}
	}
}

func TestBootstrapOverwrites(t *testing.T) {
	root := t.TempDir()
	first := sampleBootstrap()
	if err := WriteBootstrap(root, first); err != nil {
		t.Fatalf("first WriteBootstrap: %v", err)
	}

	second := *first
	second.Postgres.Port = 55433
	if err := WriteBootstrap(root, &second); err != nil {
		t.Fatalf("second WriteBootstrap: %v", err)
	}

	got, err := ReadBootstrap(root)
	if err != nil {
		t.Fatalf("ReadBootstrap: %v", err)
	}
	if got.Postgres.Port != 55433 {
		t.Errorf("port is %d, want the rewritten 55433", got.Postgres.Port)
	}

	// The rename must leave no temporary behind.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read config root: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != BootstrapFileName {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("config root holds %v, want only %q", names, BootstrapFileName)
	}
}

func TestBootstrapRejectsForeignSchemaVersion(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, BootstrapFileName)
	if err := os.WriteFile(path, []byte(`{"schema_version": 99}`), bootstrapPerm); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := ReadBootstrap(root)
	if err == nil {
		t.Fatal("expected an error for a future schema version, got nil")
	}
	if !strings.Contains(err.Error(), "schema version") {
		t.Errorf("error %q does not mention the schema version", err)
	}
}

func TestEnsureServiceDataDirs(t *testing.T) {
	base := t.TempDir()
	roots, err := resolve("linux", envOf(map[string]string{HomeEnv: base}), "/home/u")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ensureErr := roots.Ensure(); ensureErr != nil {
		t.Fatalf("Ensure: %v", ensureErr)
	}
	if svcErr := roots.EnsureServiceDataDirs(ServicePostgres, ServiceMinIO); svcErr != nil {
		t.Fatalf("EnsureServiceDataDirs: %v", svcErr)
	}

	for _, service := range []Service{ServicePostgres, ServiceMinIO} {
		dir, dirErr := roots.ServiceDataDir(service)
		if dirErr != nil {
			t.Fatalf("ServiceDataDir(%q): %v", service, dirErr)
		}
		info, statErr := os.Stat(dir)
		if statErr != nil {
			t.Fatalf("stat %s: %v", dir, statErr)
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
		if filepath.Dir(dir) != roots.Data {
			t.Errorf("%s is not a child of the data root %s", dir, roots.Data)
		}
	}

	// Re-running setup is the everyday path.
	if svcErr := roots.EnsureServiceDataDirs(ServicePostgres, ServiceMinIO); svcErr != nil {
		t.Fatalf("second EnsureServiceDataDirs: %v", svcErr)
	}
	// The data root itself stays tight; only the children are mounted.
	info, err := os.Stat(roots.Data)
	if err != nil {
		t.Fatalf("stat data root: %v", err)
	}
	if perm := info.Mode().Perm(); perm != rootPerm {
		t.Errorf("data root has mode %#o, want %#o", perm, rootPerm)
	}
}

// A service name becomes a path under the data root, so an unvalidated one
// could point a container's bind mount at the config root holding the
// unlock key. Reject anything that is not a plain single segment.
func TestServiceDataDirRejectsTraversal(t *testing.T) {
	roots, err := resolve("linux", envOf(map[string]string{HomeEnv: t.TempDir()}), "/home/u")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	for _, name := range []Service{"", ".", "..", "../config", "a/b", "./x", "sub/"} {
		t.Run(string(name), func(t *testing.T) {
			dir, dirErr := roots.ServiceDataDir(name)
			if !errors.Is(dirErr, ErrInvalidService) {
				t.Fatalf("ServiceDataDir(%q) = %q, %v; want ErrInvalidService", name, dir, dirErr)
			}
			if ensErr := roots.EnsureServiceDataDirs(name); !errors.Is(ensErr, ErrInvalidService) {
				t.Errorf("EnsureServiceDataDirs(%q) = %v; want ErrInvalidService", name, ensErr)
			}
		})
	}
}

// The pointer is hand-editable, so a tolerant decoder would silently
// accept a password somebody added in good faith.
func TestReadBootstrapRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	if err := WriteBootstrap(root, sampleBootstrap()); err != nil {
		t.Fatalf("WriteBootstrap: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(root, BootstrapFileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var generic map[string]any
	if decErr := json.Unmarshal(raw, &generic); decErr != nil {
		t.Fatalf("decode: %v", decErr)
	}
	pg, ok := generic["postgres"].(map[string]any)
	if !ok {
		t.Fatal("postgres section is not an object")
	}
	pg["password"] = "hunter2"

	edited, err := json.Marshal(generic)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if writeErr := os.WriteFile(filepath.Join(root, BootstrapFileName), edited, bootstrapPerm); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	if _, err := ReadBootstrap(root); err == nil {
		t.Fatal("a hand-added postgres.password was accepted; it must be refused, not ignored")
	}
}

func TestBootstrapValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Bootstrap)
	}{
		{name: "no postgres host", mutate: func(b *Bootstrap) { b.Postgres.Host = "" }},
		{name: "port zero", mutate: func(b *Bootstrap) { b.Postgres.Port = 0 }},
		{name: "port too high", mutate: func(b *Bootstrap) { b.Postgres.Port = 70000 }},
		{name: "no database", mutate: func(b *Bootstrap) { b.Postgres.Database = "" }},
		{name: "no user", mutate: func(b *Bootstrap) { b.Postgres.User = "" }},
		{name: "no bucket", mutate: func(b *Bootstrap) { b.Objects.Bucket = "" }},
		{name: "no endpoint", mutate: func(b *Bootstrap) { b.Objects.Endpoint = "" }},
		{name: "endpoint without scheme", mutate: func(b *Bootstrap) { b.Objects.Endpoint = "127.0.0.1:9000" }},
		{name: "endpoint with wrong scheme", mutate: func(b *Bootstrap) { b.Objects.Endpoint = "ftp://host" }},
		{name: "no root of trust kind", mutate: func(b *Bootstrap) { b.RootOfTrust.Kind = "" }},
		{name: "unsupported root of trust kind", mutate: func(b *Bootstrap) { b.RootOfTrust.Kind = "keychain" }},
		{name: "key file without path", mutate: func(b *Bootstrap) { b.RootOfTrust.Path = "" }},
		{name: "key file with relative path", mutate: func(b *Bootstrap) { b.RootOfTrust.Path = "rel/key" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := sampleBootstrap()
			tc.mutate(b)
			if err := WriteBootstrap(t.TempDir(), b); !errors.Is(err, ErrInvalidBootstrap) {
				t.Fatalf("WriteBootstrap = %v; want ErrInvalidBootstrap", err)
			}
		})
	}
}

func TestWriteBootstrapRejectsNil(t *testing.T) {
	if err := WriteBootstrap(t.TempDir(), nil); !errors.Is(err, ErrInvalidBootstrap) {
		t.Fatalf("WriteBootstrap(nil) = %v; want ErrInvalidBootstrap", err)
	}
}

// A URL is rich enough to smuggle a credential past a file that claims to
// hold none, so the endpoint is held to a bare origin.
func TestBootstrapRejectsCredentialBearingEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{name: "userinfo", endpoint: "http://user:hunter2@127.0.0.1:9000"},
		{name: "user only", endpoint: "http://token@127.0.0.1:9000"},
		{name: "query", endpoint: "http://127.0.0.1:9000?access_key=abc"},
		{name: "fragment", endpoint: "http://127.0.0.1:9000#tok"},
		{name: "path", endpoint: "http://127.0.0.1:9000/secret-prefix"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := sampleBootstrap()
			b.Objects.Endpoint = tc.endpoint
			if err := WriteBootstrap(t.TempDir(), b); !errors.Is(err, ErrInvalidBootstrap) {
				t.Fatalf("WriteBootstrap(%q) = %v; want ErrInvalidBootstrap", tc.endpoint, err)
			}
		})
	}

	// A trailing slash is an origin, not a path, and must stay valid.
	b := sampleBootstrap()
	b.Objects.Endpoint = "http://127.0.0.1:9000/"
	if err := WriteBootstrap(t.TempDir(), b); err != nil {
		t.Errorf("bare origin with trailing slash rejected: %v", err)
	}
}

func TestBootstrapRejectsURLShapedHost(t *testing.T) {
	for _, host := range []string{
		"http://127.0.0.1",
		"user:pw@127.0.0.1",
		"127.0.0.1/db",
		"127.0.0.1:5432",
		"host name",
		"[::1]",         // bracketed: net.JoinHostPort would produce [[::1]]:5432
		"[2001:db8::1]", // same
		"db?password=x", // query delimiter
		"db#frag",       // fragment delimiter
		"db\tname",      // tab
		"db\nname",      // newline
		"db,name",       // comma
	} {
		t.Run(host, func(t *testing.T) {
			b := sampleBootstrap()
			b.Postgres.Host = host
			if err := WriteBootstrap(t.TempDir(), b); !errors.Is(err, ErrInvalidBootstrap) {
				t.Fatalf("WriteBootstrap(host=%q) = %v; want ErrInvalidBootstrap", host, err)
			}
		})
	}
}

// A JSON decoder stops at the first value, so a second object appended to
// the file would be silently ignored while a reader assumes it applies.
func TestReadBootstrapRejectsTrailingContent(t *testing.T) {
	root := t.TempDir()
	if err := WriteBootstrap(root, sampleBootstrap()); err != nil {
		t.Fatalf("WriteBootstrap: %v", err)
	}
	path := filepath.Join(root, BootstrapFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	appended := append(raw, []byte("\n{\"schema_version\": 1}\n")...)
	if writeErr := os.WriteFile(path, appended, bootstrapPerm); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	if _, err := ReadBootstrap(root); !errors.Is(err, ErrInvalidBootstrap) {
		t.Fatalf("ReadBootstrap = %v; want ErrInvalidBootstrap for trailing content", err)
	}
}

func TestServiceRejectsUnknownName(t *testing.T) {
	roots, err := resolve("linux", envOf(map[string]string{HomeEnv: t.TempDir()}), "/home/u")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, dirErr := roots.ServiceDataDir(Service("redis")); !errors.Is(dirErr, ErrInvalidService) {
		t.Fatalf("ServiceDataDir(redis) = %v; want ErrInvalidService", dirErr)
	}
}

func TestBootstrapAcceptsIPLiteralHosts(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "2001:db8::1", "db.internal", "pg_primary", "a-b.c"} {
		t.Run(host, func(t *testing.T) {
			b := sampleBootstrap()
			b.Postgres.Host = host
			if err := WriteBootstrap(t.TempDir(), b); err != nil {
				t.Fatalf("WriteBootstrap(host=%q) = %v; want acceptance", host, err)
			}
		})
	}
}

// MkdirAll succeeds on a directory that already exists no matter who owns
// it or how it is moded — the dangerous case being one Docker created as
// root on an earlier run. Detect it at setup with an actionable error
// instead of at container start.
func TestEnsureServiceDataDirsRejectsUnusableExisting(t *testing.T) {
	roots, err := resolve("linux", envOf(map[string]string{HomeEnv: t.TempDir()}), "/home/u")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ensureErr := roots.Ensure(); ensureErr != nil {
		t.Fatalf("Ensure: %v", ensureErr)
	}
	if svcErr := roots.EnsureServiceDataDirs(ServicePostgres); svcErr != nil {
		t.Fatalf("EnsureServiceDataDirs: %v", svcErr)
	}
	dir, err := roots.ServiceDataDir(ServicePostgres)
	if err != nil {
		t.Fatalf("ServiceDataDir: %v", err)
	}

	// Widened mode: the container would be exposed to other local users.
	if chmodErr := os.Chmod(dir, 0o755); chmodErr != nil {
		t.Fatalf("chmod: %v", chmodErr)
	}
	if svcErr := roots.EnsureServiceDataDirs(ServicePostgres); !errors.Is(svcErr, ErrServiceDataDir) {
		t.Fatalf("widened mode accepted: %v", svcErr)
	}
	if chmodErr := os.Chmod(dir, 0o700); chmodErr != nil {
		t.Fatalf("restore chmod: %v", chmodErr)
	}

	// A file where the directory should be.
	minioDir, err := roots.ServiceDataDir(ServiceMinIO)
	if err != nil {
		t.Fatalf("ServiceDataDir: %v", err)
	}
	if writeErr := os.WriteFile(minioDir, []byte("not a dir"), 0o600); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}
	if svcErr := roots.EnsureServiceDataDirs(ServiceMinIO); svcErr == nil {
		t.Fatal("a regular file was accepted as a service data directory")
	}
}

// The writability probe must leave nothing behind.
func TestEnsureServiceDataDirsLeavesNoProbeFile(t *testing.T) {
	roots, err := resolve("linux", envOf(map[string]string{HomeEnv: t.TempDir()}), "/home/u")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if svcErr := roots.EnsureServiceDataDirs(ServicePostgres, ServiceMinIO); svcErr != nil {
		t.Fatalf("EnsureServiceDataDirs: %v", svcErr)
	}
	for _, service := range []Service{ServicePostgres, ServiceMinIO} {
		dir, dirErr := roots.ServiceDataDir(service)
		if dirErr != nil {
			t.Fatalf("ServiceDataDir: %v", dirErr)
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			t.Fatalf("read %s: %v", dir, readErr)
		}
		if len(entries) != 0 {
			t.Errorf("%s is not empty after setup: %v", dir, entries)
		}
	}
}

// The uid-mismatch branch cannot be reached in a unit test without root,
// so the message is asserted directly. Both properties are load-bearing
// and both were review findings: the advice must not suggest deletion (the
// directory holds the authoritative data), and it must not print a shell
// command (the default macOS data root contains a space, and MAESTRO_HOME
// may contain metacharacters).
func TestOwnershipErrorGuidance(t *testing.T) {
	const dir = "/Users/dr/Library/Application Support/maestro/data/postgres"
	err := ownershipError(dir, 0, 501, 20)

	if !errors.Is(err, ErrServiceDataDir) {
		t.Fatalf("got %v, want ErrServiceDataDir", err)
	}
	msg := err.Error()

	for _, want := range []string{dir, "uid 0", "uid 501", "gid 20", "Do not delete"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not mention %q: %s", want, msg)
		}
	}
	for _, forbidden := range []string{"sudo", "chown", "rm ", "$(", "&&", "|"} {
		if strings.Contains(msg, forbidden) {
			t.Errorf("message contains shell-command text %q; state the action instead: %s", forbidden, msg)
		}
	}
}

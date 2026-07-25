package paths

import (
	"encoding/json"
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
	if svcErr := roots.EnsureServiceDataDirs("postgres", "minio"); svcErr != nil {
		t.Fatalf("EnsureServiceDataDirs: %v", svcErr)
	}

	for _, service := range []string{"postgres", "minio"} {
		dir := roots.ServiceDataDir(service)
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
	if svcErr := roots.EnsureServiceDataDirs("postgres", "minio"); svcErr != nil {
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

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A relocated tray instance (GH #936) spawns its core with BOTH flags:
//
//	mcpproxy serve --data-dir <root> --config <root>/mcp_config.json
//
// against a root that has never been used before, so the named config does not
// exist yet. LoadFromFile is deliberately strict about a missing explicit path
// — it is also the hot-reload entry point, where "the file vanished" must never
// mean "reset to defaults" — so the serve entry point seeds the file first.
// Without that seeding the documented flow was dead on arrival: the core exited
// immediately with "failed to load config file …: no such file or directory".
func TestAFreshInstanceRootIsSeededAndThenLoads(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ConfigFileName)

	created, err := EnsureConfigFile(path, root)
	if err != nil {
		t.Fatalf("EnsureConfigFile: %v", err)
	}
	if !created {
		t.Fatal("a config that did not exist must be reported as created")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file was not created at %s: %v", path, err)
	}

	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("the seeded config must load: %v", err)
	}
	if cfg.DataDir != root {
		t.Fatalf("seeded config should own the instance root, got DataDir=%q want %q",
			cfg.DataDir, root)
	}
}

// The seeding must be a no-op for an existing install: this runs on every
// `serve` with an explicit --config, and overwriting there would erase every
// server the user has.
func TestEnsureConfigFileNeverTouchesAnExistingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ConfigFileName)
	original := []byte(`{"listen":"127.0.0.1:9999","mcpServers":[]}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	created, err := EnsureConfigFile(path, root)
	if err != nil {
		t.Fatalf("EnsureConfigFile: %v", err)
	}
	if created {
		t.Fatal("an existing config must not be reported as created")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("existing config was rewritten:\n got %s\nwant %s", after, original)
	}
}

// Nothing to do without an explicit path — the implicit path already creates
// its own default inside the data directory (see Load).
func TestEnsureConfigFileIgnoresAnEmptyPath(t *testing.T) {
	created, err := EnsureConfigFile("", t.TempDir())
	if err != nil {
		t.Fatalf("EnsureConfigFile: %v", err)
	}
	if created {
		t.Fatal("an empty path creates nothing")
	}
}

// The counterweight to the seeding: LoadFromFile itself stays strict. It is
// also the hot-reload path (internal/runtime/lifecycle.go,
// internal/runtime/configsvc), where a config file that has gone missing must
// fail the reload and keep the running config — never silently become a fresh
// default that drops every server the user had.
func TestLoadFromFileStillRefusesAMissingExplicitPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", ConfigFileName)
	if _, err := LoadFromFile(missing); err == nil {
		t.Fatal("LoadFromFile must not invent a config for a path that is not there")
	}
}

// The parent may not exist yet either: MCPPROXY_HOME can name a directory that
// has never been created.
func TestEnsureConfigFileCreatesTheInstanceRootItself(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mcpproxy-qa")
	path := filepath.Join(root, ConfigFileName)

	if _, err := EnsureConfigFile(path, root); err != nil {
		t.Fatalf("EnsureConfigFile: %v", err)
	}
	if _, err := LoadFromFile(path); err != nil {
		t.Fatalf("the seeded config must load: %v", err)
	}
}

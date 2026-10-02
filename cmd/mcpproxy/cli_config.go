package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// cliDiagnosticsWriter receives the loader's removed-key / deprecated-key
// findings (Spec 107 FR-032) for every CLI command that loads the config
// through loadCLIConfig. It is stderr so structured stdout (`-o json`) stays
// clean; tests swap it for a buffer.
var cliDiagnosticsWriter io.Writer = os.Stderr

// loadCLIConfig loads a CLI command's config from explicitPath (the command's
// --config flag) when set, falling back to the default search path, and applies
// the global --data-dir flag on top (GH #854/#897/#908). Without the DataDir
// override, socket.DetectSocketPath probes the default data dir and the command
// either reports "daemon is not reachable" or silently talks to the wrong
// daemon instance. Every per-command loader below must go through this helper
// (enforced by TestLoadersHonorGlobalDataDirFlag).
func loadCLIConfig(explicitPath string) (*config.Config, error) {
	var cfg *config.Config
	var err error
	if path := resolveCLIConfigPath(explicitPath); path != "" {
		cfg, err = config.LoadFromFile(path)
	} else if dataDir != "" && !legacyConfigExists() {
		// A --data-dir was given but no config file exists anywhere: use
		// defaults rooted at that directory. Legacy discovery would create
		// $HOME/.mcpproxy/mcp_config.json and report the HOME defaults, which
		// contradicts the data dir the operator named.
		cfg = config.DefaultConfig()
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return nil, err
	}
	// The server-build loader may have dropped removed keys / retired
	// auth_broker modes and recorded a diagnostic each; several callers
	// SaveConfig the result, so the operator must see the drop here (the
	// personal build records none — opaque carriers, FR-040).
	config.WriteLoadDiagnostics(cfg, cliDiagnosticsWriter)
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	return cfg, nil
}

// resolveCLIConfigPath picks the config file a management command reads. The
// command's own --config wins, then the global -c/--config, then
// <data-dir>/mcp_config.json when --data-dir was given and that file exists.
// "" means legacy discovery (cwd, then $HOME/.mcpproxy). Every per-command
// loader goes through it so the global flags behave the same in every position
// and for every subcommand, whether or not it registers a local --config.
func resolveCLIConfigPath(local string) string {
	if local != "" {
		return local
	}
	if configFile != "" {
		return configFile
	}
	if dataDir != "" {
		p := config.GetConfigPath(dataDir)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// legacyConfigExists reports whether the loader's discovery locations (cwd,
// then $HOME/.mcpproxy) already hold a config file.
func legacyConfigExists() bool {
	if _, err := os.Stat(config.ConfigFileName); err == nil {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, config.DefaultDataDir, config.ConfigFileName)); err == nil {
			return true
		}
	}
	return false
}

// defaultHomeConfigPath is the documented fallback for loaders that require an
// existing file (auth, call, code, tools).
func defaultHomeConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(home, ".mcpproxy", "mcp_config.json"), nil
}

// Per-command loaders for commands whose config flow previously used bare
// config.Load() and ignored --data-dir (GH #908).

func loadActivityConfig() (*config.Config, error) {
	return loadCLIConfig(configFile)
}

func loadCredentialConfig() (*config.Config, error) {
	return loadCLIConfig(configFile)
}

func loadSecurityConfig() (*config.Config, error) {
	return loadCLIConfig(configFile)
}

func loadTrustCertConfig() (*config.Config, error) {
	return loadCLIConfig(configFile)
}

func loadTUIConfig() (*config.Config, error) {
	return loadCLIConfig(configFile)
}

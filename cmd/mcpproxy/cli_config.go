package main

import (
	"io"
	"os"

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
	if explicitPath != "" {
		cfg, err = config.LoadFromFile(explicitPath)
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

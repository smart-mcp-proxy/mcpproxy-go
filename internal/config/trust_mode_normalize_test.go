package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeTrustModes covers the upgrade-regression half of GH #938 finding
// 1: rejecting a bogus trust_mode at the WRITE seams (REST/MCP/CLI) is right,
// but the LOAD path must not brick an existing install whose config already
// carries a bogus value the previous release happily persisted. The load path
// normalizes to the fail-closed tier (manual) and reports what it changed.
func TestNormalizeTrustModes(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Servers = []*ServerConfig{
		{Name: "bogus", URL: "https://example.com/mcp", Protocol: "streamable-http", TrustMode: "yolo"},
		{Name: "wrong-case", URL: "https://example.com/mcp", Protocol: "streamable-http", TrustMode: "Scan"},
		{Name: "good", URL: "https://example.com/mcp", Protocol: "streamable-http", TrustMode: "scan"},
		{Name: "inherit", URL: "https://example.com/mcp", Protocol: "streamable-http"},
	}

	changed := NormalizeTrustModes(cfg)

	require.Len(t, changed, 2, "only the two invalid values are normalized, got %+v", changed)
	assert.Equal(t, "bogus", changed[0].Server)
	assert.Equal(t, "yolo", changed[0].Original)
	assert.Equal(t, "wrong-case", changed[1].Server)
	assert.Equal(t, "Scan", changed[1].Original)

	assert.Equal(t, string(TrustModeManual), cfg.Servers[0].TrustMode, "an unrecognized value fails closed to manual")
	assert.Equal(t, string(TrustModeManual), cfg.Servers[1].TrustMode)
	assert.Equal(t, "scan", cfg.Servers[2].TrustMode, "valid values are untouched")
	assert.Equal(t, "", cfg.Servers[3].TrustMode, "empty still means inherit")

	assert.Empty(t, NormalizeTrustModes(cfg), "idempotent: a second pass changes nothing")
	assert.Empty(t, NormalizeTrustModes(nil), "nil-safe")
}

// TestLoadFromFile_BogusTrustModeDoesNotBlockStartup is the regression this PR
// would otherwise have introduced: an mcp_config.json carrying the bogus
// trust_mode that the PREVIOUS release persisted through the supported REST API
// made config.LoadFromFile fail, so `mcpproxy serve` refused to start at all.
func TestLoadFromFile_BogusTrustModeDoesNotBlockStartup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	raw := map[string]interface{}{
		"listen":   "127.0.0.1:0",
		"data_dir": dir,
		"mcpServers": []map[string]interface{}{{
			"name":       "srv",
			"url":        "https://example.com/mcp",
			"protocol":   "streamable-http",
			"enabled":    true,
			"trust_mode": "yolo",
		}},
	}
	data, err := json.Marshal(raw)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))

	cfg, err := LoadFromFile(path)
	require.NoError(t, err, "a bogus trust_mode must not make the daemon refuse to start")
	require.Len(t, cfg.Servers, 1)
	assert.Equal(t, string(TrustModeManual), cfg.Servers[0].TrustMode,
		"the bogus value is migrated to the fail-closed tier instead of bricking the load")
	assert.Empty(t, cfg.ValidateDetailed(), "the normalized config validates cleanly")
}

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// CFG-MODE-01: use the real loader and saver, including the on-disk enum.
func TestArgumentErrorModeLoadSave(t *testing.T) {
	for _, mode := range []string{"omitted", "", "detailed", "redacted", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			server := map[string]interface{}{"name": "upstream", "url": "http://127.0.0.1:1/mcp", "protocol": "streamable-http", "enabled": false}
			if mode != "omitted" {
				server["tool_argument_error_mode"] = mode
			}
			fixture, err := json.Marshal(map[string]interface{}{"data_dir": dir, "mcpServers": []interface{}{server}})
			require.NoError(t, err)
			path := filepath.Join(dir, "mcp_config.json")
			require.NoError(t, os.WriteFile(path, fixture, 0600))
			cfg, err := LoadFromFile(path)
			if mode == "unknown" {
				require.ErrorContains(t, err, "tool_argument_error_mode")
				after, readErr := os.ReadFile(path)
				require.NoError(t, readErr)
				require.Equal(t, fixture, after)
				return
			}
			require.NoError(t, err)
			require.NoError(t, SaveConfig(cfg, path))
			roundtrip, err := LoadFromFile(path)
			require.NoError(t, err)
			encoded, err := json.Marshal(roundtrip.Servers[0])
			require.NoError(t, err)
			var got map[string]interface{}
			require.NoError(t, json.Unmarshal(encoded, &got))
			if mode == "omitted" || mode == "" {
				require.NotContains(t, got, "tool_argument_error_mode")
			} else {
				require.Equal(t, mode, got["tool_argument_error_mode"])
			}
		})
	}
}

func TestArgumentErrorModeInvalidSaveDoesNotWrite(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Servers = []*ServerConfig{{Name: "upstream", URL: "http://127.0.0.1:1/mcp", Protocol: "streamable-http", ToolArgumentErrorMode: "redacted"}}
	path := filepath.Join(cfg.DataDir, "mcp_config.json")
	require.NoError(t, SaveConfig(cfg, path))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	cfg.Servers[0].ToolArgumentErrorMode = "unknown"
	require.ErrorContains(t, cfg.Validate(), "tool_argument_error_mode")
	require.NotEmpty(t, cfg.ValidateDetailed())
	require.NotEmpty(t, ValidateServerForLoad(cfg.Servers[0]))
	require.ErrorContains(t, SaveConfig(cfg, path), "tool_argument_error_mode")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

// CFG-MODE-02: exercise actual copies and merges, including auth preservation.
func TestArgumentErrorModeCopyMerge(t *testing.T) {
	base := &ServerConfig{Name: "upstream", Enabled: true, ToolArgumentErrorMode: "redacted", Headers: map[string]string{"Authorization": "synthetic-token"}, OAuth: &OAuthConfig{ClientID: "synthetic-client", ClientSecret: "synthetic-secret"}}
	copied := CopyServerConfig(base)
	require.Equal(t, base, copied)
	for _, tc := range []struct{ mode, want string }{{"", "redacted"}, {"detailed", "detailed"}, {"redacted", "redacted"}} {
		t.Run("patch_"+tc.mode, func(t *testing.T) {
			merged, _, err := MergeServerConfig(base, &ServerConfig{Enabled: true, ToolArgumentErrorMode: tc.mode}, DefaultMergeOptions())
			require.NoError(t, err)
			require.Equal(t, tc.want, merged.ToolArgumentErrorMode)
			require.True(t, merged.Enabled)
			require.Equal(t, base.Headers, merged.Headers)
			require.Equal(t, base.OAuth, merged.OAuth)
			require.Equal(t, "redacted", base.ToolArgumentErrorMode)
		})
	}
	_, _, err := MergeServerConfig(base, &ServerConfig{ToolArgumentErrorMode: "unknown"}, DefaultMergeOptions())
	require.ErrorContains(t, err, "tool_argument_error_mode")
}

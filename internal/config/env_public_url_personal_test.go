//go:build !server

package config_test

// Spec 107 T048 (FR-025, FR-040): MCPPROXY_PUBLIC_URL is the one nested
// `server_edition.*` key with an env alias, applied by the build-tagged
// applyServerEditionEnvOverrides (T051). The personal build's stub is a
// no-op: the block is an opaque carrier there, so the variable must leave
// the document's bytes untouched in meaning — the file's public_url survives
// and the env value never appears. This is the personal twin of
// TestFrontDoor_PublicURLEnvOverride (internal/serveredition/front_door_test.go);
// it is green on the PR-B base by construction and pins that T051's stub stays
// a no-op.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestPersonalBuild_PublicURLEnvIsIgnored(t *testing.T) {
	dir := t.TempDir()
	doc := map[string]any{
		"listen":   "127.0.0.1:8080",
		"data_dir": dir,
		"server_edition": map[string]any{
			"enabled":      true,
			"admin_emails": []string{"admin@example.com"},
			"oauth":        map[string]any{"provider": "google", "client_id": "cid", "client_secret": "csec"},
			"public_url":   "https://file.example",
		},
	}
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	path := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, os.WriteFile(path, data, 0600))

	t.Setenv("MCPPROXY_PUBLIC_URL", "https://env.example")

	cfg, err := config.LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.ServerEdition, "the carrier must survive the load")

	carrier, err := json.Marshal(cfg.ServerEdition)
	require.NoError(t, err)
	var block map[string]any
	require.NoError(t, json.Unmarshal(carrier, &block))
	assert.Equal(t, "https://file.example", block["public_url"], "the personal build never interprets the variable")
	assert.NotContains(t, string(carrier), "env.example")
}

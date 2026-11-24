//go:build unix

package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/stretchr/testify/require"
)

// Review #2 (PR #1884): a hardlink to mcp_config.json made before a save
// keeps the old inode, which still holds api_key, after SaveConfig renames a
// new file over the config. An upstream ${file:} read through that link must
// be refused.
func TestSaveConfig_HardlinkMadeBeforeSaveIsRefused(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	cfgPath := filepath.Join(dir, ConfigFileName)

	dirs := SecretFileDirs{allowed}
	cfg := DefaultConfig()
	cfg.DataDir = dir
	cfg.APIKey = "admin-key-before-save"
	cfg.SecretFileDirs = &dirs
	require.NoError(t, SaveConfig(cfg, cfgPath))

	link := filepath.Join(allowed, "token")
	require.NoError(t, os.Link(cfgPath, link))

	cfg.APIKey = "admin-key-after-save"
	require.NoError(t, SaveConfig(cfg, cfgPath))

	r := BuildSecretResolver(cfg, cfgPath, secret.ScopeUpstream)
	_, err := r.ExpandSecretRefs(context.Background(), "${file:"+link+"}")
	require.Error(t, err)
	require.True(t, secret.IsPolicyDenial(err), "want a policy denial, got %v", err)
}

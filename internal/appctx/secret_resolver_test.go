package appctx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/stretchr/testify/require"
)

// Review #8 (PR #1884): with --config pointing outside data_dir, the upstream
// resolver's deny list must name that file, so an upstream ${file:} cannot
// read the loaded config even when it sits under an allowed root.
func TestUpstreamSecretResolver_DeniesLoadedConfigPath(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed")
	dataDir := filepath.Join(dir, "data")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.MkdirAll(dataDir, 0o700))
	cfgPath := filepath.Join(allowed, "elsewhere.json")

	dirs := config.SecretFileDirs{allowed}
	seed := config.DefaultConfig()
	seed.DataDir = dataDir
	seed.SecretFileDirs = &dirs
	require.NoError(t, config.SaveConfig(seed, cfgPath))

	cfg, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)

	_, err = upstreamSecretResolver(cfg).ExpandSecretRefs(context.Background(), "${file:"+cfgPath+"}")
	require.Error(t, err)
	require.True(t, secret.IsPolicyDenial(err), "want a policy denial, got %v", err)
}

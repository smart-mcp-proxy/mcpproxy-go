package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// Spec 108-h T088: the tool-surface golden for `profiles`. The three existing
// goldens carry the tool (the declared additions, see toolsListAllowedAdditions);
// this adds the one fixture no golden had, read_only_mode, where the management
// tools are gone and `profiles` is still there.

const toolsListReadOnlySurface = "default_server_read_only"

func newToolsListProxy(t *testing.T, configure func(*config.Config)) *MCPProxyServer {
	t.Helper()
	tmpDir := t.TempDir()
	logger := zap.NewNop()
	sm, err := storage.NewManager(tmpDir, logger.Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { sm.Close() })
	idx, err := index.NewManager(tmpDir, logger)
	require.NoError(t, err)
	t.Cleanup(func() { idx.Close() })
	cfg := config.DefaultConfig()
	cfg.DataDir = tmpDir
	cfg.ToolsLimit = 20
	cfg.AllowServerAdd = true
	cfg.AllowServerRemove = true
	if configure != nil {
		configure(cfg)
	}
	um := upstream.NewManager(logger, cfg, nil, secret.NewResolver(), nil)
	cm, err := cache.NewManager(sm.GetDB(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { cm.Close() })
	tr := truncate.NewTruncator(0)
	return NewMCPProxyServer(sm, idx, um, cm, func() *truncate.Truncator { return tr }, logger, nil, false, cfg, nil)
}

func TestToolsList_ProfilesPresentUnderReadOnlyMode(t *testing.T) {
	proxy := newToolsListProxy(t, func(cfg *config.Config) { cfg.ReadOnlyMode = true })
	tools := map[string]json.RawMessage{}
	for name, st := range proxy.server.ListTools() {
		raw, err := json.Marshal(st.Tool)
		require.NoError(t, err)
		tools[name] = raw
	}
	require.Contains(t, tools, "profiles", "profiles is registered outside buildManagementTools, so read_only_mode keeps it")
	for _, gone := range []string{"upstream_servers", "quarantine_security", "search_servers", "list_registries"} {
		assert.NotContains(t, tools, gone, "%s is a management tool: read_only_mode removes it", gone)
	}

	got := renderToolsListGolden(t, tools)
	path := toolsListGoldenPath(toolsListReadOnlySurface)
	if outDir := os.Getenv(toolsListGoldenWriteEnv); outDir != "" {
		require.NoError(t, os.MkdirAll(outDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(outDir, toolsListReadOnlySurface+".json"), got, 0o644))
		t.Skipf("golden written to %s (%s set); comparison skipped", outDir, toolsListGoldenWriteEnv)
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "missing golden %s", path)
	if !bytes.Equal(got, normalizeGoldenEOL(raw)) {
		reportToolsListDiff(t, toolsListReadOnlySurface, normalizeGoldenEOL(raw), got)
		t.Errorf("read-only surface is not byte-identical to its golden")
	}

	// The profiles entry is the same definition the regular surface carries.
	regular := decodeToolsListGolden(t, toolsListGoldenPath("default_server"))
	assert.JSONEq(t, string(regular["profiles"]), string(tools["profiles"]))
}

func TestToolsList_ProfilesAnnotations(t *testing.T) {
	tool := buildProfilesTool()
	require.NotNil(t, tool.Annotations.DestructiveHint)
	require.NotNil(t, tool.Annotations.ReadOnlyHint)
	require.NotNil(t, tool.Annotations.OpenWorldHint)
	require.NotNil(t, tool.Annotations.IdempotentHint)
	assert.True(t, *tool.Annotations.DestructiveHint)
	assert.False(t, *tool.Annotations.ReadOnlyHint)
	assert.False(t, *tool.Annotations.OpenWorldHint)
	assert.False(t, *tool.Annotations.IdempotentHint)
	assert.Equal(t, "Manage profiles and client bindings", tool.Annotations.Title)
}

func TestToolsList_ProfilesNotOnDirectServer(t *testing.T) {
	proxy := newToolsListProxy(t, nil)
	require.NotNil(t, proxy.directServer)
	assert.NotContains(t, toolMapNames(proxy.directServer.ListTools()), "profiles")
}

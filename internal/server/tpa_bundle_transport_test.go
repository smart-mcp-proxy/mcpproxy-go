package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// fileBundle is a minimal, valid, RUNNABLE corpus so a successful load is
// distinguishable from the embedded default.
const fileBundle = `{
  "bundle_version": "0.1.0",
  "schema_version": "0.1.0",
  "signature_count": 1,
  "rules": [
    {"id": "TPA-2099-9001", "detector": "stdio_canary", "engine": "regex",
     "target": "tool_description", "pattern": "stdio-bundle-canary",
     "category": "prompt-injection", "level": "high", "confidence": 0.9}
  ],
  "skipped": []
}`

// TestTPABundleConfiguredInStdioMode is FR-019 in stdio transport:
// scanner.ConfigureBundle was only ever reached from startCustomHTTPServer, a
// branch Start() skips entirely when listen is empty. The trust_mode:scan gate
// is transport-independent, so a stdio deployment silently ran the build's
// EMBEDDED signatures while the operator had configured a corpus path.
func TestTPABundleConfiguredInStdioMode(t *testing.T) {
	t.Cleanup(func() { scanner.ConfigureBundle("", zap.NewNop()) })

	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "scanner-bundle.json")
	require.NoError(t, os.WriteFile(bundlePath, []byte(fileBundle), 0o600))

	cfg := config.DefaultConfig()
	cfg.DataDir = dir
	cfg.Listen = "" // stdio transport
	cfg.Security = &config.SecurityConfig{TPABundlePath: bundlePath}

	srv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })

	info := scanner.BundleStatus()
	assert.Equal(t, scanner.BundleSourceFile, info.Source,
		"the configured corpus must be installed regardless of transport")
	assert.Equal(t, bundlePath, info.Path)
	assert.Equal(t, 1, info.RunnableRules)
	assert.Empty(t, info.LoadError)
}

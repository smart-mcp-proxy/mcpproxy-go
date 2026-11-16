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

// fileBundle is a valid, RUNNABLE corpus distinguishable from the embedded
// default: the seed signatures plus one canary rule. It carries the seed
// because the real activation self-check (installed by NewServer) scores
// every file drop on the eval corpus, whose tpa_* samples only the seed
// signatures catch (Spec 087 FR-008, Spec 101 SC-005); a one-rule corpus is
// refused as gate_failure.
func fileBundle(t *testing.T) []byte {
	t.Helper()
	return seedDerivedBundle(t, seedFixtureRule{
		ID: "TPA-2099-9001", Detector: "stdio_canary",
		Pattern: "stdio-bundle-canary", Category: "prompt-injection",
	})
}

// TestTPABundleConfiguredInStdioMode is FR-019 in stdio transport:
// scanner.ConfigureBundle was only ever reached from startCustomHTTPServer, a
// branch Start() skips entirely when listen is empty. The trust_mode:scan gate
// is transport-independent, so a stdio deployment silently ran the build's
// EMBEDDED signatures while the operator had configured a corpus path.
func TestTPABundleConfiguredInStdioMode(t *testing.T) {
	t.Cleanup(func() { scanner.ConfigureBundle("", zap.NewNop()) })

	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "scanner-bundle.json")
	require.NoError(t, os.WriteFile(bundlePath, fileBundle(t), 0o600))

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
	assert.Equal(t, seedRunnableRules(t)+1, info.RunnableRules, "the seed rules plus the canary")
	assert.Empty(t, info.LoadError)
}

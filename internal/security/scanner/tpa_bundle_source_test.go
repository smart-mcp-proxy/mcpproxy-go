package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// miniBundle is a valid single-rule bundle used to prove a FILE-sourced corpus
// really replaces the embedded default (spec 086 FR-019: the bundle path MUST
// NOT be hardcoded).
const miniBundle = `{
  "bundle_version": "0.1.0",
  "schema_version": "0.1.0",
  "generated_at": "2026-07-30T12:00:00Z",
  "signature_count": 1,
  "rules": [
    {"id": "TPA-2099-0001", "detector": "file_only", "engine": "regex",
     "target": "tool_description", "pattern": "file-bundle-canary",
     "category": "prompt-injection", "level": "high", "confidence": 0.9}
  ],
  "skipped": []
}`

// writeBundle writes content to a temp file and returns its path.
func writeBundle(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scanner-bundle.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// restoreEmbeddedBundle resets the process-wide active bundle so a test's
// file-sourced corpus never leaks into the rest of the package.
func restoreEmbeddedBundle(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { ConfigureBundle("", zap.NewNop()) })
}

// TestBundleStatus_DefaultsToEmbedded is the visibility half of GH #938 finding
// 2: with no configuration, an operator must still be able to ask which
// signature corpus is running.
func TestBundleStatus_DefaultsToEmbedded(t *testing.T) {
	restoreEmbeddedBundle(t)
	ConfigureBundle("", zap.NewNop())

	info := BundleStatus()
	assert.Equal(t, BundleSourceEmbedded, info.Source)
	assert.Equal(t, "0.1.0", info.BundleVersion)
	assert.Positive(t, info.RunnableRules, "the embedded bundle must contribute runnable rules")
	assert.Positive(t, info.SignatureCount, "signature_count must be surfaced")
	assert.NotEmpty(t, info.Fingerprint, "a corpus fingerprint is the identity signal an operator compares")
	assert.False(t, info.LoadedAt.IsZero())
	assert.Empty(t, info.LoadError)
}

// TestConfigureBundle_FromFile proves the configured path actually replaces the
// embedded corpus (FR-019) and that its rules are the ones that fire.
func TestConfigureBundle_FromFile(t *testing.T) {
	restoreEmbeddedBundle(t)
	path := writeBundle(t, miniBundle)

	ConfigureBundle(path, zap.NewNop())

	info := BundleStatus()
	assert.Equal(t, BundleSourceFile, info.Source)
	assert.Equal(t, path, info.Path)
	assert.Equal(t, 1, info.RunnableRules)
	assert.Equal(t, 1, info.SignatureCount)
	assert.Equal(t, "2026-07-30T12:00:00Z", info.GeneratedAt, "freshness signal must be surfaced when the bundle carries one")
	assert.Empty(t, info.LoadError)

	check := defaultBundleCheck()
	require.NotNil(t, check)
	sigs := check.Inspect(detect.ToolView{Server: "s", Name: "t", Description: "harmless file-bundle-canary here"}, detect.RegistryView{})
	require.Len(t, sigs, 1, "the file-sourced rule must be the live corpus")
	assert.Equal(t, "tpa.TPA-2099-0001.file_only", sigs[0].CheckID)
}

// TestConfigureBundle_BadPathKeepsLastKnownGood is the fail-closed rule: a
// missing/broken configured bundle must never leave the scanner with NO corpus,
// and the failure must be visible rather than silent.
func TestConfigureBundle_BadPathKeepsLastKnownGood(t *testing.T) {
	restoreEmbeddedBundle(t)
	ConfigureBundle("", zap.NewNop())
	embedded := BundleStatus()

	ConfigureBundle(filepath.Join(t.TempDir(), "does-not-exist.json"), zap.NewNop())

	info := BundleStatus()
	assert.Equal(t, BundleSourceEmbedded, info.Source, "a failed file load must keep the last-known-good corpus")
	assert.Equal(t, embedded.RunnableRules, info.RunnableRules)
	assert.NotEmpty(t, info.LoadError, "the failure must be visible to the operator")
	assert.Contains(t, info.LoadError, "does-not-exist.json")
	require.NotNil(t, defaultBundleCheck(), "scanning must continue with the embedded corpus")
}

// TestConfigureBundle_UnsupportedVersionRejected keeps the contract §4
// version gate meaningful for file-sourced bundles too.
func TestConfigureBundle_UnsupportedVersionRejected(t *testing.T) {
	restoreEmbeddedBundle(t)
	ConfigureBundle("", zap.NewNop())

	path := writeBundle(t, `{"bundle_version":"9.9.0","schema_version":"9.9.0","rules":[],"skipped":[]}`)
	ConfigureBundle(path, zap.NewNop())

	info := BundleStatus()
	assert.Equal(t, BundleSourceEmbedded, info.Source)
	assert.Contains(t, info.LoadError, "unsupported bundle_version")
}

// TestConfigureBundle_LogsThroughInjectedLogger is the other half of GH #938
// finding 2: the one status report was emitted through the UNCONFIGURED global
// zap.L(), so it reached no console and no log file. It must go through the
// injected logger.
func TestConfigureBundle_LogsThroughInjectedLogger(t *testing.T) {
	restoreEmbeddedBundle(t)
	core, logs := observer.New(zapcore.DebugLevel)
	ConfigureBundle("", zap.New(core))

	entries := logs.FilterMessageSnippet("TPA scanner bundle").All()
	require.NotEmpty(t, entries, "loading the bundle must be logged through the injected logger")
	fields := entries[0].ContextMap()
	assert.Contains(t, fields, "runnable_rules")
	assert.Contains(t, fields, "source")
	assert.Contains(t, fields, "bundle_version")
}

// TestConfigureBundle_HotReload proves the path is re-readable at runtime
// (FR-019 hot-reload): a second call with a different file swaps the corpus.
func TestConfigureBundle_HotReload(t *testing.T) {
	restoreEmbeddedBundle(t)
	path := writeBundle(t, miniBundle)
	ConfigureBundle(path, zap.NewNop())
	require.Equal(t, BundleSourceFile, BundleStatus().Source)

	ConfigureBundle("", zap.NewNop())
	assert.Equal(t, BundleSourceEmbedded, BundleStatus().Source,
		"clearing the configured path must fall back to the embedded corpus")
	assert.Empty(t, BundleStatus().LoadError)
}

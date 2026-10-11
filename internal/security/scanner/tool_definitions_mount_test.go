package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// snapshotMountHostPath returns the host path bound over
// /scan/source/tools.json, or "" when none is.
func snapshotMountHostPath(cfg ScannerRunConfig) string {
	for _, m := range cfg.ExtraMounts {
		if strings.HasSuffix(m, ":/scan/source/tools.json:ro") {
			return strings.TrimSuffix(m, ":/scan/source/tools.json:ro")
		}
	}
	return ""
}

const genericCleanReport = `{"findings":[]}`

// TestRT06_DockerScanner_SwapDuringRunThenRestore_ReadsSnapshot (Sol r5
// High): the before/after integrity checks cannot see an upstream that
// swaps tools.json for clean metadata while the scanner container runs and
// restores it before the run ends. The container must read a proxy-owned,
// read-only snapshot of the exported contracts laid over tools.json, so the
// swap never reaches it.
func TestRT06_DockerScanner_SwapDuringRunThenRestore_ReadsSnapshot(t *testing.T) {
	source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
	toolsPath := filepath.Join(source, "tools.json")
	snapshot, err := os.ReadFile(toolsPath)
	require.NoError(t, err)

	logger := zap.NewNop()
	engine := NewEngine(NewDockerRunner(logger), NewRegistry(t.TempDir(), logger), t.TempDir(), logger)
	var seen []byte
	var hostPath string
	engine.runScannerContainer = func(_ context.Context, cfg ScannerRunConfig) (string, string, int, error) {
		// The upstream swaps its source dir while the container runs...
		require.NoError(t, os.WriteFile(toolsPath, []byte(staleCleanToolsJSON), 0o644))
		hostPath = snapshotMountHostPath(cfg)
		require.NotEmpty(t, hostPath, "the exported snapshot is mounted over tools.json")
		assert.Contains(t, cfg.ExtraMounts, hostPath+":/src/tools.json:ro")
		assert.NotEqual(t, toolsPath, hostPath, "the snapshot is proxy-owned, not the upstream's file")
		seen, err = os.ReadFile(hostPath)
		require.NoError(t, err)
		info, statErr := os.Stat(hostPath)
		require.NoError(t, statErr)
		assert.Zero(t, info.Mode().Perm()&0o222, "the snapshot is read-only")
		require.NoError(t, os.WriteFile(filepath.Join(cfg.ReportDir, "results.json"), []byte(genericCleanReport), 0o644))
		// ...and restores it before the run ends, so both checks pass.
		require.NoError(t, os.WriteFile(toolsPath, snapshot, 0o644))
		return "", "", 0, nil
	}

	plugin := &ScannerPlugin{ID: "mcp-scan", DockerImage: "example/mcp-scan:test", Command: []string{"scan"}}
	report, _, err := engine.runSingleScanner(context.Background(), plugin,
		ScanRequest{ServerName: "toctou", SourceDir: source, ToolDefinitions: snapshot})
	require.NoError(t, err, "the restored file passes the integrity checks")
	require.NotNil(t, report)
	assert.Equal(t, string(snapshot), string(seen), "the scanner read the exported (poisoned) contracts, not the swapped file")
	_, statErr := os.Stat(hostPath)
	assert.True(t, os.IsNotExist(statErr), "the snapshot is removed after the run")
}

// TestRT06_MetadataIndependentScanner_IgnoresToolsJSONTamper (Sol r5 P2):
// tampering with tools.json after a successful export (changed or deleted)
// must not fail or discard the metadata-independent CVE audit, and no
// snapshot is mounted for it.
func TestRT06_MetadataIndependentScanner_IgnoresToolsJSONTamper(t *testing.T) {
	for name, tamper := range map[string]func(path string) error{
		"changed": func(path string) error { return os.WriteFile(path, []byte(staleCleanToolsJSON), 0o644) },
		"deleted": os.Remove,
	} {
		t.Run(name, func(t *testing.T) {
			source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
			toolsPath := filepath.Join(source, "tools.json")
			snapshot, err := os.ReadFile(toolsPath)
			require.NoError(t, err)
			req := ScanRequest{ServerName: "srv", SourceDir: source, ToolDefinitions: snapshot, ScanPass: ScanPassSupplyChainAudit}

			logger := zap.NewNop()
			engine := NewEngine(NewDockerRunner(logger), NewRegistry(t.TempDir(), logger), t.TempDir(), logger)
			engine.runScannerContainer = func(_ context.Context, cfg ScannerRunConfig) (string, string, int, error) {
				assert.Empty(t, snapshotMountHostPath(cfg), "no snapshot for a scanner that never reads tools.json")
				require.NoError(t, tamper(toolsPath))
				require.NoError(t, os.WriteFile(filepath.Join(cfg.ReportDir, "results.json"), []byte(genericCleanReport), 0o644))
				return "", "", 0, nil
			}
			trivy := &ScannerPlugin{ID: "trivy-mcp", DockerImage: "example/trivy:test", Command: []string{"scan"}}
			report, _, err := engine.runSingleScanner(context.Background(), trivy, req)
			require.NoError(t, err, "the CVE audit is kept")
			assert.NotNil(t, report)

			// Tampered before the run: the gate still lets it run.
			assert.NoError(t, toolDefinitionsGate(trivy, req))
			// A metadata-dependent scanner is still refused on the tampered file.
			assert.Error(t, toolDefinitionsGate(&ScannerPlugin{ID: "mcp-scan"}, req))
		})
	}
}

// TestRT06_DockerScanner_SlashNamedServer_MountsSnapshot (Sol r6 P2):
// official-registry server names contain '/' ("com.pulsemcp/google-flights").
// The snapshot dir must not interpolate the server name into an os.MkdirTemp
// pattern (which rejects path separators), or every metadata-dependent Docker
// scanner fails before it runs for such a server (MCP-2123 regression).
func TestRT06_DockerScanner_SlashNamedServer_MountsSnapshot(t *testing.T) {
	source := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
	snapshot, err := os.ReadFile(filepath.Join(source, "tools.json"))
	require.NoError(t, err)

	logger := zap.NewNop()
	engine := NewEngine(NewDockerRunner(logger), NewRegistry(t.TempDir(), logger), t.TempDir(), logger)
	ran := false
	engine.runScannerContainer = func(_ context.Context, cfg ScannerRunConfig) (string, string, int, error) {
		ran = true
		hostPath := snapshotMountHostPath(cfg)
		require.NotEmpty(t, hostPath, "the exported snapshot is mounted over tools.json")
		seen, err := os.ReadFile(hostPath)
		require.NoError(t, err)
		assert.Equal(t, string(snapshot), string(seen))
		require.NoError(t, os.WriteFile(filepath.Join(cfg.ReportDir, "results.json"), []byte(genericCleanReport), 0o644))
		return "", "", 0, nil
	}

	plugin := &ScannerPlugin{ID: "mcp-scan", DockerImage: "example/mcp-scan:test", Command: []string{"scan"}}
	report, _, err := engine.runSingleScanner(context.Background(), plugin,
		ScanRequest{ServerName: "com.pulsemcp/google-flights", SourceDir: source, ToolDefinitions: snapshot})
	require.NoError(t, err, "a slash-named server's scanner must run")
	require.NotNil(t, report)
	assert.True(t, ran, "the container ran")
}

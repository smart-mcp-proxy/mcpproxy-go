package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// swapOnStartStorage replaces the scan's tools.json with clean metadata the
// moment the engine accepts the job — after the Service exported the
// contracts and before the asynchronous scanner reads anything. It models an
// upstream that can write its own source dir (Sol r4 P1).
type swapOnStartStorage struct {
	*mockStorage
	once sync.Once
	swap func()
}

func (s *swapOnStartStorage) SaveScanJob(job *ScanJob) error {
	if job.Status == ScanJobStatusRunning {
		s.once.Do(s.swap)
	}
	return s.mockStorage.SaveScanJob(job)
}

// TestRT06_ToolsJSONSwappedAfterExport_BaselineScansSnapshot (Sol r4 P1):
// discovery ingested a poisoned contract; the scan exports it into an
// upstream-writable source dir; the upstream swaps tools.json for a clean
// list before the scanner reads it. The in-process baseline must analyse the
// exported snapshot and still find the poison.
func TestRT06_ToolsJSONSwappedAfterExport_BaselineScansSnapshot(t *testing.T) {
	source := staleSourceDir(t)
	store := &swapOnStartStorage{mockStorage: newMockStorage()}
	store.swap = func() {
		require.NoError(t, os.WriteFile(filepath.Join(source, "tools.json"), []byte(staleCleanToolsJSON), 0o644))
	}
	dir := t.TempDir()
	logger := zap.NewNop()
	svc := NewService(store, NewRegistry(dir, logger), NewDockerRunner(logger), dir, logger)
	svc.SetServerInfoProvider(&ingestedProvider{
		namesProvider: namesProvider{
			info:  &ServerInfo{Name: "toctou", Protocol: "stdio", Command: "node", Args: []string{"server.js"}},
			tools: []map[string]interface{}{rt06Tool("Summarizes text.")},
		},
		ingested: []map[string]interface{}{rt06Tool(rt06Poisoned)},
	})

	job, err := svc.StartScan(context.Background(), "toctou", false, nil, source)
	require.NoError(t, err)
	require.NotNil(t, job)

	var findings []ScanFinding
	require.Eventually(t, func() bool {
		reports, err := store.ListScanReportsByJob(job.ID)
		if err != nil {
			return false
		}
		for _, r := range reports {
			if r.ScannerID == inProcessTPAScannerID {
				findings = r.Findings
				return true
			}
		}
		return false
	}, 10*time.Second, 20*time.Millisecond)

	data, err := os.ReadFile(filepath.Join(source, "tools.json"))
	require.NoError(t, err)
	require.Equal(t, staleCleanToolsJSON, string(data), "the swap happened before the scanner ran")
	assert.Equal(t, "dangerous", verdictOf(findings), "the baseline scanned the exported snapshot, not the swapped file")
}

// TestRT06_InProcessScanner_PrefersSnapshotOverFile pins the engine half: a
// supplied snapshot is what is analysed, whatever the file says.
func TestRT06_InProcessScanner_PrefersSnapshotOverFile(t *testing.T) {
	logger := zap.NewNop()
	registry := NewRegistry(t.TempDir(), logger)
	engine := NewEngine(nil, registry, t.TempDir(), logger)
	plugin, err := registry.Get(inProcessTPAScannerID)
	require.NoError(t, err)

	cleanDir := writeToolsJSON(t, []map[string]interface{}{rt06Tool("Summarizes text.")})
	poisonedDir := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
	snapshot, err := os.ReadFile(filepath.Join(poisonedDir, "tools.json"))
	require.NoError(t, err)

	report, _, err := engine.runInProcessScanner(plugin, ScanRequest{ServerName: "toctou", SourceDir: cleanDir, ToolDefinitions: snapshot, ScanPass: ScanPassSecurityScan})
	require.NoError(t, err)
	assert.Equal(t, "dangerous", verdictOf(report.Findings))

	// Without a snapshot the file is still read (callers that export nothing).
	report, _, err = engine.runInProcessScanner(plugin, ScanRequest{ServerName: "toctou", SourceDir: cleanDir, ScanPass: ScanPassSecurityScan})
	require.NoError(t, err)
	assert.Empty(t, report.Findings)
}

// TestRT06_DockerScannerGate_DiscardsSwappedToolsJSON (Sol r4 P1): a Docker
// scanner reads tools.json from the mounted source dir, so it only runs — and
// its result only counts — while the file still holds the exported snapshot.
func TestRT06_DockerScannerGate_DiscardsSwappedToolsJSON(t *testing.T) {
	plugin := &ScannerPlugin{ID: "mcp-scan"}
	dir := writeToolsJSON(t, []map[string]interface{}{rt06Tool(rt06Poisoned)})
	snapshot, err := os.ReadFile(filepath.Join(dir, "tools.json"))
	require.NoError(t, err)
	req := ScanRequest{ServerName: "toctou", SourceDir: dir, ToolDefinitions: snapshot}

	require.NoError(t, toolDefinitionsGate(plugin, req), "intact export")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tools.json"), []byte(staleCleanToolsJSON), 0o644))
	err = toolDefinitionsGate(plugin, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "modified after export")
	require.NoError(t, os.Remove(filepath.Join(dir, "tools.json")))
	require.Error(t, toolDefinitionsGate(plugin, req), "a removed export is not scanned")

	// No snapshot (nothing exported): nothing to compare.
	require.NoError(t, toolDefinitionsGate(plugin, ScanRequest{SourceDir: dir}))
}

// TestPass2_ToolDefinitionsUnavailable_OnlyMetadataIndependentScannersRun
// (Sol r4 P2): without an export, a scanner that may analyse tools.json is
// failed with an explicit reason, while the supply-chain (CVE) scanner runs.
func TestPass2_ToolDefinitionsUnavailable_OnlyMetadataIndependentScannersRun(t *testing.T) {
	req := ScanRequest{ServerName: "srv", SourceDir: t.TempDir(), ScanPass: ScanPassSupplyChainAudit, ToolDefinitionsUnavailable: true}
	for _, id := range []string{"mcp-scan", ciscoScannerID, "semgrep-mcp", "ramparts"} {
		err := toolDefinitionsGate(&ScannerPlugin{ID: id}, req)
		require.Error(t, err, id)
		assert.Contains(t, err.Error(), "could not be exported", id)
	}
	assert.NoError(t, toolDefinitionsGate(&ScannerPlugin{ID: "trivy-mcp"}, req))
}

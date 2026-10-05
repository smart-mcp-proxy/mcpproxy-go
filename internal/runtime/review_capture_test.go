package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func TestShouldCaptureReviewDefinitionsAfterScan(t *testing.T) {
	exported := &scanner.ScanContext{SourceMethod: "tool_definitions_only", ToolsExported: 3}

	build := func(t *testing.T, server *config.ServerConfig) *Runtime {
		return setupQuarantineRuntime(t, nil, []*config.ServerConfig{server})
	}
	quarantined := func() *config.ServerConfig {
		return &config.ServerConfig{Name: "srv", Enabled: true, Quarantined: true}
	}

	t.Run("eligible: quarantined, no records, completed scan that exported tools", func(t *testing.T) {
		rt := build(t, quarantined())
		seedReviewScanJob(t, rt, "srv", "j1", scanner.ScanJobStatusCompleted, exported, nil)
		require.True(t, rt.ShouldCaptureReviewDefinitionsAfterScan("srv"))
	})

	t.Run("not eligible: trusted server", func(t *testing.T) {
		rt := build(t, &config.ServerConfig{Name: "srv", Enabled: true})
		seedReviewScanJob(t, rt, "srv", "j1", scanner.ScanJobStatusCompleted, exported, nil)
		require.False(t, rt.ShouldCaptureReviewDefinitionsAfterScan("srv"))
	})

	t.Run("not eligible: disabled quarantined server", func(t *testing.T) {
		rt := build(t, &config.ServerConfig{Name: "srv", Enabled: false, Quarantined: true})
		seedReviewScanJob(t, rt, "srv", "j1", scanner.ScanJobStatusCompleted, exported, nil)
		require.False(t, rt.ShouldCaptureReviewDefinitionsAfterScan("srv"))
	})

	t.Run("not eligible: records already captured", func(t *testing.T) {
		rt := build(t, quarantined())
		seedReviewScanJob(t, rt, "srv", "j1", scanner.ScanJobStatusCompleted, exported, nil)
		require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "srv", ToolName: "a", Status: storage.ToolApprovalStatusPending,
		}))
		require.False(t, rt.ShouldCaptureReviewDefinitionsAfterScan("srv"))
	})

	t.Run("not eligible: scan exported no tools", func(t *testing.T) {
		rt := build(t, quarantined())
		seedReviewScanJob(t, rt, "srv", "j1", scanner.ScanJobStatusCompleted, &scanner.ScanContext{SourceMethod: "working_dir", TotalFiles: 5}, nil)
		require.False(t, rt.ShouldCaptureReviewDefinitionsAfterScan("srv"))
	})

	t.Run("not eligible: newest job running or failed", func(t *testing.T) {
		for _, status := range []string{scanner.ScanJobStatusRunning, scanner.ScanJobStatusFailed} {
			rt := build(t, quarantined())
			seedReviewScanJob(t, rt, "srv", "j1", status, exported, nil)
			require.False(t, rt.ShouldCaptureReviewDefinitionsAfterScan("srv"), status)
		}
	})

	t.Run("not eligible: an older completed job does not count when the newest failed", func(t *testing.T) {
		rt := build(t, quarantined())
		seedReviewScanJob(t, rt, "srv", "old", scanner.ScanJobStatusCompleted, exported, nil)
		newer := &scanner.ScanJob{
			ID: "new", ServerName: "srv", Status: scanner.ScanJobStatusFailed, ScanPass: scanner.ScanPassSecurityScan,
			StartedAt: time.Now(), ScanContext: exported,
		}
		require.NoError(t, rt.storageManager.SaveScanJob(newer))
		require.False(t, rt.ShouldCaptureReviewDefinitionsAfterScan("srv"))
	})

	t.Run("not eligible: no scan job or unknown server", func(t *testing.T) {
		rt := build(t, quarantined())
		require.False(t, rt.ShouldCaptureReviewDefinitionsAfterScan("srv"))
		require.False(t, rt.ShouldCaptureReviewDefinitionsAfterScan("ghost"))
		require.False(t, rt.ShouldCaptureReviewDefinitionsAfterScan(""))
	})
}

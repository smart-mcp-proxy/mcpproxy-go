package runtime

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestReviewDefaultAllowed pins the fail-closed default selection (D43.2):
// the core decides which tools the review screens pre-check.
func TestReviewDefaultAllowed(t *testing.T) {
	tiers := []contracts.Tier{
		contracts.TierRead, contracts.TierWrite, contracts.TierDestructive,
		contracts.TierUnannotated, contracts.TierUnknown,
	}
	statuses := []string{
		storage.ToolApprovalStatusPending, storage.ToolApprovalStatusChanged, storage.ToolApprovalStatusApproved,
	}
	scanVerdicts := []string{"clean", "not_scanned", "warnings", "dangerous"}

	for _, tier := range tiers {
		for _, status := range statuses {
			for _, verdict := range scanVerdicts {
				for _, disabled := range []bool{false, true} {
					for _, held := range []string{"", "scan gate"} {
						tool := ReviewTool{Tier: tier, ApprovalStatus: status, ScanVerdict: verdict, Disabled: disabled, HeldReason: held}
						var want bool
						switch {
						case disabled:
							want = false
						case status == storage.ToolApprovalStatusApproved:
							want = true
						default:
							want = tier == contracts.TierRead && verdict == "clean" && held == ""
						}
						require.Equal(t, want, reviewDefaultAllowed(tool),
							"tier=%s status=%s verdict=%s disabled=%v held=%q", tier, status, verdict, disabled, held)
					}
				}
			}
		}
	}

	// A tool held by the scan gate can carry HeldVerdict "clean" (only coverage
	// failed), which surfaces as scan_verdict "clean". It must still start
	// unchecked.
	require.False(t, reviewDefaultAllowed(ReviewTool{
		Tier: contracts.TierRead, ApprovalStatus: storage.ToolApprovalStatusPending, ScanVerdict: "clean", HeldReason: "coverage failed",
	}))
	require.True(t, reviewDefaultAllowed(ReviewTool{
		Tier: contracts.TierRead, ApprovalStatus: storage.ToolApprovalStatusPending, ScanVerdict: "clean",
	}))
}

func TestReviewTool_DefaultAllowedSerialisedWhenFalse(t *testing.T) {
	raw, err := json.Marshal(ReviewTool{Name: "x"})
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	value, present := decoded["default_allowed"]
	require.True(t, present, "default_allowed must always be present so an old core is distinguishable from false")
	require.Equal(t, false, value)
}

func saveTieredRecord(t *testing.T, rt *Runtime, server, tool, status string, readOnly, destructive *bool) {
	t.Helper()
	var annotations *config.ToolAnnotations
	if readOnly != nil || destructive != nil {
		annotations = &config.ToolAnnotations{ReadOnlyHint: readOnly, DestructiveHint: destructive}
	}
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: server, ToolName: tool, Status: status, CurrentDescription: "d",
		CurrentHash: "h-" + tool, ApprovedHash: "h-" + tool, CurrentAnnotations: annotations,
	}))
}

func defaultsOf(review *ServerReview) map[string]bool {
	out := map[string]bool{}
	for _, tool := range review.Tools {
		out[tool.Name] = tool.DefaultAllowed
	}
	return out
}

func TestServerReview_DefaultAllowedFollowsCoverage(t *testing.T) {
	yes, no := true, false
	newRuntime := func(t *testing.T, quarantined bool) *Runtime {
		return setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "srv", Enabled: true, Quarantined: quarantined}})
	}
	seedMixed := func(t *testing.T, rt *Runtime) {
		saveTieredRecord(t, rt, "srv", "read_a", storage.ToolApprovalStatusPending, &yes, nil)
		saveTieredRecord(t, rt, "srv", "write_a", storage.ToolApprovalStatusPending, &no, nil)
		saveTieredRecord(t, rt, "srv", "delete_a", storage.ToolApprovalStatusPending, &no, &yes)
		saveTieredRecord(t, rt, "srv", "plain_a", storage.ToolApprovalStatusPending, nil, nil)
	}
	toolsCtx := func(names ...string) *scanner.ScanContext {
		return &scanner.ScanContext{SourceMethod: "tool_definitions_only", ToolsExported: len(names), ToolNames: names}
	}

	t.Run("current: only the clean read tool starts allowed", func(t *testing.T) {
		rt := newRuntime(t, true)
		seedMixed(t, rt)
		seedReviewScanJob(t, rt, "srv", "j1", scanner.ScanJobStatusCompleted, toolsCtx("read_a", "write_a", "delete_a", "plain_a"), nil)
		review := reviewOf(t, rt, "srv")
		require.Equal(t, ReviewScanCoverageCurrent, review.Server.Scan.Coverage)
		require.Equal(t, map[string]bool{"read_a": true, "write_a": false, "delete_a": false, "plain_a": false}, defaultsOf(review))
	})

	t.Run("none: nothing starts allowed", func(t *testing.T) {
		rt := newRuntime(t, true)
		seedMixed(t, rt)
		review := reviewOf(t, rt, "srv")
		require.Equal(t, ReviewScanCoverageNone, review.Server.Scan.Coverage)
		require.Equal(t, map[string]bool{"read_a": false, "write_a": false, "delete_a": false, "plain_a": false}, defaultsOf(review))
	})

	t.Run("tools_not_scanned: nothing starts allowed", func(t *testing.T) {
		rt := newRuntime(t, true)
		seedMixed(t, rt)
		seedReviewScanJob(t, rt, "srv", "j1", scanner.ScanJobStatusCompleted, &scanner.ScanContext{SourceMethod: "working_dir", TotalFiles: 5}, nil)
		review := reviewOf(t, rt, "srv")
		require.Equal(t, ReviewScanCoverageToolsNotScanned, review.Server.Scan.Coverage)
		for name, allowed := range defaultsOf(review) {
			require.False(t, allowed, name)
		}
	})

	t.Run("stale: only the tool the scan did not cover starts unchecked", func(t *testing.T) {
		rt := newRuntime(t, true)
		saveTieredRecord(t, rt, "srv", "read_a", storage.ToolApprovalStatusPending, &yes, nil)
		saveTieredRecord(t, rt, "srv", "read_b", storage.ToolApprovalStatusPending, &yes, nil)
		seedReviewScanJob(t, rt, "srv", "j1", scanner.ScanJobStatusCompleted, toolsCtx("read_a"), nil)
		review := reviewOf(t, rt, "srv")
		require.Equal(t, ReviewScanCoverageStale, review.Server.Scan.Coverage)
		require.Equal(t, map[string]bool{"read_a": true, "read_b": false}, defaultsOf(review))
	})

	t.Run("dangerous finding unchecks the tool", func(t *testing.T) {
		rt := newRuntime(t, true)
		saveTieredRecord(t, rt, "srv", "read_a", storage.ToolApprovalStatusPending, &yes, nil)
		saveTieredRecord(t, rt, "srv", "read_b", storage.ToolApprovalStatusPending, &yes, nil)
		seedReviewScanJob(t, rt, "srv", "j1", scanner.ScanJobStatusCompleted, toolsCtx("read_a", "read_b"), []scanner.ScanFinding{
			{RuleID: "TPA-1", Severity: "critical", ThreatLevel: scanner.ThreatLevelDangerous, Title: "hidden instruction", Location: "tool:read_b", Scanner: "tpa"},
		})
		require.Equal(t, map[string]bool{"read_a": true, "read_b": false}, defaultsOf(reviewOf(t, rt, "srv")))
	})

	t.Run("approved and enabled stays allowed on a requarantined server", func(t *testing.T) {
		rt := newRuntime(t, true)
		saveTieredRecord(t, rt, "srv", "write_a", storage.ToolApprovalStatusApproved, &no, nil)
		review := reviewOf(t, rt, "srv")
		require.Equal(t, map[string]bool{"write_a": true}, defaultsOf(review))
	})
}

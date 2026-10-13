package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/qapilot"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Quarantine/approval testing pilot — the dispatch gate with a real upstream
// (QP-02, QP-16), the MCP surface's inability to release a server (QP-06)
// and the security-approval gate (QP-04).

func qapilotResultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, res)
	require.NotEmpty(t, res.Content)
	raw, err := json.Marshal(res.Content[0])
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	text, _ := m["text"].(string)
	return text
}

// TestQAPilot_DispatchCountingUpstream proves, with an upstream that counts
// the calls it receives, that the dispatch verdicts the model predicts are
// what the proxy does: a refused call never reaches the upstream, an
// admitted call reaches it exactly once. The cells are the policy's
// server-level and tool-level verdicts for one read-tier tool.
func TestQAPilot_DispatchCountingUpstream(t *testing.T) {
	qapilot.Track(t, "dispatch/counting-upstream", "QP-02", "QP-16")
	cells := []struct {
		name        string
		quarantined bool
		enabled     bool
		approval    string
		disabled    bool
		trustAuto   bool
		expect      qapilot.DispatchClass
		marker      string // substring the refusal carries, "" for admission
	}{
		{"quarantined server, approved tool", true, true, storage.ToolApprovalStatusApproved, false, false, qapilot.DispatchServerQuarantined, "QUARANTINED_SERVER_BLOCKED"},
		{"quarantined server, pending tool", true, true, storage.ToolApprovalStatusPending, false, false, qapilot.DispatchServerQuarantined, "QUARANTINED_SERVER_BLOCKED"},
		{"disabled server, approved tool", false, false, storage.ToolApprovalStatusApproved, false, false, qapilot.DispatchServerDisabled, "TOOL_BLOCKED"},
		{"released server, pending tool", false, true, storage.ToolApprovalStatusPending, false, false, qapilot.DispatchPendingApproval, "TOOL_QUARANTINED"},
		{"released server, changed tool", false, true, storage.ToolApprovalStatusChanged, false, false, qapilot.DispatchChanged, "TOOL_QUARANTINED"},
		{"released server, blocked tool", false, true, storage.ToolApprovalStatusApproved, true, false, qapilot.DispatchBlockedByUser, "TOOL_BLOCKED"},
		{"released server, approved tool", false, true, storage.ToolApprovalStatusApproved, false, false, qapilot.DispatchReady, ""},
		{"auto-trust server, pending tool", false, true, storage.ToolApprovalStatusPending, false, true, qapilot.DispatchReady, ""},
		{"auto-trust server, blocked tool", false, true, storage.ToolApprovalStatusApproved, true, true, qapilot.DispatchBlockedByUser, "TOOL_BLOCKED"},
	}
	for _, cell := range cells {
		cell := cell
		t.Run(cell.name, func(t *testing.T) {
			proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
			proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
			spec := readSpec("probe")
			spec.Approval = cell.approval
			up := startCountingUpstream(t, proxy, rt, "a", spec)
			// The proxy's own upstream manager is separate from the runtime's
			// (fixture note in scope_fixture_test.go); shut it down too so no
			// managed-client goroutine outlives the subtest.
			t.Cleanup(func() { _ = proxy.upstreamManager.ShutdownAll(context.Background()) })
			// The persisted server row the gate reads: quarantine, enablement
			// and trust mode come from storage (the one read dispatch makes).
			row := &config.ServerConfig{Name: "a", URL: up.URL, Protocol: "streamable-http", Enabled: cell.enabled, Quarantined: cell.quarantined}
			row.MarkQuarantineExplicitlySet(true)
			if cell.trustAuto {
				row.TrustMode = string(config.TrustModeAuto)
			}
			require.NoError(t, proxy.storage.SaveUpstreamServer(row))
			rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
				s.Quarantined = cell.quarantined
				s.Enabled = cell.enabled
			})
			if cell.disabled {
				rec, err := proxy.storage.GetToolApproval("a", "probe")
				require.NoError(t, err)
				rec.Disabled = true
				require.NoError(t, proxy.storage.SaveToolApproval(rec))
			}
			// The policy model's verdict for this cell.
			model := qapilot.NewServer("a", cell.quarantined, cell.enabled, qapilot.TrustManual, true)
			if cell.trustAuto {
				model.Trust = qapilot.TrustAuto
			}
			model.Tools["probe"] = &qapilot.Tool{Status: qapilot.Status(cell.approval), Disabled: cell.disabled}
			require.Equal(t, cell.expect, model.Dispatch("probe", false), "cell table and model must agree")
			// The implementation's classifier for the same facts.
			require.Equal(t, preflight.ToolClass(cell.expect), proxy.evaluateExactToolGate("a", "probe").class)

			before := up.count.Load()
			res := callToolReadVariant(t, proxy, adminCtx(), "a:probe")
			after := up.count.Load()
			text := qapilotResultText(t, res)
			if cell.expect == qapilot.DispatchReady {
				require.False(t, res.IsError, "admitted call must succeed: %s", text)
				require.Equal(t, int64(1), after-before, "an admitted call reaches the upstream exactly once")
				return
			}
			require.True(t, res.IsError, "refused call must be an error result: %s", text)
			require.Contains(t, text, cell.marker)
			require.Equal(t, before, after, "a refused call must never reach the upstream (%s)", text)
		})
	}
}

// TestQAPilot_MCPCannotReleaseServer: the MCP management surface has no
// unquarantine operation, and an upstream_servers patch that asks for
// quarantined:false on a quarantined server leaves it quarantined in the live
// config, in storage and on disk (QP-06).
func TestQAPilot_MCPCannotReleaseServer(t *testing.T) {
	sc := &config.ServerConfig{Name: "held", Command: "true", Protocol: "stdio", Enabled: true, Quarantined: true}
	sc.MarkQuarantineExplicitlySet(true)
	srv, cfgPath := newSpec116Server(t, sc)
	require.True(t, storedServer(t, srv, "held").Quarantined, "fixture: the server starts quarantined")

	t.Run("no-unquarantine-op", func(t *testing.T) {
		qapilot.Track(t, "mcp/no-unquarantine-op", "QP-06")
		for _, op := range []string{"unquarantine", "unquarantine_server", "approve_server", "release"} {
			res, err := srv.mcpProxy.handleQuarantineSecurity(context.Background(), mcpCall(map[string]interface{}{"operation": op, "name": "held"}))
			require.NoError(t, err)
			require.True(t, res.IsError, "operation %q must be refused", op)
			require.Contains(t, qapilotResultText(t, res), "Unknown quarantine operation")
			require.True(t, storedServer(t, srv, "held").Quarantined, "operation %q must not release the server", op)
			require.True(t, liveServer(srv, "held").Quarantined)
		}
	})
	t.Run("patch-ignores-unquarantine", func(t *testing.T) {
		qapilot.Track(t, "mcp/patch-ignores-unquarantine", "QP-06")
		for _, op := range []string{"patch", "update"} {
			res, err := srv.mcpProxy.handleUpstreamServers(context.Background(), mcpCall(map[string]interface{}{"operation": op, "name": "held", "quarantined": false}))
			require.NoError(t, err)
			text := qapilotResultText(t, res)
			require.True(t, storedServer(t, srv, "held").Quarantined, "%s quarantined:false must not release the server (%s)", op, text)
			require.True(t, liveServer(srv, "held").Quarantined, "%s: live config", op)
			require.True(t, diskServer(t, cfgPath, "held").Quarantined, "%s: config file", op)
		}
		// Quarantining through the same door IS allowed (true never releases).
		sc2 := &config.ServerConfig{Name: "free", Command: "true", Protocol: "stdio", Enabled: true}
		sc2.MarkQuarantineExplicitlySet(true)
		require.NoError(t, srv.AddServer(context.Background(), sc2))
		require.False(t, storedServer(t, srv, "free").Quarantined)
		res, err := srv.mcpProxy.handleUpstreamServers(context.Background(), mcpCall(map[string]interface{}{"operation": "patch", "name": "free", "quarantined": true}))
		require.NoError(t, err)
		require.False(t, res.IsError, qapilotResultText(t, res))
		require.True(t, storedServer(t, srv, "free").Quarantined, "patch quarantined:true must quarantine")
	})
}

// TestQAPilot_SecurityApprovalTable: the review-path release
// (security/approve) is scan-gated — no report or a hard-tier finding refuses
// an unforced approval and the server stays quarantined; force bypasses both;
// a clean or soft-only report admits (QP-04).
func TestQAPilot_SecurityApprovalTable(t *testing.T) {
	qapilot.Track(t, "security-approve/table", "QP-04")
	type seed int
	const (
		noScan seed = iota
		cleanScan
		softScan
		hardScan
	)
	cells := []struct {
		name   string
		seed   seed
		force  bool
		admit  bool
		errSub string
	}{
		{"no scan, unforced", noScan, false, false, "no scan results"},
		{"no scan, forced", noScan, true, true, ""},
		{"clean scan, unforced", cleanScan, false, true, ""},
		{"soft-tier findings only, unforced", softScan, false, true, ""},
		{"hard-tier finding, unforced", hardScan, false, false, "dangerous"},
		{"hard-tier finding, forced", hardScan, true, true, ""},
	}
	for _, cell := range cells {
		cell := cell
		t.Run(cell.name, func(t *testing.T) {
			sc := &config.ServerConfig{Name: "held", Command: "true", Protocol: "stdio", Enabled: true, Quarantined: true}
			sc.MarkQuarantineExplicitlySet(true)
			srv, _ := newSpec116Server(t, sc)
			st := srv.runtime.StorageManager()
			if cell.seed != noScan {
				now := time.Now()
				var findings []scanner.ScanFinding
				switch cell.seed {
				case softScan:
					findings = []scanner.ScanFinding{{RuleID: "detect/directive", Tier: scanner.TierSoft, ThreatLevel: scanner.ThreatLevelWarning, ThreatType: "tool_poisoning", Severity: scanner.SeverityMedium, Title: "directive", Location: "tool:probe"}}
				case hardScan:
					findings = []scanner.ScanFinding{{RuleID: "detect/phrase_injection", Tier: scanner.TierHard, ThreatLevel: scanner.ThreatLevelDangerous, ThreatType: "prompt_injection", Severity: scanner.SeverityCritical, Title: "injection", Location: "tool:probe"}}
				}
				require.NoError(t, st.SaveScanJob(&scanner.ScanJob{
					ID: "job-1", ServerName: "held", Status: scanner.ScanJobStatusCompleted, ScanPass: scanner.ScanPassSecurityScan,
					Scanners: []string{"tpa-baseline"}, StartedAt: now, CompletedAt: now,
					ScannerStatuses: []scanner.ScannerJobStatus{{ScannerID: "tpa-baseline", Status: scanner.ScanJobStatusCompleted, FindingsCount: len(findings)}},
				}))
				require.NoError(t, st.SaveScanReport(&scanner.ScanReport{ID: "rep-1", JobID: "job-1", ServerName: "held", ScannerID: "tpa-baseline", Findings: findings, ScannedAt: now}))
			}
			svc := scanner.NewService(st, scanner.NewRegistry(t.TempDir(), zap.NewNop()), scanner.NewDockerRunner(zap.NewNop()), t.TempDir(), zap.NewNop())
			svc.SetServerUnquarantiner(&serverUnquarantinerAdapter{server: srv})

			err := svc.ApproveServer(context.Background(), "held", cell.force, "pilot")
			if cell.admit {
				require.NoError(t, err)
				require.False(t, storedServer(t, srv, "held").Quarantined, "approval must release the server")
				require.False(t, liveServer(srv, "held").Quarantined)
				return
			}
			require.Error(t, err)
			require.Contains(t, strings.ToLower(err.Error()), cell.errSub)
			require.True(t, storedServer(t, srv, "held").Quarantined, "a refused approval must leave the server quarantined")
			require.True(t, liveServer(srv, "held").Quarantined)
			_, berr := st.GetIntegrityBaseline("held")
			require.Error(t, berr, "a refused approval must not write an integrity baseline")
		})
	}
}

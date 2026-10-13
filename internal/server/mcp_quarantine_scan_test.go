package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// quarantine_security is the security surface agents actually reach for, and it
// had no way to run or read a TPA scan. These tests cover the two operations
// that close that gap (scan_server / get_scan_report) plus the one-line scan
// status every listing/inspection response now carries.

// scanTestProxy builds a proxy wired to a real runtime plus a fake scanner
// service, and returns both so a test can pin scan fixtures.
func scanTestProxy(t *testing.T, servers ...*config.ServerConfig) (*MCPProxyServer, *fakeSecurityScanner) {
	t.Helper()
	proxy, rt := createTestProxyWithRuntime(t, servers)
	// list_quarantined reads the persisted upstream list, mirroring production
	// where an added server is always saved to BBolt.
	for _, sc := range servers {
		require.NoError(t, rt.StorageManager().SaveUpstreamServer(sc))
	}
	fake := newFakeSecurityScanner()
	proxy.mainServer.securityScanner = fake
	return proxy, fake
}

func decodeToolJSON(t *testing.T, result *mcp.CallToolResult) map[string]interface{} {
	t.Helper()
	require.NotNil(t, result)
	require.NotEmpty(t, result.Content)
	text, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected text content")
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text.Text), &payload), "response must be JSON: %s", text.Text)
	return payload
}

func settledSummary(status string, risk int) *scanner.ScanSummary {
	at := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	return &scanner.ScanSummary{
		LastScanAt:    &at,
		RiskScore:     risk,
		Status:        status,
		FindingCounts: &scanner.FindingCounts{Dangerous: 1, Total: 1},
		ScannersRun:   1,
		ScannersTotal: 1,
	}
}

// TestQuarantineSecurity_ScanServer_ReturnsVerdictWhenScanSettles is the happy
// path: the operation starts a scan and, because the baseline scan finishes
// in-process, answers with the verdict rather than only a job id.
func TestQuarantineSecurity_ScanServer_ReturnsVerdictWhenScanSettles(t *testing.T) {
	proxy, fake := scanTestProxy(t, &config.ServerConfig{Name: "github", Enabled: true, Quarantined: true})
	fake.onStartScan = func(serverName string) {
		fake.setScanResult(serverName,
			&scanner.ScanJob{ID: "job-1", ServerName: serverName, Status: scanner.ScanJobStatusCompleted},
			settledSummary("dangerous", 80))
	}

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "scan_server",
		"name":      "github",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError, "scan_server must succeed")

	payload := decodeToolJSON(t, result)
	assert.Equal(t, []string{"github"}, fake.startedScans(), "the scan is actually triggered")
	assert.Equal(t, "completed", payload["status"])
	assert.Equal(t, "dangerous", payload["verdict"])
	assert.Equal(t, float64(80), payload["risk_score"])
	assert.Equal(t, "job-1", payload["job_id"])
	assert.Contains(t, payload["next_steps"], "get_scan_report")
}

// TestQuarantineSecurity_ScanServer_AsyncWhenScanStillRunning covers the other
// branch: a scan that has not settled must report "scan started" with a job id,
// never a fabricated verdict.
func TestQuarantineSecurity_ScanServer_AsyncWhenScanStillRunning(t *testing.T) {
	proxy, fake := scanTestProxy(t, &config.ServerConfig{Name: "github", Enabled: true})
	fake.setScanResult("github",
		&scanner.ScanJob{ID: "job-2", ServerName: "github", Status: scanner.ScanJobStatusRunning},
		nil)

	// Keep the test fast: the wait loop honours context cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, err := proxy.handleQuarantineSecurity(ctx, quarantineRequest(map[string]interface{}{
		"operation": "scan_server",
		"name":      "github",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	payload := decodeToolJSON(t, result)
	assert.Equal(t, "scan started", payload["status"])
	assert.Equal(t, "job-2", payload["job_id"])
	assert.NotContains(t, payload, "verdict", "an unfinished scan has no verdict to report")
}

// TestQuarantineSecurity_ScanServer_NeverReportsScanningAsAVerdict covers the
// completion window inside the scanner engine: it flips the job to a terminal
// status BEFORE the completion callback persists the report and before the job
// leaves the active map, so a summary read in between still answers "scanning".
// Reporting that pair verbatim would produce status=completed with
// verdict=scanning — a non-verdict that reads like a result. The wait must keep
// polling and fall back to the honest async answer.
func TestQuarantineSecurity_ScanServer_NeverReportsScanningAsAVerdict(t *testing.T) {
	proxy, fake := scanTestProxy(t, &config.ServerConfig{Name: "github", Enabled: true})
	fake.setScanResult("github",
		&scanner.ScanJob{ID: "job-window", ServerName: "github", Status: scanner.ScanJobStatusCompleted},
		&scanner.ScanSummary{Status: "scanning"})

	// Bound the wait: the loop honours context cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	result, err := proxy.handleQuarantineSecurity(ctx, quarantineRequest(map[string]interface{}{
		"operation": "scan_server",
		"name":      "github",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	payload := decodeToolJSON(t, result)
	assert.Equal(t, "scan started", payload["status"])
	assert.NotContains(t, payload, "verdict", "'scanning' is not a verdict")
	assert.Equal(t, "job-window", payload["job_id"])
}

// TestQuarantineSecurity_ScanServer_WaitsThroughTheCompletionWindow is the
// other half: once the completion callback lands and the summary carries a real
// verdict, the still-open call reports it rather than timing out into async.
func TestQuarantineSecurity_ScanServer_WaitsThroughTheCompletionWindow(t *testing.T) {
	proxy, fake := scanTestProxy(t, &config.ServerConfig{Name: "github", Enabled: true})
	fake.setScanResult("github",
		&scanner.ScanJob{ID: "job-late", ServerName: "github", Status: scanner.ScanJobStatusCompleted},
		&scanner.ScanSummary{Status: "scanning"})

	go func() {
		time.Sleep(300 * time.Millisecond)
		fake.setScanResult("github",
			&scanner.ScanJob{ID: "job-late", ServerName: "github", Status: scanner.ScanJobStatusCompleted},
			settledSummary("clean", 0))
	}()

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "scan_server",
		"name":      "github",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	payload := decodeToolJSON(t, result)
	assert.Equal(t, "completed", payload["status"])
	assert.Equal(t, "clean", payload["verdict"])
	assert.Equal(t, "job-late", payload["job_id"])
}

// TestQuarantineSecurity_ScanServer_Pass2DoesNotMaskTheBaselineVerdict: with
// deep scan enabled, a completed Pass 1 auto-starts the Pass-2 supply-chain
// audit, which becomes the server's ACTIVE job. Polling the generic scan status
// would then see a job whose id never matches the one scan_server started and
// time out into "scan started" — hiding a baseline verdict that was already
// available. The wait polls Pass 1 specifically.
func TestQuarantineSecurity_ScanServer_Pass2DoesNotMaskTheBaselineVerdict(t *testing.T) {
	proxy, fake := scanTestProxy(t, &config.ServerConfig{Name: "github", Enabled: true})
	// The ACTIVE job for this server is the Pass-2 audit; Pass 1 is the job
	// scan_server started and the one whose verdict it promised.
	fake.setScanResult("github",
		&scanner.ScanJob{ID: "job-pass2", ServerName: "github", Status: scanner.ScanJobStatusRunning},
		settledSummary("clean", 0))
	fake.setPassJob("github", scanner.ScanPassSecurityScan,
		&scanner.ScanJob{ID: "job-pass1", ServerName: "github", Status: scanner.ScanJobStatusRunning})

	go func() {
		time.Sleep(300 * time.Millisecond)
		fake.setPassJob("github", scanner.ScanPassSecurityScan,
			&scanner.ScanJob{ID: "job-pass1", ServerName: "github", Status: scanner.ScanJobStatusCompleted})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := proxy.handleQuarantineSecurity(ctx, quarantineRequest(map[string]interface{}{
		"operation": "scan_server",
		"name":      "github",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	payload := decodeToolJSON(t, result)
	assert.Equal(t, "completed", payload["status"],
		"the Pass-2 audit running in the background must not hide the settled baseline")
	assert.Equal(t, "clean", payload["verdict"])
	assert.Equal(t, "job-pass1", payload["job_id"], "scan_server reports the Pass-1 job it started")
}

// TestQuarantineSecurity_GetScanReport_LabelsPartialFindingsOfTheRunningScan is
// the other half of the findings-provenance label: per-scanner reports are
// persisted as each scanner finishes, so a running scan's own partial output
// can be what GetScanReport returns. Calling that "the previous scan" is just as
// wrong as leaving it unlabelled.
func TestQuarantineSecurity_GetScanReport_LabelsPartialFindingsOfTheRunningScan(t *testing.T) {
	proxy, fake := scanTestProxy(t, &config.ServerConfig{Name: "github", Enabled: true})
	fake.setScanResult("github",
		&scanner.ScanJob{ID: "job-live", ServerName: "github", Status: scanner.ScanJobStatusRunning},
		&scanner.ScanSummary{Status: "scanning"})
	fake.reports["github"] = &scanner.AggregatedReport{
		JobID:      "job-live",
		ServerName: "github",
		Findings: []scanner.ScanFinding{{
			RuleID:  "TPA-2026-0001",
			Scanner: "tpa-descriptions",
		}},
	}

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "get_scan_report",
		"name":      "github",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	payload := decodeToolJSON(t, result)
	assert.Equal(t, "job-live", payload["job_id"])
	assert.Contains(t, payload["findings_from"], "currently running",
		"a running job's own partial findings must not be labelled as a previous scan's")
	assert.NotContains(t, payload["findings_from"], "previous")
}

// TestQuarantineSecurity_ScanServer_RequiresName pins the argument contract.
func TestQuarantineSecurity_ScanServer_RequiresName(t *testing.T) {
	proxy, _ := scanTestProxy(t)

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "scan_server",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "'name'")
}

// TestQuarantineSecurity_ScanServer_ReportsStartFailure — a scan that cannot
// start (disconnected server, no tool definitions) says so.
func TestQuarantineSecurity_ScanServer_ReportsStartFailure(t *testing.T) {
	proxy, fake := scanTestProxy(t, &config.ServerConfig{Name: "github", Enabled: true})
	fake.startScanErr = errors.New("server is disconnected")

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "scan_server",
		"name":      "github",
	}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "server is disconnected")
}

// TestQuarantineSecurity_GetScanReport_ReturnsVerdictAndFindings covers the
// read side, including the finding projection.
func TestQuarantineSecurity_GetScanReport_ReturnsVerdictAndFindings(t *testing.T) {
	proxy, fake := scanTestProxy(t, &config.ServerConfig{Name: "github", Enabled: true})
	fake.setScanResult("github",
		&scanner.ScanJob{ID: "job-3", ServerName: "github", Status: scanner.ScanJobStatusCompleted},
		settledSummary("warnings", 30))
	fake.reports["github"] = &scanner.AggregatedReport{
		JobID:      "job-3",
		ServerName: "github",
		Verdict:    "warnings",
		RiskScore:  30,
		Findings: []scanner.ScanFinding{{
			RuleID:      "TPA-2026-0001",
			Title:       "Hidden instructions in tool description",
			Severity:    scanner.SeverityHigh,
			ThreatLevel: "warning",
			ThreatType:  "tool_poisoning",
			Scanner:     "tpa-descriptions",
			Location:    "tools.json",
		}},
	}

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "get_scan_report",
		"name":      "github",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	payload := decodeToolJSON(t, result)
	assert.Equal(t, "warnings", payload["verdict"])
	assert.Equal(t, "job-3", payload["job_id"])
	assert.Equal(t, float64(1), payload["findings_total"])
	findings, ok := payload["findings"].([]interface{})
	require.True(t, ok)
	require.Len(t, findings, 1)
	assert.Equal(t, "TPA-2026-0001", findings[0].(map[string]interface{})["rule_id"])
	assert.Contains(t, payload["scan_status"], "warnings")
}

// TestQuarantineSecurity_GetScanReport_LabelsPreviousFindingsWhileScanning:
// the summary and the report are independent latest-by-server reads, so while a
// new scan runs the report is still the PREVIOUS job's. Its findings must be
// labelled as such, never presented as the running scan's result.
func TestQuarantineSecurity_GetScanReport_LabelsPreviousFindingsWhileScanning(t *testing.T) {
	proxy, fake := scanTestProxy(t, &config.ServerConfig{Name: "github", Enabled: true})
	fake.setScanResult("github",
		&scanner.ScanJob{ID: "job-new", ServerName: "github", Status: scanner.ScanJobStatusRunning},
		&scanner.ScanSummary{Status: "scanning"})
	fake.reports["github"] = &scanner.AggregatedReport{
		JobID:      "job-old",
		ServerName: "github",
		Verdict:    "warnings",
		RiskScore:  30,
		Findings: []scanner.ScanFinding{{
			RuleID:  "TPA-2026-0001",
			Title:   "Hidden instructions in tool description",
			Scanner: "tpa-descriptions",
		}},
	}

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "get_scan_report",
		"name":      "github",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	payload := decodeToolJSON(t, result)
	assert.Contains(t, payload["scan_status"], "scan running")
	assert.Equal(t, "job-old", payload["job_id"])
	assert.Contains(t, payload, "findings_from",
		"findings from an older job must say which scan they came from")
	assert.Contains(t, payload["findings_from"], "previous")
}

// TestQuarantineSecurity_GetScanReport_NeverScanned is the honest empty state:
// no report is NOT a clean verdict.
func TestQuarantineSecurity_GetScanReport_NeverScanned(t *testing.T) {
	proxy, _ := scanTestProxy(t, &config.ServerConfig{Name: "github", Enabled: true})

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "get_scan_report",
		"name":      "github",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	payload := decodeToolJSON(t, result)
	assert.Equal(t, neverScannedHint, payload["scan_status"])
	assert.NotContains(t, payload, "verdict")
	assert.Contains(t, payload["next_steps"], "scan_server")
}

// TestQuarantineSecurity_ListQuarantined_CarriesScanStatus — the listing an
// agent starts from must say whether each held server was ever scanned.
func TestQuarantineSecurity_ListQuarantined_CarriesScanStatus(t *testing.T) {
	proxy, fake := scanTestProxy(t,
		&config.ServerConfig{Name: "scanned", Enabled: true, Quarantined: true},
		&config.ServerConfig{Name: "unscanned", Enabled: true, Quarantined: true},
	)
	fake.setScanResult("scanned",
		&scanner.ScanJob{ID: "job-4", ServerName: "scanned", Status: scanner.ScanJobStatusCompleted},
		settledSummary("clean", 0))

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "list_quarantined",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)

	payload := decodeToolJSON(t, result)
	statuses, ok := payload["scan_status"].(map[string]interface{})
	require.True(t, ok, "list_quarantined must carry per-server scan status")
	assert.Contains(t, statuses["scanned"], "clean")
	assert.Equal(t, neverScannedHint, statuses["unscanned"])
	assert.Contains(t, payload["scan_hint"], "scan_server")
}

// TestQuarantineSecurity_InspectTools_CarriesScanStatus — same for the
// tool-approval inspection, in both the populated and the empty state.
func TestQuarantineSecurity_InspectTools_CarriesScanStatus(t *testing.T) {
	proxy, fake := scanTestProxy(t, &config.ServerConfig{Name: "github", Enabled: true, Quarantined: true})
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:         "github",
		ToolName:           "create_issue",
		Status:             storage.ToolApprovalStatusPending,
		CurrentHash:        "h1",
		CurrentDescription: "Creates a GitHub issue",
		CurrentSchema:      `{"type":"object"}`,
	}))

	inspect := func() *mcp.CallToolResult {
		result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
			"operation": "inspect_tools",
			"name":      "github",
		}))
		require.NoError(t, err)
		require.False(t, result.IsError)
		return result
	}

	payload := decodeToolJSON(t, inspect())
	assert.Equal(t, neverScannedHint, payload["scan_status"])

	fake.setScanResult("github",
		&scanner.ScanJob{ID: "job-5", ServerName: "github", Status: scanner.ScanJobStatusCompleted},
		settledSummary("dangerous", 90))
	payload = decodeToolJSON(t, inspect())
	assert.Contains(t, payload["scan_status"], "dangerous")
}

// TestQuarantineSecurity_InspectTools_EmptyStateCarriesScanStatus covers the
// no-records early return, which is plain text rather than JSON.
func TestQuarantineSecurity_InspectTools_EmptyStateCarriesScanStatus(t *testing.T) {
	proxy, _ := scanTestProxy(t, &config.ServerConfig{Name: "github", Enabled: true})

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "inspect_tools",
		"name":      "github",
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, neverScannedHint)
}

// TestQuarantineSecurity_ScanOps_WithoutScannerService — a proxy with no
// scanner (stdio-only mode) must explain that, not crash.
func TestQuarantineSecurity_ScanOps_WithoutScannerService(t *testing.T) {
	proxy, _ := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "github", Enabled: true}})

	for _, op := range []string{"scan_server", "get_scan_report"} {
		result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
			"operation": op,
			"name":      "github",
		}))
		require.NoError(t, err)
		require.True(t, result.IsError, "%s must report the missing scanner", op)
		assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "unavailable")
	}
}

// TestQuarantineSecurity_QuarantineServerOperationName — the enum advertises
// "quarantine_server"; the dispatch used to accept only the old "quarantine"
// spelling, so the advertised name returned "Unknown quarantine operation".
func TestQuarantineSecurity_QuarantineServerOperationName(t *testing.T) {
	proxy, _ := scanTestProxy(t, &config.ServerConfig{Name: "github", Enabled: true})

	result, err := proxy.handleQuarantineSecurity(context.Background(), quarantineRequest(map[string]interface{}{
		"operation": "quarantine_server",
		"name":      "github",
	}))
	require.NoError(t, err)
	assert.NotContains(t, result.Content[0].(mcp.TextContent).Text, "Unknown quarantine operation")
}

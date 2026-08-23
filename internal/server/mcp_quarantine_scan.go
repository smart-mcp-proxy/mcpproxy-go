package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// Scanning through quarantine_security (the surface agents actually reach for
// when a server is held). The TPA scanner shipped with no MCP entry point at
// all: an agent could list, inspect and approve quarantined tools but had no
// way to ask "has this server been scanned, and what did the scan say?" — the
// answer lived only in the web UI and the CLI.

const (
	// scanVerdictWait is how long scan_server waits for the just-started
	// Pass-1 scan to settle before answering "started, poll get_scan_report".
	// The in-process baseline scan on a connected server finishes well inside
	// this; anything slower (a cold upstream that has to connect first) is
	// reported as async rather than holding the agent's call open.
	scanVerdictWait = 8 * time.Second

	// scanVerdictPoll is the polling interval used while waiting.
	scanVerdictPoll = 250 * time.Millisecond

	// scanReportMaxFindings caps how many findings get inlined into a
	// get_scan_report response. The full report stays available over REST and
	// in the web UI; an MCP response is a token budget.
	scanReportMaxFindings = 10

	// neverScannedHint is the one-line status carried by list/inspect
	// responses for a server with no scan on record.
	neverScannedHint = "never scanned — run scan_server first"

	// scanSummaryStatusRunning is the sentinel ScanSummary.Status the scanner
	// service reports while a Pass-1 scan is still registered as active.
	scanSummaryStatusRunning = "scanning"
)

// securityScanner returns the scanner service backing the scan operations, or
// nil when this process has none (scanner disabled, or a test proxy without a
// main server).
func (p *MCPProxyServer) securityScanner() securityScannerService {
	if p.mainServer == nil {
		return nil
	}
	return p.mainServer.securityScannerSvc()
}

// scannerUnavailableResult is the shared "no scanner here" answer. It names the
// reason rather than pretending the server is clean.
func scannerUnavailableResult(operation string) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf(
		"Security scanning is unavailable in this process, so '%s' cannot run. Scanning requires the mcpproxy core (not a stdio-only proxy).", operation))
}

// handleScanServer triggers a Pass-1 (offline baseline) security scan for a
// server and reports the verdict if the scan settles quickly.
func (p *MCPProxyServer) handleScanServer(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	serverName := request.GetString("name", "")
	if serverName == "" {
		return mcp.NewToolResultError("Missing required parameter 'name' (server name)"), nil
	}

	svc := p.securityScanner()
	if svc == nil {
		return scannerUnavailableResult("scan_server"), nil
	}

	job, err := svc.StartScan(ctx, serverName, false, nil, "")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to start scan for server '%s': %v", serverName, err)), nil
	}

	response := map[string]interface{}{
		"operation":   "scan_server",
		"server_name": serverName,
		"scan_pass":   "baseline",
		"note":        "The offline baseline scan runs in-process and needs no Docker. Optional deep scanners (Docker) run only when deep scan is enabled.",
	}
	jobID := ""
	if job != nil {
		jobID = job.ID
		response["job_id"] = job.ID
	}

	if summary := p.awaitScanVerdict(ctx, svc, serverName, jobID); summary != nil {
		response["status"] = "completed"
		response["verdict"] = summary.Status
		response["risk_score"] = summary.RiskScore
		if summary.FindingCounts != nil {
			response["finding_counts"] = summary.FindingCounts
		}
		if summary.LastScanAt != nil {
			response["last_scan_at"] = summary.LastScanAt.UTC().Format(time.RFC3339)
		}
		response["next_steps"] = fmt.Sprintf("Use operation='get_scan_report' name='%s' for the findings.", serverName)
	} else {
		response["status"] = "scan started"
		response["next_steps"] = fmt.Sprintf("Scan is still running. Use operation='get_scan_report' name='%s' to read the verdict.", serverName)
	}

	return jsonToolResult(response, "scan result")
}

// awaitScanVerdict polls until the scan job reaches a terminal state, then
// returns the resulting summary. Returns nil on timeout, on context
// cancellation, or when no summary is available — the caller then answers
// "scan started" instead of inventing a verdict.
func (p *MCPProxyServer) awaitScanVerdict(ctx context.Context, svc securityScannerService, serverName, jobID string) *scanner.ScanSummary {
	deadline := time.Now().Add(scanVerdictWait)
	ticker := time.NewTicker(scanVerdictPoll)
	defer ticker.Stop()

	for {
		job, err := svc.GetScanStatus(ctx, serverName)
		if err == nil && job != nil && scanJobSettled(job.Status) && (jobID == "" || job.ID == jobID) {
			// The engine flips the job to a terminal status BEFORE its
			// completion callback persists the report and BEFORE the job is
			// dropped from the active map. A summary read inside that window
			// still answers "scanning", which would be reported here as
			// status=completed + verdict=scanning — a verdict that reads as a
			// clean-ish result but is really "not known yet". Keep polling
			// until the settled job's own verdict is visible, or time out into
			// the async answer.
			if summary := svc.GetScanSummary(ctx, serverName); summary != nil && summary.Status != scanSummaryStatusRunning {
				return summary
			}
		}

		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func scanJobSettled(status string) bool {
	switch status {
	case scanner.ScanJobStatusCompleted, scanner.ScanJobStatusFailed, scanner.ScanJobStatusCancelled:
		return true
	default:
		return false
	}
}

// handleGetScanReport returns the latest scan verdict + findings for a server.
func (p *MCPProxyServer) handleGetScanReport(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	serverName := request.GetString("name", "")
	if serverName == "" {
		return mcp.NewToolResultError("Missing required parameter 'name' (server name)"), nil
	}

	svc := p.securityScanner()
	if svc == nil {
		return scannerUnavailableResult("get_scan_report"), nil
	}

	summary := svc.GetScanSummary(ctx, serverName)
	report, reportErr := svc.GetScanReport(ctx, serverName)
	if summary == nil && (reportErr != nil || report == nil) {
		return jsonToolResult(map[string]interface{}{
			"operation":   "get_scan_report",
			"server_name": serverName,
			"scan_status": neverScannedHint,
			"next_steps":  fmt.Sprintf("Use operation='scan_server' name='%s' to run the offline baseline scan.", serverName),
		}, "scan report")
	}

	response := map[string]interface{}{
		"operation":   "get_scan_report",
		"server_name": serverName,
		"scan_status": scanStatusLineFrom(summary, serverName),
	}
	if summary != nil {
		response["verdict"] = summary.Status
		response["risk_score"] = summary.RiskScore
		if summary.FindingCounts != nil {
			response["finding_counts"] = summary.FindingCounts
		}
		if summary.LastScanAt != nil {
			response["last_scan_at"] = summary.LastScanAt.UTC().Format(time.RFC3339)
		}
		response["scanners_run"] = summary.ScannersRun
		response["scanners_failed"] = summary.ScannersFailed
		response["scanners_total"] = summary.ScannersTotal
		if summary.DeepScan != nil {
			response["deep_scan"] = summary.DeepScan
		}
	}

	if reportErr == nil && report != nil {
		response["job_id"] = report.JobID
		if summary == nil {
			response["verdict"] = report.Verdict
			response["risk_score"] = report.RiskScore
		}
		response["findings_total"] = len(report.Findings)
		findings := make([]map[string]interface{}, 0, scanReportMaxFindings)
		for i, f := range report.Findings {
			if i >= scanReportMaxFindings {
				response["findings_truncated"] = len(report.Findings) - scanReportMaxFindings
				break
			}
			finding := map[string]interface{}{
				"rule_id":      f.RuleID,
				"title":        f.Title,
				"severity":     f.Severity,
				"threat_level": f.ThreatLevel,
				"threat_type":  f.ThreatType,
				"scanner":      f.Scanner,
			}
			if f.Location != "" {
				finding["location"] = f.Location
			}
			if f.Description != "" {
				finding["description"] = f.Description
			}
			findings = append(findings, finding)
		}
		response["findings"] = findings
	}

	return jsonToolResult(response, "scan report")
}

// scanStatusLineFrom renders the one-line scan status carried by every
// quarantine_security listing/inspection response.
func scanStatusLineFrom(summary *scanner.ScanSummary, serverName string) string {
	if summary == nil {
		return neverScannedHint
	}
	if summary.Status == scanSummaryStatusRunning {
		return fmt.Sprintf("scan running for '%s' — re-run get_scan_report shortly", serverName)
	}
	line := fmt.Sprintf("last scan verdict: %s (risk %d)", summary.Status, summary.RiskScore)
	if summary.LastScanAt != nil {
		line += fmt.Sprintf(", scanned %s", summary.LastScanAt.UTC().Format(time.RFC3339))
	}
	return line
}

// scanStatusLine resolves the one-line scan status for a server, tolerating a
// process with no scanner at all.
func (p *MCPProxyServer) scanStatusLine(ctx context.Context, serverName string) string {
	svc := p.securityScanner()
	if svc == nil {
		return "scan status unavailable — this process runs no security scanner"
	}
	return scanStatusLineFrom(svc.GetScanSummary(ctx, serverName), serverName)
}

// scanStatusLines resolves scan status for a batch of servers (list_quarantined).
func (p *MCPProxyServer) scanStatusLines(ctx context.Context, serverNames []string) map[string]string {
	lines := make(map[string]string, len(serverNames))
	for _, name := range serverNames {
		if name == "" {
			continue
		}
		lines[name] = p.scanStatusLine(ctx, name)
	}
	return lines
}

// jsonToolResult marshals a response map into an MCP text result.
func jsonToolResult(payload map[string]interface{}, what string) (*mcp.CallToolResult, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize %s: %v", what, err)), nil
	}
	return mcp.NewToolResultText(string(jsonBytes)), nil
}

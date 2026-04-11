package scanner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestAggregateReports(t *testing.T) {
	reports := []*ScanReport{
		{
			ID:        "r1",
			ScannerID: "scanner-a",
			Findings: []ScanFinding{
				{Severity: SeverityCritical, Title: "Critical bug"},
				{Severity: SeverityHigh, Title: "High issue"},
			},
			RiskScore: 40,
		},
		{
			ID:        "r2",
			ScannerID: "scanner-b",
			Findings: []ScanFinding{
				{Severity: SeverityMedium, Title: "Medium issue"},
				{Severity: SeverityLow, Title: "Low issue"},
			},
			RiskScore: 7,
		},
	}

	agg := AggregateReports("job-1", "test-server", reports)

	if agg.JobID != "job-1" {
		t.Errorf("expected job ID 'job-1', got %q", agg.JobID)
	}
	if agg.ServerName != "test-server" {
		t.Errorf("expected server 'test-server', got %q", agg.ServerName)
	}
	if len(agg.Findings) != 4 {
		t.Errorf("expected 4 findings, got %d", len(agg.Findings))
	}
	if agg.Summary.Critical != 1 {
		t.Errorf("expected 1 critical, got %d", agg.Summary.Critical)
	}
	if agg.Summary.High != 1 {
		t.Errorf("expected 1 high, got %d", agg.Summary.High)
	}
	if agg.Summary.Total != 4 {
		t.Errorf("expected 4 total, got %d", agg.Summary.Total)
	}
	if len(agg.Reports) != 2 {
		t.Errorf("expected 2 reports, got %d", len(agg.Reports))
	}
	// Risk score should be calculated from aggregated findings
	if agg.RiskScore <= 0 {
		t.Error("expected positive risk score")
	}
}

func TestAggregateReportsSupplyChainAuditFlag(t *testing.T) {
	reports := []*ScanReport{
		{
			ID:        "r1",
			ScannerID: "trivy-mcp",
			Findings: []ScanFinding{
				// CVE-prefixed rule ID → should be flagged.
				{
					RuleID:      "CVE-2023-12345",
					Severity:    SeverityHigh,
					Title:       "CVE in lodash",
					Description: "Prototype pollution",
					PackageName: "lodash",
					ScanPass:    ScanPassSupplyChainAudit,
				},
				// PackageName populated, no CVE prefix → should be flagged.
				{
					RuleID:      "GHSA-xxxx-yyyy-zzzz",
					Severity:    SeverityMedium,
					Title:       "Known advisory",
					PackageName: "express",
					ScanPass:    ScanPassSupplyChainAudit,
				},
			},
		},
		{
			ID:        "r2",
			ScannerID: "mcp-ai-scanner",
			Findings: []ScanFinding{
				// AI scanner finding promoted to Pass 2 via full-filesystem rescan.
				// No CVE prefix, no PackageName → must NOT be flagged as supply chain.
				{
					RuleID:      "AI-01-001",
					Severity:    SeverityHigh,
					Title:       "get-env dumps process.env",
					Description: "Data exfiltration via environment dump",
					Location:    "target/dist/tools/get-env.js",
					ScanPass:    ScanPassSupplyChainAudit,
				},
				// Pass 1 AI finding, also not a CVE.
				{
					RuleID:      "AI-02-001",
					Severity:    SeverityMedium,
					Title:       "SSRF in gzip-file-as-resource",
					Description: "Server-side request forgery",
					Location:    "target/dist/tools/gzip-file-as-resource.js",
					ScanPass:    ScanPassSecurityScan,
				},
			},
		},
	}

	agg := AggregateReports("job-supply-chain", "test-server", reports)

	if len(agg.Findings) != 4 {
		t.Fatalf("expected 4 findings, got %d", len(agg.Findings))
	}

	byRule := map[string]ScanFinding{}
	for _, f := range agg.Findings {
		byRule[f.RuleID] = f
	}

	if !byRule["CVE-2023-12345"].SupplyChainAudit {
		t.Error("CVE-prefixed rule should be flagged SupplyChainAudit=true")
	}
	if !byRule["GHSA-xxxx-yyyy-zzzz"].SupplyChainAudit {
		t.Error("finding with PackageName should be flagged SupplyChainAudit=true")
	}
	if byRule["AI-01-001"].SupplyChainAudit {
		t.Error("AI scanner finding (no CVE prefix, no PackageName) must not be flagged as SupplyChainAudit even when ScanPass=2")
	}
	if byRule["AI-02-001"].SupplyChainAudit {
		t.Error("Pass 1 AI finding must not be flagged as SupplyChainAudit")
	}
}

func TestAggregateReportsEmpty(t *testing.T) {
	agg := AggregateReports("job-1", "server", nil)
	if agg.RiskScore != 0 {
		t.Errorf("expected 0 risk score, got %d", agg.RiskScore)
	}
	if agg.Summary.Total != 0 {
		t.Errorf("expected 0 total, got %d", agg.Summary.Total)
	}
	// Empty reports means no scanner succeeded
	if agg.ScanComplete {
		t.Error("expected ScanComplete=false when no reports")
	}
	if agg.ScannersRun != 0 {
		t.Errorf("expected ScannersRun=0, got %d", agg.ScannersRun)
	}
}

func TestAggregateReportsScanComplete(t *testing.T) {
	reports := []*ScanReport{
		{
			ID:        "r1",
			ScannerID: "scanner-a",
			Findings:  []ScanFinding{},
			RiskScore: 0,
		},
	}

	agg := AggregateReports("job-1", "test-server", reports)
	if !agg.ScanComplete {
		t.Error("expected ScanComplete=true when at least one report exists")
	}
	if agg.ScannersRun != 1 {
		t.Errorf("expected ScannersRun=1, got %d", agg.ScannersRun)
	}
}

func TestAggregateReportsWithJobStatusAllFailed(t *testing.T) {
	// No successful reports
	var reports []*ScanReport

	job := &ScanJob{
		ID:         "job-fail",
		ServerName: "test-server",
		Status:     ScanJobStatusFailed,
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "scanner-a", Status: ScanJobStatusFailed, Error: "image not found"},
			{ScannerID: "scanner-b", Status: ScanJobStatusFailed, Error: "timeout"},
		},
	}

	agg := AggregateReportsWithJobStatus("job-fail", "test-server", reports, job)

	if agg.ScanComplete {
		t.Error("expected ScanComplete=false when all scanners failed")
	}
	if agg.ScannersRun != 0 {
		t.Errorf("expected ScannersRun=0, got %d", agg.ScannersRun)
	}
	if agg.ScannersFailed != 2 {
		t.Errorf("expected ScannersFailed=2, got %d", agg.ScannersFailed)
	}
	if agg.ScannersTotal != 2 {
		t.Errorf("expected ScannersTotal=2, got %d", agg.ScannersTotal)
	}
	if agg.RiskScore != 0 {
		t.Errorf("expected risk score 0, got %d", agg.RiskScore)
	}
}

func TestAggregateReportsWithJobStatusPartialFailure(t *testing.T) {
	reports := []*ScanReport{
		{
			ID:        "r1",
			ScannerID: "scanner-a",
			Findings: []ScanFinding{
				{Severity: SeverityHigh, Title: "Found issue"},
			},
			RiskScore: 30,
		},
	}

	job := &ScanJob{
		ID:         "job-partial",
		ServerName: "test-server",
		Status:     ScanJobStatusCompleted,
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "scanner-a", Status: ScanJobStatusCompleted, FindingsCount: 1},
			{ScannerID: "scanner-b", Status: ScanJobStatusFailed, Error: "image not found"},
		},
	}

	agg := AggregateReportsWithJobStatus("job-partial", "test-server", reports, job)

	if !agg.ScanComplete {
		t.Error("expected ScanComplete=true when at least one scanner succeeded")
	}
	if agg.ScannersRun != 1 {
		t.Errorf("expected ScannersRun=1, got %d", agg.ScannersRun)
	}
	if agg.ScannersFailed != 1 {
		t.Errorf("expected ScannersFailed=1, got %d", agg.ScannersFailed)
	}
	if agg.ScannersTotal != 2 {
		t.Errorf("expected ScannersTotal=2, got %d", agg.ScannersTotal)
	}
	if len(agg.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(agg.Findings))
	}
}

func TestAggregateReportsEmptyScan(t *testing.T) {
	// Scanners "succeed" but scan 0 files (quarantined/disconnected server).
	// This should set scan_complete=false and empty_scan=true.
	reports := []*ScanReport{
		{
			ID:        "r1",
			ScannerID: "semgrep-mcp",
			Findings:  nil, // No findings because nothing was scanned
			RiskScore: 0,
		},
		{
			ID:        "r2",
			ScannerID: "trivy-mcp",
			Findings:  nil,
			RiskScore: 0,
		},
	}

	job := &ScanJob{
		ID:         "job-empty",
		ServerName: "test-server",
		Status:     ScanJobStatusCompleted,
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "cisco-mcp-scanner", Status: ScanJobStatusFailed, Error: "tools.json not found"},
			{ScannerID: "semgrep-mcp", Status: ScanJobStatusCompleted, FindingsCount: 0},
			{ScannerID: "trivy-mcp", Status: ScanJobStatusCompleted, FindingsCount: 0},
		},
		ScanContext: &ScanContext{
			SourceMethod: "docker_extract",
			TotalFiles:   0, // Key: no files were extracted
		},
	}

	agg := AggregateReportsWithJobStatus("job-empty", "test-server", reports, job)

	if agg.ScanComplete {
		t.Error("expected ScanComplete=false when 0 files scanned")
	}
	if !agg.EmptyScan {
		t.Error("expected EmptyScan=true when scanners ran but had no files")
	}
	if agg.RiskScore != 0 {
		t.Errorf("expected risk score 0, got %d", agg.RiskScore)
	}
}

func TestAggregateReportsDockerExtractWithToolsExported(t *testing.T) {
	// Docker extraction found container but 0 source files. However, tool
	// definitions were exported (ToolsExported > 0), so Cisco scanner had
	// data to analyze. This is a valid scan, not empty.
	reports := []*ScanReport{
		{ID: "r1", ScannerID: "cisco-mcp-scanner", Findings: nil},
		{ID: "r2", ScannerID: "semgrep-mcp", Findings: nil},
	}

	job := &ScanJob{
		ID:         "job-docker-tools",
		ServerName: "test-server",
		Status:     ScanJobStatusCompleted,
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "cisco-mcp-scanner", Status: ScanJobStatusCompleted},
			{ScannerID: "semgrep-mcp", Status: ScanJobStatusCompleted},
		},
		ScanContext: &ScanContext{
			SourceMethod:  "docker_extract",
			TotalFiles:    0,  // No source files
			ToolsExported: 13, // But tool definitions were analyzed
		},
	}

	agg := AggregateReportsWithJobStatus("job-docker-tools", "test-server", reports, job)

	if !agg.ScanComplete {
		t.Error("expected ScanComplete=true when tool definitions were exported")
	}
	if agg.EmptyScan {
		t.Error("expected EmptyScan=false when tool definitions were analyzed")
	}
}

func TestAggregateReportsEmptyScanURLServer(t *testing.T) {
	// URL-based servers (HTTP/SSE) with 0 files should NOT be marked as empty scan,
	// since they don't have filesystem source — they use behavioral scanning.
	reports := []*ScanReport{
		{ID: "r1", ScannerID: "scanner-a", Findings: nil},
	}

	job := &ScanJob{
		ID:         "job-url",
		ServerName: "http-server",
		Status:     ScanJobStatusCompleted,
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "scanner-a", Status: ScanJobStatusCompleted},
		},
		ScanContext: &ScanContext{
			SourceMethod: "url",
			TotalFiles:   0, // Expected for URL servers
		},
	}

	agg := AggregateReportsWithJobStatus("job-url", "http-server", reports, job)

	if !agg.ScanComplete {
		t.Error("expected ScanComplete=true for URL-based servers with 0 files")
	}
	if agg.EmptyScan {
		t.Error("expected EmptyScan=false for URL-based servers")
	}
}

func TestAggregateReportsToolDefinitionsOnly(t *testing.T) {
	// "tool_definitions_only" scan (no source files but Cisco scanner analyzed
	// tool descriptions) should be considered a valid scan, not empty.
	reports := []*ScanReport{
		{ID: "r1", ScannerID: "cisco-mcp-scanner", Findings: nil},
	}

	job := &ScanJob{
		ID:         "job-tools",
		ServerName: "test-server",
		Status:     ScanJobStatusCompleted,
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "cisco-mcp-scanner", Status: ScanJobStatusCompleted},
		},
		ScanContext: &ScanContext{
			SourceMethod: "tool_definitions_only",
			TotalFiles:   0,
		},
	}

	agg := AggregateReportsWithJobStatus("job-tools", "test-server", reports, job)

	if !agg.ScanComplete {
		t.Error("expected ScanComplete=true for tool_definitions_only scan")
	}
	if agg.EmptyScan {
		t.Error("expected EmptyScan=false for tool_definitions_only scan")
	}
}

func TestAggregateReportsNonEmptySuccessfulScan(t *testing.T) {
	// Normal successful scan with files and no findings = genuinely clean.
	reports := []*ScanReport{
		{ID: "r1", ScannerID: "scanner-a", Findings: nil},
	}

	job := &ScanJob{
		ID:         "job-clean",
		ServerName: "clean-server",
		Status:     ScanJobStatusCompleted,
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "scanner-a", Status: ScanJobStatusCompleted},
		},
		ScanContext: &ScanContext{
			SourceMethod: "docker_extract",
			TotalFiles:   42, // Files were actually scanned
		},
	}

	agg := AggregateReportsWithJobStatus("job-clean", "clean-server", reports, job)

	if !agg.ScanComplete {
		t.Error("expected ScanComplete=true when files were scanned and no findings")
	}
	if agg.EmptyScan {
		t.Error("expected EmptyScan=false when files were actually scanned")
	}
}

func TestEngineResolveScanners(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	registry := NewRegistry(dir, logger)

	// Mark one scanner as installed
	registry.scanners["mcp-scan"].Status = ScannerStatusInstalled

	// Use nil docker to skip image existence checks in tests
	engine := NewEngine(nil, registry, dir, logger)

	// Resolve all installed
	scanners, err := engine.resolveScanners(nil)
	if err != nil {
		t.Fatalf("resolveScanners: %v", err)
	}
	if len(scanners) != 1 {
		t.Errorf("expected 1 installed scanner, got %d", len(scanners))
	}
	if scanners[0].plugin.ID != "mcp-scan" {
		t.Errorf("expected mcp-scan, got %s", scanners[0].plugin.ID)
	}

	// Resolve specific
	scanners, err = engine.resolveScanners([]string{"mcp-scan"})
	if err != nil {
		t.Fatalf("resolveScanners specific: %v", err)
	}
	if len(scanners) != 1 {
		t.Errorf("expected 1 scanner, got %d", len(scanners))
	}

	// Resolve non-installed
	_, err = engine.resolveScanners([]string{"cisco-mcp-scanner"})
	if err == nil {
		t.Error("expected error for non-installed scanner")
	}

	// Resolve nonexistent
	_, err = engine.resolveScanners([]string{"nonexistent"})
	if err == nil {
		t.Error("expected error for nonexistent scanner")
	}
}

func TestEngineNoScanners(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	registry := NewRegistry(dir, logger)
	// Don't install any scanners

	docker := NewDockerRunner(logger)
	engine := NewEngine(docker, registry, dir, logger)

	_, err := engine.StartScan(context.Background(), ScanRequest{
		ServerName: "test-server",
	}, nil)
	if err == nil {
		t.Error("expected error when no scanners installed")
	}
}

func TestEngineParseResultsSARIF(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	registry := NewRegistry(dir, logger)
	docker := NewDockerRunner(logger)
	engine := NewEngine(docker, registry, dir, logger)

	sarifData := []byte(`{
		"version": "2.1.0",
		"runs": [{
			"tool": {"driver": {"name": "test"}},
			"results": [{
				"ruleId": "R1",
				"level": "error",
				"message": {"text": "Found vulnerability"}
			}]
		}]
	}`)

	report, err := engine.parseResults(sarifData, "test-scanner")
	if err != nil {
		t.Fatalf("parseResults: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(report.Findings))
	}
	if report.Findings[0].Severity != SeverityHigh {
		t.Errorf("expected severity %q, got %q", SeverityHigh, report.Findings[0].Severity)
	}
	if report.SarifRaw == nil {
		t.Error("expected raw SARIF to be preserved")
	}
	if report.RiskScore <= 0 {
		t.Error("expected positive risk score")
	}
}

func TestEngineParseResultsGenericJSON(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	registry := NewRegistry(dir, logger)
	docker := NewDockerRunner(logger)
	engine := NewEngine(docker, registry, dir, logger)

	// Generic JSON with findings array
	findings := []ScanFinding{
		{Severity: SeverityHigh, Title: "Found issue", Scanner: "test"},
	}
	data, _ := json.Marshal(map[string]any{"findings": findings})

	report, err := engine.parseResults(data, "test-scanner")
	if err != nil {
		t.Fatalf("parseResults: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(report.Findings))
	}
}

func TestEngineParseResultsGenericJSONResults(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	registry := NewRegistry(dir, logger)
	docker := NewDockerRunner(logger)
	engine := NewEngine(docker, registry, dir, logger)

	// Generic JSON with results array
	findings := []ScanFinding{
		{Severity: SeverityMedium, Title: "Warning"},
	}
	data, _ := json.Marshal(map[string]any{"results": findings})

	report, err := engine.parseResults(data, "test-scanner")
	if err != nil {
		t.Fatalf("parseResults: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(report.Findings))
	}
	// Scanner should be set
	if report.Findings[0].Scanner != "test-scanner" {
		t.Errorf("expected scanner 'test-scanner', got %q", report.Findings[0].Scanner)
	}
}

func TestEngineParseResultsUnparseable(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	registry := NewRegistry(dir, logger)
	docker := NewDockerRunner(logger)
	engine := NewEngine(docker, registry, dir, logger)

	// Unparseable data should result in clean report (no findings)
	report, err := engine.parseResults([]byte(`not json at all`), "test-scanner")
	if err != nil {
		t.Fatalf("parseResults should not error for unparseable: %v", err)
	}
	if len(report.Findings) != 0 {
		t.Errorf("expected 0 findings for unparseable, got %d", len(report.Findings))
	}
	if report.RiskScore != 0 {
		t.Errorf("expected 0 risk score, got %d", report.RiskScore)
	}
}

func TestEngineConcurrentScanPrevention(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	registry := NewRegistry(dir, logger)
	registry.scanners["test"] = &ScannerPlugin{
		ID:     "test",
		Status: ScannerStatusInstalled,
	}

	docker := NewDockerRunner(logger)
	engine := NewEngine(docker, registry, dir, logger)

	// Simulate an active scan
	engine.mu.Lock()
	engine.activeScans["test-server"] = &ScanJob{ID: "existing-job"}
	engine.mu.Unlock()

	_, err := engine.StartScan(context.Background(), ScanRequest{
		ServerName: "test-server",
	}, nil)
	if err == nil {
		t.Error("expected error for concurrent scan")
	}
}

func TestEngineCancelScan(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	registry := NewRegistry(dir, logger)
	docker := NewDockerRunner(logger)
	engine := NewEngine(docker, registry, dir, logger)

	// Add active scan
	engine.mu.Lock()
	engine.activeScans["test-server"] = &ScanJob{ID: "job-1", Status: ScanJobStatusRunning}
	engine.mu.Unlock()

	if err := engine.CancelScan("test-server"); err != nil {
		t.Fatalf("CancelScan: %v", err)
	}

	// Should no longer be active
	if engine.GetActiveJob("test-server") != nil {
		t.Error("expected no active job after cancel")
	}

	// Cancel non-existent
	if err := engine.CancelScan("nonexistent"); err == nil {
		t.Error("expected error cancelling non-existent scan")
	}
}

func TestUpdateScannerStatus(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	registry := NewRegistry(dir, logger)
	docker := NewDockerRunner(logger)
	engine := NewEngine(docker, registry, dir, logger)

	job := &ScanJob{
		ID: "job-1",
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "s1", Status: ScanJobStatusPending},
			{ScannerID: "s2", Status: ScanJobStatusPending},
		},
	}

	now := time.Now()
	engine.updateScannerStatus(job, "s1", ScanJobStatusRunning, now, time.Time{}, "", 0)
	if job.ScannerStatuses[0].Status != ScanJobStatusRunning {
		t.Errorf("expected running, got %s", job.ScannerStatuses[0].Status)
	}

	engine.updateScannerStatus(job, "s1", ScanJobStatusCompleted, time.Time{}, now, "", 3)
	if job.ScannerStatuses[0].FindingsCount != 3 {
		t.Errorf("expected 3 findings, got %d", job.ScannerStatuses[0].FindingsCount)
	}

	engine.updateScannerStatus(job, "s2", ScanJobStatusFailed, time.Time{}, now, "timeout", 0)
	if job.ScannerStatuses[1].Error != "timeout" {
		t.Errorf("expected error 'timeout', got %q", job.ScannerStatuses[1].Error)
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	if truncate("hello world", 5) != "hello..." {
		t.Errorf("expected 'hello...', got %q", truncate("hello world", 5))
	}
}

func TestPrepareReportDirForEngine(t *testing.T) {
	dir := t.TempDir()
	reportDir, err := PrepareReportDir(dir, "job-123", "scanner-1")
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(dir, "scanner-reports", "job-123", "scanner-1")
	if reportDir != expected {
		t.Errorf("expected %s, got %s", expected, reportDir)
	}
	if _, err := os.Stat(reportDir); os.IsNotExist(err) {
		t.Error("directory should exist")
	}
}

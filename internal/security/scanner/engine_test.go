package scanner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// mockDockerRunner is a test double for DockerRunner that doesn't need Docker
type mockDockerRunner struct {
	results map[string]mockScanResult // keyed by scanner ID
}

type mockScanResult struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}

func newMockEngine(t *testing.T, scanners []*ScannerPlugin, results map[string]mockScanResult) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	logger := zap.NewNop()

	registry := NewRegistry(dir, logger)
	// Override registry with test scanners
	for _, s := range scanners {
		registry.scanners[s.ID] = s
	}

	docker := NewDockerRunner(logger)
	engine := NewEngine(docker, registry, dir, logger)

	return engine, dir
}

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

func TestAggregateReportsEmpty(t *testing.T) {
	agg := AggregateReports("job-1", "server", nil)
	if agg.RiskScore != 0 {
		t.Errorf("expected 0 risk score, got %d", agg.RiskScore)
	}
	if agg.Summary.Total != 0 {
		t.Errorf("expected 0 total, got %d", agg.Summary.Total)
	}
}

func TestEngineResolveScanners(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	registry := NewRegistry(dir, logger)

	// Mark one scanner as installed
	registry.scanners["mcp-scan"].Status = ScannerStatusInstalled

	docker := NewDockerRunner(logger)
	engine := NewEngine(docker, registry, dir, logger)

	// Resolve all installed
	scanners, err := engine.resolveScanners(nil)
	if err != nil {
		t.Fatalf("resolveScanners: %v", err)
	}
	if len(scanners) != 1 {
		t.Errorf("expected 1 installed scanner, got %d", len(scanners))
	}
	if scanners[0].ID != "mcp-scan" {
		t.Errorf("expected mcp-scan, got %s", scanners[0].ID)
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

type testCallback struct {
	mu             sync.Mutex
	started        bool
	scannerStarted []string
	scannerDone    []string
	scannerFailed  []string
	completed      bool
	failed         bool
	reports        []*ScanReport
}

func (c *testCallback) OnScanStarted(_ *ScanJob) { c.mu.Lock(); c.started = true; c.mu.Unlock() }
func (c *testCallback) OnScannerStarted(_ *ScanJob, id string) {
	c.mu.Lock()
	c.scannerStarted = append(c.scannerStarted, id)
	c.mu.Unlock()
}
func (c *testCallback) OnScannerCompleted(_ *ScanJob, id string, report *ScanReport) {
	c.mu.Lock()
	c.scannerDone = append(c.scannerDone, id)
	c.reports = append(c.reports, report)
	c.mu.Unlock()
}
func (c *testCallback) OnScannerFailed(_ *ScanJob, id string, _ error) {
	c.mu.Lock()
	c.scannerFailed = append(c.scannerFailed, id)
	c.mu.Unlock()
}
func (c *testCallback) OnScanCompleted(_ *ScanJob, reports []*ScanReport) {
	c.mu.Lock()
	c.completed = true
	c.mu.Unlock()
}
func (c *testCallback) OnScanFailed(_ *ScanJob, _ error) { c.mu.Lock(); c.failed = true; c.mu.Unlock() }

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

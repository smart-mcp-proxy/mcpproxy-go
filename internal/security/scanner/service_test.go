package scanner

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// mockStorage implements the Storage interface for testing
type mockStorage struct {
	mu        sync.Mutex
	scanners  map[string]*ScannerPlugin
	jobs      map[string]*ScanJob
	reports   map[string]*ScanReport
	baselines map[string]*IntegrityBaseline
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		scanners:  make(map[string]*ScannerPlugin),
		jobs:      make(map[string]*ScanJob),
		reports:   make(map[string]*ScanReport),
		baselines: make(map[string]*IntegrityBaseline),
	}
}

func (m *mockStorage) SaveScanner(s *ScannerPlugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scanners[s.ID] = s
	return nil
}

func (m *mockStorage) GetScanner(id string) (*ScannerPlugin, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.scanners[id]
	if !ok {
		return nil, fmt.Errorf("scanner not found: %s", id)
	}
	return s, nil
}

func (m *mockStorage) ListScanners() ([]*ScannerPlugin, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*ScannerPlugin, 0, len(m.scanners))
	for _, s := range m.scanners {
		result = append(result, s)
	}
	return result, nil
}

func (m *mockStorage) DeleteScanner(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.scanners, id)
	return nil
}

func (m *mockStorage) SaveScanJob(job *ScanJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[job.ID] = job
	return nil
}

func (m *mockStorage) GetScanJob(id string) (*ScanJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, fmt.Errorf("scan job not found: %s", id)
	}
	return j, nil
}

func (m *mockStorage) ListScanJobs(serverName string) ([]*ScanJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*ScanJob
	for _, j := range m.jobs {
		if serverName != "" && j.ServerName != serverName {
			continue
		}
		result = append(result, j)
	}
	return result, nil
}

func (m *mockStorage) GetLatestScanJob(serverName string) (*ScanJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *ScanJob
	for _, j := range m.jobs {
		if j.ServerName != serverName {
			continue
		}
		if latest == nil || j.StartedAt.After(latest.StartedAt) {
			latest = j
		}
	}
	if latest == nil {
		return nil, fmt.Errorf("no scan jobs found for server: %s", serverName)
	}
	return latest, nil
}

func (m *mockStorage) DeleteScanJob(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.jobs, id)
	return nil
}

func (m *mockStorage) DeleteServerScanJobs(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, j := range m.jobs {
		if j.ServerName == serverName {
			delete(m.jobs, id)
		}
	}
	return nil
}

func (m *mockStorage) SaveScanReport(report *ScanReport) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reports[report.ID] = report
	return nil
}

func (m *mockStorage) GetScanReport(id string) (*ScanReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.reports[id]
	if !ok {
		return nil, fmt.Errorf("scan report not found: %s", id)
	}
	return r, nil
}

func (m *mockStorage) ListScanReports(serverName string) ([]*ScanReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*ScanReport
	for _, r := range m.reports {
		if serverName != "" && r.ServerName != serverName {
			continue
		}
		result = append(result, r)
	}
	return result, nil
}

func (m *mockStorage) ListScanReportsByJob(jobID string) ([]*ScanReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*ScanReport
	for _, r := range m.reports {
		if r.JobID == jobID {
			result = append(result, r)
		}
	}
	return result, nil
}

func (m *mockStorage) DeleteScanReport(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.reports, id)
	return nil
}

func (m *mockStorage) DeleteServerScanReports(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.reports {
		if r.ServerName == serverName {
			delete(m.reports, id)
		}
	}
	return nil
}

func (m *mockStorage) SaveIntegrityBaseline(baseline *IntegrityBaseline) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.baselines[baseline.ServerName] = baseline
	return nil
}

func (m *mockStorage) GetIntegrityBaseline(serverName string) (*IntegrityBaseline, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.baselines[serverName]
	if !ok {
		return nil, fmt.Errorf("integrity baseline not found: %s", serverName)
	}
	return b, nil
}

func (m *mockStorage) DeleteIntegrityBaseline(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.baselines, serverName)
	return nil
}

func (m *mockStorage) ListIntegrityBaselines() ([]*IntegrityBaseline, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*IntegrityBaseline, 0, len(m.baselines))
	for _, b := range m.baselines {
		result = append(result, b)
	}
	return result, nil
}

// mockEmitter records emitted events for test assertions
type mockEmitter struct {
	mu     sync.Mutex
	events []mockEvent
}

type mockEvent struct {
	eventType string
	data      map[string]interface{}
}

func newMockEmitter() *mockEmitter {
	return &mockEmitter{}
}

func (e *mockEmitter) EmitSecurityScanStarted(serverName string, scanners []string, jobID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, mockEvent{
		eventType: "scan_started",
		data:      map[string]interface{}{"server_name": serverName, "scanners": scanners, "job_id": jobID},
	})
}

func (e *mockEmitter) EmitSecurityScanProgress(serverName, scannerID, status string, progress int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, mockEvent{
		eventType: "scan_progress",
		data:      map[string]interface{}{"server_name": serverName, "scanner_id": scannerID, "status": status, "progress": progress},
	})
}

func (e *mockEmitter) EmitSecurityScanCompleted(serverName string, findingsSummary map[string]int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, mockEvent{
		eventType: "scan_completed",
		data:      map[string]interface{}{"server_name": serverName, "findings_summary": findingsSummary},
	})
}

func (e *mockEmitter) EmitSecurityScanFailed(serverName, scannerID, errMsg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, mockEvent{
		eventType: "scan_failed",
		data:      map[string]interface{}{"server_name": serverName, "scanner_id": scannerID, "error": errMsg},
	})
}

func (e *mockEmitter) EmitSecurityIntegrityAlert(serverName, alertType, action string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, mockEvent{
		eventType: "integrity_alert",
		data:      map[string]interface{}{"server_name": serverName, "alert_type": alertType, "action": action},
	})
}

func (e *mockEmitter) eventCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.events)
}

func (e *mockEmitter) lastEventType() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.events) == 0 {
		return ""
	}
	return e.events[len(e.events)-1].eventType
}

// helper to create a service with test registry and mock storage
func newTestService(t *testing.T) (*Service, *mockStorage, *mockEmitter) {
	t.Helper()
	logger := zap.NewNop()
	store := newMockStorage()
	docker := NewDockerRunner(logger)
	registry := &Registry{
		scanners: map[string]*ScannerPlugin{
			"test-scanner": {
				ID:          "test-scanner",
				Name:        "Test Scanner",
				DockerImage: "test/scanner:latest",
				Inputs:      []string{"source"},
				Outputs:     []string{"sarif"},
				Command:     []string{"scan"},
				Status:      ScannerStatusAvailable,
			},
			"scanner-b": {
				ID:          "scanner-b",
				Name:        "Scanner B",
				DockerImage: "test/scanner-b:latest",
				Inputs:      []string{"source"},
				Outputs:     []string{"sarif"},
				Command:     []string{"scan-b"},
				Status:      ScannerStatusAvailable,
			},
		},
		logger: logger,
	}
	svc := NewService(store, registry, docker, t.TempDir(), logger)
	emitter := newMockEmitter()
	svc.SetEmitter(emitter)
	return svc, store, emitter
}

func TestServiceListScannersEmpty(t *testing.T) {
	svc, _, _ := newTestService(t)

	scanners, err := svc.ListScanners(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return registry scanners (2 from test setup)
	if len(scanners) != 2 {
		t.Fatalf("expected 2 scanners from registry, got %d", len(scanners))
	}

	// All should have "available" status since nothing is installed
	for _, s := range scanners {
		if s.Status != ScannerStatusAvailable {
			t.Errorf("expected status 'available' for %s, got %s", s.ID, s.Status)
		}
	}
}

func TestServiceListScannersMerge(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Install "test-scanner" into storage
	_ = store.SaveScanner(&ScannerPlugin{
		ID:          "test-scanner",
		Name:        "Test Scanner",
		DockerImage: "test/scanner:latest",
		Status:      ScannerStatusInstalled,
		InstalledAt: time.Now(),
		ConfiguredEnv: map[string]string{
			"API_KEY": "secret-key",
		},
	})

	// Also add a custom scanner not in registry
	_ = store.SaveScanner(&ScannerPlugin{
		ID:          "custom-scanner",
		Name:        "Custom Scanner",
		DockerImage: "custom/scanner:latest",
		Status:      ScannerStatusConfigured,
		Custom:      true,
	})

	scanners, err := svc.ListScanners(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2 from registry + 1 custom = 3
	if len(scanners) != 3 {
		t.Fatalf("expected 3 scanners, got %d", len(scanners))
	}

	// Find test-scanner - should have merged state from storage
	var testScanner *ScannerPlugin
	var customScanner *ScannerPlugin
	var scannerB *ScannerPlugin
	for _, s := range scanners {
		switch s.ID {
		case "test-scanner":
			testScanner = s
		case "custom-scanner":
			customScanner = s
		case "scanner-b":
			scannerB = s
		}
	}

	if testScanner == nil {
		t.Fatal("test-scanner not found in results")
	}
	if testScanner.Status != ScannerStatusInstalled {
		t.Errorf("expected test-scanner status 'installed', got %s", testScanner.Status)
	}
	if testScanner.ConfiguredEnv["API_KEY"] != "secret-key" {
		t.Error("expected configured env to be merged from storage")
	}
	// Metadata should come from registry
	if testScanner.Description != "" {
		// Registry test-scanner has no description, but Name should match
		if testScanner.Name != "Test Scanner" {
			t.Errorf("expected name from registry, got %s", testScanner.Name)
		}
	}

	if customScanner == nil {
		t.Fatal("custom-scanner not found in results")
	}
	if customScanner.Status != ScannerStatusConfigured {
		t.Errorf("expected custom-scanner status 'configured', got %s", customScanner.Status)
	}

	if scannerB == nil {
		t.Fatal("scanner-b not found in results")
	}
	if scannerB.Status != ScannerStatusAvailable {
		t.Errorf("expected scanner-b status 'available', got %s", scannerB.Status)
	}
}

func TestServiceConfigureScanner(t *testing.T) {
	svc, store, _ := newTestService(t)

	// First install the scanner
	_ = store.SaveScanner(&ScannerPlugin{
		ID:          "test-scanner",
		Name:        "Test Scanner",
		DockerImage: "test/scanner:latest",
		Status:      ScannerStatusInstalled,
		InstalledAt: time.Now(),
	})

	// Configure it
	env := map[string]string{
		"API_KEY":    "my-key",
		"API_SECRET": "my-secret",
	}
	err := svc.ConfigureScanner(context.Background(), "test-scanner", env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify storage was updated
	updated, err := store.GetScanner("test-scanner")
	if err != nil {
		t.Fatalf("failed to get scanner: %v", err)
	}
	if updated.Status != ScannerStatusConfigured {
		t.Errorf("expected status 'configured', got %s", updated.Status)
	}
	if updated.ConfiguredEnv["API_KEY"] != "my-key" {
		t.Error("expected API_KEY to be set")
	}
	if updated.ConfiguredEnv["API_SECRET"] != "my-secret" {
		t.Error("expected API_SECRET to be set")
	}
}

func TestServiceConfigureScannerNotInstalled(t *testing.T) {
	svc, _, _ := newTestService(t)

	err := svc.ConfigureScanner(context.Background(), "nonexistent", map[string]string{"KEY": "val"})
	if err == nil {
		t.Fatal("expected error for non-installed scanner")
	}
}

func TestServiceApproveServerNoCritical(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Create a scan job and report with only medium findings
	job := &ScanJob{
		ID:         "job-1",
		ServerName: "my-server",
		Status:     ScanJobStatusCompleted,
		Scanners:   []string{"test-scanner"},
		StartedAt:  time.Now().Add(-1 * time.Minute),
	}
	_ = store.SaveScanJob(job)

	report := &ScanReport{
		ID:         "report-1",
		JobID:      "job-1",
		ServerName: "my-server",
		ScannerID:  "test-scanner",
		Findings: []ScanFinding{
			{RuleID: "R1", Severity: SeverityMedium, Title: "Medium issue"},
			{RuleID: "R2", Severity: SeverityLow, Title: "Low issue"},
		},
		ScannedAt: time.Now(),
	}
	_ = store.SaveScanReport(report)

	// Approve should succeed
	err := svc.ApproveServer(context.Background(), "my-server", false, "admin@test.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify baseline was created
	baseline, err := store.GetIntegrityBaseline("my-server")
	if err != nil {
		t.Fatalf("expected baseline to exist: %v", err)
	}
	if baseline.ApprovedBy != "admin@test.com" {
		t.Errorf("expected approved_by 'admin@test.com', got %s", baseline.ApprovedBy)
	}
	if len(baseline.ScanReportIDs) != 1 || baseline.ScanReportIDs[0] != "report-1" {
		t.Errorf("expected scan report IDs [report-1], got %v", baseline.ScanReportIDs)
	}
}

func TestServiceApproveServerBlockedByCritical(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Create a scan job and report with critical findings
	job := &ScanJob{
		ID:         "job-crit",
		ServerName: "risky-server",
		Status:     ScanJobStatusCompleted,
		Scanners:   []string{"test-scanner"},
		StartedAt:  time.Now().Add(-1 * time.Minute),
	}
	_ = store.SaveScanJob(job)

	report := &ScanReport{
		ID:         "report-crit",
		JobID:      "job-crit",
		ServerName: "risky-server",
		ScannerID:  "test-scanner",
		Findings: []ScanFinding{
			{RuleID: "C1", Severity: SeverityCritical, Title: "Critical vuln"},
			{RuleID: "C2", Severity: SeverityCritical, Title: "Another critical"},
			{RuleID: "M1", Severity: SeverityMedium, Title: "Medium issue"},
		},
		ScannedAt: time.Now(),
	}
	_ = store.SaveScanReport(report)

	// Approve without force should fail
	err := svc.ApproveServer(context.Background(), "risky-server", false, "admin@test.com")
	if err == nil {
		t.Fatal("expected error due to critical findings")
	}

	// Verify baseline was NOT created
	_, err = store.GetIntegrityBaseline("risky-server")
	if err == nil {
		t.Fatal("expected baseline to not exist after rejected approval")
	}
}

func TestServiceApproveServerForce(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Create a scan job and report with critical findings
	job := &ScanJob{
		ID:         "job-force",
		ServerName: "risky-server",
		Status:     ScanJobStatusCompleted,
		Scanners:   []string{"test-scanner"},
		StartedAt:  time.Now().Add(-1 * time.Minute),
	}
	_ = store.SaveScanJob(job)

	report := &ScanReport{
		ID:         "report-force",
		JobID:      "job-force",
		ServerName: "risky-server",
		ScannerID:  "test-scanner",
		Findings: []ScanFinding{
			{RuleID: "C1", Severity: SeverityCritical, Title: "Critical vuln"},
		},
		ScannedAt: time.Now(),
	}
	_ = store.SaveScanReport(report)

	// Force approve should succeed even with critical findings
	err := svc.ApproveServer(context.Background(), "risky-server", true, "admin@test.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify baseline was created
	baseline, err := store.GetIntegrityBaseline("risky-server")
	if err != nil {
		t.Fatalf("expected baseline to exist: %v", err)
	}
	if baseline.ApprovedBy != "admin@test.com" {
		t.Errorf("expected approved_by 'admin@test.com', got %s", baseline.ApprovedBy)
	}
}

func TestServiceApproveServerNoScanForce(t *testing.T) {
	svc, store, _ := newTestService(t)

	// No scan results exist. Force approve should still work.
	err := svc.ApproveServer(context.Background(), "new-server", true, "admin@test.com")
	if err != nil {
		t.Fatalf("unexpected error with force and no scan: %v", err)
	}

	baseline, err := store.GetIntegrityBaseline("new-server")
	if err != nil {
		t.Fatalf("expected baseline: %v", err)
	}
	if baseline.ServerName != "new-server" {
		t.Errorf("expected server_name 'new-server', got %s", baseline.ServerName)
	}
}

func TestServiceApproveServerNoScanNoForce(t *testing.T) {
	svc, _, _ := newTestService(t)

	// No scan results. Without force, should fail.
	err := svc.ApproveServer(context.Background(), "new-server", false, "admin@test.com")
	if err == nil {
		t.Fatal("expected error when no scan results and no force")
	}
}

func TestServiceRejectServer(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Set up artifacts for the server
	_ = store.SaveScanJob(&ScanJob{
		ID: "job-rej", ServerName: "bad-server", Status: ScanJobStatusCompleted,
		StartedAt: time.Now(),
	})
	_ = store.SaveScanReport(&ScanReport{
		ID: "report-rej", JobID: "job-rej", ServerName: "bad-server", ScannerID: "test-scanner",
		ScannedAt: time.Now(),
	})
	_ = store.SaveIntegrityBaseline(&IntegrityBaseline{
		ServerName: "bad-server", ApprovedAt: time.Now(), ApprovedBy: "admin",
	})

	// Reject
	err := svc.RejectServer(context.Background(), "bad-server")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all artifacts cleaned up
	jobs, _ := store.ListScanJobs("bad-server")
	if len(jobs) != 0 {
		t.Errorf("expected 0 jobs after rejection, got %d", len(jobs))
	}

	reports, _ := store.ListScanReports("bad-server")
	if len(reports) != 0 {
		t.Errorf("expected 0 reports after rejection, got %d", len(reports))
	}

	_, err = store.GetIntegrityBaseline("bad-server")
	if err == nil {
		t.Error("expected baseline to be deleted after rejection")
	}
}

func TestServiceOverview(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Set up some data
	_ = store.SaveScanner(&ScannerPlugin{ID: "s1", Status: ScannerStatusInstalled})
	_ = store.SaveScanner(&ScannerPlugin{ID: "s2", Status: ScannerStatusConfigured})

	now := time.Now()
	_ = store.SaveScanJob(&ScanJob{
		ID: "j1", ServerName: "server-a", Status: ScanJobStatusCompleted,
		Scanners: []string{"s1"}, StartedAt: now.Add(-2 * time.Hour),
	})
	_ = store.SaveScanJob(&ScanJob{
		ID: "j2", ServerName: "server-b", Status: ScanJobStatusRunning,
		Scanners: []string{"s1"}, StartedAt: now.Add(-1 * time.Minute),
	})
	_ = store.SaveScanJob(&ScanJob{
		ID: "j3", ServerName: "server-a", Status: ScanJobStatusCompleted,
		Scanners: []string{"s2"}, StartedAt: now.Add(-30 * time.Minute),
	})

	_ = store.SaveScanReport(&ScanReport{
		ID: "r1", JobID: "j1", ServerName: "server-a", ScannerID: "s1",
		Findings: []ScanFinding{
			{Severity: SeverityCritical}, {Severity: SeverityHigh}, {Severity: SeverityMedium},
		},
		ScannedAt: now,
	})
	_ = store.SaveScanReport(&ScanReport{
		ID: "r2", JobID: "j3", ServerName: "server-a", ScannerID: "s2",
		Findings: []ScanFinding{
			{Severity: SeverityLow}, {Severity: SeverityInfo},
		},
		ScannedAt: now,
	})

	overview, err := svc.GetOverview(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if overview.ScannersInstalled != 2 {
		t.Errorf("expected 2 scanners installed, got %d", overview.ScannersInstalled)
	}
	if overview.TotalScans != 3 {
		t.Errorf("expected 3 total scans, got %d", overview.TotalScans)
	}
	if overview.ActiveScans != 1 {
		t.Errorf("expected 1 active scan, got %d", overview.ActiveScans)
	}
	if overview.ServersScanned != 2 {
		t.Errorf("expected 2 servers scanned, got %d", overview.ServersScanned)
	}
	if overview.FindingsBySeverity.Critical != 1 {
		t.Errorf("expected 1 critical finding, got %d", overview.FindingsBySeverity.Critical)
	}
	if overview.FindingsBySeverity.High != 1 {
		t.Errorf("expected 1 high finding, got %d", overview.FindingsBySeverity.High)
	}
	if overview.FindingsBySeverity.Medium != 1 {
		t.Errorf("expected 1 medium finding, got %d", overview.FindingsBySeverity.Medium)
	}
	if overview.FindingsBySeverity.Low != 1 {
		t.Errorf("expected 1 low finding, got %d", overview.FindingsBySeverity.Low)
	}
	if overview.FindingsBySeverity.Info != 1 {
		t.Errorf("expected 1 info finding, got %d", overview.FindingsBySeverity.Info)
	}
	if overview.FindingsBySeverity.Total != 5 {
		t.Errorf("expected 5 total findings, got %d", overview.FindingsBySeverity.Total)
	}
}

func TestServiceGetScanner(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Getting an installed scanner should return from storage
	_ = store.SaveScanner(&ScannerPlugin{
		ID:          "test-scanner",
		Name:        "Test Scanner Installed",
		DockerImage: "test/scanner:latest",
		Status:      ScannerStatusInstalled,
	})

	sc, err := svc.GetScanner(context.Background(), "test-scanner")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sc.Status != ScannerStatusInstalled {
		t.Errorf("expected installed status from storage, got %s", sc.Status)
	}

	// Getting a non-installed scanner should fall back to registry
	sc, err = svc.GetScanner(context.Background(), "scanner-b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sc.Status != ScannerStatusAvailable {
		t.Errorf("expected available status from registry, got %s", sc.Status)
	}

	// Getting a non-existent scanner should error
	_, err = svc.GetScanner(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent scanner")
	}
}

func TestServiceGetScanReport(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Set up job and reports
	_ = store.SaveScanJob(&ScanJob{
		ID: "j1", ServerName: "server-a", Status: ScanJobStatusCompleted,
		Scanners: []string{"s1", "s2"}, StartedAt: time.Now(),
	})
	_ = store.SaveScanReport(&ScanReport{
		ID: "r1", JobID: "j1", ServerName: "server-a", ScannerID: "s1",
		Findings: []ScanFinding{
			{RuleID: "R1", Severity: SeverityHigh, Title: "High issue"},
		},
		ScannedAt: time.Now(),
	})
	_ = store.SaveScanReport(&ScanReport{
		ID: "r2", JobID: "j1", ServerName: "server-a", ScannerID: "s2",
		Findings: []ScanFinding{
			{RuleID: "R2", Severity: SeverityLow, Title: "Low issue"},
		},
		ScannedAt: time.Now(),
	})

	agg, err := svc.GetScanReport(context.Background(), "server-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agg.JobID != "j1" {
		t.Errorf("expected job ID j1, got %s", agg.JobID)
	}
	if len(agg.Findings) != 2 {
		t.Errorf("expected 2 aggregated findings, got %d", len(agg.Findings))
	}
	if agg.Summary.High != 1 || agg.Summary.Low != 1 {
		t.Errorf("unexpected summary: %+v", agg.Summary)
	}
}

func TestServiceGetScanReportNoScan(t *testing.T) {
	svc, _, _ := newTestService(t)

	_, err := svc.GetScanReport(context.Background(), "no-such-server")
	if err == nil {
		t.Fatal("expected error when no scan exists")
	}
}

func TestServiceRemoveScanner(t *testing.T) {
	svc, store, _ := newTestService(t)

	_ = store.SaveScanner(&ScannerPlugin{
		ID:          "test-scanner",
		Name:        "Test Scanner",
		DockerImage: "test/scanner:latest",
		Status:      ScannerStatusInstalled,
	})

	err := svc.RemoveScanner(context.Background(), "test-scanner")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = store.GetScanner("test-scanner")
	if err == nil {
		t.Error("expected scanner to be deleted from storage")
	}
}

func TestServiceRemoveScannerNotInstalled(t *testing.T) {
	svc, _, _ := newTestService(t)

	err := svc.RemoveScanner(context.Background(), "test-scanner")
	if err == nil {
		t.Fatal("expected error for non-installed scanner")
	}
}

func TestServiceNoopEmitterDefault(t *testing.T) {
	logger := zap.NewNop()
	store := newMockStorage()
	docker := NewDockerRunner(logger)
	registry := &Registry{scanners: make(map[string]*ScannerPlugin), logger: logger}
	svc := NewService(store, registry, docker, t.TempDir(), logger)

	// Default emitter should be NoopEmitter - should not panic
	svc.emitter.EmitSecurityScanStarted("test", []string{"s1"}, "j1")
	svc.emitter.EmitSecurityScanCompleted("test", map[string]int{"high": 1})
	svc.emitter.EmitSecurityScanFailed("test", "s1", "error")
	svc.emitter.EmitSecurityScanProgress("test", "s1", "running", 50)
	svc.emitter.EmitSecurityIntegrityAlert("test", "mismatch", "quarantine")
}

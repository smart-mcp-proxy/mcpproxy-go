package storage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"go.uber.org/zap"
)

func newTestDB(t *testing.T) *BoltDB {
	t.Helper()
	dir := t.TempDir()
	logger := zap.NewNop().Sugar()
	db, err := NewBoltDB(dir, logger)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestScannerCRUD(t *testing.T) {
	db := newTestDB(t)

	// Test Save + Get
	s := &scanner.ScannerPlugin{
		ID:          "test-scanner",
		Name:        "Test Scanner",
		Vendor:      "Test Vendor",
		Description: "A test scanner",
		DockerImage: "test/scanner:latest",
		Status:      scanner.ScannerStatusInstalled,
		InstalledAt: time.Now().Truncate(time.Second),
		Inputs:      []string{"source"},
		Outputs:     []string{"sarif"},
	}

	if err := db.SaveScanner(s); err != nil {
		t.Fatalf("SaveScanner failed: %v", err)
	}

	got, err := db.GetScanner("test-scanner")
	if err != nil {
		t.Fatalf("GetScanner failed: %v", err)
	}
	if got.Name != "Test Scanner" {
		t.Errorf("expected name 'Test Scanner', got %q", got.Name)
	}
	if got.Vendor != "Test Vendor" {
		t.Errorf("expected vendor 'Test Vendor', got %q", got.Vendor)
	}
	if got.Status != scanner.ScannerStatusInstalled {
		t.Errorf("expected status %q, got %q", scanner.ScannerStatusInstalled, got.Status)
	}

	// Test List
	scanners, err := db.ListScanners()
	if err != nil {
		t.Fatalf("ListScanners failed: %v", err)
	}
	if len(scanners) != 1 {
		t.Errorf("expected 1 scanner, got %d", len(scanners))
	}

	// Test Update
	s.Status = scanner.ScannerStatusConfigured
	s.ConfiguredEnv = map[string]string{"API_KEY": "secret"}
	if err := db.SaveScanner(s); err != nil {
		t.Fatalf("SaveScanner (update) failed: %v", err)
	}
	got, err = db.GetScanner("test-scanner")
	if err != nil {
		t.Fatalf("GetScanner after update failed: %v", err)
	}
	if got.Status != scanner.ScannerStatusConfigured {
		t.Errorf("expected status %q, got %q", scanner.ScannerStatusConfigured, got.Status)
	}

	// Test Delete
	if err := db.DeleteScanner("test-scanner"); err != nil {
		t.Fatalf("DeleteScanner failed: %v", err)
	}
	_, err = db.GetScanner("test-scanner")
	if err == nil {
		t.Error("expected error after delete, got nil")
	}

	// List after delete should be empty
	scanners, err = db.ListScanners()
	if err != nil {
		t.Fatalf("ListScanners after delete failed: %v", err)
	}
	if len(scanners) != 0 {
		t.Errorf("expected 0 scanners after delete, got %d", len(scanners))
	}
}

func TestScannerGetNotFound(t *testing.T) {
	db := newTestDB(t)

	_, err := db.GetScanner("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent scanner, got nil")
	}
}

func TestScanJobCRUD(t *testing.T) {
	db := newTestDB(t)

	now := time.Now().Truncate(time.Second)

	// Test Save + Get
	job := &scanner.ScanJob{
		ID:         "job-001",
		ServerName: "server-a",
		Status:     scanner.ScanJobStatusPending,
		Scanners:   []string{"scanner-1", "scanner-2"},
		StartedAt:  now,
		ScannerStatuses: []scanner.ScannerJobStatus{
			{
				ScannerID: "scanner-1",
				Status:    scanner.ScanJobStatusPending,
			},
		},
	}

	if err := db.SaveScanJob(job); err != nil {
		t.Fatalf("SaveScanJob failed: %v", err)
	}

	got, err := db.GetScanJob("job-001")
	if err != nil {
		t.Fatalf("GetScanJob failed: %v", err)
	}
	if got.ServerName != "server-a" {
		t.Errorf("expected server 'server-a', got %q", got.ServerName)
	}
	if got.Status != scanner.ScanJobStatusPending {
		t.Errorf("expected status %q, got %q", scanner.ScanJobStatusPending, got.Status)
	}
	if len(got.Scanners) != 2 {
		t.Errorf("expected 2 scanners, got %d", len(got.Scanners))
	}

	// Test ListScanJobs - all
	job2 := &scanner.ScanJob{
		ID:         "job-002",
		ServerName: "server-b",
		Status:     scanner.ScanJobStatusRunning,
		Scanners:   []string{"scanner-1"},
		StartedAt:  now.Add(time.Minute),
	}
	if err := db.SaveScanJob(job2); err != nil {
		t.Fatalf("SaveScanJob (job2) failed: %v", err)
	}

	allJobs, err := db.ListScanJobs("")
	if err != nil {
		t.Fatalf("ListScanJobs (all) failed: %v", err)
	}
	if len(allJobs) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(allJobs))
	}

	// Test ListScanJobs - filtered by server
	serverAJobs, err := db.ListScanJobs("server-a")
	if err != nil {
		t.Fatalf("ListScanJobs (server-a) failed: %v", err)
	}
	if len(serverAJobs) != 1 {
		t.Errorf("expected 1 job for server-a, got %d", len(serverAJobs))
	}

	// Test GetLatestScanJob
	job3 := &scanner.ScanJob{
		ID:         "job-003",
		ServerName: "server-a",
		Status:     scanner.ScanJobStatusCompleted,
		Scanners:   []string{"scanner-1"},
		StartedAt:  now.Add(2 * time.Minute),
	}
	if err := db.SaveScanJob(job3); err != nil {
		t.Fatalf("SaveScanJob (job3) failed: %v", err)
	}

	latest, err := db.GetLatestScanJob("server-a")
	if err != nil {
		t.Fatalf("GetLatestScanJob failed: %v", err)
	}
	if latest.ID != "job-003" {
		t.Errorf("expected latest job ID 'job-003', got %q", latest.ID)
	}

	// Test GetLatestScanJob - nonexistent server
	_, err = db.GetLatestScanJob("nonexistent-server")
	if err == nil {
		t.Error("expected error for nonexistent server, got nil")
	}

	// Test Delete
	if err := db.DeleteScanJob("job-001"); err != nil {
		t.Fatalf("DeleteScanJob failed: %v", err)
	}
	_, err = db.GetScanJob("job-001")
	if err == nil {
		t.Error("expected error after delete, got nil")
	}

	// Test DeleteServerScanJobs
	if err := db.DeleteServerScanJobs("server-a"); err != nil {
		t.Fatalf("DeleteServerScanJobs failed: %v", err)
	}
	serverAJobs, err = db.ListScanJobs("server-a")
	if err != nil {
		t.Fatalf("ListScanJobs after DeleteServerScanJobs failed: %v", err)
	}
	if len(serverAJobs) != 0 {
		t.Errorf("expected 0 jobs for server-a after delete, got %d", len(serverAJobs))
	}

	// server-b job should still exist
	serverBJobs, err := db.ListScanJobs("server-b")
	if err != nil {
		t.Fatalf("ListScanJobs (server-b) failed: %v", err)
	}
	if len(serverBJobs) != 1 {
		t.Errorf("expected 1 job for server-b, got %d", len(serverBJobs))
	}
}

func TestScanJobGetNotFound(t *testing.T) {
	db := newTestDB(t)

	_, err := db.GetScanJob("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent scan job, got nil")
	}
}

func TestScanReportCRUD(t *testing.T) {
	db := newTestDB(t)

	now := time.Now().Truncate(time.Second)

	// Test Save + Get
	report := &scanner.ScanReport{
		ID:         "report-001",
		JobID:      "job-001",
		ServerName: "server-a",
		ScannerID:  "scanner-1",
		Findings: []scanner.ScanFinding{
			{
				RuleID:      "RULE-001",
				Severity:    scanner.SeverityCritical,
				Category:    "injection",
				Title:       "SQL Injection",
				Description: "Potential SQL injection vulnerability",
				Location:    "handler.go:42",
				Scanner:     "scanner-1",
			},
			{
				RuleID:      "RULE-002",
				Severity:    scanner.SeverityLow,
				Category:    "style",
				Title:       "Missing comment",
				Description: "Function lacks documentation",
				Scanner:     "scanner-1",
			},
		},
		RiskScore: 75,
		SarifRaw:  json.RawMessage(`{"version":"2.1.0"}`),
		ScannedAt: now,
	}

	if err := db.SaveScanReport(report); err != nil {
		t.Fatalf("SaveScanReport failed: %v", err)
	}

	got, err := db.GetScanReport("report-001")
	if err != nil {
		t.Fatalf("GetScanReport failed: %v", err)
	}
	if got.ServerName != "server-a" {
		t.Errorf("expected server 'server-a', got %q", got.ServerName)
	}
	if got.RiskScore != 75 {
		t.Errorf("expected risk score 75, got %d", got.RiskScore)
	}
	if len(got.Findings) != 2 {
		t.Errorf("expected 2 findings, got %d", len(got.Findings))
	}
	if got.Findings[0].Severity != scanner.SeverityCritical {
		t.Errorf("expected first finding severity %q, got %q", scanner.SeverityCritical, got.Findings[0].Severity)
	}

	// Test ListScanReports - all
	report2 := &scanner.ScanReport{
		ID:         "report-002",
		JobID:      "job-001",
		ServerName: "server-b",
		ScannerID:  "scanner-1",
		Findings:   []scanner.ScanFinding{},
		RiskScore:  0,
		ScannedAt:  now,
	}
	if err := db.SaveScanReport(report2); err != nil {
		t.Fatalf("SaveScanReport (report2) failed: %v", err)
	}

	report3 := &scanner.ScanReport{
		ID:         "report-003",
		JobID:      "job-002",
		ServerName: "server-a",
		ScannerID:  "scanner-2",
		Findings:   []scanner.ScanFinding{},
		RiskScore:  10,
		ScannedAt:  now.Add(time.Minute),
	}
	if err := db.SaveScanReport(report3); err != nil {
		t.Fatalf("SaveScanReport (report3) failed: %v", err)
	}

	allReports, err := db.ListScanReports("")
	if err != nil {
		t.Fatalf("ListScanReports (all) failed: %v", err)
	}
	if len(allReports) != 3 {
		t.Errorf("expected 3 reports, got %d", len(allReports))
	}

	// Test ListScanReports - filtered by server
	serverAReports, err := db.ListScanReports("server-a")
	if err != nil {
		t.Fatalf("ListScanReports (server-a) failed: %v", err)
	}
	if len(serverAReports) != 2 {
		t.Errorf("expected 2 reports for server-a, got %d", len(serverAReports))
	}

	// Test ListScanReportsByJob
	job1Reports, err := db.ListScanReportsByJob("job-001")
	if err != nil {
		t.Fatalf("ListScanReportsByJob (job-001) failed: %v", err)
	}
	if len(job1Reports) != 2 {
		t.Errorf("expected 2 reports for job-001, got %d", len(job1Reports))
	}

	job2Reports, err := db.ListScanReportsByJob("job-002")
	if err != nil {
		t.Fatalf("ListScanReportsByJob (job-002) failed: %v", err)
	}
	if len(job2Reports) != 1 {
		t.Errorf("expected 1 report for job-002, got %d", len(job2Reports))
	}

	// Test Delete single report
	if err := db.DeleteScanReport("report-001"); err != nil {
		t.Fatalf("DeleteScanReport failed: %v", err)
	}
	_, err = db.GetScanReport("report-001")
	if err == nil {
		t.Error("expected error after delete, got nil")
	}

	// Test DeleteServerScanReports
	if err := db.DeleteServerScanReports("server-a"); err != nil {
		t.Fatalf("DeleteServerScanReports failed: %v", err)
	}
	serverAReports, err = db.ListScanReports("server-a")
	if err != nil {
		t.Fatalf("ListScanReports after DeleteServerScanReports failed: %v", err)
	}
	if len(serverAReports) != 0 {
		t.Errorf("expected 0 reports for server-a after delete, got %d", len(serverAReports))
	}

	// server-b report should still exist
	serverBReports, err := db.ListScanReports("server-b")
	if err != nil {
		t.Fatalf("ListScanReports (server-b) failed: %v", err)
	}
	if len(serverBReports) != 1 {
		t.Errorf("expected 1 report for server-b, got %d", len(serverBReports))
	}
}

func TestScanReportGetNotFound(t *testing.T) {
	db := newTestDB(t)

	_, err := db.GetScanReport("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent scan report, got nil")
	}
}

func TestIntegrityBaselineCRUD(t *testing.T) {
	db := newTestDB(t)

	now := time.Now().Truncate(time.Second)

	// Test Save + Get
	baseline := &scanner.IntegrityBaseline{
		ServerName:    "server-a",
		ImageDigest:   "sha256:abc123",
		SourceHash:    "def456",
		LockfileHash:  "ghi789",
		DiffManifest:  []string{"file1.go", "file2.go"},
		ToolHashes:    map[string]string{"tool1": "hash1", "tool2": "hash2"},
		ScanReportIDs: []string{"report-001"},
		ApprovedAt:    now,
		ApprovedBy:    "admin",
	}

	if err := db.SaveIntegrityBaseline(baseline); err != nil {
		t.Fatalf("SaveIntegrityBaseline failed: %v", err)
	}

	got, err := db.GetIntegrityBaseline("server-a")
	if err != nil {
		t.Fatalf("GetIntegrityBaseline failed: %v", err)
	}
	if got.ServerName != "server-a" {
		t.Errorf("expected server 'server-a', got %q", got.ServerName)
	}
	if got.ImageDigest != "sha256:abc123" {
		t.Errorf("expected image digest 'sha256:abc123', got %q", got.ImageDigest)
	}
	if got.ApprovedBy != "admin" {
		t.Errorf("expected approved by 'admin', got %q", got.ApprovedBy)
	}
	if len(got.ToolHashes) != 2 {
		t.Errorf("expected 2 tool hashes, got %d", len(got.ToolHashes))
	}
	if len(got.DiffManifest) != 2 {
		t.Errorf("expected 2 diff manifest entries, got %d", len(got.DiffManifest))
	}

	// Test List
	baseline2 := &scanner.IntegrityBaseline{
		ServerName:   "server-b",
		ImageDigest:  "sha256:xyz789",
		SourceHash:   "aaa111",
		LockfileHash: "bbb222",
		ApprovedAt:   now,
		ApprovedBy:   "admin",
	}
	if err := db.SaveIntegrityBaseline(baseline2); err != nil {
		t.Fatalf("SaveIntegrityBaseline (baseline2) failed: %v", err)
	}

	baselines, err := db.ListIntegrityBaselines()
	if err != nil {
		t.Fatalf("ListIntegrityBaselines failed: %v", err)
	}
	if len(baselines) != 2 {
		t.Errorf("expected 2 baselines, got %d", len(baselines))
	}

	// Test Update (overwrite)
	baseline.ImageDigest = "sha256:updated"
	if err := db.SaveIntegrityBaseline(baseline); err != nil {
		t.Fatalf("SaveIntegrityBaseline (update) failed: %v", err)
	}
	got, err = db.GetIntegrityBaseline("server-a")
	if err != nil {
		t.Fatalf("GetIntegrityBaseline after update failed: %v", err)
	}
	if got.ImageDigest != "sha256:updated" {
		t.Errorf("expected updated image digest, got %q", got.ImageDigest)
	}

	// Test Delete
	if err := db.DeleteIntegrityBaseline("server-a"); err != nil {
		t.Fatalf("DeleteIntegrityBaseline failed: %v", err)
	}
	_, err = db.GetIntegrityBaseline("server-a")
	if err == nil {
		t.Error("expected error after delete, got nil")
	}

	// server-b should still exist
	baselines, err = db.ListIntegrityBaselines()
	if err != nil {
		t.Fatalf("ListIntegrityBaselines after delete failed: %v", err)
	}
	if len(baselines) != 1 {
		t.Errorf("expected 1 baseline after delete, got %d", len(baselines))
	}
}

func TestIntegrityBaselineGetNotFound(t *testing.T) {
	db := newTestDB(t)

	_, err := db.GetIntegrityBaseline("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent baseline, got nil")
	}
}

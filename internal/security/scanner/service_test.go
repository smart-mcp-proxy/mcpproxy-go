package scanner

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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

func (m *mockStorage) ListScanJobMetas(serverName string) ([]*ScanJobMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*ScanJobMeta
	for _, j := range m.jobs {
		if serverName != "" && j.ServerName != serverName {
			continue
		}
		result = append(result, &ScanJobMeta{
			ID:          j.ID,
			ServerName:  j.ServerName,
			Status:      j.Status,
			ScanPass:    j.ScanPass,
			StartedAt:   j.StartedAt,
			CompletedAt: j.CompletedAt,
		})
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

func (e *mockEmitter) EmitSecurityScannerChanged(scannerID, status, errMsg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, mockEvent{
		eventType: "scanner_changed",
		data:      map[string]interface{}{"scanner_id": scannerID, "status": status, "error": errMsg},
	})
}

// mockUnquarantiner records UnquarantineServer calls for test assertions.
type mockUnquarantiner struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (m *mockUnquarantiner) UnquarantineServer(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, serverName)
	return m.err
}

func (m *mockUnquarantiner) Calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.calls))
	copy(out, m.calls)
	return out
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

// TestServiceSetIsolationMode verifies the setter propagates the resolved
// isolation mode to the scan engine (MCP-34.4 / D3 option b), which is what
// gates the Docker-scanner skip path. Default ("") leaves Docker behaviour
// intact.
func TestServiceSetIsolationMode(t *testing.T) {
	svc, _, _ := newTestService(t)

	if svc.engine.isolationMode != "" {
		t.Fatalf("expected default isolation mode to be empty, got %q", svc.engine.isolationMode)
	}

	svc.SetIsolationMode("sandbox")
	if svc.engine.isolationMode != "sandbox" {
		t.Errorf("expected engine isolation mode 'sandbox', got %q", svc.engine.isolationMode)
	}

	svc.SetIsolationMode("docker")
	if svc.engine.isolationMode != "docker" {
		t.Errorf("expected engine isolation mode 'docker', got %q", svc.engine.isolationMode)
	}
}

// TestServiceResolveIsolationModePerServer verifies the per-server resolver
// (MCP-34.4 review fix): a per-server resolved mode takes precedence over the
// engine-wide default, so a server pinned to isolation.mode:docker keeps
// running Docker scanners under a global sandbox default, and a server resolved
// to sandbox/none skips them under a global docker default. A "" resolver
// result (or no resolver) falls back to the engine-wide default.
func TestServiceResolveIsolationModePerServer(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.SetIsolationMode("sandbox") // engine-wide default

	// No resolver wired yet → fall back to the engine default.
	if got := svc.resolveIsolationMode("any"); got != "sandbox" {
		t.Errorf("with no resolver, expected fallback to engine default 'sandbox', got %q", got)
	}

	perServer := map[string]string{
		"pinned-docker":  "docker", // overrides the global sandbox default
		"pinned-none":    "none",
		"pinned-sandbox": "sandbox",
		"inherits":       "", // resolver yields "" → fall back to default
	}
	svc.SetIsolationModeResolver(func(serverName string) string { return perServer[serverName] })

	cases := map[string]string{
		"pinned-docker":  "docker",
		"pinned-none":    "none",
		"pinned-sandbox": "sandbox",
		"inherits":       "sandbox", // "" → engine default
		"unknown-server": "sandbox", // not in map → "" → engine default
	}
	for server, want := range cases {
		if got := svc.resolveIsolationMode(server); got != want {
			t.Errorf("resolveIsolationMode(%q) = %q, want %q", server, got, want)
		}
	}
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

// TestServiceInstallInProcessScanner verifies that enabling an in-process
// (Docker-less) scanner like tpa-descriptions lands it in the "installed"
// state synchronously without ever touching Docker. Before MCP-2396 the
// enable path always assumed a Docker image, so an empty-image in-process
// scanner fell through to the pull path and got stuck in "error" with an
// empty error message — prefail-skipping every scan with an unactionable
// "reconfigure it from the Security page" notice.
func TestServiceInstallInProcessScanner(t *testing.T) {
	svc, store, emitter := newTestService(t)

	// Register a bundled in-process scanner mirroring tpa-descriptions: no
	// Docker image, seeds as "installed".
	svc.registry.scanners["tpa-descriptions"] = &ScannerPlugin{
		ID:        "tpa-descriptions",
		Name:      "TPA Descriptions",
		InProcess: true,
		Inputs:    []string{"mcp_connection"},
		Outputs:   []string{"sarif"},
		Status:    ScannerStatusInstalled,
	}

	if err := svc.InstallScanner(context.Background(), "tpa-descriptions"); err != nil {
		t.Fatalf("InstallScanner(tpa-descriptions) returned error: %v", err)
	}

	got, err := svc.GetScanner(context.Background(), "tpa-descriptions")
	if err != nil {
		t.Fatalf("GetScanner failed: %v", err)
	}
	if got.Status != ScannerStatusInstalled {
		t.Fatalf("expected status %q, got %q (err=%q)", ScannerStatusInstalled, got.Status, got.ErrorMsg)
	}
	if got.ErrorMsg != "" {
		t.Errorf("expected empty ErrorMsg after enabling in-process scanner, got %q", got.ErrorMsg)
	}

	// Must be persisted as installed so a restart keeps it enabled.
	persisted, err := store.GetScanner("tpa-descriptions")
	if err != nil {
		t.Fatalf("expected in-process scanner persisted to storage: %v", err)
	}
	if persisted.Status != ScannerStatusInstalled {
		t.Errorf("expected persisted status 'installed', got %q", persisted.Status)
	}

	// A scanner_changed event announcing "installed" (not "error"/"pulling")
	// must have been emitted so the UI reflects the enabled state.
	var sawInstalled bool
	for _, ev := range emitter.events {
		if ev.eventType != "scanner_changed" {
			continue
		}
		if ev.data["scanner_id"] == "tpa-descriptions" {
			if ev.data["status"] != ScannerStatusInstalled {
				t.Errorf("expected scanner_changed status 'installed', got %v (err=%v)", ev.data["status"], ev.data["error"])
			}
			sawInstalled = true
		}
	}
	if !sawInstalled {
		t.Errorf("expected a scanner_changed event for tpa-descriptions, got %+v", emitter.events)
	}
}

// TestServiceHealsInProcessScannerStuckInError verifies that a service
// constructed with an in-process scanner whose persisted state is "error"
// (the bad state an older build left behind) self-heals to "installed" at
// startup, so the engine runs it instead of prefail-skipping it (MCP-2396).
func TestServiceHealsInProcessScannerStuckInError(t *testing.T) {
	logger := zap.NewNop()
	store := newMockStorage()
	// Pre-seed storage with a stale error record, as an older buggy enable
	// path would have left it.
	_ = store.SaveScanner(&ScannerPlugin{
		ID:        "tpa-descriptions",
		Name:      "TPA Descriptions",
		InProcess: true,
		Status:    ScannerStatusError,
		ErrorMsg:  "",
	})

	registry := &Registry{
		scanners: map[string]*ScannerPlugin{
			"tpa-descriptions": {
				ID:        "tpa-descriptions",
				Name:      "TPA Descriptions",
				InProcess: true,
				Inputs:    []string{"mcp_connection"},
				Outputs:   []string{"sarif"},
				Status:    ScannerStatusInstalled,
			},
		},
		logger: logger,
	}

	svc := NewService(store, registry, NewDockerRunner(logger), t.TempDir(), logger)

	// Registry should now report the in-process scanner as installed.
	reg, err := registry.Get("tpa-descriptions")
	if err != nil {
		t.Fatalf("registry.Get failed: %v", err)
	}
	if reg.Status != ScannerStatusInstalled {
		t.Errorf("expected registry status 'installed' after heal, got %q", reg.Status)
	}

	// And the heal should be persisted so it survives subsequent restarts.
	persisted, err := store.GetScanner("tpa-descriptions")
	if err != nil {
		t.Fatalf("GetScanner failed: %v", err)
	}
	if persisted.Status != ScannerStatusInstalled {
		t.Errorf("expected persisted status 'installed' after heal, got %q", persisted.Status)
	}

	// ListScanners (registry+storage merge) must surface it as installed.
	list, err := svc.ListScanners(context.Background())
	if err != nil {
		t.Fatalf("ListScanners failed: %v", err)
	}
	for _, sc := range list {
		if sc.ID == "tpa-descriptions" && sc.Status != ScannerStatusInstalled {
			t.Errorf("expected merged status 'installed', got %q", sc.Status)
		}
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
	err := svc.ConfigureScanner(context.Background(), "test-scanner", env, "")
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

	err := svc.ConfigureScanner(context.Background(), "nonexistent", map[string]string{"KEY": "val"}, "")
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

// TestServiceApproveServerCriticalNonDangerousConsistentWithVerdict locks Spec
// 077 US1 Codex round-4 finding #3: the approval gate is PURELY tier-driven and
// can never disagree with the server verdict (GetScanSummary). A Critical-SEVERITY
// but NON-dangerous finding — e.g. a critical CVE, which the classifier maps to
// threat_level "warnings" because supply-chain findings inform rather than gate —
// must NOT block an unforced approval, AND the summary must report a non-dangerous
// status. Gate (approval) and verdict (summary) must AGREE: both non-blocking.
// The removed `Summary.Critical > 0` guard used to reject this approval while the
// summary showed the very same server as non-dangerous.
func TestServiceApproveServerCriticalNonDangerousConsistentWithVerdict(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Two critical-severity CVEs (supply-chain → threat_level "warnings", not
	// "dangerous"; no HARD tier) plus a medium. None is a blocking finding.
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
			{RuleID: "CVE-2025-1111", Severity: SeverityCritical, Title: "Critical CVE", PackageName: "left-pad"},
			{RuleID: "CVE-2025-2222", Severity: SeverityCritical, Title: "Another critical CVE", PackageName: "colors"},
			{RuleID: "M1", Severity: SeverityMedium, Title: "Medium issue"},
		},
		ScannedAt: time.Now(),
	}
	_ = store.SaveScanReport(report)

	// Gate: an unforced approval must SUCCEED — no dangerous/hard-tier finding.
	if err := svc.ApproveServer(context.Background(), "risky-server", false, "admin@test.com"); err != nil {
		t.Fatalf("a critical-but-non-dangerous finding must not block unforced approval: %v", err)
	}
	if _, err := store.GetIntegrityBaseline("risky-server"); err != nil {
		t.Fatalf("expected baseline after approving a non-dangerous server: %v", err)
	}

	// Verdict: the summary must AGREE that the server is non-dangerous. A gate that
	// allowed approval while the verdict said "dangerous" would be the disagreement
	// finding #3 forbids.
	summary := svc.GetScanSummary(context.Background(), "risky-server")
	if summary == nil {
		t.Fatal("expected a scan summary")
	}
	if summary.Status == "dangerous" {
		t.Fatalf("gate allowed approval but verdict is %q — gate and verdict disagree", summary.Status)
	}
	if summary.FindingCounts != nil && summary.FindingCounts.Dangerous > 0 {
		t.Fatalf("gate allowed approval but verdict reports %d dangerous finding(s) — inconsistent", summary.FindingCounts.Dangerous)
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

// TestServiceApproveServerBlockedByHardFinding locks Spec 077 US1 Codex finding
// #1: the approval gate must block on any HARD-tier baseline finding, not only on
// Summary.Critical. A curated hard phrase.injection is SeverityHigh (not
// Critical) with threat_level "dangerous", so the old Critical-only gate let a
// dangerous server be unquarantined. The gate now reuses isBlockingFinding — the
// SAME predicate that drives the "dangerous" verdict — so it cannot disagree with
// the summary. --force must still override.
func TestServiceApproveServerBlockedByHardFinding(t *testing.T) {
	svc, store, _ := newTestService(t)

	job := &ScanJob{
		ID:         "job-hard",
		ServerName: "poisoned-server",
		Status:     ScanJobStatusCompleted,
		Scanners:   []string{"tpa-descriptions"},
		StartedAt:  time.Now().Add(-1 * time.Minute),
	}
	_ = store.SaveScanJob(job)

	// A hard phrase_injection finding: High severity (NOT Critical), dangerous
	// threat level, hard tier — exactly the shape the Critical-only gate missed.
	report := &ScanReport{
		ID:         "report-hard",
		JobID:      "job-hard",
		ServerName: "poisoned-server",
		ScannerID:  "tpa-descriptions",
		Findings: []ScanFinding{
			{
				RuleID:      "phrase.injection",
				Severity:    SeverityHigh,
				Category:    "phrase_injection",
				ThreatType:  ThreatPromptInjection,
				ThreatLevel: ThreatLevelDangerous,
				Title:       "Instruction-override directive",
				Tier:        TierHard,
			},
		},
		ScannedAt: time.Now(),
	}
	_ = store.SaveScanReport(report)

	// Unforced approve must fail even though there are zero Critical findings.
	if err := svc.ApproveServer(context.Background(), "poisoned-server", false, "admin@test.com"); err == nil {
		t.Fatal("expected error: a hard-tier (dangerous) finding must block unforced approval")
	}
	if _, err := store.GetIntegrityBaseline("poisoned-server"); err == nil {
		t.Fatal("expected no baseline after a rejected approval")
	}

	// --force must still override.
	if err := svc.ApproveServer(context.Background(), "poisoned-server", true, "admin@test.com"); err != nil {
		t.Fatalf("force approve should succeed despite the hard finding: %v", err)
	}
	if _, err := store.GetIntegrityBaseline("poisoned-server"); err != nil {
		t.Fatalf("expected baseline after forced approval: %v", err)
	}
}

// TestServiceApproveServerSoftFindingDoesNotBlock proves the gate's counterpart:
// a SOFT baseline finding (review-only) must NOT block an unforced approval, even
// at High severity — the two-tier model, not raw severity, governs blocking.
func TestServiceApproveServerSoftFindingDoesNotBlock(t *testing.T) {
	svc, store, _ := newTestService(t)

	job := &ScanJob{
		ID:         "job-soft",
		ServerName: "reviewable-server",
		Status:     ScanJobStatusCompleted,
		Scanners:   []string{"tpa-descriptions"},
		StartedAt:  time.Now().Add(-1 * time.Minute),
	}
	_ = store.SaveScanJob(job)

	report := &ScanReport{
		ID:         "report-soft",
		JobID:      "job-soft",
		ServerName: "reviewable-server",
		ScannerID:  "tpa-descriptions",
		Findings: []ScanFinding{
			{
				RuleID:      "directive.imperative",
				Severity:    SeverityHigh,
				Category:    "prompt_injection",
				ThreatType:  ThreatPromptInjection,
				ThreatLevel: ThreatLevelWarning,
				Title:       "Soft directive",
				Tier:        TierSoft,
			},
		},
		ScannedAt: time.Now(),
	}
	_ = store.SaveScanReport(report)

	if err := svc.ApproveServer(context.Background(), "reviewable-server", false, "admin@test.com"); err != nil {
		t.Fatalf("a soft finding must not block unforced approval: %v", err)
	}
	if _, err := store.GetIntegrityBaseline("reviewable-server"); err != nil {
		t.Fatalf("expected baseline after approving a soft-only server: %v", err)
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

// TestServiceApproveServerCallsUnquarantiner verifies that a successful
// approval actually invokes the wired ServerUnquarantiner so the server is
// removed from quarantine (Bug F-01).
func TestServiceApproveServerCallsUnquarantiner(t *testing.T) {
	svc, store, _ := newTestService(t)
	unq := &mockUnquarantiner{}
	svc.SetServerUnquarantiner(unq)

	// Set up a clean scan (no critical findings)
	job := &ScanJob{
		ID:         "job-approve",
		ServerName: "qs-server",
		Status:     ScanJobStatusCompleted,
		Scanners:   []string{"test-scanner"},
		StartedAt:  time.Now().Add(-1 * time.Minute),
	}
	_ = store.SaveScanJob(job)
	_ = store.SaveScanReport(&ScanReport{
		ID:         "report-approve",
		JobID:      "job-approve",
		ServerName: "qs-server",
		ScannerID:  "test-scanner",
		Findings: []ScanFinding{
			{RuleID: "L1", Severity: SeverityLow, Title: "Low issue"},
		},
		ScannedAt: time.Now(),
	})

	if err := svc.ApproveServer(context.Background(), "qs-server", false, "admin@test.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := unq.Calls()
	if len(calls) != 1 || calls[0] != "qs-server" {
		t.Fatalf("expected unquarantiner called once for 'qs-server', got %v", calls)
	}

	// Baseline should still be saved
	if _, err := store.GetIntegrityBaseline("qs-server"); err != nil {
		t.Errorf("expected baseline saved: %v", err)
	}
}

// TestServiceApproveServerBlockedDoesNotUnquarantine verifies the tier-driven
// blocking guard stops approval BEFORE touching state (no unquarantine, no
// baseline). It uses a HARD-tier (dangerous) baseline finding — the shape that
// actually blocks under Spec 077's tier-driven gate (Codex round-4 finding #3
// dropped the legacy Critical-severity guard, so a bare critical no longer
// blocks; a dangerous hard-tier finding still does).
func TestServiceApproveServerBlockedDoesNotUnquarantine(t *testing.T) {
	svc, store, _ := newTestService(t)
	unq := &mockUnquarantiner{}
	svc.SetServerUnquarantiner(unq)

	_ = store.SaveScanJob(&ScanJob{
		ID:         "job-crit2",
		ServerName: "risky",
		Status:     ScanJobStatusCompleted,
		Scanners:   []string{"test-scanner"},
		StartedAt:  time.Now(),
	})
	_ = store.SaveScanReport(&ScanReport{
		ID:         "report-crit2",
		JobID:      "job-crit2",
		ServerName: "risky",
		ScannerID:  "test-scanner",
		Findings: []ScanFinding{
			{
				RuleID:      "phrase.injection",
				Severity:    SeverityHigh,
				Category:    "phrase_injection",
				ThreatType:  ThreatPromptInjection,
				ThreatLevel: ThreatLevelDangerous,
				Title:       "Instruction-override directive",
				Tier:        TierHard,
			},
		},
		ScannedAt: time.Now(),
	})

	if err := svc.ApproveServer(context.Background(), "risky", false, "admin@test.com"); err == nil {
		t.Fatal("expected the tier-driven guard to block approval on a dangerous hard-tier finding")
	}

	if calls := unq.Calls(); len(calls) != 0 {
		t.Errorf("unquarantiner must not be called when approval is blocked, got %v", calls)
	}
	if _, err := store.GetIntegrityBaseline("risky"); err == nil {
		t.Error("baseline must not be created when approval is blocked")
	}
}

// TestServiceApproveServerUnquarantinerError verifies that an unquarantiner
// error is surfaced to the caller and the baseline is still recorded (so the
// user can retry).
func TestServiceApproveServerUnquarantinerError(t *testing.T) {
	svc, store, _ := newTestService(t)
	unq := &mockUnquarantiner{err: fmt.Errorf("boom")}
	svc.SetServerUnquarantiner(unq)

	if err := svc.ApproveServer(context.Background(), "retry-server", true, "admin@test.com"); err == nil {
		t.Fatal("expected error from unquarantiner to surface")
	}

	// Baseline should still have been saved before the unquarantine call
	if _, err := store.GetIntegrityBaseline("retry-server"); err != nil {
		t.Errorf("expected baseline to be saved even when unquarantine fails: %v", err)
	}
	if calls := unq.Calls(); len(calls) != 1 {
		t.Errorf("expected exactly 1 unquarantiner call, got %v", calls)
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

func TestServiceGetScanSummaryAllFailed(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Create a failed scan job (all scanners failed)
	now := time.Now()
	_ = store.SaveScanJob(&ScanJob{
		ID:         "j-fail",
		ServerName: "server-a",
		Status:     ScanJobStatusFailed,
		Scanners:   []string{"s1", "s2"},
		StartedAt:  now,
		Error:      "all scanners failed",
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "s1", Status: ScanJobStatusFailed, Error: "image not found"},
			{ScannerID: "s2", Status: ScanJobStatusFailed, Error: "timeout"},
		},
	})

	summary := svc.GetScanSummary(context.Background(), "server-a")
	if summary == nil {
		t.Fatal("expected non-nil summary")
	}
	if summary.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", summary.Status)
	}
}

func TestServiceGetScanSummaryPartialSuccess(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Create a completed job where one scanner succeeded and one failed
	now := time.Now()
	_ = store.SaveScanJob(&ScanJob{
		ID:         "j-partial",
		ServerName: "server-a",
		Status:     ScanJobStatusCompleted,
		Scanners:   []string{"s1", "s2"},
		StartedAt:  now,
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "s1", Status: ScanJobStatusCompleted, FindingsCount: 0},
			{ScannerID: "s2", Status: ScanJobStatusFailed, Error: "image not found"},
		},
	})
	_ = store.SaveScanReport(&ScanReport{
		ID: "r1", JobID: "j-partial", ServerName: "server-a", ScannerID: "s1",
		Findings: []ScanFinding{}, ScannedAt: now,
	})

	summary := svc.GetScanSummary(context.Background(), "server-a")
	if summary == nil {
		t.Fatal("expected non-nil summary")
	}
	// Spec 077 US3 (FR-008/FR-014): a failed Docker deep scanner no longer
	// downgrades the baseline to "degraded". The verdict is derived solely from
	// baseline findings — here there are none, so it stays "clean". The deep
	// failure is surfaced via DeepScan only when the layer is enabled (off here).
	if summary.Status != "clean" {
		t.Errorf("expected status 'clean' (deep-scan failure must not degrade the baseline), got %q", summary.Status)
	}
	if summary.DeepScan != nil {
		t.Errorf("DeepScan descriptor must be omitted when deep scan is off, got %+v", summary.DeepScan)
	}
	// Coverage counts remain populated for informational display.
	if summary.ScannersTotal != 2 || summary.ScannersRun != 1 || summary.ScannersFailed != 1 {
		t.Errorf("expected coverage 1 run / 1 failed / 2 total, got %d run / %d failed / %d total",
			summary.ScannersRun, summary.ScannersFailed, summary.ScannersTotal)
	}
}

// TestServiceGetScanSummaryDeepFailureWithInfoFindings is the Spec 077 US3
// successor to the MCP-2401 degrade test: when the surviving baseline produced
// only informational findings and a Docker deep scanner failed, the verdict must
// stay "clean" (baseline-only, never "degraded"). With the deep-scan layer
// enabled, the failure is surfaced via the informational DeepScan descriptor.
func TestServiceGetScanSummaryDeepFailureWithInfoFindings(t *testing.T) {
	svc, store, _ := newTestService(t)
	svc.SetDeepScan(true, nil)

	now := time.Now()
	_ = store.SaveScanJob(&ScanJob{
		ID:         "j-deep-info",
		ServerName: "server-a",
		Status:     ScanJobStatusCompleted,
		Scanners:   []string{"s1", "s2"},
		StartedAt:  now,
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "s1", Status: ScanJobStatusCompleted, FindingsCount: 1},
			{ScannerID: "s2", Status: ScanJobStatusFailed, Error: "image not found"},
		},
	})
	_ = store.SaveScanReport(&ScanReport{
		ID: "r1", JobID: "j-deep-info", ServerName: "server-a", ScannerID: "s1",
		Findings: []ScanFinding{
			{RuleID: "info-1", ThreatLevel: ThreatLevelInfo, Severity: "info", Title: "informational"},
		},
		ScannedAt: now,
	})

	summary := svc.GetScanSummary(context.Background(), "server-a")
	if summary == nil {
		t.Fatal("expected non-nil summary")
	}
	if summary.Status != "clean" {
		t.Errorf("expected status 'clean' (deep-scan failure must not degrade the baseline), got %q", summary.Status)
	}
	// s1/s2 are unknown (non-in-process) ids → treated as deep scanners. With
	// deep scan enabled, the failure of s2 is surfaced informationally.
	if summary.DeepScan == nil {
		t.Fatal("expected DeepScan descriptor when deep scan is enabled")
	}
	if !summary.DeepScan.Enabled {
		t.Errorf("DeepScan.Enabled must be true")
	}
	if len(summary.DeepScan.ScannersFailed) != 1 || summary.DeepScan.ScannersFailed[0].ID != "s2" {
		t.Errorf("expected s2 in DeepScan.ScannersFailed, got %+v", summary.DeepScan.ScannersFailed)
	}
	if summary.ScannersFailed != 1 {
		t.Errorf("expected 1 failed scanner (coverage count), got %d", summary.ScannersFailed)
	}
}

func TestServiceGetScanSummaryClean(t *testing.T) {
	svc, store, _ := newTestService(t)

	now := time.Now()
	_ = store.SaveScanJob(&ScanJob{
		ID:         "j-clean",
		ServerName: "server-a",
		Status:     ScanJobStatusCompleted,
		Scanners:   []string{"s1"},
		StartedAt:  now,
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "s1", Status: ScanJobStatusCompleted, FindingsCount: 0},
		},
	})
	_ = store.SaveScanReport(&ScanReport{
		ID: "r1", JobID: "j-clean", ServerName: "server-a", ScannerID: "s1",
		Findings: []ScanFinding{}, ScannedAt: now,
	})

	summary := svc.GetScanSummary(context.Background(), "server-a")
	if summary == nil {
		t.Fatal("expected non-nil summary")
	}
	if summary.Status != "clean" {
		t.Errorf("expected status 'clean', got %q", summary.Status)
	}
	// MCP-2401: full coverage (no failed scanners) keeps the verdict "clean".
	if summary.ScannersTotal != 1 || summary.ScannersRun != 1 || summary.ScannersFailed != 0 {
		t.Errorf("expected coverage 1 run / 0 failed / 1 total, got %d run / %d failed / %d total",
			summary.ScannersRun, summary.ScannersFailed, summary.ScannersTotal)
	}
}

func TestServiceGetScanSummaryEmptyScan(t *testing.T) {
	svc, store, _ := newTestService(t)

	now := time.Now()
	_ = store.SaveScanJob(&ScanJob{
		ID:         "j-empty",
		ServerName: "server-a",
		Status:     ScanJobStatusCompleted,
		Scanners:   []string{"s1", "s2"},
		StartedAt:  now,
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "s1", Status: ScanJobStatusFailed, Error: "tools.json not found"},
			{ScannerID: "s2", Status: ScanJobStatusCompleted, FindingsCount: 0},
		},
		ScanContext: &ScanContext{
			SourceMethod: "docker_extract",
			TotalFiles:   0, // No files extracted
		},
	})
	_ = store.SaveScanReport(&ScanReport{
		ID: "r1", JobID: "j-empty", ServerName: "server-a", ScannerID: "s2",
		Findings: []ScanFinding{}, ScannedAt: now,
	})

	summary := svc.GetScanSummary(context.Background(), "server-a")
	if summary == nil {
		t.Fatal("expected non-nil summary")
	}
	if summary.Status != "failed" {
		t.Errorf("expected status 'failed' for empty scan, got %q", summary.Status)
	}
}

func TestServiceGetScanReportWithJobStatus(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Create a job where one scanner failed and one succeeded
	now := time.Now()
	_ = store.SaveScanJob(&ScanJob{
		ID:         "j-mixed",
		ServerName: "server-a",
		Status:     ScanJobStatusCompleted,
		Scanners:   []string{"s1", "s2"},
		StartedAt:  now,
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "s1", Status: ScanJobStatusCompleted, FindingsCount: 1},
			{ScannerID: "s2", Status: ScanJobStatusFailed, Error: "docker image not found"},
		},
	})
	_ = store.SaveScanReport(&ScanReport{
		ID: "r1", JobID: "j-mixed", ServerName: "server-a", ScannerID: "s1",
		Findings: []ScanFinding{
			{RuleID: "R1", Severity: SeverityHigh, Title: "Issue"},
		},
		ScannedAt: now,
	})

	agg, err := svc.GetScanReport(context.Background(), "server-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !agg.ScanComplete {
		t.Error("expected ScanComplete=true (one scanner succeeded)")
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
}

func TestServiceGetScanReportAllFailed(t *testing.T) {
	svc, store, _ := newTestService(t)

	now := time.Now()
	_ = store.SaveScanJob(&ScanJob{
		ID:         "j-allfail",
		ServerName: "server-a",
		Status:     ScanJobStatusFailed,
		Scanners:   []string{"s1", "s2"},
		StartedAt:  now,
		Error:      "all scanners failed",
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "s1", Status: ScanJobStatusFailed, Error: "image not found"},
			{ScannerID: "s2", Status: ScanJobStatusFailed, Error: "timeout"},
		},
	})
	// No reports (all failed)

	agg, err := svc.GetScanReport(context.Background(), "server-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agg.ScanComplete {
		t.Error("expected ScanComplete=false when all scanners failed")
	}
	if agg.ScannersFailed != 2 {
		t.Errorf("expected ScannersFailed=2, got %d", agg.ScannersFailed)
	}
	if agg.ScannersTotal != 2 {
		t.Errorf("expected ScannersTotal=2, got %d", agg.ScannersTotal)
	}
	if agg.ScannersRun != 0 {
		t.Errorf("expected ScannersRun=0, got %d", agg.ScannersRun)
	}
	if agg.RiskScore != 0 {
		t.Errorf("expected 0 risk score, got %d", agg.RiskScore)
	}
}

func TestServiceNoopEmitterDefault(t *testing.T) {
	logger := zap.NewNop()
	store := newMockStorage()
	docker := NewDockerRunner(logger)
	registry := &Registry{scanners: make(map[string]*ScannerPlugin), logger: logger}
	svc := NewService(store, registry, docker, t.TempDir(), logger)

	// Default emitter should be NoopEmitter - should not panic
	em := svc.emit()
	em.EmitSecurityScanStarted("test", []string{"s1"}, "j1")
	em.EmitSecurityScanCompleted("test", map[string]int{"high": 1})
	em.EmitSecurityScanFailed("test", "s1", "error")
	em.EmitSecurityScanProgress("test", "s1", "running", 50)
	em.EmitSecurityIntegrityAlert("test", "mismatch", "quarantine")
	em.EmitSecurityScannerChanged("s1", "installed", "")
}

// --- Two-pass scanning tests ---

func TestFindLatestPassJobs(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Create Pass 1 and Pass 2 jobs
	pass1Job := &ScanJob{
		ID:         "job-pass1",
		ServerName: "my-server",
		Status:     ScanJobStatusCompleted,
		ScanPass:   ScanPassSecurityScan,
		Scanners:   []string{"test-scanner"},
		StartedAt:  time.Now().Add(-5 * time.Minute),
	}
	pass2Job := &ScanJob{
		ID:         "job-pass2",
		ServerName: "my-server",
		Status:     ScanJobStatusCompleted,
		ScanPass:   ScanPassSupplyChainAudit,
		Scanners:   []string{"test-scanner"},
		StartedAt:  time.Now().Add(-2 * time.Minute),
	}
	_ = store.SaveScanJob(pass1Job)
	_ = store.SaveScanJob(pass2Job)

	p1, p2, err := svc.findLatestPassJobs("my-server")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p1 == nil {
		t.Fatal("expected Pass 1 job, got nil")
	}
	if p1.ID != "job-pass1" {
		t.Errorf("expected Pass 1 job ID 'job-pass1', got %s", p1.ID)
	}
	if p2 == nil {
		t.Fatal("expected Pass 2 job, got nil")
	}
	if p2.ID != "job-pass2" {
		t.Errorf("expected Pass 2 job ID 'job-pass2', got %s", p2.ID)
	}
}

func TestFindLatestPassJobsLegacy(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Legacy job with ScanPass == 0 (before two-pass was added)
	legacyJob := &ScanJob{
		ID:         "job-legacy",
		ServerName: "my-server",
		Status:     ScanJobStatusCompleted,
		ScanPass:   0,
		Scanners:   []string{"test-scanner"},
		StartedAt:  time.Now().Add(-5 * time.Minute),
	}
	_ = store.SaveScanJob(legacyJob)

	p1, p2, err := svc.findLatestPassJobs("my-server")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p1 == nil {
		t.Fatal("expected legacy job to be treated as Pass 1")
	}
	if p1.ID != "job-legacy" {
		t.Errorf("expected legacy job ID, got %s", p1.ID)
	}
	if p2 != nil {
		t.Error("expected no Pass 2 job for legacy scan")
	}
}

func TestGetScanReportMergesBothPasses(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Create Pass 1 job and report
	pass1Job := &ScanJob{
		ID:         "job-pass1",
		ServerName: "my-server",
		Status:     ScanJobStatusCompleted,
		ScanPass:   ScanPassSecurityScan,
		Scanners:   []string{"test-scanner"},
		StartedAt:  time.Now().Add(-5 * time.Minute),
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "test-scanner", Status: ScanJobStatusCompleted, FindingsCount: 1},
		},
	}
	pass1Report := &ScanReport{
		ID:         "report-pass1",
		JobID:      "job-pass1",
		ServerName: "my-server",
		ScannerID:  "test-scanner",
		Findings: []ScanFinding{
			{
				RuleID:      "TOOL-001",
				Title:       "Tool poisoning detected",
				Severity:    SeverityHigh,
				ThreatType:  ThreatToolPoisoning,
				ThreatLevel: ThreatLevelDangerous,
				Scanner:     "test-scanner",
			},
		},
		ScannedAt: time.Now().Add(-5 * time.Minute),
	}

	// Create Pass 2 job and report
	pass2Job := &ScanJob{
		ID:         "job-pass2",
		ServerName: "my-server",
		Status:     ScanJobStatusCompleted,
		ScanPass:   ScanPassSupplyChainAudit,
		Scanners:   []string{"test-scanner"},
		StartedAt:  time.Now().Add(-2 * time.Minute),
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "test-scanner", Status: ScanJobStatusCompleted, FindingsCount: 1},
		},
	}
	pass2Report := &ScanReport{
		ID:         "report-pass2",
		JobID:      "job-pass2",
		ServerName: "my-server",
		ScannerID:  "test-scanner",
		Findings: []ScanFinding{
			{
				RuleID:           "CVE-2026-1234",
				Title:            "Known CVE in dependency",
				Severity:         SeverityMedium,
				ThreatType:       ThreatSupplyChain,
				ThreatLevel:      ThreatLevelWarning,
				Scanner:          "test-scanner",
				PackageName:      "authlib",
				InstalledVersion: "1.3.0",
				FixedVersion:     "1.3.2",
			},
		},
		ScannedAt: time.Now().Add(-2 * time.Minute),
	}

	_ = store.SaveScanJob(pass1Job)
	_ = store.SaveScanReport(pass1Report)
	_ = store.SaveScanJob(pass2Job)
	_ = store.SaveScanReport(pass2Report)

	report, err := svc.GetScanReport(context.Background(), "my-server")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should merge findings from both passes
	if len(report.Findings) != 2 {
		t.Fatalf("expected 2 merged findings, got %d", len(report.Findings))
	}

	// Verify pass tags
	foundPass1 := false
	foundPass2 := false
	for _, f := range report.Findings {
		if f.ScanPass == ScanPassSecurityScan {
			foundPass1 = true
		}
		if f.ScanPass == ScanPassSupplyChainAudit {
			foundPass2 = true
		}
	}
	if !foundPass1 {
		t.Error("expected at least one finding tagged as Pass 1")
	}
	if !foundPass2 {
		t.Error("expected at least one finding tagged as Pass 2")
	}

	// Verify pass completion flags
	if !report.Pass1Complete {
		t.Error("expected Pass1Complete to be true")
	}
	if !report.Pass2Complete {
		t.Error("expected Pass2Complete to be true")
	}
	if report.Pass2Running {
		t.Error("expected Pass2Running to be false")
	}
}

func TestGetScanReportPass2NotStarted(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Only Pass 1 completed
	pass1Job := &ScanJob{
		ID:         "job-pass1-only",
		ServerName: "my-server",
		Status:     ScanJobStatusCompleted,
		ScanPass:   ScanPassSecurityScan,
		Scanners:   []string{"test-scanner"},
		StartedAt:  time.Now().Add(-5 * time.Minute),
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "test-scanner", Status: ScanJobStatusCompleted},
		},
	}
	_ = store.SaveScanJob(pass1Job)

	report, err := svc.GetScanReport(context.Background(), "my-server")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !report.Pass1Complete {
		t.Error("expected Pass1Complete to be true")
	}
	if report.Pass2Complete {
		t.Error("expected Pass2Complete to be false")
	}
	if report.Pass2Running {
		t.Error("expected Pass2Running to be false")
	}
}

func TestGetScanSummaryBothPasses(t *testing.T) {
	svc, store, _ := newTestService(t)

	// Pass 1 with no findings
	pass1Job := &ScanJob{
		ID:         "job-p1",
		ServerName: "my-server",
		Status:     ScanJobStatusCompleted,
		ScanPass:   ScanPassSecurityScan,
		Scanners:   []string{"test-scanner"},
		StartedAt:  time.Now().Add(-5 * time.Minute),
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "test-scanner", Status: ScanJobStatusCompleted},
		},
	}
	pass1Report := &ScanReport{
		ID:         "report-p1",
		JobID:      "job-p1",
		ServerName: "my-server",
		ScannerID:  "test-scanner",
		Findings:   []ScanFinding{},
		ScannedAt:  time.Now().Add(-5 * time.Minute),
	}

	// Pass 2 with warning-level findings
	pass2Job := &ScanJob{
		ID:         "job-p2",
		ServerName: "my-server",
		Status:     ScanJobStatusCompleted,
		ScanPass:   ScanPassSupplyChainAudit,
		Scanners:   []string{"test-scanner"},
		StartedAt:  time.Now().Add(-2 * time.Minute),
		ScannerStatuses: []ScannerJobStatus{
			{ScannerID: "test-scanner", Status: ScanJobStatusCompleted, FindingsCount: 1},
		},
	}
	pass2Report := &ScanReport{
		ID:         "report-p2",
		JobID:      "job-p2",
		ServerName: "my-server",
		ScannerID:  "test-scanner",
		Findings: []ScanFinding{
			{
				RuleID:      "CVE-2026-5678",
				Severity:    SeverityHigh,
				ThreatType:  ThreatSupplyChain,
				ThreatLevel: ThreatLevelWarning,
			},
		},
		ScannedAt: time.Now().Add(-2 * time.Minute),
	}

	_ = store.SaveScanJob(pass1Job)
	_ = store.SaveScanReport(pass1Report)
	_ = store.SaveScanJob(pass2Job)
	_ = store.SaveScanReport(pass2Report)

	summary := svc.GetScanSummary(context.Background(), "my-server")
	if summary == nil {
		t.Fatal("expected non-nil summary")
	}

	// Summary should reflect Pass 2 findings (warning status)
	if summary.Status != "warnings" {
		t.Errorf("expected status 'warnings' (from Pass 2 findings), got %s", summary.Status)
	}

	if summary.FindingCounts == nil {
		t.Fatal("expected FindingCounts to be non-nil")
	}
	if summary.FindingCounts.Warning != 1 {
		t.Errorf("expected 1 warning, got %d", summary.FindingCounts.Warning)
	}
	if summary.FindingCounts.Total != 1 {
		t.Errorf("expected 1 total finding, got %d", summary.FindingCounts.Total)
	}
}

func TestScanJobScanPassField(t *testing.T) {
	// Verify ScanPass is correctly set on new jobs via the engine
	logger := zap.NewNop()
	store := newMockStorage()
	docker := NewDockerRunner(logger)
	registry := &Registry{scanners: make(map[string]*ScannerPlugin), logger: logger}
	_ = NewService(store, registry, docker, t.TempDir(), logger)

	// Create a job with ScanPass set
	job := &ScanJob{
		ID:         "test-job-pass",
		ServerName: "test-server",
		Status:     ScanJobStatusCompleted,
		ScanPass:   ScanPassSupplyChainAudit,
		Scanners:   []string{"scanner-a"},
		StartedAt:  time.Now(),
	}

	_ = store.SaveScanJob(job)

	retrieved, err := store.GetScanJob("test-job-pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retrieved.ScanPass != ScanPassSupplyChainAudit {
		t.Errorf("expected ScanPass=%d, got %d", ScanPassSupplyChainAudit, retrieved.ScanPass)
	}
}

func TestScanFindingScanPassTag(t *testing.T) {
	// Verify ScanPass is preserved on findings through aggregation
	findings := []ScanFinding{
		{RuleID: "RULE-1", Severity: SeverityHigh, ThreatType: ThreatToolPoisoning, ThreatLevel: ThreatLevelDangerous, ScanPass: ScanPassSecurityScan},
		{RuleID: "CVE-001", Severity: SeverityMedium, ThreatType: ThreatSupplyChain, ThreatLevel: ThreatLevelWarning, ScanPass: ScanPassSupplyChainAudit},
	}

	report := &ScanReport{
		ID:        "report-test",
		ScannerID: "scanner-a",
		Findings:  findings,
		ScannedAt: time.Now(),
	}

	agg := AggregateReports("job-test", "server-test", []*ScanReport{report})

	if len(agg.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(agg.Findings))
	}

	if agg.Findings[0].ScanPass != ScanPassSecurityScan {
		t.Errorf("expected first finding ScanPass=%d, got %d", ScanPassSecurityScan, agg.Findings[0].ScanPass)
	}
	if agg.Findings[1].ScanPass != ScanPassSupplyChainAudit {
		t.Errorf("expected second finding ScanPass=%d, got %d", ScanPassSupplyChainAudit, agg.Findings[1].ScanPass)
	}
}

// countingStorage wraps any Storage and counts storage probes. listCalls counts
// the heavy full-history ListScanJobs path (MCP-2205 asserts this stays 0 on the
// report hot paths); metaCalls counts the lightweight ListScanJobMetas index
// path used by the GetScanSummary negative-cache behavior (spec 047).
type countingStorage struct {
	Storage
	listCalls atomic.Int64
	metaCalls atomic.Int64
}

func newCountingStorage(inner Storage) *countingStorage {
	return &countingStorage{Storage: inner}
}

func (c *countingStorage) ListScanJobs(serverName string) ([]*ScanJob, error) {
	c.listCalls.Add(1)
	return c.Storage.ListScanJobs(serverName)
}

func (c *countingStorage) ListScanJobMetas(serverName string) ([]*ScanJobMeta, error) {
	c.metaCalls.Add(1)
	return c.Storage.ListScanJobMetas(serverName)
}

// erroringStorage returns a transient error from ListScanJobs while delegating
// other calls to the inner Storage. Used to verify that non-errNoScans errors
// do NOT populate the negative cache.
type erroringStorage struct {
	Storage
	listCalls atomic.Int64
	err       error
}

func (e *erroringStorage) ListScanJobs(string) ([]*ScanJob, error) {
	e.listCalls.Add(1)
	return nil, e.err
}

func (e *erroringStorage) ListScanJobMetas(string) ([]*ScanJobMeta, error) {
	e.listCalls.Add(1)
	return nil, e.err
}

// Spec 047 — Phase 3 (US1): cache the "no scans found" sentinel.

func TestGetScanSummary_CachesNegativeResult(t *testing.T) {
	store := newCountingStorage(newMockStorage())
	svc := NewService(store, NewRegistry("/tmp/scanner-cache-test", zap.NewNop()), nil, "/tmp/scanner-cache-test", zap.NewNop())

	const N = 10
	for i := 0; i < N; i++ {
		if got := svc.GetScanSummary(context.Background(), "never-scanned"); got != nil {
			t.Fatalf("call %d: expected nil summary for never-scanned server, got %+v", i, got)
		}
	}

	// Without the negative-cache fix, this would be N storage calls.
	if got := store.metaCalls.Load(); got != 1 {
		t.Errorf("expected exactly 1 ListScanJobMetas call after %d GetScanSummary invocations, got %d", N, got)
	}
}

func TestGetScanSummary_DoesNotCacheOnTransientError(t *testing.T) {
	store := &erroringStorage{Storage: newMockStorage(), err: fmt.Errorf("transient bbolt I/O failure")}
	svc := NewService(store, NewRegistry("/tmp/scanner-cache-test", zap.NewNop()), nil, "/tmp/scanner-cache-test", zap.NewNop())

	const N = 5
	for i := 0; i < N; i++ {
		_ = svc.GetScanSummary(context.Background(), "io-failing")
	}

	// Transient error must NOT populate the negative cache: every call retries.
	if got := store.listCalls.Load(); got != int64(N) {
		t.Errorf("expected %d storage probes (no caching of transient errors), got %d", N, got)
	}
}

func TestGetScanSummary_OverwritesNilSentinelOnRealScan(t *testing.T) {
	mock := newMockStorage()
	store := newCountingStorage(mock)
	svc := NewService(store, NewRegistry("/tmp/scanner-cache-test", zap.NewNop()), nil, "/tmp/scanner-cache-test", zap.NewNop())

	// First call: cache the negative sentinel.
	if got := svc.GetScanSummary(context.Background(), "later-scanned"); got != nil {
		t.Fatalf("expected nil summary on first call, got %+v", got)
	}
	if got := store.metaCalls.Load(); got != 1 {
		t.Fatalf("expected 1 ListScanJobMetas call after first GetScanSummary, got %d", got)
	}

	// Simulate a real scan landing for that server: insert a completed Pass-1 job
	// then ask the service to refresh its cached summary by calling
	// cacheScanSummary directly with a real summary (mirrors what the engine
	// does on scan completion).
	now := time.Now()
	job := &ScanJob{
		ID:         "job-real",
		ServerName: "later-scanned",
		ScanPass:   ScanPassSecurityScan,
		Status:     ScanJobStatusCompleted,
		StartedAt:  now,
	}
	if err := mock.SaveScanJob(job); err != nil {
		t.Fatalf("SaveScanJob: %v", err)
	}
	real := &ScanSummary{Status: "clean", LastScanAt: &now}
	svc.cacheScanSummary("later-scanned", real)

	// Subsequent GetScanSummary returns the real summary, not the nil sentinel.
	if got := svc.GetScanSummary(context.Background(), "later-scanned"); got == nil || got.Status != "clean" {
		t.Errorf("expected real summary {Status: clean}, got %+v", got)
	}
}

// TestGetScanReportByJobID_LatencyIndependentOfHistory verifies the MCP-2205
// fix: aggregating a Pass-1 report must NOT deserialize the full per-server scan
// history (ListScanJobs), whose job payloads carry large stdout/stderr. The
// companion Pass-2 lookup uses the lightweight metadata index instead, so report
// latency does not grow with how many times a server has been scanned.
func TestGetScanReportByJobID_LatencyIndependentOfHistory(t *testing.T) {
	mock := newMockStorage()
	store := newCountingStorage(mock)
	svc := NewService(store, NewRegistry(t.TempDir(), zap.NewNop()), nil, t.TempDir(), zap.NewNop())

	now := time.Now()

	// Pass-1 job under inspection + its report.
	pass1 := &ScanJob{ID: "p1", ServerName: "srv", Status: ScanJobStatusCompleted, ScanPass: ScanPassSecurityScan, StartedAt: now.Add(-10 * time.Minute)}
	_ = mock.SaveScanJob(pass1)
	_ = mock.SaveScanReport(&ScanReport{ID: "r1", JobID: "p1", ServerName: "srv", ScannerID: "s",
		Findings: []ScanFinding{{RuleID: "T1", Title: "tool poisoning", Scanner: "s", ThreatLevel: ThreatLevelDangerous}}})

	// Companion Pass-2 job that started after Pass-1 + its report.
	pass2 := &ScanJob{ID: "p2", ServerName: "srv", Status: ScanJobStatusCompleted, ScanPass: ScanPassSupplyChainAudit, StartedAt: now.Add(-8 * time.Minute)}
	_ = mock.SaveScanJob(pass2)
	_ = mock.SaveScanReport(&ScanReport{ID: "r2", JobID: "p2", ServerName: "srv", ScannerID: "s",
		Findings: []ScanFinding{{RuleID: "CVE-1", Title: "known cve", Scanner: "s", ThreatLevel: ThreatLevelWarning}}})

	// Large historical backlog for the same server (the symptom: reports get
	// slower the more a server is scanned).
	for i := 0; i < 100; i++ {
		_ = mock.SaveScanJob(&ScanJob{
			ID:         fmt.Sprintf("noise-%d", i),
			ServerName: "srv",
			Status:     ScanJobStatusCompleted,
			ScanPass:   ScanPassSecurityScan,
			StartedAt:  now.Add(time.Duration(-20-i) * time.Minute),
		})
	}

	agg, err := svc.GetScanReportByJobID(context.Background(), "p1")
	if err != nil {
		t.Fatalf("GetScanReportByJobID: %v", err)
	}

	// Correctness: companion Pass-2 still merged in.
	if !agg.Pass1Complete || !agg.Pass2Complete {
		t.Errorf("expected both passes complete, got pass1=%v pass2=%v", agg.Pass1Complete, agg.Pass2Complete)
	}
	if len(agg.Findings) != 2 {
		t.Fatalf("expected 2 merged findings (Pass-1 + companion Pass-2), got %d", len(agg.Findings))
	}

	// Latency guard: must not scan the full per-server job history.
	if got := store.listCalls.Load(); got != 0 {
		t.Errorf("expected 0 ListScanJobs (full-history) calls in report path, got %d", got)
	}
}

// TestGetScanReport_LatestLatencyIndependentOfHistory verifies the MCP-2205 fix
// also covers the "latest report" path (GetScanReport by server name, used by the
// Web UI server-detail view). Resolving the latest Pass-1/Pass-2 jobs must use the
// lightweight metadata index plus targeted GetScanJob loads (at most two full job
// deserializations), not a full ListScanJobs over the server's scan history.
func TestGetScanReport_LatestLatencyIndependentOfHistory(t *testing.T) {
	mock := newMockStorage()
	store := newCountingStorage(mock)
	svc := NewService(store, NewRegistry(t.TempDir(), zap.NewNop()), nil, t.TempDir(), zap.NewNop())

	now := time.Now()

	// Latest Pass-1 + Pass-2 with reports.
	_ = mock.SaveScanJob(&ScanJob{ID: "latest-p1", ServerName: "srv", Status: ScanJobStatusCompleted, ScanPass: ScanPassSecurityScan, StartedAt: now.Add(-2 * time.Minute)})
	_ = mock.SaveScanReport(&ScanReport{ID: "lr1", JobID: "latest-p1", ServerName: "srv", ScannerID: "s",
		Findings: []ScanFinding{{RuleID: "T1", Title: "tp", Scanner: "s", ThreatLevel: ThreatLevelDangerous}}})
	_ = mock.SaveScanJob(&ScanJob{ID: "latest-p2", ServerName: "srv", Status: ScanJobStatusCompleted, ScanPass: ScanPassSupplyChainAudit, StartedAt: now.Add(-1 * time.Minute)})
	_ = mock.SaveScanReport(&ScanReport{ID: "lr2", JobID: "latest-p2", ServerName: "srv", ScannerID: "s",
		Findings: []ScanFinding{{RuleID: "CVE-9", Title: "cve", Scanner: "s", ThreatLevel: ThreatLevelWarning}}})

	// Large historical backlog.
	for i := 0; i < 100; i++ {
		_ = mock.SaveScanJob(&ScanJob{ID: fmt.Sprintf("old-%d", i), ServerName: "srv", Status: ScanJobStatusCompleted, ScanPass: ScanPassSecurityScan, StartedAt: now.Add(time.Duration(-10-i) * time.Minute)})
	}

	report, err := svc.GetScanReport(context.Background(), "srv")
	if err != nil {
		t.Fatalf("GetScanReport: %v", err)
	}
	if len(report.Findings) != 2 {
		t.Fatalf("expected 2 merged findings, got %d", len(report.Findings))
	}
	if got := store.listCalls.Load(); got != 0 {
		t.Errorf("expected 0 ListScanJobs (full-history) calls in latest-report path, got %d", got)
	}
}

// TestServiceDeepScanOffByDefault verifies Spec 077 US3 AS1/FR-006: with the
// deep-scan layer disabled (the default), only the deterministic in-process
// baseline scanner is resolved to run — every Docker-based scanner is dropped
// so no container is ever invoked, even when the scanner has been installed.
// Enabling deep scan brings the installed Docker scanner back into the set.
func TestServiceDeepScanOffByDefault(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	store := newMockStorage()
	docker := NewDockerRunner(logger)
	registry := NewRegistry(dir, logger) // bundled: in-process tpa + Docker scanners
	// Install a Docker scanner so it WOULD resolve if deep scan were on.
	registry.scanners["mcp-scan"].Status = ScannerStatusInstalled

	svc := NewService(store, registry, docker, dir, logger)

	// Default: deep scan is OFF.
	if svc.engine.deepScanEnabled {
		t.Fatalf("deep scan must be disabled by default (FR-006)")
	}

	resolved, err := svc.engine.resolveScanners(nil, "")
	if err != nil {
		t.Fatalf("resolveScanners: %v", err)
	}
	ids := make(map[string]bool)
	for _, rs := range resolved {
		ids[rs.plugin.ID] = true
	}
	if !ids[inProcessTPAScannerID] {
		t.Errorf("baseline in-process scanner must always resolve; got %v", ids)
	}
	if ids["mcp-scan"] {
		t.Errorf("Docker scanner must NOT resolve while deep scan is off; got %v", ids)
	}
	for id := range ids {
		if p, gErr := registry.Get(id); gErr == nil && !p.InProcess {
			t.Errorf("no Docker (non-in-process) scanner may resolve while deep scan is off; got %q", id)
		}
	}

	// Turning deep scan on brings the installed Docker scanner back.
	svc.SetDeepScan(true, nil)
	if !svc.engine.deepScanEnabled {
		t.Fatalf("SetDeepScan(true) must enable the deep-scan layer")
	}
	resolved, err = svc.engine.resolveScanners(nil, "")
	if err != nil {
		t.Fatalf("resolveScanners (deep on): %v", err)
	}
	ids = make(map[string]bool)
	for _, rs := range resolved {
		ids[rs.plugin.ID] = true
	}
	if !ids["mcp-scan"] {
		t.Errorf("Docker scanner must resolve once deep scan is enabled; got %v", ids)
	}

	// A per-scanner allow-list restricts which deep scanners are eligible.
	registry.scanners["semgrep-mcp"].Status = ScannerStatusInstalled
	svc.SetDeepScan(true, []string{"mcp-scan"})
	resolved, err = svc.engine.resolveScanners(nil, "")
	if err != nil {
		t.Fatalf("resolveScanners (allow-list): %v", err)
	}
	ids = make(map[string]bool)
	for _, rs := range resolved {
		ids[rs.plugin.ID] = true
	}
	if !ids["mcp-scan"] {
		t.Errorf("allow-listed deep scanner must resolve; got %v", ids)
	}
	if ids["semgrep-mcp"] {
		t.Errorf("non-allow-listed deep scanner must be dropped; got %v", ids)
	}
}

// TestServiceDeepScanGatesPackageSourceFetch verifies Spec 077 US3: published-
// package-source extraction is a facet of the opt-in deep-scan layer, so it must
// not run (no network egress) while deep scan is off. Turning the deep-scan
// layer off must force the source resolver's fetch fallback off; the server
// layer re-enables it (honoring fetch_package_source) only when deep scan is on.
func TestServiceDeepScanGatesPackageSourceFetch(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	store := newMockStorage()
	docker := NewDockerRunner(logger)
	registry := NewRegistry(dir, logger)

	svc := NewService(store, registry, docker, dir, logger)

	// Simulate an operator who had opted into source fetch previously.
	svc.SetFetchPackageSource(true)
	if !svc.sourceResolver.fetchPackageSource {
		t.Fatalf("precondition: fetch should be enabled after SetFetchPackageSource(true)")
	}

	// Deep scan OFF (the default) must forbid the published-package-source fetch
	// so scanning an npx/uvx server performs no network egress.
	svc.SetDeepScan(false, nil)
	if svc.sourceResolver.fetchPackageSource {
		t.Errorf("deep scan off must force source fetch off (no egress by default)")
	}

	// The server layer is the authority when deep scan is on: it calls
	// SetFetchPackageSource with deep_scan.fetch_package_source (default true).
	svc.SetDeepScan(true, nil)
	svc.SetFetchPackageSource(true)
	if !svc.sourceResolver.fetchPackageSource {
		t.Errorf("deep scan on with fetch_package_source=true must allow source fetch")
	}

	// An explicit fetch_package_source=false still wins while deep scan is on.
	svc.SetFetchPackageSource(false)
	if svc.sourceResolver.fetchPackageSource {
		t.Errorf("explicit fetch_package_source=false must disable source fetch")
	}
}

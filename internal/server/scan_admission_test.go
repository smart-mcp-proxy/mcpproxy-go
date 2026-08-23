package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// fakeSecurityScanner is a test double for securityScannerService. It records
// ApproveServer / StartScan calls and lets each test pin per-server scan
// summaries and prior-baseline state, so the spec-086 admission gate
// (maybeStartAdmissionScan / maybeAutoApproveScanSettled) can be exercised with
// no real scanner backend.
type fakeSecurityScanner struct {
	mu          sync.Mutex
	summaries   map[string]*scanner.ScanSummary
	hasBaseline map[string]bool
	approveErr  error

	// Scan-surface fixtures (quarantine_security scan_server /
	// get_scan_report). jobs/reports are keyed by server name; startScanErr
	// and reportErr let a test drive the failure branches.
	jobs map[string]*scanner.ScanJob
	// passJobs pins a job for one specific (server, pass) pair, so a test can
	// make the ACTIVE job differ from the Pass-1 job — the shape produced when
	// a completed Pass 1 auto-starts the Pass-2 deep audit.
	passJobs     map[passKey]*scanner.ScanJob
	reports      map[string]*scanner.AggregatedReport
	startScanErr error
	reportErr    error
	// onStartScan runs inside StartScan, so a test can make the scan "settle"
	// (publish a completed job + summary) exactly when it is triggered.
	onStartScan func(serverName string)

	approveCalls   []string
	startScanCalls []string
}

func newFakeSecurityScanner() *fakeSecurityScanner {
	return &fakeSecurityScanner{
		summaries:   map[string]*scanner.ScanSummary{},
		hasBaseline: map[string]bool{},
		jobs:        map[string]*scanner.ScanJob{},
		passJobs:    map[passKey]*scanner.ScanJob{},
		reports:     map[string]*scanner.AggregatedReport{},
	}
}

// passKey addresses one (server, scan pass) pair in the fake.
type passKey struct {
	server string
	pass   int
}

// setPassJob pins the job a per-pass lookup resolves to, independently of the
// job the generic status lookup returns.
func (f *fakeSecurityScanner) setPassJob(serverName string, pass int, job *scanner.ScanJob) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.passJobs[passKey{server: serverName, pass: pass}] = job
}

// setScanResult publishes a settled scan for a server: the job the status poll
// sees and the summary the verdict is read from.
func (f *fakeSecurityScanner) setScanResult(serverName string, job *scanner.ScanJob, summary *scanner.ScanSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs[serverName] = job
	f.summaries[serverName] = summary
}

func (f *fakeSecurityScanner) GetScanSummary(_ context.Context, serverName string) *scanner.ScanSummary {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.summaries[serverName]
}

func (f *fakeSecurityScanner) ApproveServer(_ context.Context, serverName string, _ bool, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.approveErr != nil {
		return f.approveErr
	}
	f.approveCalls = append(f.approveCalls, serverName)
	return nil
}

func (f *fakeSecurityScanner) StartScan(_ context.Context, serverName string, _ bool, _ []string, _ string) (*scanner.ScanJob, error) {
	f.mu.Lock()
	f.startScanCalls = append(f.startScanCalls, serverName)
	err := f.startScanErr
	hook := f.onStartScan
	f.mu.Unlock()

	// The hook may publish the settled job, so read it back afterwards.
	if hook != nil {
		hook(serverName)
	}
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// StartScan starts Pass 1 and hands back THAT job, so prefer a pinned
	// Pass-1 job when a test has made the passes differ.
	if job, ok := f.passJobs[passKey{server: serverName, pass: scanner.ScanPassSecurityScan}]; ok && job != nil {
		return job, nil
	}
	return f.jobs[serverName], nil
}

func (f *fakeSecurityScanner) GetScanStatus(_ context.Context, serverName string) (*scanner.ScanJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	job, ok := f.jobs[serverName]
	if !ok {
		return nil, errors.New("no scan job")
	}
	return job, nil
}

// GetScanStatusByPass mirrors the service: a per-pass lookup. The fake keys one
// job per server, so pass 1 resolves to it and any other pass falls through to
// the same job — enough to exercise the "poll Pass 1, not whatever is active"
// contract, with passJobs available when a test needs the two to differ.
func (f *fakeSecurityScanner) GetScanStatusByPass(_ context.Context, serverName string, pass int) (*scanner.ScanJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if job, ok := f.passJobs[passKey{server: serverName, pass: pass}]; ok {
		return job, nil
	}
	job, ok := f.jobs[serverName]
	if !ok {
		return nil, errors.New("no scan job")
	}
	return job, nil
}

func (f *fakeSecurityScanner) GetScanReport(_ context.Context, serverName string) (*scanner.AggregatedReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reportErr != nil {
		return nil, f.reportErr
	}
	report, ok := f.reports[serverName]
	if !ok {
		return nil, errors.New("no scan found")
	}
	return report, nil
}

func (f *fakeSecurityScanner) HasApprovalBaseline(serverName string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hasBaseline[serverName]
}

func (f *fakeSecurityScanner) ApplySecurityConfig(*config.SecurityConfig) {}
func (f *fakeSecurityScanner) SetIsolationMode(string)                    {}
func (f *fakeSecurityScanner) DeepScanEnabled() bool                      { return false }

func (f *fakeSecurityScanner) approvedServers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.approveCalls...)
}

func (f *fakeSecurityScanner) startedScans() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.startScanCalls...)
}

// newAdmissionTestServer builds a Server whose runtime config carries the given
// servers and whose securityScanner is the supplied fake. The event loop is NOT
// started — tests drive the admission methods directly.
func newAdmissionTestServer(t *testing.T, fake securityScannerService, servers ...*config.ServerConfig) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Servers = servers
	rt, err := runtime.New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	// Persist the servers to storage too: maybeStartAdmissionScans reads the
	// server list from StorageManager().ListUpstreamServers() (a synchronized
	// snapshot) rather than the shared live config, mirroring production where an
	// added server is always saved to BBolt before servers.changed fires.
	for _, sc := range servers {
		if sc != nil {
			require.NoError(t, rt.StorageManager().SaveUpstreamServer(sc))
		}
	}
	return &Server{
		logger:              zap.NewNop(),
		runtime:             rt,
		securityScanner:     fake,
		admissionScanKicked: make(map[string]bool),
	}
}

func scanModeServer(name string, quarantined bool) *config.ServerConfig {
	return &config.ServerConfig{Name: name, TrustMode: string(config.TrustModeScan), Quarantined: quarantined}
}

// TestMaybeAutoApproveScanSettled drives the REAL settle handler (not just the
// pure predicate) against a fake scanner + live runtime config, covering the
// fail-closed branches that shouldAutoApproveScanSettled alone cannot reach:
// stale/missing config, manual-never-approve, empty verdict, ApproveServer
// error, and the FR-011 admission-window gate (prior baseline).
func TestMaybeAutoApproveScanSettled(t *testing.T) {
	t.Run("scan+quarantined+clean+no-baseline: approves", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		fake.summaries["srv"] = &scanner.ScanSummary{Status: "clean"}
		s := newAdmissionTestServer(t, fake, scanModeServer("srv", true))

		s.maybeAutoApproveScanSettled(context.Background(), "srv")

		assert.Equal(t, []string{"srv"}, fake.approvedServers())
	})

	t.Run("missing config entry: never approves", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		fake.summaries["ghost"] = &scanner.ScanSummary{Status: "clean"}
		// No servers in config → findServerConfig returns nil.
		s := newAdmissionTestServer(t, fake)

		s.maybeAutoApproveScanSettled(context.Background(), "ghost")

		assert.Empty(t, fake.approvedServers())
	})

	t.Run("manual mode + clean: never approves", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		fake.summaries["srv"] = &scanner.ScanSummary{Status: "clean"}
		manual := &config.ServerConfig{Name: "srv", TrustMode: string(config.TrustModeManual), Quarantined: true}
		s := newAdmissionTestServer(t, fake, manual)

		s.maybeAutoApproveScanSettled(context.Background(), "srv")

		assert.Empty(t, fake.approvedServers())
	})

	t.Run("scan mode + nil summary (empty verdict): never approves", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		// No summary entry → GetScanSummary returns nil → verdict "".
		s := newAdmissionTestServer(t, fake, scanModeServer("srv", true))

		s.maybeAutoApproveScanSettled(context.Background(), "srv")

		assert.Empty(t, fake.approvedServers())
	})

	t.Run("ApproveServer error: no panic, stays quarantined", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		fake.summaries["srv"] = &scanner.ScanSummary{Status: "clean"}
		fake.approveErr = errors.New("hard-tier finding blocks approval")
		s := newAdmissionTestServer(t, fake, scanModeServer("srv", true))

		assert.NotPanics(t, func() {
			s.maybeAutoApproveScanSettled(context.Background(), "srv")
		})
		assert.Empty(t, fake.approvedServers())
	})

	t.Run("prior baseline (operator re-quarantine): never auto-approves (FR-011 admission window)", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		fake.summaries["srv"] = &scanner.ScanSummary{Status: "clean"}
		fake.hasBaseline["srv"] = true // already approved once
		s := newAdmissionTestServer(t, fake, scanModeServer("srv", true))

		s.maybeAutoApproveScanSettled(context.Background(), "srv")

		assert.Empty(t, fake.approvedServers(), "a re-quarantine of a previously-approved server must not be silently overridden")
	})
}

// TestMaybeStartAdmissionScans drives the FR-011 "trigger a scan" path: a
// scan-mode, quarantined, never-scanned server gets exactly one baseline scan
// kicked; manual mode, already-scanned, and previously-approved servers do not.
func TestMaybeStartAdmissionScans(t *testing.T) {
	waitForScans := func(t *testing.T, fake *fakeSecurityScanner, want int) {
		t.Helper()
		require.Eventually(t, func() bool { return len(fake.startedScans()) == want }, 2*time.Second, 5*time.Millisecond)
	}

	t.Run("scan+quarantined+never-scanned: kicks one scan, deduped on repeat", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		s := newAdmissionTestServer(t, fake, scanModeServer("srv", true))

		s.maybeStartAdmissionScans(context.Background())
		waitForScans(t, fake, 1)

		// A second servers.changed must not restart the scan.
		s.maybeStartAdmissionScans(context.Background())
		assert.Equal(t, []string{"srv"}, fake.startedScans())
	})

	t.Run("manual mode: never kicks a scan", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		manual := &config.ServerConfig{Name: "srv", TrustMode: string(config.TrustModeManual), Quarantined: true}
		s := newAdmissionTestServer(t, fake, manual)

		s.maybeStartAdmissionScans(context.Background())
		time.Sleep(50 * time.Millisecond)
		assert.Empty(t, fake.startedScans())
	})

	t.Run("already scanned: never kicks a scan", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		fake.summaries["srv"] = &scanner.ScanSummary{Status: "scanning"}
		s := newAdmissionTestServer(t, fake, scanModeServer("srv", true))

		s.maybeStartAdmissionScans(context.Background())
		time.Sleep(50 * time.Millisecond)
		assert.Empty(t, fake.startedScans())
	})

	t.Run("prior baseline: never re-scans a re-quarantined server", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		fake.hasBaseline["srv"] = true
		s := newAdmissionTestServer(t, fake, scanModeServer("srv", true))

		s.maybeStartAdmissionScans(context.Background())
		time.Sleep(50 * time.Millisecond)
		assert.Empty(t, fake.startedScans())
	})
}

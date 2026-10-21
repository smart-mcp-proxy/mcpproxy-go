package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 101 FR-019 / tasks B17, T362: the post-activation re-protection sweep.
// The tests call handleBundleActivation with a synthetic BundleActivation, so
// they need neither the refresher nor a real bundle.

const (
	reprotectDigestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	reprotectDigestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	reprotectDigestC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

// reprotectProbe records what the sweep reported through its seams.
type reprotectProbe struct {
	mu       sync.Mutex
	statuses []scanner.BundleReprotectStatus
	held     []string
	hits     int
}

func (p *reprotectProbe) lastStatus(t *testing.T) scanner.BundleReprotectStatus {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.NotEmpty(t, p.statuses, "the sweep must publish a status")
	return p.statuses[len(p.statuses)-1]
}

func (p *reprotectProbe) heldServers() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.held...)
}

func (p *reprotectProbe) hitCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hits
}

// newReprotectServer builds a test Server over the given servers with fast
// pacing and every outward seam routed into the returned probe. The held-evidence
// refresh seam wraps the real runtime method, so records are really refreshed.
func newReprotectServer(t *testing.T, fake securityScannerService, sec *config.SecurityConfig, servers ...*config.ServerConfig) (*Server, *reprotectProbe) {
	t.Helper()
	s := newInformationalTestServer(t, fake, sec, servers...)
	probe := &reprotectProbe{}
	s.bundleReprotect.serverPause = time.Nanosecond
	s.bundleReprotect.busyRetryDelay = time.Millisecond
	s.bundleReprotect.statusSink = func(st scanner.BundleReprotectStatus) {
		probe.mu.Lock()
		defer probe.mu.Unlock()
		probe.statuses = append(probe.statuses, st)
	}
	s.bundleReprotect.refreshHeld = func(server string) error {
		probe.mu.Lock()
		probe.held = append(probe.held, server)
		probe.mu.Unlock()
		return s.runtime.RefreshHeldScanEvidence(server)
	}
	s.bundleReprotect.recordHit = func() {
		probe.mu.Lock()
		defer probe.mu.Unlock()
		probe.hits++
	}
	return s, probe
}

func seedToolRecords(t *testing.T, s *Server, server string, statuses map[string]string) {
	t.Helper()
	for tool, status := range statuses {
		require.NoError(t, s.runtime.StorageManager().SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName:         server,
			ToolName:           tool,
			Status:             status,
			ApprovedHash:       "approved-hash-" + tool,
			CurrentHash:        "current-hash-" + tool,
			CurrentDescription: "Does " + tool + ".",
			CurrentSchema:      `{"type":"object"}`,
		}))
	}
}

func approvedOnly(tools ...string) map[string]string {
	m := map[string]string{}
	for _, tool := range tools {
		m[tool] = storage.ToolApprovalStatusApproved
	}
	return m
}

// awaitClosed fails the test instead of hanging when ch never closes.
func awaitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a sweep milestone")
	}
}

func activation(digest string) scanner.BundleActivation {
	return scanner.BundleActivation{Digest: digest, Sequence: "7", Authority: "operator", Source: scanner.BundleSourceFetch, ActivatedAt: time.Now()}
}

func reprotectMarker(t *testing.T, s *Server) storage.BundleReprotectState {
	t.Helper()
	state, err := s.runtime.StorageManager().LoadBundleReprotectState()
	require.NoError(t, err)
	return state
}

func recordsOf(t *testing.T, s *Server, server string) map[string]storage.ToolApprovalRecord {
	t.Helper()
	recs, err := s.runtime.StorageManager().ListToolApprovals(server)
	require.NoError(t, err)
	out := map[string]storage.ToolApprovalRecord{}
	for _, r := range recs {
		out[r.ToolName] = *r
	}
	return out
}

func TestReprotect_OneCachedScanPerNonQuarantinedServerWithApprovedRecords(t *testing.T) {
	fake := newFakeSecurityScanner()
	s, probe := newReprotectServer(t, fake, nil,
		enabledServer("alpha", config.TrustModeManual),
		enabledServer("bravo", config.TrustModeManual),
		&config.ServerConfig{Name: "quar", Enabled: true, Quarantined: true},
		enabledServer("held", config.TrustModeManual),
		enabledServer("empty", config.TrustModeManual),
	)
	seedToolRecords(t, s, "alpha", approvedOnly("a1", "a2"))
	seedToolRecords(t, s, "bravo", map[string]string{"b1": storage.ToolApprovalStatusApproved, "b2": storage.ToolApprovalStatusChanged})
	seedToolRecords(t, s, "quar", map[string]string{"q1": storage.ToolApprovalStatusApproved, "q2": storage.ToolApprovalStatusPending})
	seedToolRecords(t, s, "held", map[string]string{"h1": storage.ToolApprovalStatusPending})

	s.handleBundleActivation(activation(reprotectDigestA))

	assert.Equal(t, []string{"alpha@" + reprotectDigestA, "bravo@" + reprotectDigestA}, fake.cachedScans(),
		"one cached scan per non-quarantined server with approved records; quarantined servers never get a scan job")
	assert.Equal(t, []string{"bravo", "held", "quar"}, probe.heldServers(),
		"leg (i) covers every server with pending or changed records, quarantined included")
	assert.Empty(t, fake.startScanAttempts(), "the sweep never goes through the live StartScan (no EnsureConnected, no tools/list)")
	assert.Empty(t, fake.approvedServers(), "RT-02: nothing is approved")
}

func TestReprotect_NeverApprovesUnquarantinesOrChangesRecords(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.cachedScanResult["alpha"] = &scanner.ScanSummary{Status: "dangerous"}
	s, _ := newReprotectServer(t, fake, nil,
		enabledServer("alpha", config.TrustModeScan),
		&config.ServerConfig{Name: "quar", Enabled: true, Quarantined: true, TrustMode: string(config.TrustModeScan)},
	)
	seedToolRecords(t, s, "alpha", map[string]string{"a1": storage.ToolApprovalStatusApproved, "a2": storage.ToolApprovalStatusPending})
	seedToolRecords(t, s, "quar", map[string]string{"q1": storage.ToolApprovalStatusChanged})
	beforeAlpha, beforeQuar := recordsOf(t, s, "alpha"), recordsOf(t, s, "quar")

	s.handleBundleActivation(activation(reprotectDigestA))

	for tool, before := range beforeAlpha {
		after := recordsOf(t, s, "alpha")[tool]
		assert.Equal(t, before.Status, after.Status, tool)
		assert.Equal(t, before.ApprovedHash, after.ApprovedHash, tool)
		assert.Equal(t, before.ApprovedBy, after.ApprovedBy, tool)
	}
	assert.Equal(t, storage.ToolApprovalStatusChanged, recordsOf(t, s, "quar")["q1"].Status)
	assert.Equal(t, beforeQuar["q1"].ApprovedHash, recordsOf(t, s, "quar")["q1"].ApprovedHash)
	assert.Empty(t, fake.approvedServers(), "no ApproveServer")
	stored, err := s.runtime.StorageManager().GetUpstreamServer("quar")
	require.NoError(t, err)
	assert.True(t, stored.Quarantined, "no unquarantine")
}

func TestReprotect_SameDigestRefiredIsNothingAndARollbackIsOneMoreSweep(t *testing.T) {
	fake := newFakeSecurityScanner()
	s, _ := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))

	s.handleBundleActivation(activation(reprotectDigestA))
	require.Len(t, fake.cachedScans(), 1)

	s.handleBundleActivation(activation(reprotectDigestA))
	assert.Len(t, fake.cachedScans(), 1, "the same digest re-fired yields no job")

	rollback := activation(reprotectDigestB)
	rollback.Rollback, rollback.PreviousDigest = true, reprotectDigestA
	s.handleBundleActivation(rollback)
	assert.Equal(t, []string{"alpha@" + reprotectDigestA, "alpha@" + reprotectDigestB}, fake.cachedScans(),
		"a rollback is one more history row and therefore exactly one more sweep")

	s.handleBundleActivation(activation(reprotectDigestA))
	assert.Len(t, fake.cachedScans(), 3, "returning to an earlier digest is a new activation, not a repeat")
}

func TestReprotect_BusyServerReArmsWithoutSpendingAFailure(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.cachedScanErrs = map[string][]error{"alpha": {scanner.ErrScanInProgress, scanner.ErrScanInProgress, scanner.ErrScanInProgress, scanner.ErrScanInProgress}}
	s, probe := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))

	s.handleBundleActivation(activation(reprotectDigestA))

	assert.Len(t, fake.cachedScanAttempts(), 5, "four busy answers re-armed, the fifth attempt started the scan")
	assert.Len(t, fake.cachedScans(), 1)
	assert.NotEqual(t, scanner.ReprotectStateFailed, probe.lastStatus(t).State, "busy is not a failure")
	assert.Equal(t, reprotectDigestA, reprotectMarker(t, s).FullDigest, "a sweep that finished without failures writes the marker")
}

func TestReprotect_FailuresCapAtThreeAttemptsPerServerAndKeepTheMarkerUnwritten(t *testing.T) {
	boom := errors.New("scanner unavailable")
	fake := newFakeSecurityScanner()
	fake.cachedScanErrs = map[string][]error{"alpha": {boom, boom, boom, boom, boom}}
	s, probe := newReprotectServer(t, fake, nil,
		enabledServer("alpha", config.TrustModeManual),
		enabledServer("bravo", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))
	seedToolRecords(t, s, "bravo", approvedOnly("b1"))

	s.handleBundleActivation(activation(reprotectDigestA))

	attempts := 0
	for _, name := range fake.cachedScanAttempts() {
		if name == "alpha" {
			attempts++
		}
	}
	assert.Equal(t, 3, attempts, "at most 3 attempts per server")
	assert.Equal(t, []string{"bravo@" + reprotectDigestA}, fake.cachedScans(), "a failing server does not stop the others")
	assert.Equal(t, scanner.ReprotectStateFailed, probe.lastStatus(t).State)
	assert.Empty(t, reprotectMarker(t, s).FullDigest, "the marker is written only on a zero-failure completion, so the next start retries")
}

func TestReprotect_NoApprovedContractsIsNotAFailure(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.cachedScanErrs = map[string][]error{"alpha": {scanner.ErrNoCachedContracts}}
	s, probe := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))

	s.handleBundleActivation(activation(reprotectDigestA))

	assert.Len(t, fake.cachedScanAttempts(), 1, "no retry for a server with nothing to scan")
	assert.NotEqual(t, scanner.ReprotectStateFailed, probe.lastStatus(t).State)
}

func TestReprotect_AutoBaselineScanOffSuspendsTheScanLegButNotTheEvidenceLeg(t *testing.T) {
	fake := newFakeSecurityScanner()
	off := false
	s, probe := newReprotectServer(t, fake, &config.SecurityConfig{AutoBaselineScan: &off},
		enabledServer("alpha", config.TrustModeManual),
		enabledServer("held", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))
	seedToolRecords(t, s, "held", map[string]string{"h1": storage.ToolApprovalStatusPending})

	s.handleBundleActivation(activation(reprotectDigestA))

	assert.Empty(t, fake.cachedScanAttempts(), "leg (ii) honours the auto_baseline_scan kill switch")
	assert.Equal(t, []string{"held"}, probe.heldServers(), "leg (i) always runs")
	last := probe.lastStatus(t)
	assert.Equal(t, scanner.ReprotectStateSuspended, last.State)
	assert.Equal(t, scanner.ReprotectReasonAutoBaselineScanOff, last.Reason)
	assert.Empty(t, reprotectMarker(t, s).FullDigest, "nothing was scanned, so the sweep is not complete")
}

func TestReprotect_StatusAndHitAccounting(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.cachedScanResult["alpha"] = &scanner.ScanSummary{Status: "dangerous", FindingCounts: &scanner.FindingCounts{Total: 2, Dangerous: 1}}
	fake.cachedScanResult["bravo"] = &scanner.ScanSummary{Status: "clean"}
	s, probe := newReprotectServer(t, fake, nil,
		enabledServer("alpha", config.TrustModeManual),
		enabledServer("bravo", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))
	seedToolRecords(t, s, "bravo", approvedOnly("b1"))

	s.handleBundleActivation(activation(reprotectDigestA))

	probe.mu.Lock()
	require.GreaterOrEqual(t, len(probe.statuses), 2)
	assert.Equal(t, scanner.ReprotectStateRunning, probe.statuses[0].State, "the sweep announces itself")
	probe.mu.Unlock()
	last := probe.lastStatus(t)
	assert.Equal(t, scanner.ReprotectStateIdle, last.State)
	assert.Equal(t, 2, last.ServersScanned)
	assert.Equal(t, 1, last.ServersWithHits, "only the server whose cached scan has a hard finding is a hit")
	require.NotNil(t, last.LastCompletedAt)
	assert.Equal(t, 1, probe.hitCount(), "bundle_reprotect_hits counts servers, not findings")
	assert.Equal(t, reprotectDigestA, reprotectMarker(t, s).FullDigest)
}

func TestReprotect_StormBound_OneScanPerServerPerSweep(t *testing.T) {
	fake := newFakeSecurityScanner()
	names := []string{"s1", "s2", "s3", "s4", "s5", "s6"}
	var servers []*config.ServerConfig
	for _, n := range names {
		servers = append(servers, enabledServer(n, config.TrustModeManual))
	}
	s, _ := newReprotectServer(t, fake, nil, servers...)
	for _, n := range names {
		seedToolRecords(t, s, n, approvedOnly("t1", "t2", "t3"))
	}

	s.handleBundleActivation(activation(reprotectDigestA))

	assert.Len(t, fake.cachedScans(), len(names), "a sweep over N servers is N scans, not N x tools: at most one finding set and one activity record per server")
	seen := map[string]int{}
	for _, call := range fake.cachedScans() {
		seen[call]++
	}
	for call, n := range seen {
		assert.Equal(t, 1, n, call)
	}
}

func TestReprotect_CatchUp_RunsWhenTheMarkerDiffersAndIsSkippedWhenEqual(t *testing.T) {
	fake := newFakeSecurityScanner()
	s, _ := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))
	s.bundleReprotect.activeDigest = func() string { return reprotectDigestA }
	sm := s.runtime.StorageManager()

	// No marker yet: a construction-time activation, a crash mid-sweep or an
	// upgrade that changed the embedded corpus.
	s.runBundleReprotectCatchUp(context.Background())
	require.Equal(t, []string{"alpha@" + reprotectDigestA}, fake.cachedScans())
	assert.Equal(t, reprotectDigestA, reprotectMarker(t, s).FullDigest)

	// Marker equals the active digest: nothing to do (a fresh process, same state).
	s.bundleReprotect = bundleReprotectState{} // a new process has no in-memory dedupe
	s.bundleReprotect.serverPause, s.bundleReprotect.busyRetryDelay = time.Nanosecond, time.Millisecond
	s.bundleReprotect.activeDigest = func() string { return reprotectDigestA }
	s.runBundleReprotectCatchUp(context.Background())
	assert.Len(t, fake.cachedScans(), 1, "marker equals the active digest: skipped")

	// The embedded corpus changed with a binary upgrade.
	s.bundleReprotect.activeDigest = func() string { return reprotectDigestB }
	s.runBundleReprotectCatchUp(context.Background())
	assert.Equal(t, []string{"alpha@" + reprotectDigestA, "alpha@" + reprotectDigestB}, fake.cachedScans())
	state, err := sm.LoadBundleReprotectState()
	require.NoError(t, err)
	assert.Equal(t, reprotectDigestB, state.FullDigest)
}

func TestReprotect_CatchUp_UnreadableMarkerOrNoDigestDoesNothing(t *testing.T) {
	fake := newFakeSecurityScanner()
	s, _ := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))

	s.bundleReprotect.activeDigest = func() string { return "" }
	s.runBundleReprotectCatchUp(context.Background())
	assert.Empty(t, fake.cachedScanAttempts(), "no active digest: nothing to cover")
}

// A newer activation abandons the sweep of the superseded digest, which would
// only produce evidence for a corpus that no longer serves.
func TestReprotect_NewerActivationAbandonsASupersededSweep(t *testing.T) {
	fake := newFakeSecurityScanner()
	s, _ := newReprotectServer(t, fake, nil,
		enabledServer("alpha", config.TrustModeManual),
		enabledServer("bravo", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))
	seedToolRecords(t, s, "bravo", approvedOnly("b1"))

	inFirst := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fake.onCachedScan = func(string) {
		once.Do(func() {
			close(inFirst)
			<-release
		})
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.handleBundleActivation(activation(reprotectDigestA)) }()
	awaitClosed(t, inFirst)
	go func() { defer wg.Done(); s.handleBundleActivation(activation(reprotectDigestC)) }()
	require.Eventually(t, func() bool {
		s.bundleReprotect.mu.Lock()
		defer s.bundleReprotect.mu.Unlock()
		return s.bundleReprotect.latest == reprotectDigestC
	}, 5*time.Second, time.Millisecond, "the newer activation must be registered while the first sweep runs")
	close(release)
	waited := make(chan struct{})
	go func() { wg.Wait(); close(waited) }()
	awaitClosed(t, waited)

	assert.Equal(t, []string{"alpha@" + reprotectDigestA, "alpha@" + reprotectDigestC, "bravo@" + reprotectDigestC}, fake.cachedScans(),
		"the superseded sweep stops after the server it was on; the newer digest sweeps everything")
	assert.Equal(t, reprotectDigestC, reprotectMarker(t, s).FullDigest, "only the newest digest's completed sweep writes the marker")
}

// Shutdown: the observer side is covered by tpa_bundle_wiring_test.go; here the
// sweep goroutine must exit, scan nothing more and write nothing once told to stop.
func TestReprotect_StopDuringARunningSweepExitsAndWritesNothing(t *testing.T) {
	fake := newFakeSecurityScanner()
	s, _ := newReprotectServer(t, fake, nil,
		enabledServer("alpha", config.TrustModeManual),
		enabledServer("bravo", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))
	seedToolRecords(t, s, "bravo", approvedOnly("b1"))

	inFirst := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fake.onCachedScan = func(string) {
		once.Do(func() {
			close(inFirst)
			<-release
		})
	}

	done := make(chan struct{})
	go func() { defer close(done); s.handleBundleActivation(activation(reprotectDigestA)) }()
	awaitClosed(t, inFirst)

	stopped := make(chan struct{})
	go func() { defer close(stopped); s.stopBundleReprotect() }()
	time.Sleep(20 * time.Millisecond)
	close(release)

	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("stopBundleReprotect did not return: the sweep goroutine never exited")
	}
	awaitClosed(t, done)

	assert.Equal(t, []string{"alpha@" + reprotectDigestA}, fake.cachedScans(), "no scan starts after the stop")
	assert.Empty(t, reprotectMarker(t, s).FullDigest, "no store write after the stop")

	// A late activation after the stop is ignored.
	s.handleBundleActivation(activation(reprotectDigestB))
	assert.Len(t, fake.cachedScans(), 1)
}

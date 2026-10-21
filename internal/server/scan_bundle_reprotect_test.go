package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

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
	s.infoScanSettleTimeout = 30 * time.Millisecond // fakes settle instantly or never
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

// US3 judge finding 5: the quarantine decision for leg (ii) is taken when the
// server's scan starts, not when the sweep listed the servers - a sweep pauses
// between servers and waits on the informational queue, so the listing can be
// minutes old. A server quarantined meanwhile never gets a cached scan job.
func TestReprotect_QuarantineIsRecheckedWhenTheServersScanStarts(t *testing.T) {
	fake := newFakeSecurityScanner()
	s, _ := newReprotectServer(t, fake, nil,
		enabledServer("alpha", config.TrustModeManual),
		enabledServer("bravo", config.TrustModeScan),
	)
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))
	seedToolRecords(t, s, "bravo", approvedOnly("b1"))
	fake.onCachedScan = func(server string) {
		if server != "alpha" {
			return
		}
		sm := s.runtime.StorageManager()
		sc, err := sm.GetUpstreamServer("bravo")
		require.NoError(t, err)
		sc.Quarantined = true // the operator switched bravo to scan mode and it got quarantined
		require.NoError(t, sm.SaveUpstreamServer(sc))
	}

	s.handleBundleActivation(activation(reprotectDigestA))

	assert.Equal(t, []string{"alpha@" + reprotectDigestA}, fake.cachedScans())
	assert.Equal(t, []string{"alpha"}, fake.cachedScanAttempts(), "a server quarantined after the listing must not even be attempted")
}

// US3 judge finding 5: the settle of a re-protection job must never drive the
// scan-mode admission gate - its contracts are the stored ones, not the live
// tool list the admission verdict is about.
func TestReprotect_SettleOfACachedJobNeverAutoApprovesAServer(t *testing.T) {
	fake := newFakeSecurityScanner()
	s, _ := newReprotectServer(t, fake, nil,
		&config.ServerConfig{Name: "gate", TrustMode: string(config.TrustModeScan), Enabled: true, Quarantined: true},
	)
	fake.setScanResult("gate",
		&scanner.ScanJob{ServerName: "gate", ScanPass: scanner.ScanPassSecurityScan, Status: scanner.ScanJobStatusCompleted,
			ScanContext: &scanner.ScanContext{BundleDigest: reprotectDigestA}},
		&scanner.ScanSummary{Status: "clean"})

	s.maybeAutoApproveScanSettled(context.Background(), "gate")

	assert.Empty(t, fake.approvedServers(), "a clean re-protection job over stored contracts must not unquarantine a server")

	// Control: the same clean verdict from a live scan (no bundle digest) does
	// approve, so the assertion above is about the cached job, not the fixture.
	fake.setScanResult("gate",
		&scanner.ScanJob{ServerName: "gate", ScanPass: scanner.ScanPassSecurityScan, Status: scanner.ScanJobStatusCompleted},
		&scanner.ScanSummary{Status: "clean"})
	s.maybeAutoApproveScanSettled(context.Background(), "gate")
	assert.Equal(t, []string{"gate"}, fake.approvedServers())
}

// US3 judge finding 7: observers run on separate goroutines, so two activations
// committed milliseconds apart can reach the sweep in reverse order. The older
// one (lower Seq) must be dropped instead of becoming "latest" while the newer
// bundle serves.
func TestReprotect_AnActivationArrivingAfterANewerOneIsDropped(t *testing.T) {
	fake := newFakeSecurityScanner()
	s, _ := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))

	newer := activation(reprotectDigestB)
	newer.Seq = 2
	older := activation(reprotectDigestA)
	older.Seq = 1
	s.handleBundleActivation(newer) // the goroutine of the NEWER commit ran first
	s.handleBundleActivation(older)

	assert.Equal(t, []string{"alpha@" + reprotectDigestB}, fake.cachedScans(), "the stale activation must not start a sweep over the newer bundle's corpus")
	assert.Equal(t, reprotectDigestB, reprotectMarker(t, s).FullDigest, "the marker records the bundle that is serving")
}

// US3 judge finding 8: a sweep suspended by auto_baseline_scan=false is
// resumed when the switch comes back on, without waiting for a restart.
func TestReprotect_SuspendedSweepResumesWhenAutoBaselineScanTurnsOn(t *testing.T) {
	fake := newFakeSecurityScanner()
	off := false
	s, probe := newReprotectServer(t, fake, &config.SecurityConfig{AutoBaselineScan: &off},
		enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))
	s.bundleReprotect.activeDigest = func() string { return reprotectDigestA }

	s.handleBundleActivation(activation(reprotectDigestA))
	require.Equal(t, scanner.ReprotectStateSuspended, probe.lastStatus(t).State)
	require.Empty(t, fake.cachedScans())

	s.resumeSuspendedReprotect() // switch still off: nothing to resume
	require.Empty(t, fake.cachedScans(), "control: resuming while the switch is off does nothing")

	off = true // the operator turned auto_baseline_scan back on
	s.resumeSuspendedReprotect()

	assert.Equal(t, []string{"alpha@" + reprotectDigestA}, fake.cachedScans(), "the suspended sweep runs once the switch is on")
	assert.Equal(t, scanner.ReprotectStateIdle, probe.lastStatus(t).State)
	assert.Equal(t, reprotectDigestA, reprotectMarker(t, s).FullDigest)

	s.resumeSuspendedReprotect()
	assert.Len(t, fake.cachedScans(), 1, "a completed sweep is not resumed again")
}

// US3 judge finding 8: a sweep that did not complete (failed scan) must not
// leave its digest latched as "already swept", or the same activation (a
// rollback re-fire) could never retry it in-process.
func TestReprotect_AFailedSweepCanBeRetriedForTheSameDigest(t *testing.T) {
	fake := newFakeSecurityScanner()
	boom := errors.New("scanner unavailable")
	fake.cachedScanErrs["alpha"] = []error{boom, boom, boom}
	s, _ := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))

	s.handleBundleActivation(activation(reprotectDigestA))
	require.Empty(t, fake.cachedScans())
	require.Empty(t, reprotectMarker(t, s).FullDigest)

	s.handleBundleActivation(activation(reprotectDigestA)) // errors queue is drained: succeeds now

	assert.Equal(t, []string{"alpha@" + reprotectDigestA}, fake.cachedScans())
	assert.Equal(t, reprotectDigestA, reprotectMarker(t, s).FullDigest)
}

// T362 shutdown, end to end (US3 judge gap): Server.Shutdown must join a
// running sweep BEFORE runtime.Close begins, so the sweep never writes to a
// closing store. While the sweep is held inside its first scan, Shutdown cannot
// have reached runtime.Close; once the sweep ends, Shutdown completes and no
// further scan or marker write happened.
func TestReprotect_ServerShutdownJoinsARunningSweepBeforeTheRuntimeCloses(t *testing.T) {
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

	swept := make(chan struct{})
	go func() { defer close(swept); s.handleBundleActivation(activation(reprotectDigestA)) }()
	awaitClosed(t, inFirst)

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- s.Shutdown() }()

	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned (err=%v) while a sweep was still running: runtime.Close ran before the sweep was joined", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	awaitClosed(t, swept)
	select {
	case err := <-shutdownDone:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("Shutdown did not complete once the sweep ended")
	}

	assert.Equal(t, []string{"alpha@" + reprotectDigestA}, fake.cachedScans(), "no scan starts after Shutdown began")
	s.handleBundleActivation(activation(reprotectDigestB))
	assert.Len(t, fake.cachedScans(), 1, "an activation after Shutdown is ignored")
}

// US3 judge finding 5 / T366: the settle of a re-protection job must not start
// a review-definition capture either; it scanned stored contracts, and the
// capture follows up on a LIVE admission scan. The control proves the same
// server and job shape DO capture when the job is an ordinary scan.
func TestReprotect_SettleOfACachedJobNeverStartsAReviewCapture(t *testing.T) {
	fake := newFakeSecurityScanner()
	s, _ := newReprotectServer(t, fake, nil,
		&config.ServerConfig{Name: "gate", TrustMode: string(config.TrustModeScan), Enabled: true, Quarantined: true})
	captured := make(chan string, 4)
	s.reviewCaptureFn = func(_ context.Context, name string) error { captured <- name; return nil }

	started := time.Now().Add(-time.Hour)
	seed := func(id string, digest string) {
		job := &scanner.ScanJob{
			ID: id, ServerName: "gate", Status: scanner.ScanJobStatusCompleted, ScanPass: scanner.ScanPassSecurityScan,
			StartedAt: started, CompletedAt: started.Add(time.Second),
			ScannerStatuses: []scanner.ScannerJobStatus{{ScannerID: "tpa", Status: scanner.ScanJobStatusCompleted}},
			ScanContext:     &scanner.ScanContext{SourceMethod: "tool_definitions_only", ToolsExported: 3, BundleDigest: digest},
		}
		require.NoError(t, s.runtime.StorageManager().SaveScanJob(job))
		fake.setScanResult("gate", job, &scanner.ScanSummary{Status: "clean"})
	}

	seed("cached", reprotectDigestA)
	s.maybeCaptureReviewDefinitions("gate", "completed")
	select {
	case name := <-captured:
		t.Fatalf("a re-protection job's settle started a capture for %s", name)
	case <-time.After(200 * time.Millisecond):
	}

	seed("live", "")
	s.maybeCaptureReviewDefinitions("gate", "completed")
	select {
	case name := <-captured:
		assert.Equal(t, "gate", name)
	case <-time.After(5 * time.Second):
		t.Fatal("control: an ordinary completed scan of a quarantined server must capture")
	}
}

// Sol round 1, finding 2: Shutdown joins the sweep, so the sweep must join the
// scan worker it started. A worker still running when runtime.Close begins
// would write jobs and reports into a closing store.
func TestReprotect_StopJoinsTheScanWorkerTheSweepStarted(t *testing.T) {
	fake := newFakeSecurityScanner()
	s, _ := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))
	s.infoScanSettleTimeout = time.Minute // the stop, not the settle timeout, ends the wait

	finishCh := make(chan func(), 1)
	fake.onCachedJob = func(_ string, finish func()) { finishCh <- finish }

	swept := make(chan struct{})
	go func() { defer close(swept); s.handleBundleActivation(activation(reprotectDigestA)) }()
	var finish func()
	select {
	case finish = <-finishCh:
	case <-time.After(10 * time.Second):
		t.Fatal("the cached scan never started")
	}

	stopped := make(chan struct{})
	go func() { defer close(stopped); s.stopBundleReprotect() }()
	select {
	case <-stopped:
		t.Fatal("stopBundleReprotect returned while the scan worker was still running")
	case <-time.After(300 * time.Millisecond):
	}

	finish() // the worker's callbacks return
	awaitClosed(t, stopped)
	awaitClosed(t, swept)
	assert.Empty(t, reprotectMarker(t, s).FullDigest, "an interrupted sweep writes no completion marker")
}

// Sol round 1, finding 3: a job that is still running when the settle timeout
// fires has not produced a verdict. It must not count as scanned, must not
// feed the hit count and must not let the sweep write its completion marker -
// a process exit before the job finished would otherwise skip the re-protection
// at the next start.
func TestReprotect_SettleTimeoutIsAFailureAndWritesNoMarker(t *testing.T) {
	fake := newFakeSecurityScanner()
	// The summary is still the in-flight one when the timeout (30ms) fires.
	fake.cachedScanResult["alpha"] = &scanner.ScanSummary{Status: "scanning"}
	fake.onCachedJob = func(_ string, finish func()) { time.AfterFunc(150*time.Millisecond, finish) }
	s, probe := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))

	start := time.Now()
	s.handleBundleActivation(activation(reprotectDigestA))

	assert.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond, "the sweep joins the job it started before it ends")
	last := probe.lastStatus(t)
	assert.Equal(t, scanner.ReprotectStateFailed, last.State)
	assert.Zero(t, last.ServersScanned, "a job that did not settle is not a scanned server")
	assert.Empty(t, reprotectMarker(t, s).FullDigest, "no completion marker for a sweep with an unsettled job")
	assert.Len(t, fake.cachedScanAttempts(), 1, "a timeout is not retried into a duplicate job")
}

// Sol round 1, finding 4: a retry of a partly completed sweep (here: suspended
// by the kill switch after the first server) resumes the unfinished servers;
// it does not create a second job and a second activity/hit contribution for a
// server that already settled under the same digest.
func TestReprotect_ResumedSweepDoesNotRescanServersThatAlreadySettled(t *testing.T) {
	fake := newFakeSecurityScanner()
	on := true
	s, _ := newReprotectServer(t, fake, &config.SecurityConfig{AutoBaselineScan: &on},
		enabledServer("alpha", config.TrustModeManual),
		enabledServer("bravo", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))
	seedToolRecords(t, s, "bravo", approvedOnly("b1"))
	s.bundleReprotect.activeDigest = func() string { return reprotectDigestA }
	fake.onCachedScan = func(server string) {
		if server == "alpha" {
			on = false // automatic scanning is switched off before bravo
		}
	}

	s.handleBundleActivation(activation(reprotectDigestA))
	require.Equal(t, []string{"alpha@" + reprotectDigestA}, fake.cachedScans(), "precondition: only alpha ran before the switch went off")

	on = true
	s.resumeSuspendedReprotect()

	assert.Equal(t, []string{"alpha@" + reprotectDigestA, "bravo@" + reprotectDigestA}, fake.cachedScans(),
		"the resumed sweep scans bravo only; alpha already has its job for this digest")
	assert.Equal(t, reprotectDigestA, reprotectMarker(t, s).FullDigest)
}

func TestReprotect_RetryAfterAFailureDoesNotRescanTheServersThatSucceeded(t *testing.T) {
	boom := errors.New("scanner unavailable")
	fake := newFakeSecurityScanner()
	fake.cachedScanErrs["bravo"] = []error{boom, boom, boom}
	fake.cachedScanResult["alpha"] = &scanner.ScanSummary{Status: "dangerous"}
	s, probe := newReprotectServer(t, fake, nil,
		enabledServer("alpha", config.TrustModeManual),
		enabledServer("bravo", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))
	seedToolRecords(t, s, "bravo", approvedOnly("b1"))

	s.handleBundleActivation(activation(reprotectDigestA))
	require.Equal(t, []string{"alpha@" + reprotectDigestA}, fake.cachedScans())
	require.Equal(t, 1, probe.hitCount())

	s.handleBundleActivation(activation(reprotectDigestA)) // bravo's error queue is drained

	assert.Equal(t, []string{"alpha@" + reprotectDigestA, "bravo@" + reprotectDigestA}, fake.cachedScans(), "alpha is not scanned a second time")
	assert.Equal(t, 1, probe.hitCount(), "alpha's hit is counted once, not once per attempt")
	last := probe.lastStatus(t)
	assert.Equal(t, 2, last.ServersScanned, "the completed sweep still reports every server it covered")
	assert.Equal(t, 1, last.ServersWithHits)
	assert.Equal(t, reprotectDigestA, reprotectMarker(t, s).FullDigest)

	// A later activation of the same digest after another bundle is a new
	// activation (a rollback), so the remembered scans must not leak into it.
	s.handleBundleActivation(activation(reprotectDigestB))
	s.handleBundleActivation(activation(reprotectDigestA))
	assert.Equal(t, []string{
		"alpha@" + reprotectDigestA, "bravo@" + reprotectDigestA,
		"alpha@" + reprotectDigestB, "bravo@" + reprotectDigestB,
		"alpha@" + reprotectDigestA, "bravo@" + reprotectDigestA,
	}, fake.cachedScans())
}

// Sol round 1, finding 5: the sweep read auto_baseline_scan, then queued behind
// another informational scan on infoScanRunMu. The switch is read live at every
// decision point and the last one is when the lock is acquired.
func TestReprotect_KillSwitchIsRecheckedAfterWaitingForTheScanQueue(t *testing.T) {
	fake := newFakeSecurityScanner()
	s, probe := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))

	s.infoScanRunMu.Lock() // another informational scan holds the queue
	swept := make(chan struct{})
	go func() { defer close(swept); s.handleBundleActivation(activation(reprotectDigestA)) }()
	require.Eventually(t, func() bool {
		probe.mu.Lock()
		defer probe.mu.Unlock()
		return len(probe.statuses) > 0
	}, 5*time.Second, time.Millisecond, "the sweep must be under way")
	time.Sleep(150 * time.Millisecond) // past its own checks, blocked on the queue

	t.Setenv(config.EnvAutoBaselineScan, "false") // the operator switches automatic scanning off
	s.infoScanRunMu.Unlock()
	awaitClosed(t, swept)

	assert.Empty(t, fake.cachedScanAttempts(), "a scan switched off while queued must not start")
	assert.Equal(t, scanner.ReprotectStateSuspended, probe.lastStatus(t).State)
	assert.Empty(t, reprotectMarker(t, s).FullDigest)
}

// Sol round 1, finding 6: the startup catch-up reads the digest, then pauses.
// A real activation (sequenced) of another bundle lands meanwhile. The catch-up
// carries no sequence, so it used to slip past the staleness check, become the
// "latest" activation and abandon the newer bundle's sweep.
func TestReprotect_StaleCatchUpDoesNotSupersedeANewerActivation(t *testing.T) {
	fake := newFakeSecurityScanner()
	s, _ := newReprotectServer(t, fake, nil, enabledServer("alpha", config.TrustModeManual))
	seedToolRecords(t, s, "alpha", approvedOnly("a1"))
	s.infoScanSweepDelay = 0

	var once sync.Once
	s.bundleReprotect.activeDigest = func() string {
		once.Do(func() {
			newer := activation(reprotectDigestB)
			newer.Seq = 5
			s.handleBundleActivation(newer) // bundle B activates while the catch-up is paused
		})
		return reprotectDigestA // what the catch-up read before B activated
	}

	s.runBundleReprotectCatchUp(context.Background())

	assert.Equal(t, []string{"alpha@" + reprotectDigestB}, fake.cachedScans(),
		"the stale catch-up must not start a sweep labelled with the superseded digest")
	assert.Equal(t, reprotectDigestB, reprotectMarker(t, s).FullDigest, "the marker records the bundle that serves")
	s.bundleReprotect.mu.Lock()
	defer s.bundleReprotect.mu.Unlock()
	assert.Equal(t, reprotectDigestB, s.bundleReprotect.latest)
}

// Sol round 1, wiring finding: the startup catch-up must run wherever a Runtime
// exists, stdio included. There is no scanner Service on stdio, so only leg (i)
// (the held-evidence refresh) runs and leg (ii) stays inert.
func TestReprotect_StartupCatchUpRunsOnTheStdioTransport(t *testing.T) {
	// No listen address: stdio. The held server is part of the configuration so
	// startup reconciliation keeps it (a quarantined, disabled one: leg (i)
	// covers every configured server with held records).
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	dataDir := filepath.Join(t.TempDir(), "data")
	require.NoError(t, os.MkdirAll(dataDir, 0700))
	quarantineOn := true
	srv, err := NewServer(&config.Config{
		DataDir:           dataDir,
		APIKey:            "test-api-key-stdio-catchup",
		ToolResponseLimit: 10000,
		QuarantineEnabled: &quarantineOn,
		Servers:           []*config.ServerConfig{{Name: "held", Quarantined: true, Protocol: "stdio", Command: "true"}},
	}, zap.NewNop())
	require.NoError(t, err)
	defer func() { _ = srv.Shutdown() }()
	require.Nil(t, srv.securityScannerSvc(), "precondition: stdio has no scanner service")
	srv.infoScanSweepDelay = 0
	srv.bundleReprotect.activeDigest = func() string { return reprotectDigestA }
	refreshed := make(chan string, 4)
	srv.bundleReprotect.refreshHeld = func(server string) error {
		refreshed <- server
		return nil
	}
	sm := srv.runtime.StorageManager()
	require.NoError(t, sm.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "held", ToolName: "h1", Status: storage.ToolApprovalStatusPending,
		CurrentHash: "h", CurrentDescription: "Does h1.", CurrentSchema: `{"type":"object"}`,
	}))

	// An EOF on stdin ends the stdio transport right after its startup.
	r, w, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, w.Close())
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin; _ = r.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	go func() { defer close(started); _ = srv.Start(ctx) }()

	select {
	case name := <-refreshed:
		assert.Equal(t, "held", name, "leg (i) refreshes the held evidence at startup on stdio")
	case <-time.After(15 * time.Second):
		t.Fatal("the startup catch-up never ran on the stdio transport")
	}
	cancel()
	awaitClosed(t, started)
	assert.Empty(t, reprotectMarker(t, srv).FullDigest, "leg (ii) is inert without a scanner: no completion marker")
}

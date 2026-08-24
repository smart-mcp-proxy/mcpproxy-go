package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// newInformationalTestServer builds a Server whose config + storage carry the
// given servers and whose securityScanner is the supplied fake. The known-server
// set is left EMPTY on purpose, so every configured server looks like a new
// admission to maybeStartInformationalScans (production seeds it from the
// startup config in NewServerWithConfigPath).
func newInformationalTestServer(t *testing.T, fake securityScannerService, sec *config.SecurityConfig, servers ...*config.ServerConfig) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Servers = servers
	cfg.Security = sec
	rt, err := runtime.New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	for _, sc := range servers {
		if sc != nil {
			require.NoError(t, rt.StorageManager().SaveUpstreamServer(sc))
		}
	}
	return &Server{
		logger:                zap.NewNop(),
		runtime:               rt,
		securityScanner:       fake,
		admissionScanKicked:   make(map[string]bool),
		infoScanKnown:         make(map[string]bool),
		infoScanQueued:        make(map[string]bool),
		infoScanSettleTimeout: 2 * time.Second,
	}
}

func enabledServer(name string, mode config.TrustMode) *config.ServerConfig {
	return &config.ServerConfig{Name: name, TrustMode: string(mode), Enabled: true}
}

func waitForStartedScans(t *testing.T, fake *fakeSecurityScanner, want []string) {
	t.Helper()
	require.Eventually(t, func() bool { return len(fake.startedScans()) == len(want) }, 3*time.Second, 5*time.Millisecond)
	assert.ElementsMatch(t, want, fake.startedScans())
}

// (a) A brand-new MANUAL-trust server — the default trust mode, which the
// spec-086 admission gate never touches — must get one informational scan.
func TestInformationalAdmissionScan_ManualTrustNewServer(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.scanResult["srv"] = &scanner.ScanSummary{Status: "clean"}
	s := newInformationalTestServer(t, fake, nil, enabledServer("srv", config.TrustModeManual))

	s.maybeStartInformationalScans(context.Background())
	waitForStartedScans(t, fake, []string{"srv"})

	// A second servers.changed must neither re-scan nor treat it as new again.
	s.maybeStartInformationalScans(context.Background())
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, []string{"srv"}, fake.startedScans())

	// It must not have been approved or unquarantined: informational only.
	assert.Empty(t, fake.approvedServers())
}

// A server that was already configured at process start is the sweep's job, not
// the admission path's — otherwise every restart would re-scan the whole set.
func TestInformationalAdmissionScan_PreexistingServerNotRescanned(t *testing.T) {
	fake := newFakeSecurityScanner()
	pre := enabledServer("old", config.TrustModeManual)
	s := newInformationalTestServer(t, fake, nil, pre)
	s.seedKnownServers([]*config.ServerConfig{pre})

	s.maybeStartInformationalScans(context.Background())
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, fake.startedScans())
}

// An already-scanned server is never re-scanned by the informational path.
func TestInformationalAdmissionScan_AlreadyScannedSkipped(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.summaries["srv"] = &scanner.ScanSummary{Status: "warnings"}
	s := newInformationalTestServer(t, fake, nil, enabledServer("srv", config.TrustModeAuto))

	s.maybeStartInformationalScans(context.Background())
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, fake.startedScans())
}

// (b) The scan-mode gating path is unchanged: it still kicks exactly one scan
// for a quarantined, never-scanned scan-mode server, and the informational path
// deliberately leaves that server alone so it is never scanned twice.
func TestInformationalScan_ScanModeAdmissionPathUnchanged(t *testing.T) {
	t.Run("informational path skips the server the gating path owns", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		gated := &config.ServerConfig{
			Name:        "gated",
			TrustMode:   string(config.TrustModeScan),
			Quarantined: true,
			Enabled:     true,
		}
		s := newInformationalTestServer(t, fake, nil, gated)

		s.maybeStartInformationalScans(context.Background())
		time.Sleep(50 * time.Millisecond)
		assert.Empty(t, fake.startedScans(), "the scan-mode admission gate owns this server")

		// The gating path itself still behaves exactly as before.
		s.maybeStartAdmissionScans(context.Background())
		waitForStartedScans(t, fake, []string{"gated"})
	})

	t.Run("gating path first: informational path does not double-scan", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		fake.scanResult["gated"] = &scanner.ScanSummary{Status: "clean"}
		gated := &config.ServerConfig{
			Name:        "gated",
			TrustMode:   string(config.TrustModeScan),
			Quarantined: true,
			Enabled:     true,
		}
		s := newInformationalTestServer(t, fake, nil, gated)

		s.maybeStartAdmissionScans(context.Background())
		waitForStartedScans(t, fake, []string{"gated"})

		s.maybeStartInformationalScans(context.Background())
		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, []string{"gated"}, fake.startedScans())
	})

	// EVERY scan-mode server belongs to the gating path, in every quarantine
	// state and with or without an approval baseline. Both of the settle
	// handler's gates are mutable while a scan is in flight and it re-reads them
	// at settle time: an operator quarantine landing mid-scan, or a POST
	// .../reject (RejectServer deletes the integrity baseline) landing mid-scan,
	// would each let a clean informational verdict unquarantine the server.
	for _, tc := range []struct {
		name        string
		quarantined bool
		hasBaseline bool
	}{
		{"unquarantined, no baseline", false, false},
		{"unquarantined, has baseline", false, true},
		{"quarantined, has baseline (re-quarantine)", true, true},
	} {
		t.Run("scan-mode is never informational: "+tc.name, func(t *testing.T) {
			fake := newFakeSecurityScanner()
			fake.scanResult["srv"] = &scanner.ScanSummary{Status: "clean"}
			fake.hasBaseline["srv"] = tc.hasBaseline
			srv := enabledServer("srv", config.TrustModeScan)
			srv.Quarantined = tc.quarantined
			s := newInformationalTestServer(t, fake, nil, srv)

			s.maybeStartInformationalScans(context.Background())
			s.runBaselineSweep(context.Background())
			time.Sleep(50 * time.Millisecond)

			assert.Empty(t, fake.startScanAttempts(),
				"an informational verdict must never be able to reach the settle handler")
			assert.Empty(t, fake.approvedServers())
		})
	}
}

// A new server whose scan fails to START must stay retryable: the admission path
// records it as "known" before attempting the scan, so releasing the claim has
// to un-know it too — otherwise no later servers.changed could ever pick it up
// and (once the one-shot sweep marker is burned) nothing would scan it again.
func TestInformationalScan_FailedStartRetriesOnNextServersChanged(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.startScanErr = errors.New("server unreachable")
	s := newInformationalTestServer(t, fake, nil, enabledServer("srv", config.TrustModeManual))

	s.maybeStartInformationalScans(context.Background())
	require.Eventually(t, func() bool {
		s.infoScanMu.Lock()
		defer s.infoScanMu.Unlock()
		return !s.infoScanQueued["srv"] && !s.infoScanKnown["srv"]
	}, 2*time.Second, 5*time.Millisecond)

	fake.mu.Lock()
	fake.startScanErr = nil
	fake.mu.Unlock()

	// The next servers.changed sees it as new again and retries — no sweep needed.
	s.maybeStartInformationalScans(context.Background())
	waitForStartedScans(t, fake, []string{"srv"})
}

// The retry above must be BOUNDED. StartScan fails outright for a server it
// cannot connect to, and that path costs ~60s inside StartScan while holding the
// serialization mutex — retrying it on every servers.changed for a permanently
// broken upstream would stall the queue and respawn the process endlessly.
func TestInformationalScan_RetriesAreCapped(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.startScanErr = errors.New("server is disconnected")
	s := newInformationalTestServer(t, fake, nil, enabledServer("srv", config.TrustModeManual))

	for i := 0; i < maxInformationalScanAttempts+2; i++ {
		s.maybeStartInformationalScans(context.Background())
		require.Eventually(t, func() bool {
			s.infoScanMu.Lock()
			defer s.infoScanMu.Unlock()
			return !s.infoScanQueued["srv"]
		}, 2*time.Second, 5*time.Millisecond)
	}

	// Every attempt reached StartScan (each returns the error), but no more than
	// the cap, and the server is retired rather than re-queued forever.
	assert.Len(t, fake.startScanAttempts(), maxInformationalScanAttempts,
		"retries must stop at the cap")

	s.infoScanMu.Lock()
	retired := s.infoScanKnown["srv"]
	s.infoScanMu.Unlock()
	assert.True(t, retired, "a retired server must stay marked known")
}

// An unreadable store must never be mistaken for "nothing to sweep". Both
// storage reads in runBaselineSweep (the marker, then the inventory) fail closed
// on the same invariant: no scans start and the one-shot marker is left unset so
// the next start retries. A closed BBolt handle fails the marker read first, so
// this exercises that guard; the inventory read is guarded identically, which
// matters because listStoredServers collapses a read error into an empty slice —
// the sweep therefore calls ListUpstreamServers directly.
func TestBaselineSweep_UnreadableStoreNeverBurnsMarker(t *testing.T) {
	fake := newFakeSecurityScanner()
	s := newInformationalTestServer(t, fake, nil, enabledServer("a", config.TrustModeManual))

	require.NoError(t, s.runtime.StorageManager().Close())

	s.runBaselineSweep(context.Background())

	assert.Empty(t, fake.startedScans(), "an unreadable store must not scan anything")
}

// (d) Disabled servers are skipped by both paths — a scan would have to start
// the server to export its tool definitions.
func TestInformationalScan_DisabledServersSkipped(t *testing.T) {
	fake := newFakeSecurityScanner()
	disabled := &config.ServerConfig{Name: "off", TrustMode: string(config.TrustModeManual), Enabled: false}
	s := newInformationalTestServer(t, fake, nil, disabled)

	s.maybeStartInformationalScans(context.Background())
	s.runBaselineSweep(context.Background())
	time.Sleep(50 * time.Millisecond)

	assert.Empty(t, fake.startedScans())
	// The sweep still completes (nothing to do) and records its marker.
	state, err := s.runtime.StorageManager().LoadBaselineSweepState()
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Equal(t, 0, state.ServersScanned)
}

// A disabled server must not be STRANDED by being skipped: because a skipped
// server is neither scanned nor recorded as "known", enabling it later is what
// admits it. Before this was fixed the skip still marked the server known, so
// the admission path treated the enable as "already seen" and the one-shot,
// marker-gated sweep never came back — its badge read "not scanned" forever.
func TestInformationalScan_DisabledServerScannedOnceEnabled(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.scanResult["srv"] = &scanner.ScanSummary{Status: "clean"}
	sc := &config.ServerConfig{Name: "srv", TrustMode: string(config.TrustModeManual), Enabled: false}
	s := newInformationalTestServer(t, fake, nil, sc)
	// Present at process start AND disabled: the seed must not claim it either.
	s.seedKnownServers([]*config.ServerConfig{sc})

	s.maybeStartInformationalScans(context.Background())
	time.Sleep(50 * time.Millisecond)
	require.Empty(t, fake.startedScans(), "a disabled server is never scanned")

	// The operator enables it; servers.changed fires again.
	sc.Enabled = true
	require.NoError(t, s.runtime.StorageManager().SaveUpstreamServer(sc))
	s.maybeStartInformationalScans(context.Background())
	waitForStartedScans(t, fake, []string{"srv"})

	// Still exactly one scan, and still informational.
	s.maybeStartInformationalScans(context.Background())
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, []string{"srv"}, fake.startedScans())
	assert.Empty(t, fake.approvedServers())
}

// (c) The sweep runs once; the persisted marker prevents any re-run, even for a
// fresh process whose in-memory dedupe maps are empty.
func TestBaselineSweep_RunsOnceThenMarkerBlocksRerun(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.scanResult["a"] = &scanner.ScanSummary{
		Status:        "warnings",
		FindingCounts: &scanner.FindingCounts{Warning: 2, Total: 2},
	}
	fake.scanResult["b"] = &scanner.ScanSummary{
		Status:        "clean",
		FindingCounts: &scanner.FindingCounts{Total: 0},
	}
	a := enabledServer("a", config.TrustModeManual)
	b := enabledServer("b", config.TrustModeAuto)
	s := newInformationalTestServer(t, fake, nil, a, b)
	s.seedKnownServers([]*config.ServerConfig{a, b})

	s.runBaselineSweep(context.Background())
	assert.ElementsMatch(t, []string{"a", "b"}, fake.startedScans())

	state, err := s.runtime.StorageManager().LoadBaselineSweepState()
	require.NoError(t, err)
	require.NotNil(t, state, "the sweep must persist its one-shot marker")
	assert.Equal(t, 2, state.ServersScanned)
	assert.Equal(t, 2, state.Findings)

	// Simulate a restart: fresh in-memory state, no scan summaries yet. Only the
	// persisted marker can stop the sweep now.
	restarted := &Server{
		logger:                zap.NewNop(),
		runtime:               s.runtime,
		securityScanner:       fake,
		admissionScanKicked:   make(map[string]bool),
		infoScanKnown:         make(map[string]bool),
		infoScanQueued:        make(map[string]bool),
		infoScanSettleTimeout: time.Second,
	}
	fake.mu.Lock()
	fake.summaries = map[string]*scanner.ScanSummary{}
	fake.mu.Unlock()

	restarted.runBaselineSweep(context.Background())
	assert.ElementsMatch(t, []string{"a", "b"}, fake.startedScans(), "the marker must prevent a second sweep")
}

// A sweep cancelled by shutdown must NOT persist the marker, so the next start
// resumes it.
func TestBaselineSweep_CancelledDoesNotPersistMarker(t *testing.T) {
	fake := newFakeSecurityScanner()
	s := newInformationalTestServer(t, fake, nil, enabledServer("a", config.TrustModeManual))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.runBaselineSweep(ctx)

	assert.Empty(t, fake.startedScans())
	state, err := s.runtime.StorageManager().LoadBaselineSweepState()
	require.NoError(t, err)
	assert.Nil(t, state)
}

// Cancellation is shutdown, not a scan failure. runInformationalScan must report
// it as a context error so BOTH callers route it to the uncounted unclaim rather
// than spending one of the server's bounded retries on a scan that never ran.
func TestInformationalScan_CancellationIsNotAFailedAttempt(t *testing.T) {
	fake := newFakeSecurityScanner()
	s := newInformationalTestServer(t, fake, nil, enabledServer("srv", config.TrustModeManual))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	n, err := s.runInformationalScan(ctx, "srv")
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, n)
	assert.Empty(t, fake.startScanAttempts(), "a cancelled scan must never reach StartScan")

	// The two release paths must differ exactly in whether they charge an attempt.
	s.unclaimInformationalScan("srv")
	s.infoScanMu.Lock()
	afterUnclaim := s.infoScanAttempts["srv"]
	s.infoScanMu.Unlock()
	assert.Zero(t, afterUnclaim, "unclaim must not charge an attempt")

	s.releaseInformationalScan("srv")
	s.infoScanMu.Lock()
	afterRelease := s.infoScanAttempts["srv"]
	s.infoScanMu.Unlock()
	assert.Equal(t, 1, afterRelease, "release charges exactly one attempt")
}

// A sweep where every candidate failed (e.g. servers still connecting) must not
// burn the one-shot marker — the next start has to retry.
func TestBaselineSweep_AllScansFailedKeepsMarkerUnset(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.startScanErr = errors.New("server is disconnected")
	s := newInformationalTestServer(t, fake, nil, enabledServer("a", config.TrustModeManual))

	s.runBaselineSweep(context.Background())

	state, err := s.runtime.StorageManager().LoadBaselineSweepState()
	require.NoError(t, err)
	assert.Nil(t, state, "a sweep that scanned nothing must stay retryable")
}

// A PARTIALLY failing sweep must also stay retryable. Burning the marker as
// soon as one server scanned stranded the rest: the marker outlives the process,
// so a server that was merely still connecting would never be swept again.
func TestBaselineSweep_PartialFailureKeepsMarkerUnset(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.startScanErrByServer = map[string]error{"b": errors.New("server is disconnected")}
	fake.scanResult["a"] = &scanner.ScanSummary{Status: "clean"}
	s := newInformationalTestServer(t, fake, nil,
		enabledServer("a", config.TrustModeManual),
		enabledServer("b", config.TrustModeManual))

	s.runBaselineSweep(context.Background())
	require.Equal(t, []string{"a"}, fake.startedScans(), "a scanned, b failed to start")

	state, err := s.runtime.StorageManager().LoadBaselineSweepState()
	require.NoError(t, err)
	assert.Nil(t, state, "a sweep with any failed candidate must stay retryable")

	// Once b's transient failure clears, the retried sweep finishes and marks.
	fake.mu.Lock()
	fake.startScanErrByServer = nil
	fake.mu.Unlock()
	fake.scanResult["b"] = &scanner.ScanSummary{Status: "clean"}

	s.runBaselineSweep(context.Background())
	assert.ElementsMatch(t, []string{"a", "b"}, fake.startedScans(),
		"the retry scans only the server that still has no summary")

	state, err = s.runtime.StorageManager().LoadBaselineSweepState()
	require.NoError(t, err)
	require.NotNil(t, state, "a sweep with no failures marks itself done")
}

// A scan that fails to start releases its claim so a later servers.changed can
// retry it.
func TestInformationalScan_FailedStartIsRetryable(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.startScanErr = errors.New("server unreachable")
	s := newInformationalTestServer(t, fake, nil, enabledServer("srv", config.TrustModeManual))

	s.maybeStartInformationalScans(context.Background())
	require.Eventually(t, func() bool {
		s.infoScanMu.Lock()
		defer s.infoScanMu.Unlock()
		return !s.infoScanQueued["srv"]
	}, 2*time.Second, 5*time.Millisecond)

	fake.mu.Lock()
	fake.startScanErr = nil
	fake.mu.Unlock()

	// The sweep is the other retry route (it never consulted the known-set).
	s.runBaselineSweep(context.Background())
	assert.Equal(t, []string{"srv"}, fake.startedScans())
}

// (e) The security.auto_baseline_scan kill switch stops BOTH informational
// paths, while leaving the trust_mode:"scan" admission gate untouched.
func TestInformationalScan_KillSwitch(t *testing.T) {
	disabled := false
	sec := &config.SecurityConfig{AutoBaselineScan: &disabled}

	fake := newFakeSecurityScanner()
	gated := &config.ServerConfig{
		Name:        "gated",
		TrustMode:   string(config.TrustModeScan),
		Quarantined: true,
		Enabled:     true,
	}
	s := newInformationalTestServer(t, fake, sec, enabledServer("srv", config.TrustModeManual), gated)

	s.maybeStartInformationalScans(context.Background())
	s.runBaselineSweep(context.Background())
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, fake.startedScans(), "the kill switch must suppress every automatic informational scan")

	state, err := s.runtime.StorageManager().LoadBaselineSweepState()
	require.NoError(t, err)
	assert.Nil(t, state, "a suppressed sweep must not burn the one-shot marker")

	// The gating path is a separate contract and keeps working.
	s.maybeStartAdmissionScans(context.Background())
	waitForStartedScans(t, fake, []string{"gated"})
}

// The kill switch is documented as read live "at each decision point". A queued
// scan can sit behind infoScanRunMu for minutes, so the LAST decision point —
// immediately before StartScan — has to re-read it too, or an operator who
// disables automatic scanning still watches the queue drain into their upstreams.
func TestInformationalScan_KillSwitchCheckedAtExecutionTime(t *testing.T) {
	fake := newFakeSecurityScanner()
	s := newInformationalTestServer(t, fake, nil, enabledServer("srv", config.TrustModeManual))

	// Flip the switch after the scan would have been queued. The env override is
	// resolved inside IsAutoBaselineScanEnabled, so it takes effect immediately.
	t.Setenv(config.EnvAutoBaselineScan, "false")

	n, err := s.runInformationalScan(context.Background(), "srv")
	require.ErrorIs(t, err, errInformationalScansDisabled)
	assert.Zero(t, n)
	assert.Empty(t, fake.startScanAttempts(), "a disabled scan must never reach StartScan")
}

// A kill-switch skip says nothing about the server, so it must not consume one
// of its bounded retries — otherwise toggling the flag off and on would retire
// servers that never failed a scan.
func TestInformationalScan_KillSwitchSkipDoesNotConsumeRetries(t *testing.T) {
	fake := newFakeSecurityScanner()
	s := newInformationalTestServer(t, fake, nil, enabledServer("srv", config.TrustModeManual))

	t.Setenv(config.EnvAutoBaselineScan, "false")
	for i := 0; i < maxInformationalScanAttempts+2; i++ {
		s.maybeStartInformationalScans(context.Background())
		require.Eventually(t, func() bool {
			s.infoScanMu.Lock()
			defer s.infoScanMu.Unlock()
			return !s.infoScanQueued["srv"] && !s.infoScanKnown["srv"]
		}, 2*time.Second, 5*time.Millisecond)
	}

	s.infoScanMu.Lock()
	attempts := s.infoScanAttempts["srv"]
	s.infoScanMu.Unlock()
	assert.Zero(t, attempts, "kill-switch skips are not failed attempts")

	// Re-enabling must still scan it.
	t.Setenv(config.EnvAutoBaselineScan, "true")
	fake.scanResult["srv"] = &scanner.ScanSummary{Status: "clean"}
	s.maybeStartInformationalScans(context.Background())
	waitForStartedScans(t, fake, []string{"srv"})
}

// Disabling automatic scanning DURING the sweep's startup delay must stop it,
// and must leave the one-shot marker unset so it resumes if re-enabled.
func TestBaselineSweep_KillSwitchDuringStartupDelay(t *testing.T) {
	fake := newFakeSecurityScanner()
	s := newInformationalTestServer(t, fake, nil, enabledServer("a", config.TrustModeManual))
	s.infoScanSweepDelay = 300 * time.Millisecond

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runBaselineSweep(context.Background())
	}()

	time.Sleep(50 * time.Millisecond)
	t.Setenv(config.EnvAutoBaselineScan, "false")

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sweep did not return")
	}

	assert.Empty(t, fake.startScanAttempts(), "the sweep must re-read the flag after its delay")
	state, err := s.runtime.StorageManager().LoadBaselineSweepState()
	require.NoError(t, err)
	assert.Nil(t, state, "an abandoned sweep must not burn the one-shot marker")
}

func TestScanModeAdmissionOwns(t *testing.T) {
	tests := []struct {
		name        string
		sc          *config.ServerConfig
		hasBaseline bool
		want        bool
	}{
		{"nil", nil, false, false},
		// Every quarantine/baseline combination of a scan-mode server belongs to
		// the gating path: both of the settle handler's gates are mutable while a
		// scan is in flight, so no static snapshot of them is safe to scan on.
		{"scan+quarantined+no baseline", &config.ServerConfig{TrustMode: "scan", Quarantined: true}, false, true},
		{"scan+quarantined+baseline (re-quarantine, reject can delete it)", &config.ServerConfig{TrustMode: "scan", Quarantined: true}, true, true},
		{"scan+not quarantined+no baseline", &config.ServerConfig{TrustMode: "scan"}, false, true},
		{"scan+not quarantined+baseline", &config.ServerConfig{TrustMode: "scan"}, true, true},
		{"manual+quarantined", &config.ServerConfig{TrustMode: "manual", Quarantined: true}, false, false},
		{"auto+quarantined", &config.ServerConfig{TrustMode: "auto", Quarantined: true}, false, false},
		{"empty trust mode (manual) + quarantined", &config.ServerConfig{Quarantined: true}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scanModeAdmissionOwns(tt.sc))
		})
	}
}

func TestIsTerminalScanStatus(t *testing.T) {
	for _, status := range []string{"clean", "warnings", "dangerous", "failed"} {
		assert.True(t, isTerminalScanStatus(status), status)
	}
	for _, status := range []string{"", "scanning", "not_scanned", "queued"} {
		assert.False(t, isTerminalScanStatus(status), status)
	}
}

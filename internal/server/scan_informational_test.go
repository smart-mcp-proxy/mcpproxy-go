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

	// A scan-mode server with NO approval baseline is off-limits to the
	// informational path even while it is unquarantined. Quarantine is mutable
	// during the scan and maybeAutoApproveScanSettled re-reads it, so scanning
	// here would let an operator quarantine that lands mid-scan be silently
	// reverted by the clean settle — an informational scan causing a gating
	// state change.
	t.Run("scan-mode without approval baseline is never informational, quarantined or not", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		fake.scanResult["srv"] = &scanner.ScanSummary{Status: "clean"}
		s := newInformationalTestServer(t, fake, nil, enabledServer("srv", config.TrustModeScan))

		s.maybeStartInformationalScans(context.Background())
		s.runBaselineSweep(context.Background())
		time.Sleep(50 * time.Millisecond)

		assert.Empty(t, fake.startedScans(), "the settle handler could still act on this server")
		assert.Empty(t, fake.approvedServers())
	})

	// Once a server HAS an approval baseline the settle handler bails on it
	// unconditionally, so it is safe to give it an informational badge.
	t.Run("scan-mode WITH approval baseline is informational", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		fake.scanResult["srv"] = &scanner.ScanSummary{Status: "clean"}
		fake.hasBaseline["srv"] = true
		s := newInformationalTestServer(t, fake, nil, enabledServer("srv", config.TrustModeScan))

		s.maybeStartInformationalScans(context.Background())
		waitForStartedScans(t, fake, []string{"srv"})
		assert.Empty(t, fake.approvedServers())
	})
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
		{"scan+quarantined+no baseline", &config.ServerConfig{TrustMode: "scan", Quarantined: true}, false, true},
		{"scan+quarantined+baseline (re-quarantine)", &config.ServerConfig{TrustMode: "scan", Quarantined: true}, true, false},
		// Quarantine is mutable while a scan is in flight and the settle handler
		// re-reads it, so an unquarantined scan-mode server with no approval
		// baseline is STILL the gating path's — otherwise an operator quarantine
		// landing mid-scan would let the clean settle unquarantine it again.
		{"scan+not quarantined+no baseline (settle could still act)", &config.ServerConfig{TrustMode: "scan"}, false, true},
		{"scan+not quarantined+baseline (settle bails)", &config.ServerConfig{TrustMode: "scan"}, true, false},
		{"manual+quarantined", &config.ServerConfig{TrustMode: "manual", Quarantined: true}, false, false},
		{"empty trust mode (manual) + quarantined", &config.ServerConfig{Quarantined: true}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scanModeAdmissionOwns(tt.sc, tt.hasBaseline))
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

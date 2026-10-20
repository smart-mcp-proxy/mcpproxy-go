package server

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// Spec 118: debounced informational rescan after a tool-definition change.

const testRescanDebounce = 40 * time.Millisecond

func newRescanTestServer(t *testing.T, fake securityScannerService, sec *config.SecurityConfig, servers ...*config.ServerConfig) *Server {
	t.Helper()
	s := newInformationalTestServer(t, fake, sec, servers...)
	s.infoRescanDebounce = testRescanDebounce
	t.Cleanup(func() {
		s.infoScanMu.Lock()
		defer s.infoScanMu.Unlock()
		for _, pending := range s.infoRescanTimers {
			pending.timer.Stop()
		}
	})
	return s
}

func (s *Server) rescanPending(name string) bool {
	s.infoScanMu.Lock()
	defer s.infoScanMu.Unlock()
	return s.infoRescanTimers[name] != nil
}

func TestInformationalRescan_DebouncedAndCoalesced(t *testing.T) {
	fake := newFakeSecurityScanner()
	// The server was scanned before: a rescan is for exactly that case.
	fake.summaries["srv"] = &scanner.ScanSummary{Status: "clean"}
	fake.scanResult["srv"] = &scanner.ScanSummary{Status: "clean"}
	s := newRescanTestServer(t, fake, nil, enabledServer("srv", config.TrustModeManual))

	// A burst of changes inside the window coalesces into one scan, run after
	// the window rather than at the first change.
	for i := 0; i < 5; i++ {
		s.scheduleInformationalRescan("srv")
		time.Sleep(testRescanDebounce / 8)
	}
	assert.Empty(t, fake.startedScans(), "nothing runs inside the debounce window")
	waitForStartedScans(t, fake, []string{"srv"})

	time.Sleep(4 * testRescanDebounce)
	assert.Equal(t, []string{"srv"}, fake.startedScans(), "the burst was one rescan")
	assert.Empty(t, fake.approvedServers(), "informational: nothing is approved")
	assert.False(t, s.rescanPending("srv"))

	// A later change schedules a fresh rescan.
	s.scheduleInformationalRescan("srv")
	require.Eventually(t, func() bool { return len(fake.startedScans()) == 2 }, 3*time.Second, 5*time.Millisecond)
}

func TestInformationalRescan_SwitchesOff(t *testing.T) {
	cases := map[string]struct {
		sec *config.SecurityConfig
		env map[string]string
	}{
		"auto_rescan_on_change false":    {sec: &config.SecurityConfig{AutoRescanOnChange: boolPtr(false)}},
		"auto_baseline_scan false":       {sec: &config.SecurityConfig{AutoBaselineScan: boolPtr(false)}},
		"env MCPPROXY_AUTO_RESCAN off":   {env: map[string]string{config.EnvAutoRescanOnChange: "false"}},
		"env MCPPROXY_AUTO_BASELINE off": {env: map[string]string{config.EnvAutoBaselineScan: "0"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			fake := newFakeSecurityScanner()
			s := newRescanTestServer(t, fake, tc.sec, enabledServer("srv", config.TrustModeManual))

			s.scheduleInformationalRescan("srv")
			assert.False(t, s.rescanPending("srv"), "no timer is armed")
			time.Sleep(4 * testRescanDebounce)
			assert.Empty(t, fake.startScanAttempts(), "nothing runs automatically")
		})
	}

	t.Run("one switch flips off while the timer is pending", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		s := newRescanTestServer(t, fake, nil, enabledServer("srv", config.TrustModeManual))
		s.scheduleInformationalRescan("srv")
		require.True(t, s.rescanPending("srv"))
		t.Setenv(config.EnvAutoRescanOnChange, "false")
		time.Sleep(4 * testRescanDebounce)
		assert.Empty(t, fake.startScanAttempts(), "the switch is re-read when the timer fires")
	})

	t.Run("explicit on", func(t *testing.T) {
		fake := newFakeSecurityScanner()
		s := newRescanTestServer(t, fake, &config.SecurityConfig{AutoRescanOnChange: boolPtr(true), AutoBaselineScan: boolPtr(true)},
			enabledServer("srv", config.TrustModeManual))
		s.scheduleInformationalRescan("srv")
		waitForStartedScans(t, fake, []string{"srv"})
	})
}

func TestInformationalRescan_Eligibility(t *testing.T) {
	scanQuarantined := &config.ServerConfig{Name: "gated", TrustMode: string(config.TrustModeScan), Quarantined: true, Enabled: true}
	scanTrusted := &config.ServerConfig{Name: "scan-ok", TrustMode: string(config.TrustModeScan), Enabled: true}
	disabled := &config.ServerConfig{Name: "off", TrustMode: string(config.TrustModeManual), Enabled: false}
	fake := newFakeSecurityScanner()
	s := newRescanTestServer(t, fake, nil,
		enabledServer("manual", config.TrustModeManual),
		enabledServer("auto", config.TrustModeAuto),
		scanQuarantined, scanTrusted, disabled)

	for _, name := range []string{"manual", "auto", "gated", "scan-ok", "off", "ghost"} {
		s.scheduleInformationalRescan(name)
	}
	waitForStartedScans(t, fake, []string{"manual", "scan-ok"})
	time.Sleep(4 * testRescanDebounce)
	assert.ElementsMatch(t, []string{"manual", "scan-ok"}, fake.startedScans(),
		"auto trust, the quarantined scan-mode server the gate owns, disabled and unknown servers are skipped")
}

// A change that lands while this server's scan slot is taken (a manual scan, the
// admission scan, a rescan that already read the tool list) is not dropped: it
// re-arms and scans once the slot frees, without spending a failure attempt.
func TestInformationalRescan_BusyServerIsRetriedNotDropped(t *testing.T) {
	fake := newFakeSecurityScanner()
	s := newRescanTestServer(t, fake, nil, enabledServer("srv", config.TrustModeManual))

	s.infoScanMu.Lock()
	s.infoScanQueued["srv"] = true // an informational scan holds the claim
	s.infoScanMu.Unlock()

	s.scheduleInformationalRescan("srv")
	time.Sleep(4 * testRescanDebounce)
	assert.Empty(t, fake.startScanAttempts(), "must not scan on top of a claimed slot")
	assert.True(t, s.rescanPending("srv"), "re-armed while busy")

	s.infoScanMu.Lock()
	delete(s.infoScanQueued, "srv")
	s.infoScanMu.Unlock()
	waitForStartedScans(t, fake, []string{"srv"})

	s.infoScanMu.Lock()
	defer s.infoScanMu.Unlock()
	assert.Zero(t, s.infoScanAttempts["srv"], "a busy slot is not a failed attempt")
}

func TestInformationalRescan_ScanInProgressRearmsWithoutFailure(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.startScanErr = fmt.Errorf("%w for server srv", scanner.ErrScanInProgress)
	s := newRescanTestServer(t, fake, nil, enabledServer("srv", config.TrustModeManual))

	s.scheduleInformationalRescan("srv")
	require.Eventually(t, func() bool { return len(fake.startScanAttempts()) >= 2 }, 3*time.Second, 5*time.Millisecond,
		"a manual scan in the way re-arms the rescan")

	fake.mu.Lock()
	fake.startScanErr = nil
	fake.mu.Unlock()
	require.Eventually(t, func() bool { return len(fake.startedScans()) == 1 }, 3*time.Second, 5*time.Millisecond)

	s.infoScanMu.Lock()
	defer s.infoScanMu.Unlock()
	assert.Zero(t, s.infoScanAttempts["srv"])
}

func TestInformationalRescan_FailuresAreCapped(t *testing.T) {
	fake := newFakeSecurityScanner()
	fake.startScanErr = errors.New("server is disconnected")
	s := newRescanTestServer(t, fake, nil, enabledServer("srv", config.TrustModeManual))

	for i := 0; i < maxInformationalScanAttempts+3; i++ {
		s.scheduleInformationalRescan("srv")
		require.Eventually(t, func() bool {
			s.infoScanMu.Lock()
			defer s.infoScanMu.Unlock()
			return s.infoRescanTimers["srv"] == nil && !s.infoScanQueued["srv"]
		}, 2*time.Second, 5*time.Millisecond)
	}
	assert.Len(t, fake.startScanAttempts(), maxInformationalScanAttempts, "failed starts stop at the shared cap")
}

func TestInformationalRescan_RemovalCancelsPendingTimer(t *testing.T) {
	fake := newFakeSecurityScanner()
	s := newRescanTestServer(t, fake, nil, enabledServer("srv", config.TrustModeManual))

	s.scheduleInformationalRescan("srv")
	require.True(t, s.rescanPending("srv"))
	s.forgetInformationalScan("srv")
	assert.False(t, s.rescanPending("srv"))
	time.Sleep(4 * testRescanDebounce)
	assert.Empty(t, fake.startScanAttempts())
}

func TestInformationalRescan_ShutdownContextSkips(t *testing.T) {
	fake := newFakeSecurityScanner()
	s := newRescanTestServer(t, fake, nil, enabledServer("srv", config.TrustModeManual))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.serverCtx = ctx

	s.scheduleInformationalRescan("srv")
	time.Sleep(4 * testRescanDebounce)
	assert.Empty(t, fake.startScanAttempts())
}

func TestInformationalRescan_ObserverRegisteredAtConstruction(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	srv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })
	assert.GreaterOrEqual(t, srv.runtime.ToolDefinitionsObserverCount(), 1)
}

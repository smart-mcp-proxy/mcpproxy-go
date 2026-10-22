package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// Spec 101 US3 tasks B13, B16, B17, B19, B21 / T365: server construction wires
// the TPA bundle refresh lifecycle. Each test replaces one scanner hook with a
// recording seam, builds a real Server and asserts the wiring reached it.

// restoreScannerWiring puts every scanner-global the wiring touches back after
// the test, so a Server built here cannot leak into the rest of the package.
func restoreScannerWiring(t *testing.T) {
	t.Helper()
	prevSelfCheck, prevAdd, prevDoctor, prevObserver := enableActivationSelfCheck, addBundleActivationObserver, bundleDoctorWarnings, setBundleRefreshObserver
	t.Cleanup(func() {
		enableActivationSelfCheck, addBundleActivationObserver, bundleDoctorWarnings, setBundleRefreshObserver = prevSelfCheck, prevAdd, prevDoctor, prevObserver
		scanner.SetBundleRefresher(nil)
		scanner.SetBundleRefreshObserver(nil)
		scanner.SetBundleRuntime(nil, "")
		scanner.ConfigureBundle("", zap.NewNop())
	})
}

func wiringConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "tpa-wiring-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	cfg := config.DefaultConfig()
	cfg.DataDir = dir
	cfg.Listen = "127.0.0.1:0"
	cfg.APIKey = "tpa-wiring-key"
	path := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, config.SaveConfig(cfg, path))
	return cfg, path
}

func newWiringServer(t *testing.T) (*Server, string) {
	t.Helper()
	cfg, path := wiringConfig(t)
	srv, err := NewServerWithConfigPath(cfg, path, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = srv.Shutdown()
		time.Sleep(50 * time.Millisecond)
	})
	return srv, path
}

func TestTPAWiring_ActivationSelfCheckIsInstalledByProductionConstruction(t *testing.T) {
	restoreScannerWiring(t)
	var calls int
	enableActivationSelfCheck = func() { calls++ }

	newWiringServer(t)

	assert.Equal(t, 1, calls, "construction installs the activation self-check exactly once; US1 fixtures keep the nil default because only production wiring calls it")
}

type nudgeRecorder struct {
	mu   sync.Mutex
	secs []*config.SecurityConfig
}

func (n *nudgeRecorder) Nudge(sec *config.SecurityConfig) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.secs = append(n.secs, sec)
}

func (n *nudgeRecorder) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.secs)
}

func TestTPAWiring_ConfigReloadedNudgesTheRefresher(t *testing.T) {
	restoreScannerWiring(t)
	srv, path := newWiringServer(t)
	fake := &nudgeRecorder{}
	srv.setBundleNudger(fake)

	cfg := srv.runtime.Config()
	edited := *cfg
	sec := config.SecurityConfig{TPAFetchURL: "https://example.invalid/bundle"}
	if cfg.Security != nil {
		sec = *cfg.Security
		sec.TPAFetchURL = "https://example.invalid/bundle"
	}
	edited.Security = &sec
	require.NoError(t, config.SaveConfig(&edited, path))
	require.NoError(t, srv.runtime.ReloadConfiguration())

	require.Eventually(t, func() bool { return fake.count() >= 1 }, 5*time.Second, 10*time.Millisecond,
		"a config.reloaded event must reach the refresher's Nudge")
	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.NotNil(t, fake.secs[0])
	assert.Equal(t, "https://example.invalid/bundle", fake.secs[0].TPAFetchURL, "Nudge carries the LIVE security config")
}

func TestTPAWiring_ProductionRefresherIsRegisteredWithTheRuntimeAndScannerSlot(t *testing.T) {
	restoreScannerWiring(t)
	srv, _ := newWiringServer(t)

	require.NotNil(t, scanner.CurrentBundleRefresher(), "RefreshBundle consults this slot, so construction installs it")

	require.NoError(t, srv.Shutdown())
	assert.Nil(t, scanner.CurrentBundleRefresher(), "Runtime.Close stops the refresher and clears the slot")
}

func TestTPAWiring_DoctorWarningSourceIsRegistered(t *testing.T) {
	restoreScannerWiring(t)
	bundleDoctorWarnings = func(info scanner.BundleInfo, now time.Time) []string {
		return []string{"TPA signature bundle: canned doctor warning"}
	}
	srv, _ := newWiringServer(t)

	diag, err := srv.GetManagementService().Doctor(context.Background())
	require.NoError(t, err)
	assert.Contains(t, diag.RuntimeWarnings, "TPA signature bundle: canned doctor warning",
		"B16: the bundle doctor warnings feed Doctor's runtime warnings")
}

func TestTPAWiring_DoctorWarningSourceDoesNotHangOnAFreshBundleRuntime(t *testing.T) {
	restoreScannerWiring(t)
	srv, _ := newWiringServer(t)

	// A runtime nothing was ever published to: the source calls BundleStatus,
	// which installs the embedded default lazily and must not deadlock on the
	// activation mutex.
	scanner.SetBundleRuntime(nil, "")

	done := make(chan error, 1)
	go func() {
		_, err := srv.GetManagementService().Doctor(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("Doctor hung: the bundle warning source deadlocked on the bundle runtime")
	}
}

func TestTPAWiring_RefreshObserverMapsClassesToTelemetry(t *testing.T) {
	restoreScannerWiring(t)
	var observer func(scanner.RefreshOutcomeClass)
	setBundleRefreshObserver = func(fn func(scanner.RefreshOutcomeClass)) { observer = fn }
	var (
		mu      sync.Mutex
		classes []telemetry.TPABundleRefreshClass
	)
	prevRecord := recordBundleRefresh
	recordBundleRefresh = func(_ *telemetry.CounterRegistry, class telemetry.TPABundleRefreshClass) {
		mu.Lock()
		defer mu.Unlock()
		classes = append(classes, class)
	}
	t.Cleanup(func() { recordBundleRefresh = prevRecord })

	newWiringServer(t)
	require.NotNil(t, observer, "construction registers the refresh observer")

	for _, tc := range []struct {
		in   scanner.RefreshOutcomeClass
		want telemetry.TPABundleRefreshClass
	}{
		{scanner.RefreshClassActivated, telemetry.TPABundleRefreshActivated},
		{scanner.RefreshClassRejected, telemetry.TPABundleRefreshRejected},
		{scanner.RefreshClassUnavailable, telemetry.TPABundleRefreshUnavailable},
		{scanner.RefreshClassUnchanged, telemetry.TPABundleRefreshUnchanged},
	} {
		observer(tc.in)
		mu.Lock()
		got := classes[len(classes)-1]
		mu.Unlock()
		assert.Equal(t, tc.want, got, "scanner class %q", tc.in)
	}
	mu.Lock()
	assert.Len(t, classes, 4, "exactly one telemetry record per cycle class")
	mu.Unlock()
}

// The mapping above is proved with a recording seam; this one proves the real
// recorder moves the schema-v16 counters. It runs once the telemetry package
// carries them (Wave 1 group G4), and is skipped before that.
func TestTPAWiring_RefreshObserverIncrementsTheTelemetryCounters(t *testing.T) {
	if _, ok := reflect.TypeOf(telemetry.TPAScannerStats{}).FieldByName("BundleRefreshCycles"); !ok {
		t.Skip("telemetry schema v16 counters are not present in this tree yet (group G4)")
	}
	restoreScannerWiring(t)
	reg := telemetry.NewCounterRegistry()
	recordBundleRefresh(reg, telemetry.TPABundleRefreshActivated)
	recordBundleRefresh(reg, telemetry.TPABundleRefreshUnavailable)

	raw, err := json.Marshal(reg.Snapshot().TPAScannerStats())
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"bundle_refresh_cycles":2`)
	assert.Contains(t, string(raw), `"bundle_refresh_activations":1`)
	assert.Contains(t, string(raw), `"bundle_refresh_unavailable":1`)
}

func TestTPAWiring_ActivationObserverRegisteredAtConstructionAndUnsubscribedOnShutdown(t *testing.T) {
	restoreScannerWiring(t)
	var (
		mu          sync.Mutex
		observer    func(scanner.BundleActivation)
		unsubscribe int
	)
	addBundleActivationObserver = func(fn func(scanner.BundleActivation)) func() {
		mu.Lock()
		defer mu.Unlock()
		observer = fn
		return func() {
			mu.Lock()
			defer mu.Unlock()
			unsubscribe++
		}
	}
	srv, _ := newWiringServer(t)
	mu.Lock()
	require.NotNil(t, observer, "the activation observer is registered at construction, before any activation can be missed")
	require.Zero(t, unsubscribe)
	fn := observer
	mu.Unlock()

	// It is the Server's sweep handler.
	fn(activation(reprotectDigestA))
	srv.bundleReprotect.mu.Lock()
	assert.Equal(t, reprotectDigestA, srv.bundleReprotect.latest)
	srv.bundleReprotect.mu.Unlock()

	require.NoError(t, srv.Shutdown())
	mu.Lock()
	assert.Equal(t, 1, unsubscribe, "Shutdown unsubscribes before the runtime closes")
	mu.Unlock()

	// A dispatch already in flight when Shutdown ran must be ignored.
	fn(activation(reprotectDigestB))
	srv.bundleReprotect.mu.Lock()
	assert.Equal(t, reprotectDigestA, srv.bundleReprotect.latest, "no sweep starts after Shutdown")
	srv.bundleReprotect.mu.Unlock()
}

func TestTPAWiring_MapRefreshClass(t *testing.T) {
	assert.Equal(t, telemetry.TPABundleRefreshUnchanged, mapRefreshClass(scanner.RefreshOutcomeClass("something_new")),
		"an unknown class counts as unchanged rather than inventing a bucket")
	assert.True(t, strings.HasPrefix(string(mapRefreshClass(scanner.RefreshClassActivated)), "activated"))
}

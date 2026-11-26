package server

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// resetBundleRuntimeForTest gives the process-global bundle runtime a hermetic
// in-memory state and the embedded default: earlier tests in this package build
// full servers whose (since closed) storage the runtime would otherwise still
// hold. It resets again on cleanup.
func resetBundleRuntimeForTest(t *testing.T) {
	t.Helper()
	reset := func() {
		scanner.SetBundleRuntime(nil, "")
		scanner.ConfigureBundle("", zap.NewNop())
	}
	reset()
	t.Cleanup(reset)
}

// configureUnsignedDropBundle installs an UNSIGNED external bundle, which always
// runs with degraded coverage (FR-007), and restores the embedded default after.
func configureUnsignedDropBundle(t *testing.T) {
	t.Helper()
	resetBundleRuntimeForTest(t)
	// Production wiring (NewServer) installs the real activation self-check and
	// never removes it, so whether it ran here used to depend on test order.
	// Install it explicitly: the drop must clear it like any operator file.
	scanner.EnableActivationSelfCheck()
	path := filepath.Join(t.TempDir(), "scanner-bundle.json")
	require.NoError(t, os.WriteFile(path, fileBundle(t), 0o600))
	scanner.ConfigureBundle(path, zap.NewNop())
	degraded, reason := scanner.BundleCoverageDegraded()
	require.True(t, degraded, "an unsigned external bundle must run degraded")
	require.Equal(t, scanner.DegradedUnsignedExternalActive, reason)
}

// TestMaybeAutoApproveScanSettled_DegradedBundleSuspendsAutoApproval is FR-007:
// while the active bundle's coverage is degraded a green baseline scan must NOT
// unquarantine a server; it stays quarantined for human review.
func TestMaybeAutoApproveScanSettled_DegradedBundleSuspendsAutoApproval(t *testing.T) {
	t.Run("degraded bundle: no auto-approve, logged once at Info with the reason", func(t *testing.T) {
		configureUnsignedDropBundle(t)
		fake := newFakeSecurityScanner()
		fake.summaries["srv"] = &scanner.ScanSummary{Status: "clean"}
		s := newAdmissionTestServer(t, fake, scanModeServer("srv", true))
		core, logs := observer.New(zapcore.InfoLevel)
		s.logger = zap.New(core)

		s.maybeAutoApproveScanSettled(context.Background(), "srv")
		s.maybeAutoApproveScanSettled(context.Background(), "srv")

		assert.Empty(t, fake.approvedServers())
		entries := logs.FilterField(zap.String("reason", scanner.DegradedUnsignedExternalActive)).All()
		require.Len(t, entries, 1, "the degraded suspension is logged once, not per settle event")
		assert.Equal(t, zapcore.InfoLevel, entries[0].Level)
	})

	t.Run("not degraded: unchanged behaviour, approves", func(t *testing.T) {
		resetBundleRuntimeForTest(t)
		degraded, reason := scanner.BundleCoverageDegraded()
		require.False(t, degraded, "embedded default must not be degraded (reason %q)", reason)
		fake := newFakeSecurityScanner()
		fake.summaries["srv"] = &scanner.ScanSummary{Status: "clean"}
		s := newAdmissionTestServer(t, fake, scanModeServer("srv", true))

		s.maybeAutoApproveScanSettled(context.Background(), "srv")

		assert.Equal(t, []string{"srv"}, fake.approvedServers())
	})
}

// TestServerShutdown_RoutingListenerDoesNotTouchBundleAfterward is #2036: the
// routing listener handles config.reloaded by re-applying THIS server's bundle
// config (an empty path here) to the process-global bundle runtime. Shutdown
// unsubscribes the listener, but a closed channel still delivers its buffered
// events and nothing joined the goroutine, so a handler could run after
// Shutdown returned, when the next test had installed its own bundle, and
// silently drop it.
func TestServerShutdown_RoutingListenerDoesNotTouchBundleAfterward(t *testing.T) {
	srv := newConsistencyServer(t)

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	routingListenerBeforeEvent = func(runtime.Event) {
		once.Do(func() { close(entered) })
		<-release
	}
	t.Cleanup(func() {
		routingListenerBeforeEvent = nil
		select {
		case <-release:
		default:
			close(release)
		}
	})

	// A config.reloaded the listener has not handled yet when Shutdown runs.
	srv.routingEventsCh <- runtime.Event{Type: runtime.EventTypeConfigReloaded}
	<-entered

	shutdownDone := make(chan struct{})
	go func() {
		_ = srv.Shutdown()
		close(shutdownDone)
	}()
	require.Eventually(t, func() bool {
		srv.mu.RLock()
		defer srv.mu.RUnlock()
		return srv.shutdown
	}, 5*time.Second, time.Millisecond)

	// The next test's bundle, installed after this server began shutting down.
	configureUnsignedDropBundle(t)
	close(release)
	<-shutdownDone
	select {
	case <-srv.routingListenerDone:
	case <-time.After(10 * time.Second):
		t.Fatal("routing listener still running after Shutdown returned")
	}

	degraded, reason := scanner.BundleCoverageDegraded()
	assert.True(t, degraded, "a shut-down server's listener re-applied its own bundle config (reason %q)", reason)
}

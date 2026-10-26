package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

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
	path := filepath.Join(t.TempDir(), "scanner-bundle.json")
	require.NoError(t, os.WriteFile(path, []byte(fileBundle), 0o600))
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

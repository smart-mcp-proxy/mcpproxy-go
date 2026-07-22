package server

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// newServeErrTestServer builds a minimal server instance the same way the e2e
// harness does (temp data dir with 0700 perms, explicit listen address).
func newServeErrTestServer(t *testing.T, listen string) *Server {
	t.Helper()

	// Disable OAuth to avoid network calls during upstream connection attempts.
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	dataDir := filepath.Join(t.TempDir(), "data")
	require.NoError(t, os.MkdirAll(dataDir, 0700))

	logger := zap.NewNop()
	quarantineDisabled := false
	cfg := &config.Config{
		DataDir:           dataDir,
		Listen:            listen,
		APIKey:            "test-api-key-serveerr",
		ToolResponseLimit: 10000,
		QuarantineEnabled: &quarantineDisabled,
	}

	srv, err := NewServer(cfg, logger)
	require.NoError(t, err)
	return srv
}

// TestStartServer_PortConflict_SignalsServeErr verifies that a startup bind
// failure inside the async StartServer goroutine is delivered on ServeErr()
// as a *PortInUseError, so the process can exit with code 2 instead of
// lingering as an unreachable zombie (v0.51.0-rc.1 QA finding SERVE-PORT).
func TestStartServer_PortConflict_SignalsServeErr(t *testing.T) {
	// Occupy a port so the server's bind attempt fails.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer occupied.Close()

	srv := newServeErrTestServer(t, occupied.Addr().String())
	defer func() { _ = srv.Shutdown() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// StartServer stays async-nil: the bind failure happens in the goroutine.
	require.NoError(t, srv.StartServer(ctx))

	select {
	case serveErr := <-srv.ServeErr():
		require.Error(t, serveErr)
		var portErr *PortInUseError
		require.True(t, errors.As(serveErr, &portErr),
			"expected *PortInUseError, got %T: %v", serveErr, serveErr)
	case <-time.After(10 * time.Second):
		t.Fatal("no serve error delivered on ServeErr() within 10s")
	}
}

// TestStartServer_GracefulCancel_NoServeErr pins the graceful shutdown path:
// context cancellation must NOT fire ServeErr().
func TestStartServer_GracefulCancel_NoServeErr(t *testing.T) {
	// Find a free port (listen/close pattern used by the e2e harness).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	srv := newServeErrTestServer(t, addr)
	defer func() { _ = srv.Shutdown() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, srv.StartServer(ctx))

	// Wait for the server to come up.
	require.Eventually(t, srv.IsRunning, 15*time.Second, 50*time.Millisecond,
		"server did not reach running state")

	cancel()

	// ServeErr must stay silent on graceful cancellation.
	select {
	case serveErr := <-srv.ServeErr():
		t.Fatalf("unexpected serve error on graceful cancel: %v", serveErr)
	case <-time.After(2 * time.Second):
		// Silence is the expected outcome.
	}

	require.NoError(t, srv.Shutdown())
}

package server

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// fixtureShutdownTimeout bounds one fixture manager's shutdown. ShutdownAll
// waits at most 3s for the manager's background goroutines and 5s for the
// client disconnects, so this only matters for a wedged client.
const fixtureShutdownTimeout = 15 * time.Second

// newTestUpstreamManager is upstream.NewManager for this package's fixtures:
// the manager is shut down when the test ends. A manager that is never shut
// down keeps its network-recovery watcher (GH #1929) and every client it
// registered alive for the rest of the package run, and those clients keep
// reconnecting to their fixture's closed port — and send requests to whatever
// later listens on it.
//
// t.Cleanup runs last-registered first, so the shutdown registered here runs
// AFTER the close of any upstream fixture the test starts later. A test that
// connects the manager to such a fixture registers its own stop after the
// fixture's close (stopUpstreamBeforeFixtureClose), so no client outlives
// its upstream.
func newTestUpstreamManager(t testing.TB, logger *zap.Logger, cfg *config.Config, boltStorage *storage.BoltDB, secretResolver *secret.Resolver, storageMgr *storage.Manager) *upstream.Manager {
	t.Helper()
	um := upstream.NewManager(logger, cfg, boltStorage, secretResolver, storageMgr)
	shutdownUpstreamOnCleanup(t, um)
	return um
}

// shutdownUpstreamOnCleanup shuts um down when the test ends, the way the
// runtime does on exit (Runtime.Close → ShutdownAll). Calling it more than
// once for one manager is harmless.
func shutdownUpstreamOnCleanup(t testing.TB, um *upstream.Manager) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixtureShutdownTimeout)
		defer cancel()
		_ = um.ShutdownAll(ctx)
	})
}

// stopUpstreamBeforeFixtureClose removes server from um when the test ends.
// Register it right AFTER the cleanup that closes the server's fixture: the
// cleanups then run in the other order, so the client disconnects (and sends
// its session DELETE) while the fixture still listens, instead of reconnecting
// to a closed port until the manager shuts down.
func stopUpstreamBeforeFixtureClose(t testing.TB, um *upstream.Manager, server string) {
	t.Helper()
	t.Cleanup(func() { um.RemoveServer(server) })
}

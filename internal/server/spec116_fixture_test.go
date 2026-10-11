package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 116 PR1 test fixtures (quickstart.md §T1). The servers point at
// unreachable endpoints or are disabled, so no test depends on a live upstream.

const spec116APIKey = "spec116-test-key"

// newSpec116Server builds a real *Server on a config file holding servers, and
// boots its runtime projection (storage rows for every configured server).
func newSpec116Server(t *testing.T, servers ...*config.ServerConfig) (*Server, string) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.APIKey = spec116APIKey
	cfg.Servers = servers
	cfgPath := filepath.Join(t.TempDir(), "mcp_config.json")
	raw, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, raw, 0o600))
	loaded, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	loaded.DataDir = cfg.DataDir
	srv, err := NewServerWithConfigPath(loaded, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })
	bootSpec116(t, srv)
	return srv, cfgPath
}

// bootSpec116 projects the configured servers into storage, as the daemon's
// boot reconcile does.
func bootSpec116(t *testing.T, srv *Server) {
	t.Helper()
	require.NoError(t, srv.runtime.LoadConfiguredServers(nil))
}

func liveServer(srv *Server, name string) *config.ServerConfig {
	for _, sc := range srv.runtime.Config().Servers {
		if sc != nil && sc.Name == name {
			return sc
		}
	}
	return nil
}

func storedServer(t *testing.T, srv *Server, name string) *config.ServerConfig {
	t.Helper()
	servers, err := srv.runtime.StorageManager().ListUpstreamServers()
	require.NoError(t, err)
	for _, sc := range servers {
		if sc.Name == name {
			return sc
		}
	}
	return nil
}

func diskServer(t *testing.T, cfgPath, name string) *config.ServerConfig {
	t.Helper()
	cfg, err := config.LoadFromFile(cfgPath)
	require.NoError(t, err)
	for _, sc := range cfg.Servers {
		if sc != nil && sc.Name == name {
			return sc
		}
	}
	return nil
}

func mcpCall(args map[string]interface{}) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

// awaitReturnOrBlocked implements the dual-mode release (quickstart R3.7): it
// reports true when done delivered (the competitor returned, so the parent
// commit did not exclude it) and false once blocked() holds (the competitor is
// waiting on the lock the parked section holds).
func awaitReturnOrBlocked(t *testing.T, done <-chan error, blocked func() bool) (returned bool, err error) {
	t.Helper()
	deadline := time.After(20 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return true, err
		case <-tick.C:
			if blocked() {
				return false, nil
			}
		case <-deadline:
			t.Fatal("competitor neither returned nor blocked within 20s")
			return false, nil
		}
	}
}

func awaitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func awaitErr(t *testing.T, ch <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return nil
	}
}

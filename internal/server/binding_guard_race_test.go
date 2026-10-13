package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// TestBindingGuard_ConcurrentConfigAndBindingWritesNeverCombine pins plan D3:
// two writes that are each safe alone — turning require_mcp_auth off, and
// binding a client to a named profile — must not both succeed, because
// together they leave the binding bypassable. The guard check and the write
// run under one mutex, so exactly one of the pair is refused, whichever wins.
func TestBindingGuard_ConcurrentConfigAndBindingWritesNeverCombine(t *testing.T) {
	for i := 0; i < 15; i++ {
		cfg := config.DefaultConfig()
		cfg.DataDir = t.TempDir()
		cfg.Listen = "127.0.0.1:0"
		cfg.APIKey = "race-key"
		cfg.RequireMCPAuth = true
		cfg.Servers = []*config.ServerConfig{{Name: "a", Enabled: false}}
		cfg.Profiles = []config.ProfileConfig{{Name: "ro", Servers: []string{"a"}}}
		cfgPath := filepath.Join(t.TempDir(), "mcp_config.json")
		raw, err := json.MarshalIndent(cfg, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(cfgPath, raw, 0o600))
		srv, err := NewServerWithConfigPath(cfg, cfgPath, zap.NewNop())
		require.NoError(t, err)
		svc := srv.runtime.ClientsService()

		// cursor starts as an "All servers" switchable credential: not a named
		// binding, so turning auth off is safe by itself.
		all := ""
		m := svc.ConnectMinter()
		intent := connect.CredentialIntent{Profile: &all}
		issued, err := m.Issue("cursor", intent)
		require.NoError(t, err)
		require.NoError(t, m.Commit("cursor", intent, issued))

		off := cloneConfig(t, srv.runtime.Config())
		off.RequireMCPAuth = false

		var wg sync.WaitGroup
		var applyErr, bindErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, applyErr = srv.ApplyConfig(off, cfgPath)
		}()
		go func() {
			defer wg.Done()
			_, bindErr = svc.SetBinding(context.Background(), runtime.Actor{Kind: "api_key", Surface: profile.SurfaceAPI}, "cursor", "ro", nil)
		}()
		wg.Wait()

		var applyRefused, bindRefused *runtime.BindingGuardError
		refusals := 0
		if errors.As(applyErr, &applyRefused) {
			refusals++
		} else {
			require.NoError(t, applyErr)
		}
		if errors.As(bindErr, &bindRefused) {
			refusals++
		} else {
			require.NoError(t, bindErr)
		}
		require.Equal(t, 1, refusals, "exactly one of the two writes is refused (iteration %d): apply=%v bind=%v", i, applyErr, bindErr)
		_ = srv.Shutdown()
	}
}

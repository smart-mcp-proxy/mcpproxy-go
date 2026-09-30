package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// guardedApplyServer builds a real *Server whose config has one profile "P",
// auth ON, and an active locked client binding cursor -> ro.
func guardedApplyServer(t *testing.T, mutate func(*config.Config)) (*Server, string) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.APIKey = "guard-apply-key"
	cfg.RequireMCPAuth = true
	cfg.Servers = []*config.ServerConfig{{Name: "a", Enabled: false}}
	cfg.Profiles = []config.ProfileConfig{{Name: "ro", Servers: []string{"a"}}}
	if mutate != nil {
		mutate(cfg)
	}
	cfgPath := filepath.Join(t.TempDir(), "mcp_config.json")
	raw, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, raw, 0o600))
	srv, err := NewServerWithConfigPath(cfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })
	_, err = srv.runtime.StorageManager().MintClientCredential(
		"cursor", "mcp_cli_apply_guard_test", []byte("apply-guard-key"),
		"locked", "ro", time.Now().Add(time.Hour))
	require.NoError(t, err)
	return srv, cfgPath
}

func cloneConfig(t *testing.T, cfg *config.Config) *config.Config {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var out config.Config
	require.NoError(t, json.Unmarshal(raw, &out))
	return &out
}

// TestServerApplyConfig_RefusesBindingBypassWrites: the config routes all
// funnel through Server.ApplyConfig; a write that would leave the binding
// bypassable is refused with the delta and both fixes, and writes nothing.
func TestServerApplyConfig_RefusesBindingBypassWrites(t *testing.T) {
	srv, cfgPath := guardedApplyServer(t, nil)
	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	off := cloneConfig(t, srv.runtime.Config())
	off.RequireMCPAuth = false
	_, err = srv.ApplyConfig(off, cfgPath)
	var refusal *runtime.BindingGuardError
	require.True(t, errors.As(err, &refusal), "err=%v", err)
	require.Equal(t, []runtime.BindingRef{{ClientID: "cursor", TokenName: "client-cursor", Profile: "ro", Mode: "locked"}}, refusal.Bindings)
	require.Equal(t, []runtime.GuardFix{
		{Kind: "require_mcp_auth"},
		{Kind: "set_anonymous_profile", Target: "ro"},
	}, refusal.Fixes)

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "a refused write leaves the config file untouched")
	require.True(t, srv.runtime.Config().RequireMCPAuth, "and the live config")
}

// TestServerApplyConfig_FixesUnblockTheSameRequest: after either fix the
// same request succeeds.
func TestServerApplyConfig_FixesUnblockTheSameRequest(t *testing.T) {
	srv, cfgPath := guardedApplyServer(t, nil)

	// Fix 2: anonymous_profile P in the same request that turns auth off.
	off := cloneConfig(t, srv.runtime.Config())
	off.RequireMCPAuth = false
	off.AnonymousProfile = "ro"
	_, err := srv.ApplyConfig(off, cfgPath)
	require.NoError(t, err, "anonymous_profile equal to the binding is not bypassable")

	// Clearing it again would unconfine anonymous: refused.
	clear := cloneConfig(t, srv.runtime.Config())
	clear.AnonymousProfile = ""
	_, err = srv.ApplyConfig(clear, cfgPath)
	var refusal *runtime.BindingGuardError
	require.True(t, errors.As(err, &refusal), "err=%v", err)

	// Fix 1: turn auth on first, then clearing is fine.
	on := cloneConfig(t, srv.runtime.Config())
	on.RequireMCPAuth = true
	_, err = srv.ApplyConfig(on, cfgPath)
	require.NoError(t, err)
	clear2 := cloneConfig(t, srv.runtime.Config())
	clear2.AnonymousProfile = ""
	_, err = srv.ApplyConfig(clear2, cfgPath)
	require.NoError(t, err)
}

// TestServerApplyConfig_AlreadyBypassableStateIsNotRefused: an empty delta
// (an unrelated write while the state is already bypassable) passes, so the
// guard never blocks repairs.
func TestServerApplyConfig_AlreadyBypassableStateIsNotRefused(t *testing.T) {
	srv, cfgPath := guardedApplyServer(t, func(cfg *config.Config) {
		cfg.RequireMCPAuth = false
		cfg.AnonymousProfile = ""
	})
	next := cloneConfig(t, srv.runtime.Config())
	next.ToolsLimit = 21
	_, err := srv.ApplyConfig(next, cfgPath)
	require.NoError(t, err)
}

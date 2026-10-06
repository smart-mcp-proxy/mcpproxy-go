package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Issue #1458 item 1: retrieve_tools under /mcp/p/<profile> searches the
// per-profile physical index. A profile edit made by hand (config watcher
// reload) or by a direct ApplyConfig must reach that index when the write
// returns; a stale index must only ever HIDE tools, never expose one.

type profileReloadFixture struct {
	srv     *Server
	cfgPath string
}

// newProfileReloadFixture builds a real *Server over servers a and b, seeds the
// shared index with a:alpha_tool and b:beta_tool, then creates profile "ro"
// (servers [a], plus mutate's edits) through the config funnel.
func newProfileReloadFixture(t *testing.T, mutate func(*config.ProfileConfig)) *profileReloadFixture {
	t.Helper()
	// Not t.TempDir: a profile-index build can still be writing at cleanup.
	dir, err := os.MkdirTemp("", "profile-index-reload-*")
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	cfg.DataDir = dir
	cfg.Listen = "127.0.0.1:0"
	cfg.APIKey = "profile-index-reload-key"
	cfg.QuarantineEnabled = boolPtr(false)
	cfg.Servers = []*config.ServerConfig{
		{Name: "a", Command: "true", Protocol: "stdio", Enabled: true},
		{Name: "b", Command: "true", Protocol: "stdio", Enabled: true},
	}
	cfgPath := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, config.SaveConfig(cfg, cfgPath))

	srv, err := NewServerWithConfigPath(cfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = srv.Shutdown()
		time.Sleep(100 * time.Millisecond)
		_ = os.RemoveAll(dir)
	})

	// Settle the server set first so a later reload does not treat a and b as
	// first-seen and empty the shared index.
	require.NoError(t, srv.runtime.LoadConfiguredServers(nil))
	require.NoError(t, srv.runtime.IndexManager().BatchIndexTools([]*config.ToolMetadata{
		{Name: "a:alpha_tool", ServerName: "a", Description: "alpha_tool", ParamsJSON: "{}",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}},
		{Name: "b:beta_tool", ServerName: "b", Description: "beta_tool", ParamsJSON: "{}",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}},
	}))

	ro := config.ProfileConfig{Name: "ro", Servers: []string{"a"}}
	if mutate != nil {
		mutate(&ro)
	}
	_, _, err = srv.MutateConfig(context.Background(), runtime.Actor{Kind: "api_key"},
		func(d *config.Config) (runtime.ChangeHint, error) {
			d.Profiles = append(d.Profiles, ro)
			return runtime.ChangeHint{}, nil
		}, runtime.TokenRewrite{})
	require.NoError(t, err)
	return &profileReloadFixture{srv: srv, cfgPath: cfgPath}
}

func (f *profileReloadFixture) search(t *testing.T, query string) profileV3RetrieveResponse {
	t.Helper()
	// urlProfileCtx snapshots the scope, so recompute it after every write.
	return callRetrieveToolsV3(t, f.srv.mcpProxy, urlProfileCtx(f.srv.mcpProxy, "ro"), query, 10)
}

func reloadToolNames(resp profileV3RetrieveResponse) []string {
	var names []string
	for _, tool := range resp.Tools {
		if n, ok := tool["name"].(string); ok {
			names = append(names, n)
		}
	}
	return names
}

func (f *profileReloadFixture) editedConfig(t *testing.T, servers []string) *config.Config {
	t.Helper()
	desired, err := f.srv.runtime.GetDesiredConfig()
	require.NoError(t, err)
	next := cloneConfig(t, desired)
	for i := range next.Profiles {
		if next.Profiles[i].Name == "ro" {
			next.Profiles[i].Servers = servers
		}
	}
	return next
}

func TestRetrieveTools_ProfileWidenedByHandEditIsSearchableImmediately(t *testing.T) {
	f := newProfileReloadFixture(t, nil)
	require.Empty(t, f.search(t, "beta_tool").Tools, "baseline: b is outside ro")

	require.NoError(t, config.SaveConfig(f.editedConfig(t, []string{"a", "b"}), f.cfgPath))
	require.NoError(t, f.srv.runtime.ReloadConfiguration())

	assert.Equal(t, []string{"b:beta_tool"}, reloadToolNames(f.search(t, "beta_tool")))
}

func TestRetrieveTools_ProfileWidenedByRawApplyIsSearchableImmediately(t *testing.T) {
	f := newProfileReloadFixture(t, nil)
	require.Empty(t, f.search(t, "beta_tool").Tools)

	_, err := f.srv.ApplyConfig(f.editedConfig(t, []string{"a", "b"}), f.cfgPath)
	require.NoError(t, err)

	assert.Equal(t, []string{"b:beta_tool"}, reloadToolNames(f.search(t, "beta_tool")))
}

// What PATCH /config runs: a regression pin that stays green after the apply,
// not the funnel, owns the reconcile.
func TestRetrieveTools_ProfileWidenedByConfigPatchIsSearchableImmediately(t *testing.T) {
	f := newProfileReloadFixture(t, nil)
	require.Empty(t, f.search(t, "beta_tool").Tools)

	_, _, err := f.srv.MutateConfig(context.Background(), runtime.Actor{Kind: "api_key"},
		func(d *config.Config) (runtime.ChangeHint, error) {
			for i := range d.Profiles {
				if d.Profiles[i].Name == "ro" {
					d.Profiles[i].Servers = []string{"a", "b"}
				}
			}
			return runtime.ChangeHint{}, nil
		}, runtime.TokenRewrite{})
	require.NoError(t, err)

	assert.Equal(t, []string{"b:beta_tool"}, reloadToolNames(f.search(t, "beta_tool")))
}

// Hide, never expose: even when the per-profile index still holds a server the
// profile no longer names, retrieve_tools re-admits every hit against the live
// profile scope.
func TestRetrieveTools_ProfileNarrowedStaleIndexNeverExposes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*config.ProfileConfig)
		legacy bool
	}{
		{"legacy profile", func(p *config.ProfileConfig) { p.Servers = []string{"a", "b"} }, true},
		{"capped profile", func(p *config.ProfileConfig) {
			p.Servers = []string{"a", "b"}
			p.MaxTier = config.ProfileTierRead
			// The tools are not annotated in live state, so an allow rule is
			// what admits them under the read cap.
			p.Tools = &config.ProfileToolRules{Allow: []string{"a:*", "b:*"}}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProfileReloadFixture(t, tc.mutate)
			require.Equal(t, []string{"b:beta_tool"}, reloadToolNames(f.search(t, "beta_tool")), "baseline: b is in ro")

			require.NoError(t, config.SaveConfig(f.editedConfig(t, []string{"a"}), f.cfgPath))
			require.NoError(t, f.srv.runtime.ReloadConfiguration())
			// Force the pre-fix window: the per-profile index still holds b.
			require.NoError(t, f.srv.runtime.IndexManager().RebuildProfileFromShared("ro", []string{"a", "b"}))

			resp := f.search(t, "beta_tool")
			assert.Empty(t, resp.Tools, "a stale index must not expose a server outside the live scope")
			if tc.legacy {
				assert.Nil(t, resp.HiddenByProfile)
			} else if resp.HiddenByProfile != nil {
				assert.Equal(t, 0, *resp.HiddenByProfile, "scope rejections are not policy rejections")
			}
		})
	}
}

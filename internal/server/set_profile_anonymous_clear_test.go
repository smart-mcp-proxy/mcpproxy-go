package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// TestHandleSetProfile_ConfinedAnonymousClearReportsEffectiveScope pins that
// a caller confined by anonymous_profile is told, after clearing its session
// selection, the servers discovery and dispatch will actually use — the
// anonymous profile's — and never every configured server. The stored
// selection stays empty and the operator's profile name is not disclosed.
func TestHandleSetProfile_ConfinedAnonymousClearReportsEffectiveScope(t *testing.T) {
	type payload struct {
		ActiveProfile string   `json:"active_profile"`
		ProfileSource string   `json:"profile_source"`
		Servers       []string `json:"servers"`
	}
	call := func(t *testing.T, proxy *MCPProxyServer, ctx context.Context, slug string) payload {
		t.Helper()
		res, err := proxy.handleSetProfile(ctx, setProfileRequest(slug))
		require.NoError(t, err)
		require.False(t, res.IsError, setProfileResultText(t, res))
		var out payload
		require.NoError(t, json.Unmarshal([]byte(setProfileResultText(t, res)), &out))
		return out
	}
	callers := map[string]func() context.Context{
		"no credential":      anonCtx,
		"unrecognised token": anonymousMarkedCtx,
	}

	for name, base := range callers {
		t.Run(name, func(t *testing.T) {
			proxy, _ := newProfilesV3FixtureWithConfig(t, func(cfg *config.Config) {
				cfg.AnonymousProfile = "work-readonly"
			})
			ctx := sessionCtx(base(), "anon-clear")

			// Clearing with nothing selected.
			got := call(t, proxy, ctx, "")
			require.Equal(t, payload{ActiveProfile: "", ProfileSource: string(profile.SourceNone), Servers: []string{"github", "notion"}}, got)

			// The reported list is the scope dispatch enforces.
			res := proxy.ResolveProfileV3(ctx, proxy.profileIndexCurrent(ctx))
			require.Equal(t, string(profile.SourceAnonymous), res.Source)
			require.NotNil(t, res.Scope)
			require.False(t, res.Scope.Allows("filesystem"))
			for _, s := range got.Servers {
				require.True(t, res.Scope.Allows(s), s)
			}

			// A switchable_to target reports its own servers; clearing it
			// returns to the anonymous base, not to every server.
			got = call(t, proxy, ctx, "work-full")
			require.Equal(t, []string{"github", "notion", "filesystem"}, got.Servers)
			got = call(t, proxy, ctx, "")
			require.Equal(t, payload{ActiveProfile: "", ProfileSource: string(profile.SourceNone), Servers: []string{"github", "notion"}}, got)
			require.Empty(t, proxy.sessionStore.GetActiveProfile("anon-clear"))
		})

		t.Run(name+"/dangling anonymous_profile reports nothing", func(t *testing.T) {
			proxy, _ := newProfilesV3Fixture(t)
			cfg := proxy.currentConfig()
			cfg.AnonymousProfile = "deleted-profile"
			ctx := sessionCtx(base(), "anon-dangling")

			got := call(t, proxy, ctx, "")
			require.Equal(t, payload{ActiveProfile: "", ProfileSource: string(profile.SourceNone), Servers: []string{}}, got)
		})
	}

	// On /mcp/p/<slug> the admitted URL profile governs dispatch, whatever is
	// stored, so it governs the reported servers too.
	t.Run("admitted URL profile governs the reported servers", func(t *testing.T) {
		proxy, _ := newProfilesV3FixtureWithConfig(t, func(cfg *config.Config) {
			cfg.AnonymousProfile = "work-readonly"
		})
		idx := proxy.profileIndexFor(proxy.currentConfig())
		urlCtx := func(slug, sid string) context.Context {
			return sessionCtx(profile.WithProfileScope(anonCtx(), profileScopeFromIndex(idx, slug)), sid)
		}
		enforced := func(ctx context.Context) []string {
			res := proxy.ResolveProfileV3(ctx, proxy.profileIndexCurrent(ctx))
			require.NotNil(t, res.Scope)
			var out []string
			for _, s := range []string{"github", "notion", "filesystem"} {
				if res.Scope.Allows(s) {
					out = append(out, s)
				}
			}
			return out
		}

		wide := urlCtx("work-full", "anon-url-wide")
		got := call(t, proxy, wide, "")
		require.Equal(t, []string{"github", "notion", "filesystem"}, got.Servers)
		require.Equal(t, enforced(wide), got.Servers)

		narrow := urlCtx("work-readonly", "anon-url-narrow")
		got = call(t, proxy, narrow, "work-full")
		require.Equal(t, "work-full", got.ActiveProfile)
		require.Equal(t, []string{"github", "notion"}, got.Servers)
		require.Equal(t, enforced(narrow), got.Servers)

		// A URL profile the anonymous base does not admit is ignored, as
		// ResolveProfileV3 ignores it.
		outside := urlCtx("legacy", "anon-url-outside")
		got = call(t, proxy, outside, "")
		require.Equal(t, []string{"github", "notion"}, got.Servers)
		require.Equal(t, enforced(outside), got.Servers)
	})

	t.Run("unset anonymous_profile keeps every server", func(t *testing.T) {
		proxy, _ := newProfilesV3Fixture(t)
		got := call(t, proxy, sessionCtx(anonCtx(), "anon-unconfined"), "")
		require.Equal(t, []string{"github", "notion", "filesystem"}, got.Servers)
	})
}

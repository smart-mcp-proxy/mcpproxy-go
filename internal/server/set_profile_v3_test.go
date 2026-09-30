package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

func TestSetProfileV3ReportsOnlyTheSessionSelection(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	ctx := sessionProfileCtx(t, proxy, "session-profile-source", "work-full")
	for _, tc := range []struct {
		name, selection, wantSource string
	}{
		{name: "selected profile", selection: "work-full", wantSource: "session"},
		{name: "cleared selection", selection: "", wantSource: "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]interface{}{"profile": tc.selection}
			result, err := proxy.handleSetProfile(ctx, request)
			require.NoError(t, err)
			require.False(t, result.IsError, resultText(t, result))
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &payload))
			require.Equal(t, tc.wantSource, payload["profile_source"])
			require.Equal(t, tc.selection, payload["active_profile"])
		})
	}
}

func TestSetProfileV3SwitchableClientCanSelectDeclaredTarget(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	idx := proxy.profileIndexFor(proxy.currentConfig())

	t.Run("switchable client may select its declared one-hop target", func(t *testing.T) {
		ctx := sessionCtx(clientCtx("laptop", "work-readonly", "switchable"), "switchable-set-profile")
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{"profile": "work-full"}

		result, err := proxy.handleSetProfile(ctx, request)
		require.NoError(t, err)
		require.False(t, result.IsError, resultText(t, result))
		var payload map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &payload))
		require.Equal(t, "work-full", payload["active_profile"])
		require.Equal(t, "session", payload["profile_source"])
		require.Equal(t, "work-full", proxy.sessionStore.GetActiveProfile("switchable-set-profile"))
		require.Equal(t, string(profile.SourceSession), proxy.ResolveProfileV3(ctx, idx).Source)
	})

	t.Run("locked client cannot select the same target", func(t *testing.T) {
		ctx := sessionCtx(clientCtx("cursor", "work-readonly", "locked"), "locked-set-profile")
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{"profile": "work-full"}

		result, err := proxy.handleSetProfile(ctx, request)
		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Equal(t, "unknown profile 'work-full'", resultText(t, result))
		require.Empty(t, proxy.sessionStore.GetActiveProfile("locked-set-profile"))
	})

	t.Run("switchable All servers binding keeps legacy selectable profiles", func(t *testing.T) {
		ctx := sessionCtx(clientCtx("all-servers", "", "switchable"), "all-servers-set-profile")
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{"profile": "legacy"}

		result, err := proxy.handleSetProfile(ctx, request)
		require.NoError(t, err)
		require.False(t, result.IsError, resultText(t, result))
		require.Equal(t, "legacy", proxy.sessionStore.GetActiveProfile("all-servers-set-profile"))
	})
}

func TestSetProfileV3ConfinedAnonymousHonorsSwitchableTo(t *testing.T) {
	proxy, _ := newProfilesV3FixtureWithConfig(t, func(cfg *config.Config) {
		cfg.AnonymousProfile = "work-readonly"
	})
	ctx := sessionCtx(anonCtx(), "confined-anonymous-set-profile")
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{"profile": "legacy"}

	result, err := proxy.handleSetProfile(ctx, request)
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, "unknown profile 'legacy'", resultText(t, result))
	require.Empty(t, proxy.sessionStore.GetActiveProfile("confined-anonymous-set-profile"))
}

// Spec 108 PR 108-bd-tests, T048: the "Management and switching" table of
// contracts/enforcement-matrix.md, every column except `profiles` (which only
// exists from 108-h). One row per caller class, each in its own configuration.
//
// Fixtures: client rows use clientCtx (no stored credential), so the FR-008a
// binding guard stays inert exactly as the matrix preamble requires; only the
// guard row mints a stored locked client. The "operator locks a switchable
// client" row asserts RESOLUTION only — clearing a stored selection when a
// binding changes is 108-c (T032/T040) and is deliberately not depended on.

const managementMatrixProfile = "mgmt-on"

// withManagementOnProfile adds `mgmt-on`: work-readonly with
// management_tools:true and switchable_to:[work-full].
func withManagementOnProfile(cfg *config.Config) {
	mgmt := enforcementMatrixProfiles()[0]
	mgmt.Name = managementMatrixProfile
	mgmt.Title = ""
	mgmt.ManagementTools = boolPtr(true)
	cfg.Profiles = append(cfg.Profiles, mgmt)
}

type managementMatrixClear struct {
	name, source string
	denyAll      bool
	guarded      bool
}

type managementMatrixRow struct {
	name      string
	configure func(*config.Config)
	caller    func() context.Context
	setup     func(t *testing.T, proxy *MCPProxyServer, rt *runtime.Runtime)
	// preSelect is a session selection made before the visibility check
	// (the "API key, session on work-readonly" row).
	preSelect string
	// hiddenWhilePreSelected: upstream_servers is hidden while preSelect is
	// active and visible again once the selection is cleared.
	hiddenWhilePreSelected bool
	wantUpstreamServers    bool
	wantWorkFull           bool
	wantLegacy             bool
	// wantOwnBase: set_profile(<own pinned/bound/anonymous base>) is admitted
	// (naming the own base is not a switch, FR-022).
	ownBase    string
	afterClear managementMatrixClear
	// writesDenied asserts every mutating upstream_servers op is refused for
	// this management-enabled, non-admin caller.
	writesDenied bool
	// extraRefused are further slugs that must be refused uniformly.
	extraRefused []string
	// base is the caller's own base slug ("" = none). Refusal bytes and
	// success payloads must never name it when a different slug was requested.
	base string
}

func TestSetProfileV3_ManagementAndSwitchingMatrix(t *testing.T) {
	allPerms := []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}
	rows := []managementMatrixRow{
		{
			name:   "1 API key, no profile",
			caller: adminCtx, wantUpstreamServers: true, wantWorkFull: true, wantLegacy: true,
			afterClear: managementMatrixClear{source: string(profile.SourceNone)},
		},
		{
			name:   "2 API key, session on work-readonly",
			caller: adminCtx, preSelect: "work-readonly", hiddenWhilePreSelected: true,
			wantUpstreamServers: true, wantWorkFull: true, wantLegacy: true,
			afterClear: managementMatrixClear{source: string(profile.SourceNone)},
		},
		{
			name: "3 socket",
			caller: func() context.Context {
				return auth.WithAuthContext(context.Background(), credentialKindContext(auth.AdminContext(), auth.CredentialKindSocket))
			},
			wantUpstreamServers: true, wantWorkFull: true, wantLegacy: true,
			afterClear: managementMatrixClear{source: string(profile.SourceNone)},
		},
		{
			name:   "4 anonymous, no anonymous_profile, no client binding",
			caller: anonCtx, wantUpstreamServers: true, wantWorkFull: true, wantLegacy: true,
			afterClear: managementMatrixClear{source: string(profile.SourceNone)},
		},
		{
			// FR-008a: require_mcp_auth off + a stored locked client + no
			// anonymous_profile → the anonymous identity is deny-all.
			name: "5 anonymous under the FR-008a binding guard",
			configure: func(cfg *config.Config) {
				cfg.RequireMCPAuth = false
				cfg.AnonymousProfile = ""
			},
			setup: func(t *testing.T, proxy *MCPProxyServer, rt *runtime.Runtime) {
				_, err := rt.StorageManager().MintClientCredential("cursor", "mcp_cli_matrix_guard_test",
					[]byte("matrix-guard-test-key"), auth.ProfileModeLocked, "work-readonly", time.Now().Add(time.Hour))
				require.NoError(t, err)
			},
			caller:     anonCtx,
			afterClear: managementMatrixClear{name: "", source: string(profile.SourceAnonymous), denyAll: true, guarded: true},
		},
		{
			name:      "6 anonymous, anonymous_profile=work-readonly",
			configure: func(cfg *config.Config) { cfg.AnonymousProfile = "work-readonly" },
			caller:    anonCtx, wantWorkFull: true, base: "work-readonly",
			afterClear: managementMatrixClear{name: "work-readonly", source: string(profile.SourceAnonymous)},
		},
		{
			name:      "7 anonymous, anonymous_profile=legacy",
			configure: func(cfg *config.Config) { cfg.AnonymousProfile = "legacy" },
			caller:    anonCtx, wantLegacy: true, ownBase: "legacy", base: "legacy",
			afterClear: managementMatrixClear{name: "legacy", source: string(profile.SourceAnonymous)},
		},
		{
			name:   "8 client locked to work-readonly",
			caller: func() context.Context { return clientCtx("cursor", "work-readonly", "locked") },
			// FR-022: naming the own pin is admitted.
			ownBase: "work-readonly", base: "work-readonly",
			afterClear: managementMatrixClear{name: "work-readonly", source: string(profile.SourcePin)},
		},
		{
			name:         "9 client switchable, base work-readonly",
			caller:       func() context.Context { return clientCtx("laptop", "work-readonly", "switchable") },
			wantWorkFull: true, base: "work-readonly",
			afterClear: managementMatrixClear{name: "work-readonly", source: string(profile.SourceBinding)},
		},
		{
			name:         "10 client switchable, base All servers",
			caller:       func() context.Context { return clientCtx("all-servers", "", "switchable") },
			wantWorkFull: true, wantLegacy: true,
			afterClear: managementMatrixClear{name: "", source: string(profile.SourceBinding)},
		},
		{
			name:   "11 legacy agent token, no pin",
			caller: func() context.Context { return agentCtx([]string{"*"}, allPerms, "") },
			// Per Spec 105: an unpinned token keeps its pre-108 management tools.
			wantUpstreamServers: true, wantWorkFull: true, wantLegacy: true,
			afterClear: managementMatrixClear{source: string(profile.SourceNone)},
		},
		{
			name:      "12 legacy agent token pinned to a management_tools profile",
			configure: withManagementOnProfile,
			caller:    func() context.Context { return agentCtx([]string{"*"}, allPerms, managementMatrixProfile) },
			// The pinned token keeps the established read operation; mutating
			// operations stay refused (Spec 028 AuthorizeServerOp).
			wantUpstreamServers: true, writesDenied: true, base: managementMatrixProfile,
			afterClear: managementMatrixClear{name: managementMatrixProfile, source: string(profile.SourcePin)},
		},
		{
			name: "13 anonymous, anonymous_profile with management_tools",
			configure: func(cfg *config.Config) {
				withManagementOnProfile(cfg)
				cfg.AnonymousProfile = managementMatrixProfile
			},
			caller:              anonCtx,
			wantUpstreamServers: true, writesDenied: true, wantWorkFull: true, base: managementMatrixProfile,
			afterClear: managementMatrixClear{name: managementMatrixProfile, source: string(profile.SourceAnonymous)},
		},
		{
			name:                "14 client locked to a management_tools profile",
			configure:           withManagementOnProfile,
			caller:              func() context.Context { return clientCtx("desktop", managementMatrixProfile, "locked") },
			wantUpstreamServers: true, writesDenied: true, ownBase: managementMatrixProfile, base: managementMatrixProfile,
			afterClear: managementMatrixClear{name: managementMatrixProfile, source: string(profile.SourcePin)},
		},
		{
			// Dangling binding: refusals for everything (no switchable_to is
			// known), clear stays deny-all with source binding, never falling
			// through to a broader view.
			name:   "16 client switchable, bound profile hand-deleted",
			caller: func() context.Context { return clientCtx("laptop", "gone", "switchable") },
			base:   "gone", extraRefused: []string{"gone"}, afterClear: managementMatrixClear{name: "gone", source: string(profile.SourceBinding), denyAll: true},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			proxy, rt := newProfilesV3FixtureWithConfig(t, func(cfg *config.Config) {
				if row.configure != nil {
					row.configure(cfg)
				}
			})
			if row.setup != nil {
				row.setup(t, proxy, rt)
			}
			idx := proxy.profileIndexFor(proxy.currentConfig())
			tools := []mcp.Tool{{Name: "upstream_servers"}, {Name: "quarantine_security"}, {Name: "call_tool_read"}}
			sessions := 0
			session := func() context.Context {
				sessions++
				return sessionCtx(row.caller(), fmt.Sprintf("matrix-%s-%d", strings.ReplaceAll(row.name, " ", "-"), sessions))
			}
			setProfile := func(ctx context.Context, slug string) *mcp.CallToolResult {
				request := mcp.CallToolRequest{}
				request.Params.Arguments = map[string]interface{}{"profile": slug}
				result, err := proxy.handleSetProfile(ctx, request)
				require.NoError(t, err)
				return result
			}
			// (a) uniform refusal: every refused cell has the exact bytes the
			// caller gets for a profile that does not exist, slug substituted.
			refusedText := func(slug string) string {
				ctx := session()
				result := setProfile(ctx, slug)
				require.True(t, result.IsError, "%s: set_profile(%q) must be refused", row.name, slug)
				text := resultText(t, result)
				require.Equal(t, "", proxy.sessionStore.GetActiveProfile(sessionIDFromContext(ctx)), "a refusal leaves the session unchanged")
				require.Equal(t, fmt.Sprintf("unknown profile '%s'", slug), text)
				if row.base != "" && row.base != slug {
					require.NotContains(t, text, row.base, "a refusal must not name the caller's base profile (FR-018)")
				}
				return text
			}
			admitted := func(slug string) {
				ctx := session()
				result := setProfile(ctx, slug)
				require.False(t, result.IsError, "%s: set_profile(%q) must be admitted: %s", row.name, slug, resultText(t, result))
				var payload map[string]interface{}
				require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &payload))
				require.Equal(t, string(profile.SourceSession), payload["profile_source"])
				require.Equal(t, slug, payload["active_profile"])
				if row.base != "" && row.base != slug {
					require.NotContains(t, resultText(t, result), row.base, "a success must not name the caller's base profile (FR-018)")
				}
			}

			// upstream_servers visibility (tools/list filter AND the handler's
			// execution boundary agree).
			visible := func(ctx context.Context) bool {
				names := profileV3ToolNames(proxy.filterProfileV3Tools(ctx, tools))
				hidden := proxy.profileManagementToolHidden(ctx, "upstream_servers")
				got := false
				for _, name := range names {
					got = got || name == "upstream_servers"
				}
				require.Equal(t, got, !hidden, "tools/list and the execution boundary must agree")
				require.Contains(t, names, "call_tool_read")
				return got
			}
			if row.preSelect != "" {
				pre := session()
				require.False(t, setProfile(pre, row.preSelect).IsError)
				require.Equal(t, !row.hiddenWhilePreSelected, visible(pre),
					"upstream_servers while the session is on %s", row.preSelect)
			}
			require.Equal(t, row.wantUpstreamServers, visible(session()), "upstream_servers visibility")
			if !row.wantUpstreamServers {
				require.NotContains(t, profileV3ToolNames(proxy.filterProfileV3Tools(session(), tools)), "quarantine_security")
			}
			if row.writesDenied {
				assertManagementWritesDenied(t, proxy, session())
			}

			// set_profile("work-full") / ("legacy") / (own base).
			for _, cell := range []struct {
				slug string
				want bool
			}{{"work-full", row.wantWorkFull}, {"legacy", row.wantLegacy}} {
				if cell.want {
					admitted(cell.slug)
				} else {
					refusedText(cell.slug)
				}
			}
			if row.ownBase != "" {
				admitted(row.ownBase)
			}
			for _, slug := range row.extraRefused {
				refusedText(slug)
			}
			// A slug that does not exist is answered with the same shape.
			if auth.IsScopedCaller(session()) || row.base != "" {
				refusedText("no-such-profile")
			}

			// set_profile("") is never an error and returns the session to its
			// base: select something admitted first so the clear is not a no-op.
			ctx := session()
			for _, slug := range []string{"work-full", "legacy"} {
				if (slug == "work-full" && row.wantWorkFull) || (slug == "legacy" && row.wantLegacy) {
					require.False(t, setProfile(ctx, slug).IsError)
					break
				}
			}
			cleared := setProfile(ctx, "")
			require.False(t, cleared.IsError, "set_profile(\"\") is always admitted: %s", resultText(t, cleared))
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(resultText(t, cleared)), &payload))
			require.Equal(t, string(profile.SourceNone), payload["profile_source"])
			require.Equal(t, "", payload["active_profile"])
			require.Empty(t, proxy.sessionStore.GetActiveProfile(sessionIDFromContext(ctx)))

			resolved := proxy.ResolveProfileV3(ctx, idx)
			require.Equal(t, row.afterClear.name, resolved.Name, "profile after clear")
			require.Equal(t, row.afterClear.source, resolved.Source, "profile source after clear")
			require.Equal(t, row.afterClear.guarded, resolved.BindingGuarded)
			if row.afterClear.denyAll {
				require.NotNil(t, resolved.Scope)
				require.True(t, resolved.Scope.DeniesAll(), "must resolve deny-all, never fall through")
			}
			if row.afterClear.name == "" && row.afterClear.source == string(profile.SourceNone) {
				require.Nil(t, resolved.Scope)
			}
		})
	}

	// Row 15: a client locked to work-readonly whose config URL names its own
	// pin. The own-pin URL is admitted; any other profile URL is the uniform
	// 404 a nonexistent profile gets.
	t.Run("15 client locked to work-readonly, own-pin URL", func(t *testing.T) {
		proxy, _ := newProfilesV3Fixture(t)
		srv := proxy.mainServer
		srv.logger = zap.NewNop()
		srv.mcpProxy = proxy
		handler := (profileGateFleet{srv: srv, cfg: proxy.currentConfig()}).handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		get := func(slug string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/mcp/p/"+slug, http.NoBody).
				WithContext(clientCtx("cursor", "work-readonly", "locked"))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			return rec
		}
		require.Equal(t, http.StatusOK, get("work-readonly").Code, "the own-pin URL is admitted")
		other, missing := get("work-full"), get("no-such-profile")
		require.Equal(t, http.StatusNotFound, other.Code)
		require.Equal(t, missing.Code, other.Code)
		require.Equal(t, strings.ReplaceAll(missing.Body.String(), "no-such-profile", "work-full"), other.Body.String(),
			"another profile's URL answers exactly like a nonexistent one")
	})

	// Row 16 (continued): the dangling binding denies every tool call with the
	// Spec 105 out-of-scope refusal and dispatches nothing upstream.
	t.Run("16 dangling binding calls no upstream", func(t *testing.T) {
		proxy, rt := newProfilesV3Fixture(t)
		up := startCountingUpstream(t, proxy, rt, "github", readSpec("list_issues"))
		result, err := proxy.handleCallToolVariant(clientCtx("laptop", "gone", "switchable"),
			auditCallToolRequest("github:list_issues", nil), "call_tool_read")
		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Equal(t, "Server 'github' is not in scope for this agent token", resultText(t, result))
		require.Empty(t, up.dispatched())
	})

	// Row 17: a switchable client that selected work-full, then the operator
	// locks it to work-readonly. RESOLUTION only: the same session id now
	// resolves to the pin (stored selection ignored). Clearing the stored
	// selection on a binding change is 108-c T032/T040.
	t.Run("17 switchable then locked by the operator", func(t *testing.T) {
		proxy, _ := newProfilesV3Fixture(t)
		idx := proxy.profileIndexFor(proxy.currentConfig())
		const sid = "matrix-switchable-to-locked"
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]interface{}{"profile": "work-full"}
		result, err := proxy.handleSetProfile(sessionCtx(clientCtx("laptop", "work-readonly", "switchable"), sid), request)
		require.NoError(t, err)
		require.False(t, result.IsError, resultText(t, result))

		switchable := proxy.ResolveProfileV3(sessionCtx(clientCtx("laptop", "work-readonly", "switchable"), sid), idx)
		require.Equal(t, "work-full", switchable.Name)
		require.Equal(t, string(profile.SourceSession), switchable.Source)

		locked := proxy.ResolveProfileV3(sessionCtx(clientCtx("laptop", "work-readonly", "locked"), sid), idx)
		require.Equal(t, "work-readonly", locked.Name, "a locked binding ignores the stored selection")
		require.Equal(t, string(profile.SourcePin), locked.Source)

		other := mcp.CallToolRequest{}
		other.Params.Arguments = map[string]interface{}{"profile": "legacy"}
		refused, err := proxy.handleSetProfile(sessionCtx(clientCtx("laptop", "work-readonly", "locked"), sid), other)
		require.NoError(t, err)
		require.True(t, refused.IsError)
		require.Equal(t, "unknown profile 'legacy'", resultText(t, refused))
	})
}

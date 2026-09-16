package server

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// setProfileCtx builds a request context carrying a stable session id and an
// optional agent-token profile_pin. The pinned token carries the "*" server
// wildcard that REST minting defaults to (internal/httpapi/tokens.go) — an
// agent token with NO allowed_servers grants nothing under CanAccessServer, so
// a pin-only fixture would model a token that cannot see any server.
func setProfileCtx(sessionID, pin string) context.Context {
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: sessionID})
	if pin != "" {
		ctx = auth.WithAuthContext(ctx, &auth.AuthContext{Type: auth.AuthTypeAgent, ProfilePin: pin, AllowedServers: []string{"*"}})
	}
	return ctx
}

func newSetProfileTestServer() *MCPProxyServer {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "research-srv"},
			{Name: "deploy-srv"},
		},
		Profiles: []config.ProfileConfig{
			{Name: "research", Servers: []string{"research-srv"}},
			{Name: "deploy", Servers: []string{"deploy-srv"}},
			{Name: "mixed", Servers: []string{"research-srv", "deploy-srv"}},
		},
	}
	return &MCPProxyServer{
		config:       cfg,
		logger:       zap.NewNop(),
		sessionStore: NewSessionStore(zap.NewNop()),
	}
}

func callSetProfileTool(t *testing.T, p *MCPProxyServer, ctx context.Context, slug string) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = "set_profile"
	req.Params.Arguments = map[string]interface{}{"profile": slug}
	res, err := p.handleSetProfile(ctx, req)
	require.NoError(t, err)
	return res
}

func setProfileResultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, res.Content)
	b, err := json.Marshal(res.Content[0])
	require.NoError(t, err)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &m))
	text, _ := m["text"].(string)
	return text
}

// TestHandleSetProfile_PinnedRejectsOtherSlug verifies a profile-pinned agent
// token cannot switch away from its pinned profile via set_profile (Profiles v2 T3).
func TestHandleSetProfile_PinnedRejectsOtherSlug(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileCtx("sess-pinned", "research")

	res := callSetProfileTool(t, p, ctx, "deploy")
	require.True(t, res.IsError, "switching a pinned token to another profile must error")
	require.Contains(t, setProfileResultText(t, res), "pinned to profile 'research'")

	// The session selection must NOT have been changed by the rejected call.
	require.Equal(t, "", p.sessionStore.GetActiveProfile("sess-pinned"))
}

// TestHandleSetProfile_PinnedAllowsSameSlug verifies set_profile to the pinned
// profile itself succeeds.
func TestHandleSetProfile_PinnedAllowsSameSlug(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileCtx("sess-same", "research")

	res := callSetProfileTool(t, p, ctx, "research")
	require.False(t, res.IsError, "set_profile to the pinned profile must succeed: %s", setProfileResultText(t, res))

	var payload struct {
		ActiveProfile string   `json:"active_profile"`
		Servers       []string `json:"servers"`
	}
	require.NoError(t, json.Unmarshal([]byte(setProfileResultText(t, res)), &payload))
	require.Equal(t, "research", payload.ActiveProfile)
	require.Contains(t, payload.Servers, "research-srv")
}

// TestHandleSetProfile_UnpinnedUnchanged verifies tokens without a pin retain
// the existing T2 switching behaviour.
func TestHandleSetProfile_UnpinnedUnchanged(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileCtx("sess-free", "")

	res := callSetProfileTool(t, p, ctx, "deploy")
	require.False(t, res.IsError, "unpinned token must switch freely: %s", setProfileResultText(t, res))
	require.Equal(t, "deploy", p.sessionStore.GetActiveProfile("sess-free"))
}

// TestHandleSetProfile_DeletedPinDoesNotEnumerateProfiles is the Spec 104
// FR-016b regression on the MCP surface (cross-review finding): a token pinned
// to a profile that has since been deleted passes the pin check for its own
// slug and fell into the "unknown profile (available: ...)" error, which listed
// every remaining profile — profiles the pin makes unselectable. The error must
// not enumerate them; an unpinned caller still gets the list.
func TestHandleSetProfile_DeletedPinDoesNotEnumerateProfiles(t *testing.T) {
	p := newSetProfileTestServer()
	p.config.Profiles = []config.ProfileConfig{{Name: "deploy", Servers: []string{"deploy-srv"}}}

	res := callSetProfileTool(t, p, setProfileCtx("sess-deleted-pin", "research"), "research")
	require.True(t, res.IsError)
	text := setProfileResultText(t, res)
	require.Contains(t, text, "unknown profile 'research'")
	require.NotContains(t, text, "deploy", "a pinned token must not learn the other profiles' names: %s", text)

	// An unpinned AGENT identity (ProfilePin "") is a scoped caller too and
	// gets the same list-free refusal (Spec 105 PR D codex round 3: the
	// selectable list is fleet-sized work, so no scoped refusal carries it;
	// inverted from the pre-105 "unpinned callers keep the discovery
	// affordance" assertion). Proven with a real agent identity, not merely
	// the absence of an auth context, which is administrator-shaped.
	unpinnedAgent := auth.WithAuthContext(setProfileCtx("sess-unpinned", ""), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "unpinned-bot",
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead},
	})
	res = callSetProfileTool(t, p, unpinnedAgent, "research")
	require.True(t, res.IsError)
	require.Equal(t, "unknown profile 'research'", setProfileResultText(t, res))

	// An administrator-shaped caller (no auth context) keeps the discovery
	// affordance (SC-005).
	res = callSetProfileTool(t, p, setProfileCtx("sess-admin", ""), "research")
	require.True(t, res.IsError)
	require.Contains(t, setProfileResultText(t, res), "available: deploy")
}

// setProfileScopedCtx builds a request context for an UNPINNED agent token
// whose AllowedServers is restricted to the given servers (Spec 104 FR-016b).
func setProfileScopedCtx(sessionID string, allowed ...string) context.Context {
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: sessionID})
	return auth.WithAuthContext(ctx, &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: allowed})
}

// setProfileAdminCtx builds a request context for an API-key / socket admin.
func setProfileAdminCtx(sessionID string) context.Context {
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: sessionID})
	return auth.WithAuthContext(ctx, auth.AdminContext())
}

func setProfileScopedPayload(t *testing.T, res *mcp.CallToolResult) (string, []string) {
	t.Helper()
	require.False(t, res.IsError, "unexpected set_profile error: %s", setProfileResultText(t, res))
	var payload struct {
		ActiveProfile string   `json:"active_profile"`
		Servers       []string `json:"servers"`
	}
	require.NoError(t, json.Unmarshal([]byte(setProfileResultText(t, res)), &payload))
	return payload.ActiveProfile, payload.Servers
}

// TestHandleSetProfile_ScopedTokenClearReportsOnlyAllowedServers: an unpinned
// agent token restricted to one server that clears its selection must be told
// about THAT server only — not every configured server (Spec 104 FR-016b).
func TestHandleSetProfile_ScopedTokenClearReportsOnlyAllowedServers(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileScopedCtx("sess-scoped-clear", "research-srv")

	active, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.Equal(t, "", active)
	require.ElementsMatch(t, []string{"research-srv"}, servers,
		"clearing the selection must not enumerate servers outside the token's AllowedServers")
}

// TestHandleSetProfile_ScopedTokenSelectIntersectsAllowedServers: selecting a
// profile returns the profile's servers INTERSECTED with the token's
// AllowedServers, never the profile's complete set (Spec 104 FR-016b).
func TestHandleSetProfile_ScopedTokenSelectIntersectsAllowedServers(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileScopedCtx("sess-scoped-select", "research-srv")

	active, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, "mixed"))
	require.Equal(t, "mixed", active)
	require.ElementsMatch(t, []string{"research-srv"}, servers,
		"a profile's servers outside the token's AllowedServers must not be reported")
	require.Equal(t, "mixed", p.sessionStore.GetActiveProfile("sess-scoped-select"))

}

// TestHandleSetProfile_ScopedTokenDisjointProfileIndistinguishableFromUnknown:
// a profile entirely outside the token's reach must not be confirmable by
// probing its name — selecting it yields the SAME error as a nonexistent slug,
// and the session is not mutated (Spec 104 FR-016b, cross-review finding).
func TestHandleSetProfile_ScopedTokenDisjointProfileIndistinguishableFromUnknown(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileScopedCtx("sess-scoped-disjoint", "research-srv")

	// Start from a REAL prior selection so "no state change" is not satisfied
	// vacuously by an implementation that clears the session before refusing.
	require.False(t, callSetProfileTool(t, p, ctx, "research").IsError)
	require.Equal(t, "research", p.sessionStore.GetActiveProfile("sess-scoped-disjoint"))

	disjoint := callSetProfileTool(t, p, ctx, "deploy")
	unknown := callSetProfileTool(t, p, ctx, "nope")
	require.True(t, disjoint.IsError, "a disjoint profile must not be selectable")
	require.True(t, unknown.IsError)
	require.Equal(t,
		strings.ReplaceAll(setProfileResultText(t, unknown), "'nope'", "'deploy'"),
		setProfileResultText(t, disjoint),
		"disjoint and unknown slugs must produce the same error shape")
	require.NotContains(t, setProfileResultText(t, disjoint), "deploy-srv")
	require.Equal(t, "research", p.sessionStore.GetActiveProfile("sess-scoped-disjoint"),
		"a refused selection must leave the prior session selection untouched")
}

// TestHandleSetProfile_ScopedTokenDisjointProfilePresentVsAbsentIdentical is
// the differential form of the non-disclosure rule: the SAME slug, requested by
// the SAME scoped token, yields a byte-identical refusal whether the disjoint
// profile is configured or has been removed — so probing cannot confirm the
// operator has (or still has) a profile of that name.
func TestHandleSetProfile_ScopedTokenDisjointProfilePresentVsAbsentIdentical(t *testing.T) {
	withDeploy := newSetProfileTestServer()
	withoutDeploy := newSetProfileTestServer()
	withoutDeploy.config.Profiles = slices.DeleteFunc(slices.Clone(withoutDeploy.config.Profiles), func(pc config.ProfileConfig) bool {
		return pc.Name == "deploy"
	})
	require.Len(t, withoutDeploy.config.Profiles, len(withDeploy.config.Profiles)-1)

	ctx := setProfileScopedCtx("sess-scoped-differential", "research-srv")
	for _, p := range []*MCPProxyServer{withDeploy, withoutDeploy} {
		require.False(t, callSetProfileTool(t, p, ctx, "research").IsError)
	}

	present := callSetProfileTool(t, withDeploy, ctx, "deploy")
	absent := callSetProfileTool(t, withoutDeploy, ctx, "deploy")
	require.True(t, present.IsError)
	require.True(t, absent.IsError)
	require.Equal(t, setProfileResultText(t, absent), setProfileResultText(t, present),
		"a configured-but-unreachable profile must be refused exactly like an absent one")
	for _, p := range []*MCPProxyServer{withDeploy, withoutDeploy} {
		require.Equal(t, "research", p.sessionStore.GetActiveProfile("sess-scoped-differential"))
	}
}

// TestHandleSetProfile_EmptyAllowlistTokenSeesNothing: an agent token whose
// AllowedServers is EMPTY grants nothing under CanAccessServer (the predicate
// serverInScope applies to retrieve_tools), so set_profile must report the
// same empty reach rather than read "empty" as "unrestricted".
func TestHandleSetProfile_EmptyAllowlistTokenSeesNothing(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileScopedCtx("sess-empty-allow")

	_, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.Empty(t, servers)

	// An EXISTING profile is just as unselectable as a nonexistent one for a
	// token that can reach no server: same refusal, nothing disclosed, no
	// session mutation.
	existing := callSetProfileTool(t, p, ctx, "research")
	unknown := callSetProfileTool(t, p, ctx, "nope")
	require.True(t, existing.IsError, "an empty-allowlist token must not select any profile")
	require.True(t, unknown.IsError)
	require.Equal(t,
		strings.ReplaceAll(setProfileResultText(t, unknown), "'nope'", "'research'"),
		setProfileResultText(t, existing))
	require.Equal(t, "", p.sessionStore.GetActiveProfile("sess-empty-allow"))
	text := setProfileResultText(t, unknown)
	for _, name := range []string{"research", "deploy", "mixed"} {
		require.NotContains(t, text, name)
	}
}

// TestHandleSetProfile_ServerEditionUserScopedLikeVisibility: a server-edition
// "user" context with a server allowlist is scoped by serverInScope exactly
// like an agent token, so set_profile applies the same bound.
func TestHandleSetProfile_ServerEditionUserScopedLikeVisibility(t *testing.T) {
	p := newSetProfileTestServer()
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: "sess-user"})
	ctx = auth.WithAuthContext(ctx, &auth.AuthContext{Type: auth.AuthTypeUser, AllowedServers: []string{"deploy-srv"}})

	_, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.ElementsMatch(t, []string{"deploy-srv"}, servers)
}

// TestHandleSetProfile_ScopedTokenUnknownSlugDoesNotEnumerateAllProfiles: the
// invalid-selection error for an agent token never names a profile outside
// its reach (Spec 104 FR-016b) — and since Spec 105 PR D (codex round 3) it
// names no profile at all: the selectable list is one reach computation per
// configured profile, fleet-sized work a non-disclosing refusal may not do,
// so the pre-105 "profiles overlapping the token's scope stay listed"
// assertion is inverted here. Administrators keep the list (SC-005,
// TestHandleSetProfile_AdminUnknownSlugKeepsAvailableList).
func TestHandleSetProfile_ScopedTokenUnknownSlugDoesNotEnumerateAllProfiles(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileScopedCtx("sess-scoped-unknown", "research-srv")
	require.False(t, callSetProfileTool(t, p, ctx, "mixed").IsError)

	res := callSetProfileTool(t, p, ctx, "nope")
	require.True(t, res.IsError)
	text := setProfileResultText(t, res)
	require.Equal(t, "unknown profile 'nope'", text, "a scoped refusal names no profile, selectable or not")
	require.NotContains(t, text, "deploy", "a profile fully outside the token's scope must not be disclosed")
	require.Equal(t, "mixed", p.sessionStore.GetActiveProfile("sess-scoped-unknown"),
		"a refused selection must leave the prior session selection untouched")
}

// TestHandleSetProfile_StalePinUnknownSlugDisclosesNoProfiles: a token pinned
// to a profile that has since been removed reaches the unknown-slug branch
// (slug == pin passes the pin guard); the error must not enumerate the
// configured profiles it can never select.
func TestHandleSetProfile_StalePinUnknownSlugDisclosesNoProfiles(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileCtx("sess-stale-pin", "gone")
	// A selection recorded before the pin went stale must survive the refusal.
	p.sessionStore.SetActiveProfile("sess-stale-pin", "gone")

	res := callSetProfileTool(t, p, ctx, "gone")
	require.True(t, res.IsError)
	text := setProfileResultText(t, res)
	require.Equal(t, "unknown profile 'gone'", text, "a removed pin is refused with the list-free scoped body")
	for _, name := range []string{"research", "deploy", "mixed"} {
		require.NotContains(t, text, name)
	}
	require.Equal(t, "gone", p.sessionStore.GetActiveProfile("sess-stale-pin"),
		"a refused selection must leave the prior session selection untouched")
}

// TestHandleSetProfile_PinnedTokenClearIntersectsAllowedServers: a pinned token
// whose AllowedServers is narrower than its pin sees the intersection in
// `servers`, while `active_profile` reports the STORED selection — "" after a
// clear, not the pin (Spec 105 FR-003, FR003-G7; inverted from the pre-105
// expectation that the pin name was echoed as the active profile).
func TestHandleSetProfile_PinnedTokenClearIntersectsAllowedServers(t *testing.T) {
	p := newSetProfileTestServer()
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: "sess-pin-narrow"})
	ctx = auth.WithAuthContext(ctx, &auth.AuthContext{
		Type: auth.AuthTypeAgent, ProfilePin: "mixed", AllowedServers: []string{"deploy-srv"},
	})

	active, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.Equal(t, "", active, "a cleared selection is reported as cleared even under a pin")
	require.ElementsMatch(t, []string{"deploy-srv"}, servers)
}

// newSetProfileOrderingTestServer is the SC-005 byte-parity fixture: five
// servers declared in a non-alphabetical order and a profile that lists a
// subset in ITS OWN order (with one repeated name — a legal, unvalidated
// configuration). The pre-105 payload rendered `match.EffectiveServers(cfg)`
// (profile-declared order, duplicates kept) on select and `allServerNames`
// (config order) on clear; a set-backed rendering (Go map iteration) is
// nondeterministic on ≥3 names and dedupes, so `require.Equal` against these
// exact slices is what an ElementsMatch on a 2-server profile could not catch
// (PR D critique round 1, finding A1).
func newSetProfileOrderingTestServer() *MCPProxyServer {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "zeta-srv"},
			{Name: "alpha-srv"},
			{Name: "mid-srv"},
			{Name: "beta-srv"},
			{Name: "omega-srv"},
		},
		Profiles: []config.ProfileConfig{
			{Name: "wide", Servers: []string{"omega-srv", "alpha-srv", "mid-srv", "alpha-srv", "zeta-srv"}},
			{Name: "narrow", Servers: []string{"mid-srv"}},
		},
	}
	return &MCPProxyServer{
		config:       cfg,
		logger:       zap.NewNop(),
		sessionStore: NewSessionStore(zap.NewNop()),
	}
}

// Exact pre-105 renderings for the ordering fixture.
var (
	orderingAllServers  = []string{"zeta-srv", "alpha-srv", "mid-srv", "beta-srv", "omega-srv"}
	orderingWideServers = []string{"omega-srv", "alpha-srv", "mid-srv", "alpha-srv", "zeta-srv"}
)

// TestHandleSetProfile_AdminUnchanged pins the administrator (API-key / socket)
// behaviour byte-for-byte (SC-005): config-ordered full server list on clear,
// the profile's complete set in profile-declared order (duplicates kept) on
// select, and every configured profile in the unknown-slug error.
func TestHandleSetProfile_AdminUnchanged(t *testing.T) {
	p := newSetProfileOrderingTestServer()
	ctx := setProfileAdminCtx("sess-admin")

	_, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.Equal(t, orderingAllServers, servers, "clear must report every server in config order")

	active, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, "wide"))
	require.Equal(t, "wide", active)
	require.Equal(t, orderingWideServers, servers, "select must report the profile's servers in profile-declared order")

	res := callSetProfileTool(t, p, ctx, "nope")
	require.True(t, res.IsError)
	text := setProfileResultText(t, res)
	for _, name := range []string{"wide", "narrow"} {
		require.Contains(t, text, name)
	}
}

// TestHandleSetProfile_AdminURLScopeUnchanged (SC-005, PR D critique round 1
// finding A2): FR-003's "URL still governs the reported servers" is an
// agent-token rule. An administrator (or anonymous back-compat caller) on a
// /mcp/p/<slug> endpoint keeps the pre-105 payload — the SELECTED profile's
// servers on select and every server on clear — because SC-005 names no
// FR-003 exception for administrators.
func TestHandleSetProfile_AdminURLScopeUnchanged(t *testing.T) {
	p := newSetProfileOrderingTestServer()
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: "sess-admin-url"})
	ctx = auth.WithAuthContext(ctx, auth.AdminContext())
	ctx = profile.WithProfileScope(ctx, p.profileScopeForSlug("narrow"))

	active, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, "wide"))
	require.Equal(t, "wide", active)
	require.Equal(t, "wide", p.sessionStore.GetActiveProfile("sess-admin-url"))
	require.Equal(t, orderingWideServers, servers, "an administrator keeps the selected profile's servers on a URL-scoped endpoint")

	active, servers = setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.Equal(t, "", active)
	require.Equal(t, orderingAllServers, servers, "an administrator keeps the full server list on clear, URL or not")
}

// TestHandleSetProfile_WildcardTokenUnchanged: an agent token with the "*"
// wildcard is unrestricted by servers and keeps the full listings — in the
// same deterministic order an administrator sees (SC-005 control). Its
// REFUSAL body is a scoped caller's (spec "Unrestricted agent tokens": their
// refusal bodies change where this spec changes error shapes): list-free
// since PR D codex round 3, inverted from the pre-105 `available:` form.
func TestHandleSetProfile_WildcardTokenUnchanged(t *testing.T) {
	p := newSetProfileOrderingTestServer()
	ctx := setProfileScopedCtx("sess-wild", "*")

	_, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.Equal(t, orderingAllServers, servers)

	_, servers = setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, "wide"))
	require.Equal(t, orderingWideServers, servers)

	res := callSetProfileTool(t, p, ctx, "nope")
	require.True(t, res.IsError)
	require.Equal(t, "unknown profile 'nope'", setProfileResultText(t, res))
}

// TestHandleSetProfile_ScopedServersKeepProfileOrder: a restricted token's
// effective list is the profile's servers ∩ allowed_servers rendered in the
// profile-declared order every other consumer of EffectiveServers uses — not
// set iteration order — including on a URL-scoped endpoint where the URL
// profile governs.
func TestHandleSetProfile_ScopedServersKeepProfileOrder(t *testing.T) {
	p := newSetProfileOrderingTestServer()
	ctx := setProfileScopedCtx("sess-scoped-order", "zeta-srv", "mid-srv", "omega-srv", "beta-srv")

	_, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, "wide"))
	require.Equal(t, []string{"omega-srv", "mid-srv", "zeta-srv"}, servers)

	_, servers = setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.Equal(t, []string{"zeta-srv", "mid-srv", "beta-srv", "omega-srv"}, servers, "clear reports allowed servers in config order")

	urlCtx := profile.WithProfileScope(ctx, p.profileScopeForSlug("wide"))
	_, servers = setProfileScopedPayload(t, callSetProfileTool(t, p, urlCtx, "narrow"))
	require.Equal(t, []string{"omega-srv", "mid-srv", "zeta-srv"}, servers, "the URL profile governs, in its declared order")
}

// TestHandleSetProfile_PinnedTokenSelectsDisjointPin is the INVERTED #1225 F2
// admission decision (Spec 105 FR-003, research D1): a configured pin whose
// servers are disjoint from the token's AllowedServers has zero reach and is
// NOT selectable by its own token — admitting it (with an empty reach) told the
// token that its pin still exists, an existence oracle a deleted pin does not
// give. The refusal is the deleted-pin body and the session is not mutated.
func TestHandleSetProfile_PinnedTokenSelectsDisjointPin(t *testing.T) {
	p := newSetProfileTestServer()
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: "sess-pin-disjoint"})
	ctx = auth.WithAuthContext(ctx, &auth.AuthContext{
		Type: auth.AuthTypeAgent, ProfilePin: "deploy", AllowedServers: []string{"research-srv"},
	})

	res := callSetProfileTool(t, p, ctx, "deploy")
	require.True(t, res.IsError, "a zero-reach pin must not be selectable: %s", setProfileResultText(t, res))
	text := setProfileResultText(t, res)
	require.Contains(t, text, "unknown profile 'deploy'")
	require.NotContains(t, text, "deploy-srv", "the refusal must not name servers outside the token's reach")
	require.NotContains(t, text, "pinned", "the refusal must not confirm the pin")
	require.Equal(t, "", p.sessionStore.GetActiveProfile("sess-pin-disjoint"),
		"a refused selection must leave the session untouched")
}

// TestSetProfileFixtureIsLoadable guards the shared fixture against slugs that
// config.ValidateProfiles rejects at load time (reserved names such as "all"):
// direct handler construction bypasses validation, so without this check the
// suite could exercise a configuration no real deployment can load.
func TestSetProfileFixtureIsLoadable(t *testing.T) {
	p := newSetProfileTestServer()
	warnings, err := config.ValidateProfiles(p.config)
	require.NoError(t, err, "the set_profile fixture must be a loadable profile configuration")
	require.Empty(t, warnings)
}

// newSetProfileTestServerWithEmptyProfiles extends the shared fixture with the
// two ways a profile ends up with an empty effective server set: `empty`
// declares no servers (the deny-all placeholder ValidateProfiles allows with a
// warning) and `ghost` names only a server that is not configured, so
// warn-and-skip leaves EffectiveServers empty. Both load in a real deployment.
func newSetProfileTestServerWithEmptyProfiles(t *testing.T) *MCPProxyServer {
	t.Helper()
	p := newSetProfileTestServer()
	p.config.Profiles = append(slices.Clone(p.config.Profiles),
		config.ProfileConfig{Name: "empty"},
		config.ProfileConfig{Name: "ghost", Servers: []string{"missing-srv"}},
	)
	warnings, err := config.ValidateProfiles(p.config)
	require.NoError(t, err, "an empty profile is a legal (warned) configuration")
	require.Len(t, warnings, 2)
	return p
}

// TestHandleSetProfile_WildcardTokenRefusesEmptyProfileAdminSelectsIt locks
// the Spec 105 "Unrestricted agent tokens" exception (1): an unpinned agent
// token with the "*" wildcard is still a scoped caller, so a profile whose
// effective server set is empty intersects nothing it can reach and is refused
// exactly like an unknown slug — non-disclosing, prior selection preserved —
// while an administrator keeps selecting it (deny-all placeholder). A shortcut
// that admits every configured profile to wildcard tokens would fail here.
func TestHandleSetProfile_WildcardTokenRefusesEmptyProfileAdminSelectsIt(t *testing.T) {
	for _, slug := range []string{"empty", "ghost"} {
		t.Run(slug, func(t *testing.T) {
			p := newSetProfileTestServerWithEmptyProfiles(t)

			agent := setProfileScopedCtx("sess-wild-"+slug, "*")
			require.False(t, callSetProfileTool(t, p, agent, "research").IsError)

			refused := callSetProfileTool(t, p, agent, slug)
			require.True(t, refused.IsError, "a wildcard agent must not select a profile with no reachable servers")
			unknown := callSetProfileTool(t, p, agent, "nope")
			require.True(t, unknown.IsError)
			require.Equal(t,
				strings.ReplaceAll(setProfileResultText(t, unknown), "'nope'", "'"+slug+"'"),
				setProfileResultText(t, refused),
				"an empty profile must be refused exactly like a nonexistent one")
			// The error echoes the caller's own slug and nothing else: a
			// scoped refusal carries no `available:` list at all (Spec 105
			// PR D codex round 3; inverted from the list-carrying form).
			require.Equal(t, "unknown profile '"+slug+"'", setProfileResultText(t, refused))
			require.Equal(t, "research", p.sessionStore.GetActiveProfile("sess-wild-"+slug),
				"a refused selection must leave the prior session selection untouched")

			admin := setProfileAdminCtx("sess-admin-" + slug)
			active, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, admin, slug))
			require.Equal(t, slug, active)
			require.Empty(t, servers)
			require.Equal(t, slug, p.sessionStore.GetActiveProfile("sess-admin-"+slug))
		})
	}
}

// ---------------------------------------------------------------------------
// Spec 105 PR D (FR-003): URL precedence in the set_profile payload (G6) and
// the pinned zero-reach refusal (G8, research D1).
// ---------------------------------------------------------------------------

// setProfileURLScopedCtx builds the context of a set_profile call arriving on
// /mcp/p/<slug>: an unpinned agent token allowed BOTH servers, with the URL
// profile scope the middleware injects for that request.
func setProfileURLScopedCtx(p *MCPProxyServer, sessionID, urlSlug string) context.Context {
	ctx := setProfileScopedCtx(sessionID, "research-srv", "deploy-srv")
	scope := p.profileScopeForSlug(urlSlug)
	if scope == nil {
		panic("setProfileURLScopedCtx: fixture profile " + urlSlug + " is not configured")
	}
	return profile.WithProfileScope(ctx, scope)
}

// TestHandleSetProfile_URLScopeGovernsReportedServers (FR003-G6): on a
// URL-scoped endpoint the selection is stored, but the URL still governs the
// request (resolveActiveProfile: pin > URL > session). `active_profile`
// therefore reports the stored selection while `servers` reports the
// EFFECTIVE scope — URL profile ∩ token — not the selected profile's servers
// (spec.md "URL precedence"). Selecting the disjoint `deploy` profile on
// /mcp/p/research reports research ∩ token, and clearing the selection on the
// same endpoint reports the same URL scope, never every allowed server.
func TestHandleSetProfile_URLScopeGovernsReportedServers(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileURLScopedCtx(p, "sess-url-research", "research")

	active, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, "deploy"))
	require.Equal(t, "deploy", active, "active_profile reports the STORED selection")
	require.Equal(t, "deploy", p.sessionStore.GetActiveProfile("sess-url-research"), "the selection must still be stored")
	require.ElementsMatch(t, []string{"research-srv"}, servers,
		"servers must report the URL profile ∩ token, which governs this request — not the selected profile")

	active, servers = setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.Equal(t, "", active)
	require.Equal(t, "", p.sessionStore.GetActiveProfile("sess-url-research"))
	require.ElementsMatch(t, []string{"research-srv"}, servers,
		"clearing on a URL-scoped endpoint must still report the URL scope, not all allowed servers")

	// Selecting the URL's own profile is the degenerate case: both agree.
	active, servers = setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, "mixed"))
	require.Equal(t, "mixed", active)
	require.ElementsMatch(t, []string{"research-srv"}, servers,
		"mixed ∩ URL research ∩ token = research-srv only")
}

// TestHandleSetProfile_PinnedZeroReachRefusedLikeDeletedPin (FR003-G8,
// research D1): a pinned token whose pin exists but has zero reach — an empty
// profile, a ghost profile, or a disjoint grant — must not be able to select
// its pin: the refusal is byte-identical (slug-normalised) to the one a
// DELETED pin produces, so the token cannot learn whether its pin still
// exists, and the session is not mutated. Inverts the #1225 F2 admission
// (`TestHandleSetProfile_PinnedTokenSelectsDisjointPin`).
func TestHandleSetProfile_PinnedZeroReachRefusedLikeDeletedPin(t *testing.T) {
	cases := []struct {
		name    string
		pin     string
		allowed []string
	}{
		{name: "empty-profile", pin: "empty", allowed: []string{"*"}},
		{name: "ghost-profile", pin: "ghost", allowed: []string{"*"}},
		{name: "disjoint-grant", pin: "deploy", allowed: []string{"research-srv"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newSetProfileTestServerWithEmptyProfiles(t)
			helper := mcpserver.NewMCPServer("test", "1.0.0")
			sid := "sess-zero-reach-" + tc.name
			ctx := helper.WithContext(context.Background(), &fakeClientSession{id: sid})
			ctx = auth.WithAuthContext(ctx, &auth.AuthContext{
				Type: auth.AuthTypeAgent, ProfilePin: tc.pin, AllowedServers: tc.allowed,
			})
			// A prior selection no code path on this request would write, so
			// "no mutation" cannot be satisfied by re-storing the same value.
			p.sessionStore.SetActiveProfile(sid, "mixed")

			refused := callSetProfileTool(t, p, ctx, tc.pin)
			require.True(t, refused.IsError, "a zero-reach pin must not be selectable: %s", setProfileResultText(t, refused))
			require.Equal(t, "mixed", p.sessionStore.GetActiveProfile(sid),
				"a refused selection must leave the prior session selection untouched")

			// Oracle: the same token shape with a DELETED pin.
			deletedCtx := helper.WithContext(context.Background(), &fakeClientSession{id: sid + "-deleted"})
			deletedCtx = auth.WithAuthContext(deletedCtx, &auth.AuthContext{
				Type: auth.AuthTypeAgent, ProfilePin: "gone", AllowedServers: tc.allowed,
			})
			deleted := callSetProfileTool(t, p, deletedCtx, "gone")
			require.True(t, deleted.IsError)
			require.Equal(t,
				strings.ReplaceAll(setProfileResultText(t, deleted), "'gone'", "'<slug>'"),
				strings.ReplaceAll(setProfileResultText(t, refused), "'"+tc.pin+"'", "'<slug>'"),
				"a zero-reach pin must be refused with the deleted-pin body")
		})
	}
}

// TestResolveActiveProfileIn_UsesGivenSnapshot (PR D critique round 1,
// findings S4/N7): handleSetProfile captures ONE config snapshot for the
// admission check and must render its payload from that same snapshot, so a
// hot reload between the two cannot report `active_profile: "<slug>"` beside
// a scope (or a stale-drop) computed from a different config. The resolver
// therefore takes the snapshot explicitly: a session selection present in the
// given snapshot resolves from it even when the live config no longer has it,
// and the selection is not dropped as stale.
func TestResolveActiveProfileIn_UsesGivenSnapshot(t *testing.T) {
	p := newSetProfileTestServer()
	snapshot := p.config
	live := &config.Config{Servers: snapshot.Servers} // profiles gone from the live config
	p.config = live

	ctx := setProfileScopedCtx("sess-snapshot", "*")
	p.sessionStore.SetActiveProfile("sess-snapshot", "mixed")

	name, scope := p.resolveActiveProfileIn(ctx, snapshot)
	require.Equal(t, "mixed", name)
	require.NotNil(t, scope)
	require.ElementsMatch(t, []string{"research-srv", "deploy-srv"}, scope.AllowedServerNames())
	require.Equal(t, "mixed", p.sessionStore.GetActiveProfile("sess-snapshot"),
		"a selection the snapshot still knows must not be dropped as stale")

	// The live-config entry point keeps its behaviour: the profile is gone
	// there, so the selection is stale and resolution falls through to none.
	name, scope = p.resolveActiveProfile(ctx)
	require.Equal(t, "", name)
	require.Nil(t, scope)
	require.Equal(t, "", p.sessionStore.GetActiveProfile("sess-snapshot"))

	// A pinned token resolves its pin from the snapshot too.
	pinned := setProfileCtx("sess-snapshot-pin", "research")
	name, scope = p.resolveActiveProfileIn(pinned, snapshot)
	require.Equal(t, "research", name)
	require.Equal(t, []string{"research-srv"}, scope.AllowedServerNames())
}

// selectableProbeConfig builds a fleet of n+1 profiles: "pin" (reaching
// "pin-srv") FIRST, followed by n profiles that reach only "other-srv". Placing
// the pin first is the adversarial layout for an early-returning predicate:
// a reachable pin would answer after one iteration while a deleted or
// zero-reach pin would walk the whole slice.
func selectableProbeConfig(n int) *config.Config {
	cfg := &config.Config{Servers: []*config.ServerConfig{{Name: "pin-srv"}, {Name: "other-srv"}}}
	cfg.Profiles = append(cfg.Profiles, config.ProfileConfig{Name: "pin", Servers: []string{"pin-srv"}})
	for i := 0; i < n; i++ {
		cfg.Profiles = append(cfg.Profiles, config.ProfileConfig{Name: fmt.Sprintf("p%d", i), Servers: []string{"other-srv"}})
	}
	return cfg
}

func selectablePinnedCtx(pin string, allowed ...string) context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAgent, ProfilePin: pin, AllowedServers: allowed})
}

// TestSelectableProfileNames_PinOutcomesDoSameWork (Spec 105 PR D codex round
// 1, finding 1): profileMiddleware answers every scoped refusal through one
// constructor so status and body cannot tell "pin exists but the URL names
// another slug" from "pin deleted" or "pin has zero reach" — but the predicate
// it consults must not tell them apart by the WORK it does either (spec
// Definitions: non-disclosing = status, body AND timing class). An early
// return on the first reachable pin made a pinned caller's refusal cost one
// iteration when the pin was alive and a full slice walk when it was gone.
//
// The oracle here is deterministic, not wall-clock: the allocation profile of
// one predicate call is identical for every pin outcome over the same fleet
// (the early-returning version allocated 2 / 0 / 1 times respectively).
func TestSelectableProfileNames_PinOutcomesDoSameWork(t *testing.T) {
	const n = 64
	alive := selectableProbeConfig(n)
	deleted := selectableProbeConfig(n)
	deleted.Profiles[0].Name = "was-the-pin" // same fleet size, the pin is gone

	cases := map[string]struct {
		ctx context.Context
		cfg *config.Config
	}{
		"reachable pin first":  {selectablePinnedCtx("pin", "pin-srv"), alive},
		"zero-reach pin first": {selectablePinnedCtx("pin", "other-srv"), alive},
		"deleted pin":          {selectablePinnedCtx("pin", "pin-srv"), deleted},
	}
	allocs := map[string]float64{}
	for name, c := range cases {
		allocs[name] = testing.AllocsPerRun(20, func() { selectableProfileNames(c.ctx, c.cfg) })
	}
	for name, got := range allocs {
		require.Equal(t, allocs["reachable pin first"], got, "%s must allocate exactly like a reachable pin: %v", name, allocs)
	}
}

// TestForEachProfileSelectable_VisitsEveryProfileRegardlessOfOutcome pins the
// structural guarantee behind the allocation parity above: the predicate
// visits every configured profile, in order, for every caller kind and every
// pin outcome — never one iteration for a live pin and the whole slice for a
// dead one. A traversal counter, not a clock.
func TestForEachProfileSelectable_VisitsEveryProfileRegardlessOfOutcome(t *testing.T) {
	const n = 8
	cfg := selectableProbeConfig(n) // "pin" first, then p0..p7 reaching other-srv
	want := make([]string, 0, n+1)
	for i := range cfg.Profiles {
		want = append(want, cfg.Profiles[i].Name)
	}

	cases := map[string]struct {
		ctx        context.Context
		selectable []string
	}{
		"admin":                    {auth.WithAuthContext(context.Background(), auth.AdminContext()), want},
		"absent context":           {context.Background(), want},
		"scoped, pin-srv only":     {setProfileScopedCtx("s", "pin-srv"), []string{"pin"}},
		"scoped, empty allowlist":  {setProfileScopedCtx("s"), []string{}},
		"reachable pin first":      {selectablePinnedCtx("pin", "pin-srv"), []string{"pin"}},
		"zero-reach pin first":     {selectablePinnedCtx("pin", "other-srv"), []string{}},
		"deleted pin":              {selectablePinnedCtx("gone", "pin-srv", "other-srv"), []string{}},
		"wildcard pin on last one": {selectablePinnedCtx("p7", "*"), []string{"p7"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			visited := make([]string, 0, n+1)
			picked := []string{}
			forEachProfileSelectable(c.ctx, cfg, func(profileName string, selectable bool) {
				visited = append(visited, profileName)
				if selectable {
					picked = append(picked, profileName)
				}
			})
			require.Equal(t, want, visited, "every profile must be visited exactly once, in configured order")
			require.Equal(t, c.selectable, picked)
			require.Equal(t, c.selectable, selectableProfileNames(c.ctx, cfg))
		})
	}

	// A nil config visits nothing (and selectableProfileNames stays nil).
	forEachProfileSelectable(context.Background(), nil, func(string, bool) { t.Fatal("visited a profile of a nil config") })
	require.Nil(t, selectableProfileNames(context.Background(), nil))
}

// TestProfileIndex_SelectableAllocatesNothing (Spec 105 PR D codex round 2,
// finding 1): the URL gate's predicate does constant, allocation-free work —
// zero allocations for every refusal and admission branch, over a fleet of
// one profile and of 4 097 — so a scoped caller's refusal cannot reveal how
// many other profiles exist. Pure function, so exact and retry-free.
func TestProfileIndex_SelectableAllocatesNothing(t *testing.T) {
	fleets := map[string]*profileIndex{
		"no profiles": newProfileIndex(&config.Config{Servers: []*config.ServerConfig{{Name: "pin-srv"}, {Name: "other-srv"}}}),
		"pin only":    newProfileIndex(selectableProbeConfig(0)),
		"4096 others": newProfileIndex(selectableProbeConfig(4096)),
		"nil config":  newProfileIndex(nil),
	}
	cases := map[string]struct {
		ctx  context.Context
		slug string
	}{
		"pin mismatch, absent slug":   {selectablePinnedCtx("pin", "pin-srv"), "nope"},
		"pin mismatch, existing slug": {selectablePinnedCtx("pin", "pin-srv"), "p0"},
		"deleted pin":                 {selectablePinnedCtx("gone", "pin-srv"), "gone"},
		"zero-reach pin":              {selectablePinnedCtx("pin", "other-srv"), "pin"},
		"reachable pin":               {selectablePinnedCtx("pin", "pin-srv"), "pin"},
		"scoped, absent slug":         {setProfileScopedCtx("s", "pin-srv"), "nope"},
		"scoped, disjoint slug":       {setProfileScopedCtx("s", "pin-srv"), "p0"},
		"scoped, reachable slug":      {setProfileScopedCtx("s", "pin-srv"), "pin"},
		"scoped, empty allowlist":     {setProfileScopedCtx("s"), "pin"},
		"admin":                       {auth.WithAuthContext(context.Background(), auth.AdminContext()), "p0"},
		"absent context":              {context.Background(), "nope"},
		"empty slug":                  {selectablePinnedCtx("pin", "pin-srv"), ""},
	}
	for fleet, idx := range fleets {
		for name, c := range cases {
			allocs := testing.AllocsPerRun(50, func() { idx.selectable(c.ctx, c.slug) })
			require.Zero(t, allocs, "%s over fleet %q must not allocate", name, fleet)
		}
	}
}

// TestProfileIndex_SelectableMatchesSelectableProfileNames pins the O(1)
// predicate to the list predicate it replaces on the URL gate: for every
// caller kind and every slug (configured, absent, empty, the pin, an empty
// and a ghost profile), profileIndex.selectable answers exactly
// "slug ∈ selectableProfileNames" — the two are one rule, so the gate and
// set_profile can never disagree on admission. (Duplicate slugs are outside
// the contract: ValidateProfiles refuses to load them; the index resolves the
// first occurrence like every other lookup in this package.)
func TestProfileIndex_SelectableMatchesSelectableProfileNames(t *testing.T) {
	cfg := selectableProbeConfig(4) // pin, p0..p3
	cfg.Profiles = append(cfg.Profiles,
		config.ProfileConfig{Name: "empty"},
		config.ProfileConfig{Name: "ghost", Servers: []string{"missing-srv"}},
		config.ProfileConfig{Name: "both", Servers: []string{"pin-srv", "other-srv"}},
	)
	_, err := config.ValidateProfiles(cfg)
	require.NoError(t, err, "fixture must be a loadable profile set")
	idx := newProfileIndex(cfg)

	callers := map[string]context.Context{
		"admin":                   auth.WithAuthContext(context.Background(), auth.AdminContext()),
		"absent context":          context.Background(),
		"scoped, pin-srv":         setProfileScopedCtx("s", "pin-srv"),
		"scoped, other-srv":       setProfileScopedCtx("s", "other-srv"),
		"scoped, wildcard":        setProfileScopedCtx("s", "*"),
		"scoped, empty allowlist": setProfileScopedCtx("s"),
		"reachable pin":           selectablePinnedCtx("pin", "pin-srv"),
		"zero-reach pin":          selectablePinnedCtx("pin", "other-srv"),
		"deleted pin":             selectablePinnedCtx("gone", "*"),
		"pinned to empty":         selectablePinnedCtx("empty", "*"),
		"pinned to ghost":         selectablePinnedCtx("ghost", "*"),
	}
	slugs := []string{"pin", "p0", "p1", "p3", "empty", "ghost", "both", "nope", "", "gone", "missing-srv"}
	for caller, ctx := range callers {
		list := selectableProfileNames(ctx, cfg)
		for _, slug := range slugs {
			require.Equal(t, slices.Contains(list, slug), idx.selectable(ctx, slug), "%s asking for %q (list: %v)", caller, slug, list)
		}
	}

	// A nil snapshot selects nothing for anyone.
	nilIdx := newProfileIndex(nil)
	for caller, ctx := range callers {
		require.False(t, nilIdx.selectable(ctx, "pin"), "%s over a nil config", caller)
	}
	require.Nil(t, nilIdx.lookup("pin"))
}

// TestProfileIndexCache_BuiltOncePerSnapshot: the cache keys on the snapshot
// pointer — the same *Config hands back the same index, a new snapshot (a
// reload always publishes a new pointer) rebuilds it, and the index reflects
// the snapshot it was built from.
func TestProfileIndexCache_BuiltOncePerSnapshot(t *testing.T) {
	var cache profileIndexCache
	first := selectableProbeConfig(2)
	idx := cache.For(first)
	require.Same(t, idx, cache.For(first))
	require.Equal(t, &first.Profiles[0], idx.lookup("pin"))
	require.Equal(t, &first.Profiles[2], idx.lookup("p1"))
	require.Nil(t, idx.lookup("p2"))

	second := selectableProbeConfig(3)
	next := cache.For(second)
	require.NotSame(t, idx, next, "a new snapshot must rebuild the index")
	require.Equal(t, &second.Profiles[3], next.lookup("p2"))
	require.Same(t, next, cache.For(second))

	require.Nil(t, cache.For(nil).lookup("pin"), "a nil snapshot yields an empty index")
}

// ---------------------------------------------------------------------------
// Spec 105 PR D codex round 3.
// ---------------------------------------------------------------------------

// setProfilePinnedCtx builds a session-bearing request context for an agent
// token pinned to pin with the given AllowedServers.
func setProfilePinnedCtx(sessionID, pin string, allowed ...string) context.Context {
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: sessionID})
	return auth.WithAuthContext(ctx, &auth.AuthContext{Type: auth.AuthTypeAgent, ProfilePin: pin, AllowedServers: allowed})
}

// TestProfileIndex_ReachIsPrecomputedAtBuild (Spec 105 PR D codex round 3,
// finding 1): the reach behind the selectable predicate must be fixed when
// the index is built, never derived from the candidate profile's declared
// list at request time. A reach that scanned the declared list for every
// configured server cost nothing for a profile the snapshot lacks (nil list)
// and |servers| × |declared| for one it has — so a pinned token asking for
// its own zero-reach pin could tell "pin deleted" from "pin exists" by the
// work its uniform refusal cost (9.3 ms vs 3.2 µs over 4 096 servers on the
// tree before this fix), which research D1 forbids.
//
// The witness is the mechanism, not a clock: once the index is built, the
// declared list is not consulted any more — emptying it changes nothing for
// the built index and everything for a fresh one.
func TestProfileIndex_ReachIsPrecomputedAtBuild(t *testing.T) {
	const n = 4096
	cfg := &config.Config{}
	declared := make([]string, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("srv%d", i)
		cfg.Servers = append(cfg.Servers, &config.ServerConfig{Name: name})
		declared = append(declared, name)
	}
	cfg.Profiles = []config.ProfileConfig{{Name: "pin", Servers: declared}}
	idx := newProfileIndex(cfg)

	reachable := selectablePinnedCtx("pin", "srv4095")
	zeroReach := selectablePinnedCtx("pin", "nowhere")
	require.True(t, idx.selectable(reachable, "pin"))
	require.False(t, idx.selectable(zeroReach, "pin"))

	cfg.Profiles[0].Servers = nil
	require.True(t, idx.selectable(reachable, "pin"),
		"reach must be read from the index built at snapshot time, not from the declared list walked per request")
	require.False(t, newProfileIndex(cfg).selectable(reachable, "pin"),
		"sanity: a fresh index over the emptied profile has no reach")
}

// TestHandleSetProfile_ScopedRefusalTouchesOnlySlugAndPin (Spec 105 PR D
// codex round 3, finding 2): a scoped caller's set_profile refusal must not
// enumerate the fleet. Deciding the requested slug through the whole
// selectable list — one reach computation per configured profile — cost
// 0.3 µs over a one-profile fleet and 63 µs over 4 097 on the tree before
// this fix, with a byte-identical body: a fleet-population timing oracle
// (spec Definitions: non-disclosing = status, body AND timing class). The
// refusal now decides the requested slug alone through the per-snapshot
// index and carries no `available:` list for scoped callers (the list is
// fleet-sized work by definition; administrators keep it, SC-005).
//
// Traversal-counter seam: through the index's lookup hook, every scoped
// refusal over every fleet resolves at most the requested slug and the
// caller's pin — never a third profile — and the body is the one format
// string, so neither work nor bytes depend on the hidden fleet.
func TestHandleSetProfile_ScopedRefusalTouchesOnlySlugAndPin(t *testing.T) {
	cases := map[string]struct {
		ctx      context.Context
		slug     string
		viaIndex bool // false: stopped by the pin check before any lookup
	}{
		"deleted pin":                  {setProfilePinnedCtx("s", "gone", "pin-srv"), "gone", true},
		"zero-reach pin":               {setProfilePinnedCtx("s", "pin", "other-srv"), "pin", true},
		"scoped, absent slug":          {setProfileScopedCtx("s", "pin-srv"), "nope", true},
		"scoped, disjoint slug":        {setProfileScopedCtx("s", "pin-srv"), "p0", true},
		"scoped, empty allowlist":      {setProfileScopedCtx("s"), "pin", true},
		"scoped wildcard, absent slug": {setProfileScopedCtx("s", "*"), "nope", true},
		"pin mismatch":                 {setProfilePinnedCtx("s", "pin", "pin-srv"), "p0", false},
	}
	for fleet, n := range map[string]int{"pin only": 0, "4096 others": 4096} {
		cfg := selectableProbeConfig(n)
		p := &MCPProxyServer{config: cfg, logger: zap.NewNop(), sessionStore: NewSessionStore(zap.NewNop())}
		var touched []string
		idx := newProfileIndex(cfg)
		idx.lookupHook = func(slug string) { touched = append(touched, slug) }
		p.profileIndexes.warm.Store(idx)

		for name, c := range cases {
			touched = nil
			p.sessionStore.SetActiveProfile("s", "prior")
			res := callSetProfileTool(t, p, c.ctx, c.slug)
			require.True(t, res.IsError, "%s/%s must be refused", fleet, name)
			require.Equal(t, "prior", p.sessionStore.GetActiveProfile("s"), "%s/%s: a refusal must not mutate the session", fleet, name)

			pin := profilePinFromContext(c.ctx)
			allowed := map[string]bool{c.slug: true}
			if pin != "" {
				allowed[pin] = true
			}
			if c.viaIndex {
				require.Equal(t, fmt.Sprintf("unknown profile '%s'", c.slug), setProfileResultText(t, res),
					"%s/%s: a scoped refusal carries no available list", fleet, name)
				require.NotEmpty(t, touched, "%s/%s: the refusal must decide through the index", fleet, name)
			} else {
				require.Contains(t, setProfileResultText(t, res), "is pinned to profile 'pin'", "%s/%s", fleet, name)
			}
			require.LessOrEqual(t, len(touched), 2, "%s/%s: at most the slug and the pin: %v", fleet, name, touched)
			for _, got := range touched {
				require.True(t, allowed[got], "%s/%s: touched profile %q outside {slug, pin}: %v", fleet, name, got, touched)
			}
		}
		require.Same(t, idx, p.profileIndexCurrent(), "%s: the cached index must be reused for the same snapshot", fleet)
	}
}

// TestHandleSetProfile_AdminUnknownSlugKeepsAvailableList is the SC-005
// control for the refusal above: an administrator's unknown-slug error keeps
// the pre-105 discovery affordance byte-for-byte — every configured profile,
// in configured order, empty and ghost ones included.
func TestHandleSetProfile_AdminUnknownSlugKeepsAvailableList(t *testing.T) {
	p := newSetProfileTestServerWithEmptyProfiles(t)
	p.sessionStore.SetActiveProfile("sess-admin-unknown", "research")

	res := callSetProfileTool(t, p, setProfileAdminCtx("sess-admin-unknown"), "nope")
	require.True(t, res.IsError)
	require.Equal(t, "unknown profile 'nope' (available: research, deploy, mixed, empty, ghost)", setProfileResultText(t, res))
	require.Equal(t, "research", p.sessionStore.GetActiveProfile("sess-admin-unknown"))

	// Anonymous back-compat callers are administrator-shaped and keep it too.
	res = callSetProfileTool(t, p, setProfileCtx("sess-anon-unknown", ""), "nope")
	require.True(t, res.IsError)
	require.Equal(t, "unknown profile 'nope' (available: research, deploy, mixed, empty, ghost)", setProfileResultText(t, res))
}

// ---------------------------------------------------------------------------
// Spec 105 PR D codex round 4.
// ---------------------------------------------------------------------------

// selectableProbeConfigWithHiddenServers is selectableProbeConfig(n) over a
// fleet with hidden further configured servers that no profile declares and
// no test token is granted — the population a scoped caller must not be able
// to measure.
func selectableProbeConfigWithHiddenServers(n, hidden int) *config.Config {
	cfg := selectableProbeConfig(n)
	for i := 0; i < hidden; i++ {
		cfg.Servers = append(cfg.Servers, &config.ServerConfig{Name: fmt.Sprintf("hidden%d", i)})
	}
	return cfg
}

// TestProfileIndex_ReachCostsTheGrantNotTheFleet (Spec 105 PR D codex round
// 4, finding 1): the reach test is O(|reader grant|), never O(|fleet|). A
// reach that walked every configured server and ran the credential check on
// each cost 36 ns over one server and 25 µs over 4 096 for the same refusal
// (zero allocations either way, so the allocation guards were blind): the
// number of servers the operator runs was a timing oracle (FR-004; spec
// Definitions: timing class).
//
// Now a restricted reader performs exactly one membership test per entry of
// its own allowed_servers against the candidate's precomputed set, and a
// wildcard (or administrator) reader performs exactly one — "is the set
// non-empty" — so the count depends on nothing but the reader's own grant:
// not on the fleet, not on the candidate (present, absent, empty), not on
// the outcome. The traversal counter is the witness, not a clock; the
// outcome column pins that the cheaper rule is still the same rule.
func TestProfileIndex_ReachCostsTheGrantNotTheFleet(t *testing.T) {
	fleets := map[string]*profileIndex{
		"2 servers":           newProfileIndex(selectableProbeConfigWithHiddenServers(0, 0)),
		"4096 hidden servers": newProfileIndex(selectableProbeConfigWithHiddenServers(0, 4096)),
	}
	cases := map[string]struct {
		ctx   context.Context
		slug  string
		steps int
		reach bool
	}{
		"restricted, reachable":            {setProfileScopedCtx("s", "pin-srv"), "pin", 1, true},
		"restricted, disjoint":             {setProfileScopedCtx("s", "other-srv"), "pin", 1, false},
		"restricted, absent slug":          {setProfileScopedCtx("s", "pin-srv"), "nope", 1, false},
		"restricted, three-name grant":     {setProfileScopedCtx("s", "nowhere", "pin-srv", "hidden7"), "pin", 3, true},
		"restricted, unknown names only":   {setProfileScopedCtx("s", "nowhere", "hidden7"), "pin", 2, false},
		"restricted, empty allowlist":      {setProfileScopedCtx("s"), "pin", 0, false},
		"wildcard, reachable":              {setProfileScopedCtx("s", "*"), "pin", 1, true},
		"wildcard, absent slug":            {setProfileScopedCtx("s", "*"), "nope", 1, false},
		"wildcard among names":             {setProfileScopedCtx("s", "nowhere", "*"), "pin", 2, true},
		"pinned, reachable":                {selectablePinnedCtx("pin", "pin-srv"), "pin", 1, true},
		"pinned, zero reach":               {selectablePinnedCtx("pin", "other-srv"), "pin", 1, false},
		"pinned, deleted pin":              {selectablePinnedCtx("gone", "pin-srv", "other-srv"), "gone", 2, false},
		"pinned, mismatch":                 {selectablePinnedCtx("pin", "pin-srv"), "p0", 1, false},
		"restricted, empty-name grant":     {setProfileScopedCtx("s", ""), "pin", 1, false},
		"restricted, duplicate grant name": {setProfileScopedCtx("s", "pin-srv", "pin-srv"), "pin", 2, true},
	}
	for fleet, idx := range fleets {
		for name, c := range cases {
			steps := 0
			idx.reachHook = func() { steps++ }
			require.Equal(t, c.reach, idx.selectable(c.ctx, c.slug), "%s over %s: outcome", name, fleet)
			require.Equal(t, c.steps, steps, "%s over %s: reach must cost one membership test per granted server", name, fleet)
		}
	}
	// A profile that declares every hidden server is still reached only
	// through the reader's own grant: one test per granted name.
	wide := selectableProbeConfigWithHiddenServers(0, 4096)
	declared := make([]string, 0, len(wide.Servers))
	for _, s := range wide.Servers {
		declared = append(declared, s.Name)
	}
	wide.Profiles = append(wide.Profiles, config.ProfileConfig{Name: "wide", Servers: declared})
	idx := newProfileIndex(wide)
	steps := 0
	idx.reachHook = func() { steps++ }
	require.True(t, idx.selectable(setProfileScopedCtx("s", "hidden4095"), "wide"))
	require.Equal(t, 1, steps, "a 4 098-server candidate costs one test for a one-server grant")
}

// TestHandleSetProfile_ScopedRefusalReachCostsTheGrantNotTheFleet is the
// set_profile leg of the round-4 fix: the scoped unknown-profile refusal
// shares profileIndex.reach with the URL gate, so its cost is bounded by the
// token's own allowed_servers over a two-server and a 4 098-server fleet
// alike, for every refusal branch that reaches the index.
func TestHandleSetProfile_ScopedRefusalReachCostsTheGrantNotTheFleet(t *testing.T) {
	cases := map[string]struct {
		ctx  context.Context
		slug string
	}{
		"deleted pin":                  {setProfilePinnedCtx("s", "gone", "pin-srv"), "gone"},
		"zero-reach pin":               {setProfilePinnedCtx("s", "pin", "other-srv"), "pin"},
		"scoped, absent slug":          {setProfileScopedCtx("s", "pin-srv"), "nope"},
		"scoped, disjoint slug":        {setProfileScopedCtx("s", "pin-srv", "nowhere"), "p0"},
		"scoped, empty allowlist":      {setProfileScopedCtx("s"), "pin"},
		"scoped wildcard, absent slug": {setProfileScopedCtx("s", "*"), "nope"},
	}
	for name, c := range cases {
		steps := map[string]int{}
		for fleet, hidden := range map[string]int{"2 servers": 0, "4096 hidden servers": 4096} {
			cfg := selectableProbeConfigWithHiddenServers(1, hidden)
			p := &MCPProxyServer{config: cfg, logger: zap.NewNop(), sessionStore: NewSessionStore(zap.NewNop())}
			idx := newProfileIndex(cfg)
			idx.reachHook = func() { steps[fleet]++ }
			p.profileIndexes.warm.Store(idx)

			res := callSetProfileTool(t, p, c.ctx, c.slug)
			require.True(t, res.IsError, "%s/%s must be refused", fleet, name)
			require.Equal(t, fmt.Sprintf("unknown profile '%s'", c.slug), setProfileResultText(t, res), "%s/%s", fleet, name)
			require.Equal(t, len(auth.AuthContextFromContext(c.ctx).AllowedServers), steps[fleet],
				"%s/%s: reach must cost one membership test per granted server: %v", fleet, name, steps)
		}
		require.Equal(t, steps["2 servers"], steps["4096 hidden servers"], "%s: %v", name, steps)
	}
}

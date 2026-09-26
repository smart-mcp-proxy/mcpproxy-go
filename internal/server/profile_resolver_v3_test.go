package server

import (
	"context"
	"testing"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// sessionCtx wraps ctx with an mcp-go client session carrying sid, so
// sessionIDFromContext(ctx) resolves it (mirrors profile_resolver_test.go's
// fakeClientSession, reused here across the v2/v3 resolvers).
func sessionCtx(ctx context.Context, sid string) context.Context {
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	return helper.WithContext(ctx, &fakeClientSession{id: sid})
}

// TestResolveProfileV3_Precedence pins T029: pin > url > session > binding >
// anonymous > none, over the shared enforcement-matrix fixture
// (work-readonly --switchable_to--> work-full; legacy has no policy).
func TestResolveProfileV3_Precedence(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	idx := proxy.profileIndexFor(proxy.currentConfig())

	t.Run("locked client resolves with source pin", func(t *testing.T) {
		res := proxy.ResolveProfileV3(clientCtx("cursor", "work-readonly", "locked"), idx)
		assert.Equal(t, "work-readonly", res.Name)
		assert.Equal(t, string(profile.SourcePin), res.Source)
		assert.Equal(t, "work-readonly", res.Base)
		require.NotNil(t, res.Scope)
		require.NotNil(t, res.Policy)
	})

	t.Run("switchable client with no override resolves with source binding", func(t *testing.T) {
		res := proxy.ResolveProfileV3(clientCtx("laptop", "work-readonly", "switchable"), idx)
		assert.Equal(t, "work-readonly", res.Name)
		assert.Equal(t, string(profile.SourceBinding), res.Source)
		assert.Equal(t, "work-readonly", res.Base)
	})

	t.Run("dangling pin denies all, never falls through", func(t *testing.T) {
		res := proxy.ResolveProfileV3(clientCtx("cursor", "no-such-profile", "locked"), idx)
		assert.Equal(t, "no-such-profile", res.Name)
		assert.Equal(t, string(profile.SourcePin), res.Source)
		assert.NotNil(t, res.Scope)
		assert.True(t, res.Scope.DeniesAll(), "a dangling pin must resolve deny-all, not full reach")
		assert.Nil(t, res.Policy)
	})

	t.Run("dangling switchable binding denies all and admits no session override", func(t *testing.T) {
		ctx := clientCtx("laptop", "no-such-profile", "switchable")
		// Even a stored session selection must not override a dangling base.
		res := proxy.ResolveProfileV3(ctx, idx)
		assert.Equal(t, "no-such-profile", res.Name)
		assert.Equal(t, string(profile.SourceBinding), res.Source)
		assert.True(t, res.Scope.DeniesAll())
	})

	t.Run("no credential and no anonymous_profile resolves none", func(t *testing.T) {
		res := proxy.ResolveProfileV3(anonCtx(), idx)
		assert.Equal(t, string(profile.SourceNone), res.Source)
		assert.Nil(t, res.Scope)
	})

	t.Run("admin caller resolves none", func(t *testing.T) {
		res := proxy.ResolveProfileV3(adminCtx(), idx)
		assert.Equal(t, string(profile.SourceNone), res.Source)
	})
}

// TestResolveProfileV3_SwitchableToAdmission pins the switchable_to
// admission table (T029): a switchable credential may select its own base
// and any name in the base's switchable_to, never anything else, and never
// via a second hop (cycle A→B→A cannot escalate past A's own list).
func TestResolveProfileV3_SwitchableToAdmission(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	idx := proxy.profileIndexFor(proxy.currentConfig())
	basePolicy := idx.PolicyFor("work-readonly")
	require.NotNil(t, basePolicy)

	assert.True(t, admittedBySwitchableTo(basePolicy, "work-readonly", "work-readonly"), "own base always admitted")
	assert.True(t, admittedBySwitchableTo(basePolicy, "work-readonly", "work-full"), "declared switchable_to target admitted")
	assert.False(t, admittedBySwitchableTo(basePolicy, "work-readonly", "legacy"), "undeclared target refused")

	// work-full declares no switchable_to (nil): admits only itself, even
	// though work-readonly points at it — the relation is not symmetric and
	// a caller bound to work-full cannot reach work-readonly.
	workFullPolicy := idx.PolicyFor("work-full")
	assert.True(t, admittedBySwitchableTo(workFullPolicy, "work-full", "work-full"))
	assert.False(t, admittedBySwitchableTo(workFullPolicy, "work-full", "work-readonly"))
}

// TestResolveProfileV3_URLTier is finding F4 (zcode review round 1):
// profile_resolver_v3_test.go never drove a URL scope through
// ResolveProfileV3, leaving FR-020's url>session precedence, the
// admission-refused URL fall-through and "a locked credential may never
// switch" all uncovered — deleting the url/session tiers (lines 131-158)
// left the whole suite green.
func TestResolveProfileV3_URLTier(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	idx := proxy.profileIndexFor(proxy.currentConfig())

	t.Run("admitted URL selection overrides a switchable binding", func(t *testing.T) {
		ctx := profile.WithProfileScope(clientCtx("laptop", "work-readonly", "switchable"),
			profileScopeFromIndex(idx, "work-full"))
		res := proxy.ResolveProfileV3(ctx, idx)
		assert.Equal(t, "work-full", res.Name)
		assert.Equal(t, string(profile.SourceURL), res.Source)
		assert.Equal(t, "work-readonly", res.Base, "Base stays the BOUND profile, not the URL selection")
		require.NotNil(t, res.Scope)
		require.NotNil(t, res.Policy)
	})

	t.Run("URL selection with no base is admitted outright", func(t *testing.T) {
		ctx := profile.WithProfileScope(anonCtx(), profileScopeFromIndex(idx, "work-full"))
		res := proxy.ResolveProfileV3(ctx, idx)
		assert.Equal(t, "work-full", res.Name)
		assert.Equal(t, string(profile.SourceURL), res.Source)
		assert.Equal(t, "", res.Base)
	})

	t.Run("URL selection outside switchable_to falls through to the binding, not the URL", func(t *testing.T) {
		// work-readonly's switchable_to is only ["work-full"]; "legacy" is
		// not admitted, so the URL tier must be REFUSED, not silently
		// granted, and resolution falls through to the bound base.
		ctx := profile.WithProfileScope(clientCtx("laptop", "work-readonly", "switchable"),
			profileScopeFromIndex(idx, "legacy"))
		res := proxy.ResolveProfileV3(ctx, idx)
		assert.Equal(t, "work-readonly", res.Name, "must fall through to the base, never the unpermitted URL target")
		assert.Equal(t, string(profile.SourceBinding), res.Source)
	})

	t.Run("a locked credential may never switch via URL", func(t *testing.T) {
		// Tier 1 (pin) is authoritative and returns before the URL tier is
		// even consulted (FR-020: pin > url).
		ctx := profile.WithProfileScope(clientCtx("cursor", "work-readonly", "locked"),
			profileScopeFromIndex(idx, "work-full"))
		res := proxy.ResolveProfileV3(ctx, idx)
		assert.Equal(t, "work-readonly", res.Name, "a locked pin must never be overridden by a URL selection")
		assert.Equal(t, string(profile.SourcePin), res.Source)
	})
}

// TestResolveProfileV3_SessionTier is finding F4: the session tier
// (set_profile) — precedence under url, re-validation against the current
// base, and clearing a no-longer-permitted stored selection — had no
// coverage at all.
func TestResolveProfileV3_SessionTier(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	idx := proxy.profileIndexFor(proxy.currentConfig())

	t.Run("admitted session selection overrides a switchable binding", func(t *testing.T) {
		sid := "sess-v3-admitted"
		proxy.sessionStore.SetActiveProfile(sid, "work-full")
		ctx := sessionCtx(clientCtx("laptop", "work-readonly", "switchable"), sid)

		res := proxy.ResolveProfileV3(ctx, idx)
		assert.Equal(t, "work-full", res.Name)
		assert.Equal(t, string(profile.SourceSession), res.Source)
		assert.Equal(t, "work-readonly", res.Base)
		require.NotNil(t, res.Scope)
	})

	t.Run("URL tier outranks a stored session selection", func(t *testing.T) {
		sid := "sess-v3-url-wins"
		proxy.sessionStore.SetActiveProfile(sid, "work-full")
		ctx := sessionCtx(clientCtx("laptop", "work-readonly", "switchable"), sid)
		ctx = profile.WithProfileScope(ctx, profileScopeFromIndex(idx, "work-readonly"))

		res := proxy.ResolveProfileV3(ctx, idx)
		assert.Equal(t, "work-readonly", res.Name)
		assert.Equal(t, string(profile.SourceURL), res.Source, "url must outrank a stored session selection")
	})

	t.Run("a no-longer-permitted stored selection is cleared and falls through", func(t *testing.T) {
		sid := "sess-v3-stale-clears"
		// Stored while the caller was still admitted to "legacy" under some
		// earlier base; now bound to work-readonly, whose switchable_to does
		// not include "legacy" — the stored selection must be refused AND
		// cleared, not merely ignored for this one request.
		proxy.sessionStore.SetActiveProfile(sid, "legacy")
		ctx := sessionCtx(clientCtx("laptop", "work-readonly", "switchable"), sid)

		res := proxy.ResolveProfileV3(ctx, idx)
		assert.Equal(t, "work-readonly", res.Name, "must fall through to the base, never the stale session selection")
		assert.Equal(t, string(profile.SourceBinding), res.Source)
		assert.Equal(t, "", proxy.sessionStore.GetActiveProfile(sid),
			"the stale selection must be cleared, not merely skipped, so it doesn't linger for a later request")
	})

	t.Run("set_profile empty-string returns a switchable caller to its binding", func(t *testing.T) {
		sid := "sess-v3-explicit-clear"
		proxy.sessionStore.SetActiveProfile(sid, "") // set_profile("")
		ctx := sessionCtx(clientCtx("laptop", "work-readonly", "switchable"), sid)

		res := proxy.ResolveProfileV3(ctx, idx)
		assert.Equal(t, "work-readonly", res.Name)
		assert.Equal(t, string(profile.SourceBinding), res.Source)
	})

	t.Run("a locked credential may never switch via session selection", func(t *testing.T) {
		sid := "sess-v3-locked-no-switch"
		proxy.sessionStore.SetActiveProfile(sid, "work-full")
		ctx := sessionCtx(clientCtx("cursor", "work-readonly", "locked"), sid)

		res := proxy.ResolveProfileV3(ctx, idx)
		assert.Equal(t, "work-readonly", res.Name, "a locked pin must never be overridden by a session selection")
		assert.Equal(t, string(profile.SourcePin), res.Source)
	})
}

// TestResolveProfileV3_EmptyPinSwitchableIsLegacyAnySelectable pins that a
// switchable credential bound to "All servers" (empty pin) keeps legacy
// any-selectable behaviour (T029) — no policy restricts it.
func TestResolveProfileV3_EmptyPinSwitchableIsLegacyAnySelectable(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	idx := proxy.profileIndexFor(proxy.currentConfig())

	res := proxy.ResolveProfileV3(clientCtx("laptop", "", "switchable"), idx)
	assert.Equal(t, "", res.Name)
	assert.Equal(t, string(profile.SourceBinding), res.Source)
	assert.Equal(t, "", res.Base)
	assert.Nil(t, res.Scope)
}

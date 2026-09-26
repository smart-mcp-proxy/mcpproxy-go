package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

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

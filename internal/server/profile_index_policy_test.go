package server

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// TestProfileIndex_CompiledPolicy pins the T008/T012 contract (Spec 105 D17
// pattern, data-model.md §2): profileIndex compiles and caches one
// CompiledPolicy per profile, taken with its snapshot as one immutable pair.
func TestProfileIndex_CompiledPolicy(t *testing.T) {
	defer config.SetPolicyEnforcementReadyForTest(true)()

	cfg := &config.Config{
		Servers:  []*config.ServerConfig{{Name: "github"}, {Name: "notion"}, {Name: "filesystem"}},
		Profiles: enforcementMatrixProfiles(),
	}
	idx := newProfileIndex(cfg)

	t.Run("PolicyFor resolves a v3 profile by name", func(t *testing.T) {
		p := idx.PolicyFor("work-readonly")
		require.NotNil(t, p)
		require.Equal(t, profile.TierRead, p.Cap)
		admitted, reason, _ := p.Decide("github", "delete_repo", profile.TierDestructive)
		require.False(t, admitted)
		require.Equal(t, profile.ReasonAboveTierCap, reason)
	})

	t.Run("PolicyAt resolves by the already-looked-up position", func(t *testing.T) {
		pos := idx.position("work-full")
		require.GreaterOrEqual(t, pos, 0)
		p := idx.PolicyAt(pos)
		require.NotNil(t, p)
		require.Equal(t, profile.TierDestructive, p.Cap)
	})

	t.Run("legacy profile compiles to a non-nil, unrestricted policy (fast path)", func(t *testing.T) {
		p := idx.PolicyFor("legacy")
		require.NotNil(t, p, "data-model.md §4: Policy is non-nil for a legacy profile too, only IsLegacy() differs")
		require.Equal(t, profile.TierUnannotated, p.Cap, "legacy: no cap")
		// Every tier is admitted (no cap => the tier check never fires).
		for _, tier := range []profile.Tier{profile.TierRead, profile.TierWrite, profile.TierDestructive} {
			admitted, reason, _ := p.Decide("github", "whatever_tool", tier)
			require.True(t, admitted)
			require.Equal(t, profile.ReasonNone, reason)
		}
		// Legacy's own ProfileConfig.IsLegacy() is still the source of truth
		// for FR-011 presence — the compiled policy's behaviour above and
		// IsLegacy() are two different questions.
		pc := idx.lookup("legacy")
		require.NotNil(t, pc)
		require.True(t, pc.IsLegacy())
	})

	t.Run("unknown profile has no policy", func(t *testing.T) {
		require.Nil(t, idx.PolicyFor("does-not-exist"))
		require.Nil(t, idx.PolicyAt(-1))
	})

	t.Run("resolves identically over the live T002 fixture (real proxy+runtime, real upstreams)", func(t *testing.T) {
		proxy, _ := newProfilesV3Fixture(t)
		live := proxy.profileIndexCurrent(anonCtx())
		require.NotNil(t, live)

		p := live.PolicyFor("work-readonly")
		require.NotNil(t, p)
		admitted, reason, _ := p.Decide("github", "create_issue", profile.TierWrite)
		require.False(t, admitted)
		require.Equal(t, profile.ReasonAboveTierCap, reason)

		admitted, reason, _ = p.Decide("notion", "update_page", profile.TierWrite)
		require.True(t, admitted, "notion:update_page is admitted by an explicit allow rule")
		require.Equal(t, profile.ReasonNone, reason)

		require.Nil(t, live.PolicyFor("legacy").SwitchableTo, "legacy profile has no switchable_to")
	})

	t.Run("compiled policy taken with its snapshot as one pair: a second index over an edited snapshot never mutates the first", func(t *testing.T) {
		edited := &config.Config{
			Servers: []*config.ServerConfig{{Name: "github"}},
			Profiles: []config.ProfileConfig{
				{Name: "work-readonly", Servers: []string{"github"}, MaxTier: "write"},
			},
		}
		idx2 := newProfileIndex(edited)
		require.Equal(t, profile.TierRead, idx.PolicyFor("work-readonly").Cap, "the original snapshot's compiled policy must be unaffected by a later edit")
		require.Equal(t, profile.TierWrite, idx2.PolicyFor("work-readonly").Cap)
	})
}

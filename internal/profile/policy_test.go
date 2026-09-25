package profile

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// enforcementMatrixProfiles builds the three contracts/enforcement-matrix.md
// fixture profiles (Spec 108 spec, contracts/enforcement-matrix.md).
func enforcementMatrixProfiles(t *testing.T) map[string]*config.ProfileConfig {
	t.Helper()
	switchableTo := []string{"work-full"}
	return map[string]*config.ProfileConfig{
		"work-readonly": {
			Name:    "work-readonly",
			Title:   "Work · Read-only",
			Servers: []string{"github", "notion"},
			MaxTier: "read",
			Tools: &config.ProfileToolRules{
				Allow: []string{"notion:update_page", "github:get_secret_scanning_alert", "filesystem:read_text_file"},
				Deny:  []string{"github:*secret*"},
			},
			CodeExecution:   boolPtr(false),
			ManagementTools: boolPtr(false),
			SwitchableTo:    &switchableTo,
		},
		"work-full": {
			Name:          "work-full",
			Servers:       []string{"github", "notion", "filesystem"},
			MaxTier:       "destructive",
			Unannotated:   "as_write",
			CodeExecution: boolPtr(true),
		},
		"legacy": {
			Name:    "legacy",
			Servers: []string{"github"},
		},
	}
}

func boolPtr(b bool) *bool { return &b }

// enforcementMatrixIntrinsicTiers is the fixture's "Intrinsic tier" column
// (contracts/enforcement-matrix.md).
var enforcementMatrixIntrinsicTiers = map[string]Tier{
	"github:list_issues":               TierRead,
	"github:create_issue":              TierWrite,
	"github:delete_repo":               TierDestructive,
	"github:search_code":               TierUnannotated,
	"github:get_secret_scanning_alert": TierRead,
	"notion:update_page":               TierWrite,
	"filesystem:read_text_file":        TierRead,
}

// TestCompiledPolicy_Decide_EnforcementMatrix pins the "Expected profile
// decisions" table of contracts/enforcement-matrix.md exactly (FR-010):
// every tool x every fixture profile.
func TestCompiledPolicy_Decide_EnforcementMatrix(t *testing.T) {
	profiles := enforcementMatrixProfiles(t)
	compiled := map[string]*CompiledPolicy{}
	for name, pc := range profiles {
		compiled[name] = Compile(pc)
	}

	identity := func(id string) (server, tool string) {
		for i := 0; i < len(id); i++ {
			if id[i] == ':' {
				return id[:i], id[i+1:]
			}
		}
		return id, ""
	}

	type row struct {
		id     string
		wantWR Reason // work-readonly
		wantWF Reason // work-full
		wantLG Reason // legacy
	}
	rows := []row{
		{"github:list_issues", ReasonNone, ReasonNone, ReasonNone},
		{"github:create_issue", ReasonAboveTierCap, ReasonNone, ReasonNone},
		{"github:delete_repo", ReasonAboveTierCap, ReasonNone, ReasonNone},
		{"github:search_code", ReasonUnannotatedHidden, ReasonNone, ReasonNone},
		{"github:get_secret_scanning_alert", ReasonDeniedByRule, ReasonNone, ReasonNone},
		{"notion:update_page", ReasonNone, ReasonNone, ReasonServerNotInProfile},
		{"filesystem:read_text_file", ReasonServerNotInProfile, ReasonNone, ReasonServerNotInProfile},
	}

	for _, r := range rows {
		server, tool := identity(r.id)
		intrinsic := enforcementMatrixIntrinsicTiers[r.id]

		t.Run(r.id+"/work-readonly", func(t *testing.T) {
			admitted, reason, _ := compiled["work-readonly"].Decide(server, tool, intrinsic)
			require.Equal(t, r.wantWR, reason)
			require.Equal(t, r.wantWR == ReasonNone, admitted)
		})
		t.Run(r.id+"/work-full", func(t *testing.T) {
			admitted, reason, _ := compiled["work-full"].Decide(server, tool, intrinsic)
			require.Equal(t, r.wantWF, reason)
			require.Equal(t, r.wantWF == ReasonNone, admitted)
		})
		t.Run(r.id+"/legacy", func(t *testing.T) {
			admitted, reason, _ := compiled["legacy"].Decide(server, tool, intrinsic)
			require.Equal(t, r.wantLG, reason)
			require.Equal(t, r.wantLG == ReasonNone, admitted)
		})
	}

	// The allow rule naming "filesystem" (outside work-readonly's servers)
	// must be dropped at compile time (FR-004/FR-007 warn-and-ignore) and
	// never let filesystem:read_text_file into work-readonly's server set.
	require.NotContains(t, compiled["work-readonly"].Servers, "filesystem")

	// "after classify work-readonly github:search_code read" -> admitted.
	classified := enforcementMatrixProfiles(t)["work-readonly"]
	classified.Tools.Classify = map[string]string{"github:search_code": "read"}
	admitted, reason, profileTier := Compile(classified).Decide("github", "search_code", TierUnannotated)
	require.True(t, admitted)
	require.Equal(t, ReasonNone, reason)
	require.Equal(t, TierRead, profileTier)
}

// TestCompiledPolicy_Decide_DenyBeatsAllowOnOverlap pins the specific
// overlap cell called out by the matrix: github:get_secret_scanning_alert is
// matched by BOTH an allow rule and a deny rule under work-readonly — deny
// wins (FR-010 step 2 before step 3).
func TestCompiledPolicy_Decide_DenyBeatsAllowOnOverlap(t *testing.T) {
	pc := enforcementMatrixProfiles(t)["work-readonly"]
	require.Contains(t, pc.Tools.Allow, "github:get_secret_scanning_alert")
	require.Contains(t, pc.Tools.Deny, "github:*secret*")

	admitted, reason, _ := Compile(pc).Decide("github", "get_secret_scanning_alert", TierRead)
	require.False(t, admitted)
	require.Equal(t, ReasonDeniedByRule, reason)
}

// TestCompiledPolicy_AllowRuleCannotAddServer pins FR-006: an allow rule can
// never widen the server set. filesystem:read_text_file's allow entry names
// a server outside work-readonly's `servers` and is ignored.
func TestCompiledPolicy_AllowRuleCannotAddServer(t *testing.T) {
	cp := Compile(enforcementMatrixProfiles(t)["work-readonly"])
	admitted, reason, _ := cp.Decide("filesystem", "read_text_file", TierRead)
	require.False(t, admitted)
	require.Equal(t, ReasonServerNotInProfile, reason)
}

// TestCompiledPolicy_StaleClassificationIgnoredForAnnotatedTools pins FR-005:
// a classify entry only applies to a tool whose EFFECTIVE annotations carry
// no tier hint; an annotated tool ignores a stale classification entirely.
func TestCompiledPolicy_StaleClassificationIgnoredForAnnotatedTools(t *testing.T) {
	pc := &config.ProfileConfig{
		Name:    "p",
		Servers: []string{"github"},
		MaxTier: "read",
		Tools: &config.ProfileToolRules{
			Classify: map[string]string{"github:create_issue": "read"},
		},
	}
	cp := Compile(pc)
	// github:create_issue is annotated WRITE (intrinsic != TierUnannotated),
	// so the stale "read" classification never applies: it is evaluated at
	// its real (write) tier and excluded by the read cap.
	admitted, reason, profileTier := cp.Decide("github", "create_issue", TierWrite)
	require.False(t, admitted)
	require.Equal(t, ReasonAboveTierCap, reason)
	require.Equal(t, TierWrite, profileTier)
}

// TestIntrinsicTier_NotFoundFailsClosed pins IntrinsicTier(nil,false) =
// destructive (FR-011: a tool whose identity cannot be resolved).
func TestIntrinsicTier_NotFoundFailsClosed(t *testing.T) {
	require.Equal(t, TierDestructive, IntrinsicTier(nil, false))
	require.Equal(t, TierDestructive, IntrinsicTier(&config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}, false))
}

// TestCompiledPolicy_EffectiveUnannotatedDefaults pins FR-003's defaults as
// they flow through Compile/Decide (see also config.ProfileConfig.
// EffectiveUnannotated, tested directly in internal/config).
func TestCompiledPolicy_EffectiveUnannotatedDefaults(t *testing.T) {
	t.Run("max_tier read defaults unannotated to deny", func(t *testing.T) {
		cp := Compile(&config.ProfileConfig{Name: "p", Servers: []string{"a"}, MaxTier: "read"})
		admitted, reason, _ := cp.Decide("a", "unknown_tool", TierUnannotated)
		require.False(t, admitted)
		require.Equal(t, ReasonUnannotatedHidden, reason)
	})
	t.Run("max_tier destructive defaults unannotated to as_read", func(t *testing.T) {
		cp := Compile(&config.ProfileConfig{Name: "p", Servers: []string{"a"}, MaxTier: "destructive"})
		admitted, reason, tier := cp.Decide("a", "unknown_tool", TierUnannotated)
		require.True(t, admitted)
		require.Equal(t, ReasonNone, reason)
		require.Equal(t, TierRead, tier)
	})
}

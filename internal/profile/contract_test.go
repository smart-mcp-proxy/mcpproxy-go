package profile

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/contract/" + name)
	require.NoError(t, err)
	return b
}

// TestContractFixtures_Decode pins that every T003 golden fixture decodes
// into the shapes Go, vitest and Swift all share (data-model.md), and that
// the enum-bearing fields decode into this package's typed enum constants
// without a lossy round trip.
func TestContractFixtures_Decode(t *testing.T) {
	t.Run("profile_full.json is a non-legacy ProfileConfig", func(t *testing.T) {
		var pc config.ProfileConfig
		require.NoError(t, json.Unmarshal(readFixture(t, "profile_full.json"), &pc))
		require.Equal(t, "work-readonly", pc.Name)
		require.False(t, pc.IsLegacy())
		require.Equal(t, "read", pc.MaxTier)
		require.Equal(t, "deny", pc.Unannotated)
		require.NotNil(t, pc.Tools)
		require.Contains(t, pc.Tools.Deny, "github:*secret*")
		require.NotNil(t, pc.SwitchableTo)
		require.Equal(t, []string{"work-full"}, *pc.SwitchableTo)

		// Round-trips through Compile without error and produces a policy
		// whose Fingerprint is deterministic.
		cp := Compile(&pc)
		cp2 := Compile(&pc)
		require.Equal(t, cp.Fingerprint, cp2.Fingerprint)
	})

	t.Run("profile_legacy.json is a legacy ProfileConfig", func(t *testing.T) {
		var pc config.ProfileConfig
		require.NoError(t, json.Unmarshal(readFixture(t, "profile_legacy.json"), &pc))
		require.True(t, pc.IsLegacy())
	})

	t.Run("effective_tools.json decodes tier and reason enums", func(t *testing.T) {
		var rows []struct {
			Server        string `json:"server"`
			Tool          string `json:"tool"`
			IntrinsicTier string `json:"intrinsic_tier"`
			ProfileTier   string `json:"profile_tier"`
			Access        struct {
				Visible  bool   `json:"visible"`
				Callable bool   `json:"callable"`
				Reason   Reason `json:"reason"`
			} `json:"access"`
			ClassificationStale bool `json:"classification_stale"`
		}
		require.NoError(t, json.Unmarshal(readFixture(t, "effective_tools.json"), &rows))
		require.Len(t, rows, 3)
		require.Equal(t, ReasonAboveTierCap, rows[1].Access.Reason)
		require.Equal(t, ReasonNone, rows[0].Access.Reason)
		// list_issues (row 0) is ANNOTATED (read) but profile_full.json also
		// classifies it "write" (FR-005: a classify entry only applies to an
		// unannotated tool) -> the real annotation wins and the entry is
		// reported stale.
		require.True(t, rows[0].ClassificationStale, "an annotated tool with a classify entry must report it stale (FR-005)")
		// search_code (row 2) is UNANNOTATED and IS classified "read" by
		// profile_full.json -> the classification is applied, not stale.
		require.False(t, rows[2].ClassificationStale, "an applied classification on an unannotated tool must not be reported stale")
	})

	t.Run("client_rows.json decodes credential state and source enums", func(t *testing.T) {
		var rows []struct {
			ID              string          `json:"id"`
			CredentialState CredentialState `json:"credential_state"`
			ProfileSource   Source          `json:"profile_source"`
			Warnings        []WarningCode   `json:"warnings"`
		}
		require.NoError(t, json.Unmarshal(readFixture(t, "client_rows.json"), &rows))
		require.Len(t, rows, 2)
		require.Equal(t, CredentialStateClient, rows[0].CredentialState)
		require.Equal(t, SourcePin, rows[0].ProfileSource)
		require.Equal(t, CredentialStateAdminKey, rows[1].CredentialState)
		require.Contains(t, rows[1].Warnings, WarningClientHoldsAdminKey)
	})

	t.Run("explain_blocked.json decodes step/action/verdict enums", func(t *testing.T) {
		var explanation struct {
			Steps []struct {
				Step   ExplainStep `json:"step"`
				Status string      `json:"status"`
			} `json:"steps"`
			Verdict      string      `json:"verdict"`
			FirstFailure ExplainStep `json:"first_failure"`
			Fixes        []struct {
				Step   ExplainStep `json:"step"`
				Action FixAction   `json:"action"`
			} `json:"fixes"`
		}
		require.NoError(t, json.Unmarshal(readFixture(t, "explain_blocked.json"), &explanation))
		require.Equal(t, StepOrder(), stepsOf(explanation.Steps))
		require.Equal(t, StepTierCap, explanation.FirstFailure)
		require.Equal(t, "blocked", explanation.Verdict)
		require.Equal(t, FixAllowInProfile, explanation.Fixes[0].Action)
		require.Equal(t, FixMoveClient, explanation.Fixes[1].Action)
	})

	t.Run("activity_attributed.json decodes source and block-reason enums", func(t *testing.T) {
		var rec struct {
			Profile       string      `json:"profile"`
			ProfileSource Source      `json:"profile_source"`
			BlockReason   BlockReason `json:"block_reason"`
		}
		require.NoError(t, json.Unmarshal(readFixture(t, "activity_attributed.json"), &rec))
		require.Equal(t, SourcePin, rec.ProfileSource)
		require.Equal(t, BlockReasonTier, rec.BlockReason)
	})
}

func stepsOf(steps []struct {
	Step   ExplainStep `json:"step"`
	Status string      `json:"status"`
}) []ExplainStep {
	out := make([]ExplainStep, len(steps))
	for i, s := range steps {
		out[i] = s.Step
	}
	return out
}

// TestCompiledPolicy_Fingerprint pins fingerprint stability/sensitivity
// (T007): stable regardless of slice/map order, and changes whenever any of
// the six FR-001 policy fields changes.
func TestCompiledPolicy_Fingerprint(t *testing.T) {
	base := func() *config.ProfileConfig {
		return &config.ProfileConfig{
			Name: "p", Servers: []string{"a", "b"}, MaxTier: "read",
			Tools: &config.ProfileToolRules{
				Allow: []string{"a:one", "a:two"},
				// Two elements, not one: a single-element Deny list has no
				// non-trivial reorder, so it could never actually exercise
				// Deny-order stability no matter what the subtest asserts.
				Deny:     []string{"a:three", "a:four"},
				Classify: map[string]string{"a:x": "read", "a:y": "write"},
			},
		}
	}

	t.Run("stable across allow slice order", func(t *testing.T) {
		p1 := base()
		p2 := base()
		p2.Tools.Allow = []string{"a:two", "a:one"} // reordered
		require.Equal(t, Compile(p1).Fingerprint, Compile(p2).Fingerprint)
	})

	t.Run("stable across deny slice order", func(t *testing.T) {
		p1 := base()
		p2 := base()
		p2.Tools.Deny = []string{"a:four", "a:three"} // reordered
		require.Equal(t, Compile(p1).Fingerprint, Compile(p2).Fingerprint)
	})

	t.Run("stable across classify map insertion order (Go maps have none)", func(t *testing.T) {
		p1 := base()
		p2 := base()
		p2.Tools.Classify = map[string]string{"a:y": "write", "a:x": "read"}
		require.Equal(t, Compile(p1).Fingerprint, Compile(p2).Fingerprint)
	})

	t.Run("changes on max_tier", func(t *testing.T) {
		p1, p2 := base(), base()
		p2.MaxTier = "write"
		require.NotEqual(t, Compile(p1).Fingerprint, Compile(p2).Fingerprint)
	})

	t.Run("changes on unannotated", func(t *testing.T) {
		p1, p2 := base(), base()
		p2.Unannotated = "as_write"
		require.NotEqual(t, Compile(p1).Fingerprint, Compile(p2).Fingerprint)
	})

	t.Run("changes on tools.allow", func(t *testing.T) {
		p1, p2 := base(), base()
		p2.Tools.Allow = append(p2.Tools.Allow, "a:four")
		require.NotEqual(t, Compile(p1).Fingerprint, Compile(p2).Fingerprint)
	})

	t.Run("changes on tools.deny", func(t *testing.T) {
		p1, p2 := base(), base()
		p2.Tools.Deny = nil
		require.NotEqual(t, Compile(p1).Fingerprint, Compile(p2).Fingerprint)
	})

	t.Run("changes on tools.classify", func(t *testing.T) {
		p1, p2 := base(), base()
		p2.Tools.Classify["a:x"] = "write"
		require.NotEqual(t, Compile(p1).Fingerprint, Compile(p2).Fingerprint)
	})

	t.Run("changes on code_execution", func(t *testing.T) {
		p1, p2 := base(), base()
		f := false
		p2.CodeExecution = &f
		require.NotEqual(t, Compile(p1).Fingerprint, Compile(p2).Fingerprint)
	})

	t.Run("changes on management_tools", func(t *testing.T) {
		p1, p2 := base(), base()
		tr := true
		p2.ManagementTools = &tr
		require.NotEqual(t, Compile(p1).Fingerprint, Compile(p2).Fingerprint)
	})

	t.Run("changes on switchable_to", func(t *testing.T) {
		p1, p2 := base(), base()
		sw := []string{"other"}
		p2.SwitchableTo = &sw
		require.NotEqual(t, Compile(p1).Fingerprint, Compile(p2).Fingerprint)
	})

	t.Run("stable across switchable_to slice order", func(t *testing.T) {
		p1, p2 := base(), base()
		sw1 := []string{"x", "y"}
		sw2 := []string{"y", "x"}
		p1.SwitchableTo = &sw1
		p2.SwitchableTo = &sw2
		require.Equal(t, Compile(p1).Fingerprint, Compile(p2).Fingerprint)
	})

	t.Run("changes when tools flips from absent to present-but-empty (IsLegacy sensitivity)", func(t *testing.T) {
		withTools := &config.ProfileConfig{Name: "p", Servers: []string{"a"}, Tools: &config.ProfileToolRules{}}
		withoutTools := &config.ProfileConfig{Name: "p", Servers: []string{"a"}}
		require.True(t, withTools.IsLegacy() == false && withoutTools.IsLegacy() == true, "sanity: only Tools differs the two IsLegacy() results")
		require.NotEqual(t, Compile(withoutTools).Fingerprint, Compile(withTools).Fingerprint,
			"an absent tools object and an empty {} one are both enforcement-equivalent but IsLegacy() disagrees about them (FR-011 hidden_by_profile presence) — the fingerprint must track that flip too (FR-027)")
	})

	t.Run("never changes on name, title or servers", func(t *testing.T) {
		p1, p2 := base(), base()
		p2.Name = "different"
		p2.Title = "Different Title"
		p2.Servers = []string{"z"}
		require.Equal(t, Compile(p1).Fingerprint, Compile(p2).Fingerprint)
	})
}

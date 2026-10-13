package runtime

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 108-l (L9, T124a; config-field-checklist point 1 that came with T015): the
// Profiles v3 config fields (`profiles[]` with its policy fields, and
// `anonymous_profile`) are hot-reloadable: the config funnel already reconciles
// the profile indexes and notifies governed sessions. DetectConfigChanges must
// therefore report a lone edit of either one, applied immediately. Before this
// the apply answered "No configuration changes detected" with
// applied_immediately:false for a Settings "Anonymous callers" save, which the
// Web UI and macOS Settings both render.

func profilesV3Config() *config.Config {
	return &config.Config{
		Listen: "127.0.0.1:8080", DataDir: "/d", TLS: &config.TLSConfig{},
		Profiles: []config.ProfileConfig{
			{Name: "work-readonly", Servers: []string{"github", "notion"}, MaxTier: "read", Unannotated: "deny"},
			{Name: "work-full", Servers: []string{"github", "notion", "filesystem"}, MaxTier: "destructive"},
		},
		AnonymousProfile: "",
	}
}

// jsonClone is what PATCH /config does to the live config before merging a patch:
// a decoded copy, so slices and pointers are new values with equal content.
func jsonClone(t *testing.T, cfg *config.Config) *config.Config {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var out config.Config
	require.NoError(t, json.Unmarshal(raw, &out))
	return &out
}

func TestDetectConfigChanges_ProfilesV3Fields(t *testing.T) {
	t.Run("a lone anonymous_profile edit is a hot-reloadable change", func(t *testing.T) {
		old, edited := profilesV3Config(), profilesV3Config()
		edited.AnonymousProfile = "work-readonly"
		result := DetectConfigChanges(old, edited)
		require.True(t, result.Success)
		assert.Equal(t, []string{"anonymous_profile"}, result.ChangedFields)
		assert.True(t, result.AppliedImmediately, "no restart: the anonymous resolver reads the live snapshot")
		assert.False(t, result.RequiresRestart)
	})

	t.Run("clearing anonymous_profile is reported too", func(t *testing.T) {
		old, edited := profilesV3Config(), profilesV3Config()
		old.AnonymousProfile = "work-readonly"
		result := DetectConfigChanges(old, edited)
		assert.Equal(t, []string{"anonymous_profile"}, result.ChangedFields)
	})

	t.Run("a lone policy edit of one profile (max_tier) is reported as profiles", func(t *testing.T) {
		old, edited := profilesV3Config(), profilesV3Config()
		edited.Profiles[0].MaxTier = "write"
		result := DetectConfigChanges(old, edited)
		assert.Equal(t, []string{"profiles"}, result.ChangedFields)
		assert.True(t, result.AppliedImmediately)
		assert.False(t, result.RequiresRestart)
	})

	t.Run("adding or removing a profile is reported as profiles", func(t *testing.T) {
		old, added := profilesV3Config(), profilesV3Config()
		added.Profiles = append(added.Profiles, config.ProfileConfig{Name: "legacy", Servers: []string{"github"}})
		assert.Equal(t, []string{"profiles"}, DetectConfigChanges(old, added).ChangedFields)

		removed := profilesV3Config()
		removed.Profiles = removed.Profiles[:1]
		assert.Equal(t, []string{"profiles"}, DetectConfigChanges(old, removed).ChangedFields)
	})

	t.Run("a classify, rule or switchable_to edit is reported", func(t *testing.T) {
		old := profilesV3Config()
		classified := profilesV3Config()
		classified.Profiles[0].Tools = &config.ProfileToolRules{Classify: map[string]string{"github:search_code": "read"}}
		assert.Equal(t, []string{"profiles"}, DetectConfigChanges(old, classified).ChangedFields)

		switchTo := []string{"work-full"}
		switchable := profilesV3Config()
		switchable.Profiles[0].SwitchableTo = &switchTo
		assert.Equal(t, []string{"profiles"}, DetectConfigChanges(old, switchable).ChangedFields)
	})

	t.Run("no false positive: a JSON round trip of the same profiles is no change", func(t *testing.T) {
		old := profilesV3Config()
		result := DetectConfigChanges(old, jsonClone(t, old))
		assert.NotContains(t, result.ChangedFields, "profiles")
		assert.NotContains(t, result.ChangedFields, "anonymous_profile")
		assert.Empty(t, result.ChangedFields)
	})

	t.Run("both fields in one apply are both reported", func(t *testing.T) {
		old, edited := profilesV3Config(), profilesV3Config()
		edited.AnonymousProfile = "work-readonly"
		edited.Profiles[1].Servers = []string{"github"}
		assert.ElementsMatch(t, []string{"profiles", "anonymous_profile"}, DetectConfigChanges(old, edited).ChangedFields)
	})
}

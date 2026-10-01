package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Issue #1458 item 1: a profile edit is live the moment the apply (or the disk
// reload) returns, so its per-profile search index must be too. A stale index
// only hides in-scope tools, but a widened or new profile would search an
// incomplete store until the next discovery pass.

// seedProfileIndexes puts a:x, b:y, b:z in the shared index and builds the
// per-profile indexes, so ro holds 1 doc and full holds 3.
func seedProfileIndexes(t *testing.T, rt *Runtime) {
	t.Helper()
	require.NoError(t, rt.IndexManager().BatchIndexTools([]*config.ToolMetadata{
		toolMeta("a", "x"), toolMeta("b", "y"), toolMeta("b", "z"),
	}))
	rt.reconcileProfileIndexes()
	require.Equal(t, uint64(1), profileDocCount(t, rt.IndexManager(), "ro"))
}

func profileDirs(t *testing.T, rt *Runtime) []string {
	t.Helper()
	names, err := rt.IndexManager().ExistingProfileDirs()
	require.NoError(t, err)
	return names
}

// nextConfig builds the config a PATCH would commit: a JSON round trip of the
// desired config.
func nextConfig(t *testing.T, rt *Runtime) *config.Config {
	t.Helper()
	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	return jsonClone(t, desired)
}

func setProfileServers(cfg *config.Config, name string, servers []string) {
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name == name {
			cfg.Profiles[i].Servers = servers
		}
	}
}

func TestApplyConfig_ProfileServersEditRebuildsProfileIndexImmediately(t *testing.T) {
	rt := newFunnelRuntime(t)
	seedProfileIndexes(t, rt)

	next := nextConfig(t, rt)
	setProfileServers(next, "ro", []string{"a", "b"})
	res, err := rt.ApplyConfig(next, rt.ConfigPath())
	require.NoError(t, err)
	require.Contains(t, res.ChangedFields, "profiles")
	assert.Equal(t, uint64(3), profileDocCount(t, rt.IndexManager(), "ro"), "widened profile is searchable at once")

	next = nextConfig(t, rt)
	setProfileServers(next, "ro", []string{"b"})
	_, err = rt.ApplyConfig(next, rt.ConfigPath())
	require.NoError(t, err)
	assert.Equal(t, uint64(2), profileDocCount(t, rt.IndexManager(), "ro"), "narrowed profile drops the removed server's docs")
}

func TestApplyConfig_ProfileCreateRenameDeleteReconcileImmediately(t *testing.T) {
	rt := newFunnelRuntime(t)
	seedProfileIndexes(t, rt)

	next := nextConfig(t, rt)
	next.Profiles = append(next.Profiles, config.ProfileConfig{Name: "fresh", Servers: []string{"b"}})
	_, err := rt.ApplyConfig(next, rt.ConfigPath())
	require.NoError(t, err)
	assert.Contains(t, profileDirs(t, rt), "fresh")
	assert.Equal(t, uint64(2), profileDocCount(t, rt.IndexManager(), "fresh"))

	next = nextConfig(t, rt)
	for i := range next.Profiles {
		if next.Profiles[i].Name == "fresh" {
			next.Profiles[i].Name = "renamed"
		}
	}
	_, err = rt.ApplyConfig(next, rt.ConfigPath())
	require.NoError(t, err)
	assert.Equal(t, uint64(2), profileDocCount(t, rt.IndexManager(), "renamed"))
	assert.NotContains(t, profileDirs(t, rt), "fresh")

	next = nextConfig(t, rt)
	var kept []config.ProfileConfig
	for _, p := range next.Profiles {
		if p.Name != "renamed" {
			kept = append(kept, p)
		}
	}
	next.Profiles = kept
	_, err = rt.ApplyConfig(next, rt.ConfigPath())
	require.NoError(t, err)
	assert.NotContains(t, profileDirs(t, rt), "renamed")
}

func TestApplyConfig_AnonymousProfileOnlyEditIsHarmless(t *testing.T) {
	rt := newFunnelRuntime(t)
	seedProfileIndexes(t, rt)

	next := nextConfig(t, rt)
	next.AnonymousProfile = "ro"
	res, err := rt.ApplyConfig(next, rt.ConfigPath())
	require.NoError(t, err)
	assert.Contains(t, res.ChangedFields, "anonymous_profile")
	assert.NotContains(t, res.ChangedFields, "profiles")
	assert.Equal(t, uint64(1), profileDocCount(t, rt.IndexManager(), "ro"))
}

func TestProfileIndexInputsChanged(t *testing.T) {
	cases := []struct {
		name    string
		changed []string
		want    bool
	}{
		{"nil", nil, false},
		{"servers only", []string{"mcpServers"}, false},
		{"profiles", []string{"profiles"}, true},
		{"anonymous_profile", []string{"anonymous_profile"}, true},
		{"mixed", []string{"tools_limit", "profiles"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, profileIndexInputsChanged(tc.changed))
		})
	}
}

func TestReloadConfiguration_ProfileEditRebuildsProfileIndexImmediately(t *testing.T) {
	rt := newFunnelRuntime(t)
	desired, err := rt.GetDesiredConfig()
	require.NoError(t, err)
	require.NoError(t, config.SaveConfig(desired, rt.ConfigPath()))
	// Settle the server set first, otherwise the reload's own LoadConfiguredServers
	// treats a and b as first-seen and empties the shared index.
	require.NoError(t, rt.LoadConfiguredServers(nil))
	require.NoError(t, rt.IndexManager().BatchIndexTools([]*config.ToolMetadata{
		toolMeta("a", "x"), toolMeta("b", "y"), toolMeta("b", "z"),
	}))
	rt.reindexAffectedProfiles("a")
	rt.reindexAffectedProfiles("b")
	require.Equal(t, uint64(1), profileDocCount(t, rt.IndexManager(), "ro"))

	t.Run("widen by hand edit", func(t *testing.T) {
		edited := nextConfig(t, rt)
		setProfileServers(edited, "ro", []string{"a", "b"})
		require.NoError(t, config.SaveConfig(edited, rt.ConfigPath()))
		require.NoError(t, rt.ReloadConfiguration())
		assert.Equal(t, uint64(3), profileDocCount(t, rt.IndexManager(), "ro"))
	})

	t.Run("delete by hand edit", func(t *testing.T) {
		edited := nextConfig(t, rt)
		var kept []config.ProfileConfig
		for _, p := range edited.Profiles {
			if p.Name != "full" {
				kept = append(kept, p)
			}
		}
		edited.Profiles = kept
		require.NoError(t, config.SaveConfig(edited, rt.ConfigPath()))
		require.NoError(t, rt.ReloadConfiguration())
		assert.NotContains(t, profileDirs(t, rt), "full")
	})
}

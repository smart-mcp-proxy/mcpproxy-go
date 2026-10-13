package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Review round 5 (finding 1): ApplyConfig with a different destination path
// publishes that path with the commit, on every branch (hot change,
// unchanged config, restart-required-only), so ConfigPath, SaveConfiguration
// and ReloadConfiguration all follow it and a later reload cannot revert the
// apply from the old file.
func TestCommit_R5_ApplyConfigNewPathIsPublished(t *testing.T) {
	cases := map[string]func(c *config.Config){
		"hot change": func(c *config.Config) { c.ToolsLimit = c.ToolsLimit + 7 },
		"unchanged":  func(*config.Config) {},
		"restart-required only": func(c *config.Config) {
			c.RoutingMode = config.RoutingModeDirect
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rt, pathA := newSpec116Runtime(t, func(c *config.Config) {
				c.RoutingMode = config.RoutingModeRetrieveTools
			}, disabledStdio("a"))
			pathB := filepath.Join(t.TempDir(), "other.json")
			aBefore, err := os.ReadFile(pathA)
			require.NoError(t, err)

			next := cloneRuntimeConfig(t, rt.Config())
			mutate(next)
			_, err = rt.ApplyConfig(next, pathB)
			require.NoError(t, err)

			assert.Equal(t, pathB, rt.ConfigPath(), "published snapshot path")
			rt.mu.RLock()
			assert.Equal(t, pathB, rt.cfgPath, "r.cfgPath")
			rt.mu.RUnlock()
			_, err = os.Stat(pathB)
			require.NoError(t, err, "the apply wrote the new path")

			// SaveConfiguration writes B, never A.
			require.NoError(t, os.Remove(pathB))
			require.NoError(t, rt.SaveConfiguration())
			_, err = os.Stat(pathB)
			require.NoError(t, err, "save must write the published path")
			aAfter, err := os.ReadFile(pathA)
			require.NoError(t, err)
			assert.Equal(t, string(aBefore), string(aAfter), "the old path is never written")

			// ReloadConfiguration reads B.
			onDisk, err := config.LoadFromFile(pathB)
			require.NoError(t, err)
			onDisk.ToolsLimit = 777
			require.NoError(t, config.SaveConfig(onDisk, pathB))
			require.NoError(t, rt.ReloadConfiguration())
			assert.Equal(t, 777, rt.Config().ToolsLimit, "reload must read the published path")
		})
	}
}

// Review round 5 (finding 2): MutateGlobal works on a deep copy of the global
// configuration. A nested edit (logging, profile policy) never leaks into the
// published base when the commit aborts or fails in its disk or storage step,
// and a successful nested edit publishes a new generation without touching
// the old snapshot.
func TestCommit_R5_MutateGlobalNestedEditsDoNotLeak(t *testing.T) {
	newRT := func(t *testing.T) *Runtime {
		rt, _ := newSpec116Runtime(t, func(c *config.Config) {
			if c.Logging == nil {
				c.Logging = &config.LogConfig{}
			}
			c.Logging.Level = "info"
			c.Profiles = []config.ProfileConfig{{
				Name: "dev", Servers: []string{"a"},
				Tools: &config.ProfileToolRules{Classify: map[string]string{"a:x": "read"}},
			}}
		}, disabledStdio("a"))
		return rt
	}
	mutate := func(cfg *config.Config) error {
		cfg.Logging.Level = "debug"
		cfg.Profiles[0].Servers[0] = "zzz"
		cfg.Profiles[0].Tools.Classify["a:x"] = "destructive"
		return nil
	}
	assertUntouched := func(t *testing.T, cfg *config.Config, what string) {
		t.Helper()
		assert.Equal(t, "info", cfg.Logging.Level, "%s: logging", what)
		assert.Equal(t, "a", cfg.Profiles[0].Servers[0], "%s: profile servers", what)
		assert.Equal(t, "read", cfg.Profiles[0].Tools.Classify["a:x"], "%s: classify", what)
	}

	t.Run("abort", func(t *testing.T) {
		rt := newRT(t)
		snap := rt.ConfigSnapshot()
		_, err := rt.Commit("r5_abort", func(tx *CommitTx) error {
			if err := tx.MutateGlobal(mutate); err != nil {
				return err
			}
			return errors.New("abort")
		})
		require.Error(t, err)
		assertUntouched(t, snap.Config, "captured snapshot")
		assertUntouched(t, rt.Config(), "live config")
	})
	t.Run("disk failure", func(t *testing.T) {
		rt := newRT(t)
		snap := rt.ConfigSnapshot()
		rt.testHooks.diskWriteFault = func(string) error { return errors.New("injected disk failure") }
		_, err := rt.Commit("r5_disk", func(tx *CommitTx) error { return tx.MutateGlobal(mutate) })
		rt.testHooks.diskWriteFault = nil
		require.Error(t, err)
		assertUntouched(t, snap.Config, "captured snapshot")
		assertUntouched(t, rt.Config(), "live config")
	})
	t.Run("storage failure", func(t *testing.T) {
		rt := newRT(t)
		snap := rt.ConfigSnapshot()
		rt.testHooks.storageFault = func(string) error { return errors.New("injected storage failure") }
		_, err := rt.Commit("r5_storage", func(tx *CommitTx) error { return tx.MutateGlobal(mutate) })
		rt.testHooks.storageFault = nil
		require.Error(t, err)
		assertUntouched(t, snap.Config, "captured snapshot")
		assertUntouched(t, rt.Config(), "live config")
	})
	t.Run("success", func(t *testing.T) {
		rt := newRT(t)
		snap := rt.ConfigSnapshot()
		res, err := rt.Commit("r5_ok", func(tx *CommitTx) error { return tx.MutateGlobal(mutate) })
		require.NoError(t, err)
		require.True(t, res.Published, "a nested-only edit is a change and must publish")
		assert.Greater(t, res.Generation, snap.Version, "new generation")
		assertUntouched(t, snap.Config, "old snapshot")
		live := rt.Config()
		assert.Equal(t, "debug", live.Logging.Level)
		assert.Equal(t, "zzz", live.Profiles[0].Servers[0])
		assert.Equal(t, "destructive", live.Profiles[0].Tools.Classify["a:x"])
	})
}

// Review round 5 (finding 1, failure side): an ApplyConfig to a new path that
// fails in storage keeps the old path published and leaves no file at the
// destination.
func TestCommit_R5_ApplyConfigNewPathFailureKeepsOldPath(t *testing.T) {
	rt, pathA := newSpec116Runtime(t, nil, disabledStdio("a"))
	pathB := filepath.Join(t.TempDir(), "other.json")
	next := cloneRuntimeConfig(t, rt.Config())
	next.ToolsLimit = next.ToolsLimit + 3
	rt.testHooks.storageFault = func(string) error { return errors.New("injected storage failure") }
	_, err := rt.ApplyConfig(next, pathB)
	rt.testHooks.storageFault = nil
	require.Error(t, err)
	assert.Equal(t, pathA, rt.ConfigPath())
	rt.mu.RLock()
	assert.Equal(t, pathA, rt.cfgPath)
	rt.mu.RUnlock()
	_, statErr := os.Stat(pathB)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "the destination is left as it was")
}

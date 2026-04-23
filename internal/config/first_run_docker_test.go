package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfigDockerIsolationOffForExistingInstalls(t *testing.T) {
	cfg := DefaultConfig()
	require.NotNil(t, cfg.DockerIsolation)
	assert.False(t, cfg.DockerIsolation.Enabled,
		"DefaultConfig() must return Enabled=false so LoadFromFile's default-then-merge path preserves existing user configs unchanged")
}

func TestApplyFirstRunDockerIsolation(t *testing.T) {
	t.Run("enables when probe returns true", func(t *testing.T) {
		restore := overrideProbe(t, true)
		defer restore()
		cfg := DefaultConfig()
		applyFirstRunDockerIsolation(cfg)
		assert.True(t, cfg.DockerIsolation.Enabled)
	})

	t.Run("stays off when probe returns false", func(t *testing.T) {
		restore := overrideProbe(t, false)
		defer restore()
		cfg := DefaultConfig()
		applyFirstRunDockerIsolation(cfg)
		assert.False(t, cfg.DockerIsolation.Enabled)
	})

	t.Run("no-op on nil DockerIsolation", func(t *testing.T) {
		restore := overrideProbe(t, true)
		defer restore()
		cfg := &Config{DockerIsolation: nil}
		applyFirstRunDockerIsolation(cfg)
		assert.Nil(t, cfg.DockerIsolation)
	})

	t.Run("no-op on nil config", func(t *testing.T) {
		restore := overrideProbe(t, true)
		defer restore()
		applyFirstRunDockerIsolation(nil) // must not panic
	})
}

func TestLoadOrCreateConfigEnablesIsolationWhenDockerAvailable(t *testing.T) {
	restore := overrideProbe(t, true)
	defer restore()

	dir := t.TempDir()
	cfg, err := LoadOrCreateConfig(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg.DockerIsolation)
	assert.True(t, cfg.DockerIsolation.Enabled,
		"fresh install with Docker available should have isolation on")

	// Reload from disk to confirm the flag was persisted.
	raw, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"enabled": true`,
		"docker_isolation.enabled=true must be written to the new config file")
}

func TestLoadOrCreateConfigLeavesIsolationOffWhenDockerUnavailable(t *testing.T) {
	restore := overrideProbe(t, false)
	defer restore()

	dir := t.TempDir()
	cfg, err := LoadOrCreateConfig(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg.DockerIsolation)
	assert.False(t, cfg.DockerIsolation.Enabled,
		"fresh install without Docker should keep isolation off so servers don't fail on first connect")
}

func overrideProbe(t *testing.T, value bool) func() {
	t.Helper()
	original := dockerDaemonProbe
	dockerDaemonProbe = func() bool { return value }
	return func() { dockerDaemonProbe = original }
}

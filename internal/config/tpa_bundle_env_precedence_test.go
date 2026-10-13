package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEffectiveTPABundlePath_EnvWinsEverywhere closes the precedence hole:
// MCPPROXY_TPA_BUNDLE_PATH was only applied inside config.Load/LoadFromFile, so
// a config arriving through POST /api/v1/config/apply (which never goes through
// the loader) silently overrode the env var on the next scanner reconfigure.
// The accessor itself now enforces the precedence, so every path — loader,
// /config/apply, hot-reload, stdio — resolves the same corpus.
func TestEffectiveTPABundlePath_EnvWinsEverywhere(t *testing.T) {
	t.Setenv("MCPPROXY_TPA_BUNDLE_PATH", "/env/bundle.json")

	assert.Equal(t, "/env/bundle.json",
		(&SecurityConfig{TPABundlePath: "/posted/bundle.json"}).EffectiveTPABundlePath(),
		"a config posted to /api/v1/config/apply must not override the env var")

	var nilSec *SecurityConfig
	assert.Equal(t, "/env/bundle.json", nilSec.EffectiveTPABundlePath(),
		"a config with no security block still honours the env override")

	assert.Equal(t, "/env/bundle.json", (&SecurityConfig{}).EffectiveTPABundlePath())
}

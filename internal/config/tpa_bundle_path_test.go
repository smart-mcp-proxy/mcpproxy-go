package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSecurityConfig_TPABundlePath covers spec 086 FR-019: the TPA signature
// bundle location must be configuration-driven, never hardcoded. The accessor
// is nil-safe so callers can read it off a config with no security block.
func TestSecurityConfig_TPABundlePath(t *testing.T) {
	var nilCfg *SecurityConfig
	assert.Equal(t, "", nilCfg.EffectiveTPABundlePath(), "nil SecurityConfig means embedded default")
	assert.Equal(t, "", (&SecurityConfig{}).EffectiveTPABundlePath())
	assert.Equal(t, "/opt/tpa/scanner-bundle.json",
		(&SecurityConfig{TPABundlePath: "/opt/tpa/scanner-bundle.json"}).EffectiveTPABundlePath())
}

// TestTPABundlePathEnvOverride covers the FR-019 env-var override half.
func TestTPABundlePathEnvOverride(t *testing.T) {
	t.Setenv("MCPPROXY_TPA_BUNDLE_PATH", "/env/scanner-bundle.json")

	cfg := DefaultConfig()
	applyTLSEnvOverrides(cfg)

	if assert.NotNil(t, cfg.Security, "the env override must materialize the security block") {
		assert.Equal(t, "/env/scanner-bundle.json", cfg.Security.EffectiveTPABundlePath())
	}
}

// TestTPABundlePathEnvOverridesFile pins precedence: the env var wins over a
// value already present in mcp_config.json (the established loader convention).
func TestTPABundlePathEnvOverridesFile(t *testing.T) {
	t.Setenv("MCPPROXY_TPA_BUNDLE_PATH", "/env/scanner-bundle.json")

	cfg := DefaultConfig()
	cfg.Security = &SecurityConfig{TPABundlePath: "/file/scanner-bundle.json"}
	applyTLSEnvOverrides(cfg)

	assert.Equal(t, "/env/scanner-bundle.json", cfg.Security.EffectiveTPABundlePath())
}

// TestTPABundlePathNoEnvKeepsFile ensures an unset env var leaves the file
// value alone.
func TestTPABundlePathNoEnvKeepsFile(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Security = &SecurityConfig{TPABundlePath: "/file/scanner-bundle.json"}
	applyTLSEnvOverrides(cfg)

	assert.Equal(t, "/file/scanner-bundle.json", cfg.Security.EffectiveTPABundlePath())
}

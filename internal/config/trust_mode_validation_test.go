package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsValidTrustMode pins the accepted trust_mode vocabulary (GH #938 finding
// 1). Only the three spec-086 values plus the empty "inherit" sentinel are
// valid; anything else — including a wrong-case spelling of a real mode — is
// rejected so an operator who typos "Scan" is told instead of being silently
// downgraded to manual behaviour.
func TestIsValidTrustMode(t *testing.T) {
	valid := []string{"", "auto", "scan", "manual"}
	for _, v := range valid {
		assert.True(t, IsValidTrustMode(v), "trust_mode %q must be accepted", v)
	}
	invalid := []string{"yolo", "Scan", "SCAN", "Manual", "auto ", "none", "off"}
	for _, v := range invalid {
		assert.False(t, IsValidTrustMode(v), "trust_mode %q must be rejected", v)
	}
}

// TestValidTrustModesList ensures the operator-facing error text has a single
// source of truth listing every accepted value.
func TestValidTrustModesList(t *testing.T) {
	assert.Equal(t, []string{"auto", "scan", "manual"}, ValidTrustModes())
	assert.Equal(t, "auto, scan, manual", strings.Join(ValidTrustModes(), ", "))
}

// TestValidateDetailed_TrustMode covers the config-load half of GH #938 finding
// 1: a hand-edited mcp_config.json carrying a bogus trust_mode must surface as
// a validation error rather than being silently treated as manual.
func TestValidateDetailed_TrustMode(t *testing.T) {
	newCfg := func(mode string) *Config {
		cfg := DefaultConfig()
		cfg.Servers = []*ServerConfig{{
			Name:      "srv",
			URL:       "https://example.com/mcp",
			Protocol:  "streamable-http",
			TrustMode: mode,
		}}
		return cfg
	}

	t.Run("bogus value is reported", func(t *testing.T) {
		errs := newCfg("yolo").ValidateDetailed()
		var found *ValidationError
		for i := range errs {
			if strings.HasSuffix(errs[i].Field, ".trust_mode") {
				found = &errs[i]
				break
			}
		}
		require.NotNil(t, found, "a bogus trust_mode must produce a validation error, got %+v", errs)
		assert.Contains(t, found.Message, "yolo")
		assert.Contains(t, found.Message, "auto, scan, manual")
	})

	t.Run("wrong case is reported", func(t *testing.T) {
		errs := newCfg("Scan").ValidateDetailed()
		var found bool
		for i := range errs {
			if strings.HasSuffix(errs[i].Field, ".trust_mode") {
				found = true
			}
		}
		assert.True(t, found, "trust_mode \"Scan\" must be reported, got %+v", errs)
	})

	for _, mode := range []string{"", "auto", "scan", "manual"} {
		t.Run("valid "+mode, func(t *testing.T) {
			for _, e := range newCfg(mode).ValidateDetailed() {
				assert.NotContains(t, e.Field, "trust_mode", "valid trust_mode %q must not error: %+v", mode, e)
			}
		})
	}
}

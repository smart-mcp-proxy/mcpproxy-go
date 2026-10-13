package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 085 T017: --tool-response-mode flag → config wiring. The flag only
// overrides when explicitly set; invalid values are rejected by the
// validation that loadConfig runs immediately after applying it.
func TestApplyToolResponseModeFlag(t *testing.T) {
	t.Run("explicit flag overrides file/env value", func(t *testing.T) {
		cfg := &config.Config{ToolResponseMode: config.ToolResponseModeFull}
		applyToolResponseModeFlag(cfg, true, config.ToolResponseModeCompact)
		assert.Equal(t, config.ToolResponseModeCompact, cfg.ToolResponseMode)
	})

	t.Run("unset flag never clobbers the loaded value", func(t *testing.T) {
		cfg := &config.Config{ToolResponseMode: config.ToolResponseModeCompact}
		applyToolResponseModeFlag(cfg, false, "")
		assert.Equal(t, config.ToolResponseModeCompact, cfg.ToolResponseMode)
	})

	t.Run("explicit empty flag resets to default", func(t *testing.T) {
		cfg := &config.Config{ToolResponseMode: config.ToolResponseModeCompact}
		applyToolResponseModeFlag(cfg, true, "")
		assert.Equal(t, "", cfg.ToolResponseMode)
		require.NoError(t, cfg.Validate(), "empty mode is valid (= full)")
	})

	t.Run("invalid flag value fails the post-apply validation", func(t *testing.T) {
		cfg := &config.Config{}
		applyToolResponseModeFlag(cfg, true, "bogus")
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tool_response_mode")
	})
}

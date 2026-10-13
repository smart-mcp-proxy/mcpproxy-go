package server

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 085 T016 (⟲#1b, FR-001/FR-005/FR-015): the effective retrieve_tools
// serialization mode is resolved from the LIVE config snapshot
// (p.currentConfig()) — NOT the construction-time p.config the retrieve path
// historically read — so a hot-reload changes the resolved mode without
// reconstructing the server. Precedence: per-call detail override >
// configured tool_response_mode > full.
func TestEffectiveToolResponseMode_HotReload(t *testing.T) {
	proxy, rt := newRuntimeBackedProxy(t)

	// Default (unset) resolves to full.
	assert.Equal(t, config.ToolResponseModeFull, proxy.effectiveToolResponseMode(""))

	// Hot-reload to compact: the SAME proxy instance must resolve compact on
	// the next call — no reconstruction.
	newCfg := *rt.Config()
	newCfg.ToolResponseMode = config.ToolResponseModeCompact
	rt.UpdateConfig(&newCfg, "")
	assert.Equal(t, config.ToolResponseModeCompact, proxy.effectiveToolResponseMode(""),
		"reloaded config must change the resolved mode without reconstructing the server")

	// And back to full.
	backCfg := *rt.Config()
	backCfg.ToolResponseMode = config.ToolResponseModeFull
	rt.UpdateConfig(&backCfg, "")
	assert.Equal(t, config.ToolResponseModeFull, proxy.effectiveToolResponseMode(""))
}

func TestEffectiveToolResponseMode_DetailOverride(t *testing.T) {
	proxy, rt := newRuntimeBackedProxy(t)

	// detail overrides in BOTH directions, regardless of the configured mode.
	assert.Equal(t, config.ToolResponseModeCompact, proxy.effectiveToolResponseMode(config.ToolResponseModeCompact),
		"detail=compact must override configured full")

	newCfg := *rt.Config()
	newCfg.ToolResponseMode = config.ToolResponseModeCompact
	rt.UpdateConfig(&newCfg, "")
	assert.Equal(t, config.ToolResponseModeFull, proxy.effectiveToolResponseMode(config.ToolResponseModeFull),
		"detail=full must override configured compact")

	// Unset detail falls back to the configured mode.
	assert.Equal(t, config.ToolResponseModeCompact, proxy.effectiveToolResponseMode(""))

	// An unknown detail value is ignored (falls back to configured mode) —
	// the tool-schema enum is the real gate; this is defense in depth.
	assert.Equal(t, config.ToolResponseModeCompact, proxy.effectiveToolResponseMode("bogus"))
}

// Without a runtime (standalone constructions), resolution falls back to the
// construction-time config and still defaults to full.
func TestEffectiveToolResponseMode_NoRuntimeFallback(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	assert.Equal(t, config.ToolResponseModeFull, proxy.effectiveToolResponseMode(""))

	proxy.config.ToolResponseMode = config.ToolResponseModeCompact
	assert.Equal(t, config.ToolResponseModeCompact, proxy.effectiveToolResponseMode(""))
}

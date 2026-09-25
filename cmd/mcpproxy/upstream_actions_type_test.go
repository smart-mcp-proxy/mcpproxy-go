package main

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpstreamRowsReadActionsAsGoStringSlice is a review-round-3 finding: the
// config-mode (daemon-less) `mcpproxy upstream list` path builds
// `health["actions"]` directly from `health.CalculateHealth(...).Actions`,
// which is a native `[]string` — it never round-trips through JSON. The
// client-mode path, by contrast, decodes a JSON API response, where an array
// always comes back as `[]interface{}`. upstreamServerRows's extraction of
// ACTION (FR-012: keyed on actions[0], not the legacy `action` field) only
// type-asserted `[]interface{}`, so in config mode that assertion silently
// failed every time and ACTION fell back to the legacy `action` field
// instead. This is latent today only because `action == actions[0]` holds
// for every branch CalculateHealth returns; it pins the real invariant
// (ACTION reads actions[0]) against both slice shapes so a future divergence
// between the two fields is caught in either mode.
func TestUpstreamRowsReadActionsAsGoStringSlice(t *testing.T) {
	rows := upstreamServerRows([]map[string]interface{}{
		{
			"name":       "config-mode-server",
			"protocol":   "stdio",
			"tool_count": float64(0),
			"health": map[string]interface{}{
				"level":       "unhealthy",
				"admin_state": "enabled",
				"summary":     "Authentication required",
				// Deliberately mismatched from actions[0] so the test fails
				// if ACTION silently falls back to this legacy field instead
				// of reading actions[0], as it does the config-mode path.
				"action":  health.ActionRestart,
				"status":  "sign_in_required",
				"usable":  false,
				"actions": []string{health.ActionLogin, health.ActionApprove},
			},
		},
	})
	require.Len(t, rows, 1)
	assert.Equal(t, "auth login --server=config-mode-server", rows[0][5],
		"ACTION must key on actions[0] (native []string in config mode), not fall back to the legacy action field")
}

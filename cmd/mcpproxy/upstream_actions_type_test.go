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

// TestUpstreamRowsReadActionsAsJSONInterfaceSlice is the client-mode (daemon)
// counterpart to TestUpstreamRowsReadActionsAsGoStringSlice above: a decoded
// JSON API response always turns `actions` into `[]interface{}`, not
// `[]string`. No fixture in this file or in upstream_list_status_test.go
// exercised that shape with action != actions[0] (a round-4 review finding),
// so a future regression in the `case []interface{}:` branch at
// upstream_cmd.go's actions-extraction switch would go undetected as long as
// every real CalculateHealth branch keeps action == actions[0]. This test
// deliberately mismatches the two, the same way the []string test above
// does, so the []interface{} branch is pinned too.
func TestUpstreamRowsReadActionsAsJSONInterfaceSlice(t *testing.T) {
	rows := upstreamServerRows([]map[string]interface{}{
		{
			"name":       "client-mode-server",
			"protocol":   "stdio",
			"tool_count": float64(0),
			"health": map[string]interface{}{
				"level":       "unhealthy",
				"admin_state": "enabled",
				"summary":     "Authentication required",
				// Deliberately mismatched from actions[0], mirroring a
				// decoded JSON payload where actions is []interface{}.
				"action":  health.ActionRestart,
				"status":  "sign_in_required",
				"usable":  false,
				"actions": []interface{}{health.ActionLogin, health.ActionApprove},
			},
		},
	})
	require.Len(t, rows, 1)
	assert.Equal(t, "auth login --server=client-mode-server", rows[0][5],
		"ACTION must key on actions[0] (JSON []interface{} in client mode), not fall back to the legacy action field")
}

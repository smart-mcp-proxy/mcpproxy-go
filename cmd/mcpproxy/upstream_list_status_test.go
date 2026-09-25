package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T043 (Spec 109 FR-010–015): `mcpproxy upstream list`'s STATUS column is the
// status label, ACTION is the existing CLI hint keyed on `actions[0]`, the
// GH #938 held-tools suffix and ACTION fallback survive, `--status` filters
// (repeatable and comma-separated, union semantics), and `-o json` carries
// `status`/`usable`/`actions` alongside every legacy field unchanged.

func healthFixture(status string, usable bool, actions []interface{}, extra map[string]interface{}) map[string]interface{} {
	h := map[string]interface{}{
		"level":       "healthy",
		"admin_state": "enabled",
		"summary":     "placeholder summary",
		"status":      status,
		"usable":      usable,
		"actions":     actions,
	}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

func TestUpstreamServerRows_StatusLabelAndActionHint(t *testing.T) {
	rows := upstreamServerRows([]map[string]interface{}{
		{
			"name":       "github",
			"protocol":   "http",
			"tool_count": float64(0),
			"health": healthFixture("sign_in_required", false, []interface{}{"login"}, map[string]interface{}{
				"level":       "degraded",
				"admin_state": "enabled",
				"summary":     "Sign-in required",
				"action":      "login",
			}),
		},
		{
			"name":       "filesystem",
			"protocol":   "stdio",
			"tool_count": float64(14),
			"health": healthFixture("ready", true, []interface{}{}, map[string]interface{}{
				"summary": "Connected (14 tools)",
				"action":  "",
			}),
		},
		{
			"name":       "scratch",
			"protocol":   "stdio",
			"tool_count": float64(0),
			"health": healthFixture("disabled", false, []interface{}{"enable"}, map[string]interface{}{
				"level":       "healthy",
				"admin_state": "disabled",
				"summary":     "Disabled",
				"action":      "enable",
			}),
		},
	})
	require.Len(t, rows, 3)

	byName := map[string][]string{}
	for _, r := range rows {
		byName[r[1]] = r
	}

	// STATUS is the status label, not the free-text summary.
	assert.Equal(t, "Sign-in required", byName["github"][4])
	assert.Equal(t, "auth login --server=github", byName["github"][5])

	assert.Equal(t, "Online", byName["filesystem"][4])
	assert.Equal(t, "-", byName["filesystem"][5])

	assert.Equal(t, "Disabled", byName["scratch"][4])
	assert.Equal(t, "upstream enable scratch", byName["scratch"][5])
}

// GH #938: the held-tools suffix and ACTION fallback survive the STATUS
// column switching from free-text summary to the status label.
func TestUpstreamServerRows_HeldToolsSuffixSurvivesLabelSwitch(t *testing.T) {
	rows := upstreamServerRows([]map[string]interface{}{
		{
			"name":       "poisoned",
			"protocol":   "stdio",
			"tool_count": float64(1),
			"health": healthFixture("ready", true, []interface{}{}, map[string]interface{}{
				"summary": "Connected (1 tool)",
			}),
			"quarantine": map[string]interface{}{"changed_count": float64(1)},
		},
	})
	require.Len(t, rows, 1)
	row := rows[0]
	assert.Equal(t, "Online · 1 changed held", row[4])
	assert.Equal(t, "tools list --server=poisoned", row[5])
	assert.NotEqual(t, "✅", row[0])
}

// edit_url (new in this PR) gets the same CLI hint as configure.
func TestUpstreamServerRows_EditURLActionHint(t *testing.T) {
	rows := upstreamServerRows([]map[string]interface{}{
		{
			"name":       "broken-url",
			"protocol":   "http",
			"tool_count": float64(0),
			"health": healthFixture("needs_config", false, []interface{}{"edit_url"}, map[string]interface{}{
				"level":       "unhealthy",
				"admin_state": "enabled",
				"summary":     "Host not found",
				"action":      "edit_url",
			}),
		},
	})
	require.Len(t, rows, 1)
	assert.Equal(t, "Needs configuration", rows[0][4])
	assert.Equal(t, "Edit config", rows[0][5])
}

func TestFilterServersByStatus(t *testing.T) {
	servers := []map[string]interface{}{
		{"name": "a", "health": map[string]interface{}{"status": "ready"}},
		{"name": "b", "health": map[string]interface{}{"status": "needs_review"}},
		{"name": "c", "health": map[string]interface{}{"status": "error"}},
		{"name": "d", "health": map[string]interface{}{"status": "ready"}},
	}

	t.Run("no filter returns everything", func(t *testing.T) {
		got := filterServersByStatus(servers, nil)
		assert.Len(t, got, 4)
	})

	t.Run("single value", func(t *testing.T) {
		got := filterServersByStatus(servers, []string{"needs_review"})
		require.Len(t, got, 1)
		assert.Equal(t, "b", got[0]["name"])
	})

	// FR-015: repeatable flag — union of the matching rows.
	t.Run("repeated flag is a union", func(t *testing.T) {
		got := filterServersByStatus(servers, []string{"ready", "needs_review"})
		names := namesOf(got)
		assert.ElementsMatch(t, []string{"a", "b", "d"}, names)
	})

	// FR-015: comma-separated value is equivalent to repeating the flag.
	t.Run("comma-separated is equivalent to repeating the flag", func(t *testing.T) {
		got := filterServersByStatus(servers, []string{"ready,needs_review"})
		names := namesOf(got)
		assert.ElementsMatch(t, []string{"a", "b", "d"}, names)
	})

	t.Run("repeated and comma-separated produce the identical union", func(t *testing.T) {
		repeated := namesOf(filterServersByStatus(servers, []string{"ready", "needs_review"}))
		combined := namesOf(filterServersByStatus(servers, []string{"ready,needs_review"}))
		assert.ElementsMatch(t, repeated, combined)
	})
}

func namesOf(servers []map[string]interface{}) []string {
	names := make([]string, 0, len(servers))
	for _, s := range servers {
		names = append(names, getStringField(s, "name"))
	}
	return names
}

// -o json output carries status/usable/actions alongside every legacy field.
func TestOutputServers_JSONCarriesStatusVocabulary(t *testing.T) {
	prevFormat := globalOutputFormat
	t.Cleanup(func() { globalOutputFormat = prevFormat })
	globalOutputFormat = "json"

	servers := []map[string]interface{}{
		{
			"name":       "github",
			"enabled":    true,
			"protocol":   "http",
			"connected":  false,
			"tool_count": 0,
			"health": healthFixture("sign_in_required", false, []interface{}{"login"}, map[string]interface{}{
				"level":       "degraded",
				"admin_state": "enabled",
				"summary":     "Sign-in required",
				"action":      "login",
			}),
		},
	}

	stdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	err = outputServers(servers)
	w.Close()
	os.Stdout = stdout
	require.NoError(t, err)

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	var decoded []map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	require.Len(t, decoded, 1)

	h, ok := decoded[0]["health"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "sign_in_required", h["status"])
	assert.Equal(t, false, h["usable"])
	assert.Equal(t, []interface{}{"login"}, h["actions"])
	// Legacy fields unchanged.
	assert.Equal(t, "degraded", h["level"])
	assert.Equal(t, "enabled", h["admin_state"])
	assert.Equal(t, "Sign-in required", h["summary"])
	assert.Equal(t, "login", h["action"])
}

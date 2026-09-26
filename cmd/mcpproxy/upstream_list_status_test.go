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

// TestValidateStatusFlag pins the CLI-side validation: a typo'd or wrongly-
// cased --status value previously matched nothing in filterServersByStatus
// and silently returned an empty result set (exit 0), indistinguishable from
// "no servers in that state" — mirrors validateTrustModeFlag (GH #938).
func TestValidateStatusFlag(t *testing.T) {
	for _, valid := range []string{
		"", "ready", "connecting", "sign_in_required", "needs_review",
		"needs_secret", "needs_config", "error", "disabled", "ready,needs_review",
	} {
		assert.NoError(t, validateStatusFlag([]string{valid}), "status %q must be accepted", valid)
	}
	assert.NoError(t, validateStatusFlag(nil), "no --status at all must be accepted")

	for _, invalid := range []string{"signin_required", "READY", "bogus"} {
		err := validateStatusFlag([]string{invalid})
		require.Error(t, err, "status %q must be refused", invalid)
		assert.Contains(t, err.Error(), "ready")
	}

	// A typo alongside a valid value in the same comma-separated/repeated
	// filter must still be refused — one matching entry is not a pass.
	err := validateStatusFlag([]string{"ready,bogus"})
	require.Error(t, err, "a typo anywhere in the filter must be refused")
}

// TestUpstreamListStatusFlagRegistration exercises --status through actual
// Cobra/pflag flag registration, instead of hand-building a []string and
// calling filterServersByStatus directly. It pins that --status is a
// StringArrayVar — each repeat is captured as one raw token, and
// comma-splitting happens downstream (filterServersByStatus/validateStatusFlag)
// — so a future switch to StringSliceVar (which comma-splits itself at the
// pflag layer) would double-split silently with no test catching the change.
func TestUpstreamListStatusFlagRegistration(t *testing.T) {
	prev := upstreamListStatus
	t.Cleanup(func() {
		upstreamListStatus = prev
		if f := upstreamListCmd.Flags().Lookup("status"); f != nil {
			f.Changed = false
		}
	})
	upstreamListStatus = nil

	flags := upstreamListCmd.Flags()
	flag := flags.Lookup("status")
	require.NotNil(t, flag, "upstream list must expose --status")

	require.NoError(t, flags.Set("status", "ready,needs_review"))
	require.NoError(t, flags.Set("status", "error"))

	// StringArrayVar keeps each repeat as one literal token — no comma-split
	// happens at the pflag layer itself.
	assert.Equal(t, []string{"ready,needs_review", "error"}, upstreamListStatus)

	// The downstream comma-split still produces the expected union.
	servers := []map[string]interface{}{
		{"name": "a", "health": map[string]interface{}{"status": "ready"}},
		{"name": "b", "health": map[string]interface{}{"status": "needs_review"}},
		{"name": "c", "health": map[string]interface{}{"status": "error"}},
		{"name": "d", "health": map[string]interface{}{"status": "disabled"}},
	}
	got := namesOf(filterServersByStatus(servers, upstreamListStatus))
	assert.ElementsMatch(t, []string{"a", "b", "c"}, got)
}

// TestRunUpstreamListRejectsInvalidStatusBeforeContactingDaemon exercises
// runUpstreamList itself (not just validateStatusFlag in isolation) with an
// invalid --status value. Every other test here calls validateStatusFlag or
// filterServersByStatus directly, so a refactor that moved/dropped the
// validateStatusFlag call at the top of runUpstreamList — reintroducing the
// GH #938-style silent-empty-result bug this PR fixed — would keep the whole
// suite green. This test fails in that case: without the early validation,
// runUpstreamList would instead try to reach a daemon (none is running in
// this test process) and return a connection error with no mention of the
// bad status value.
func TestRunUpstreamListRejectsInvalidStatusBeforeContactingDaemon(t *testing.T) {
	prevStatus := upstreamListStatus
	t.Cleanup(func() { upstreamListStatus = prevStatus })
	upstreamListStatus = []string{"bogus-status"}

	err := runUpstreamList(upstreamListCmd, nil)
	require.Error(t, err, "an invalid --status value must be refused, not silently produce an empty table")
	assert.Contains(t, err.Error(), "bogus-status")
	assert.NotContains(t, err.Error(), "connection refused",
		"validation must short-circuit before runUpstreamList ever tries to reach the daemon")
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

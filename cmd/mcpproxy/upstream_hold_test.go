package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerHoldSummary is GH #938 finding 3: `mcpproxy upstream list` rendered
// a green "✅ Connected (1 tool)" for a server whose only tool was sitting held
// by the scan gate. The quarantine counts are already in the
// GET /api/v1/servers payload — the CLI just dropped them.
func TestServerHoldSummary(t *testing.T) {
	t.Run("no holds", func(t *testing.T) {
		count, label := serverHoldSummary(map[string]interface{}{"name": "srv"})
		assert.Equal(t, 0, count)
		assert.Equal(t, "", label)
	})

	t.Run("changed tool", func(t *testing.T) {
		count, label := serverHoldSummary(map[string]interface{}{
			"quarantine": map[string]interface{}{
				"pending_count": float64(0),
				"changed_count": float64(1),
				"blocked_count": float64(0),
			},
		})
		assert.Equal(t, 1, count)
		assert.Equal(t, "1 changed", label)
	})

	t.Run("mixed", func(t *testing.T) {
		count, label := serverHoldSummary(map[string]interface{}{
			"quarantine": map[string]interface{}{
				"pending_count": float64(2),
				"changed_count": float64(1),
				"blocked_count": float64(3),
			},
		})
		// The count is a "needs attention" trigger, not an exact tool total —
		// one record can be both blocked and pending — so only the label is
		// pinned exactly.
		assert.Positive(t, count)
		assert.Equal(t, "2 pending, 1 changed, 3 blocked", label)
	})
}

// TestUpstreamRowsSurfaceHolds pins the rendered row: the status must name the
// hold and the emoji must stop reading as all-clear.
func TestUpstreamRowsSurfaceHolds(t *testing.T) {
	rows := upstreamServerRows([]map[string]interface{}{
		{
			"name":       "poisoned",
			"protocol":   "stdio",
			"tool_count": float64(1),
			"status":     "Connected (1 tool)",
			"health": map[string]interface{}{
				"level":       "healthy",
				"admin_state": "enabled",
				"summary":     "Connected (1 tool)",
			},
			"quarantine": map[string]interface{}{"changed_count": float64(1)},
		},
		{
			"name":       "clean",
			"protocol":   "stdio",
			"tool_count": float64(4),
			"status":     "Connected (4 tools)",
			"health": map[string]interface{}{
				"level":       "healthy",
				"admin_state": "enabled",
				"summary":     "Connected (4 tools)",
			},
		},
	})
	require.Len(t, rows, 2)

	// Rows are name-sorted by the caller; assert by content instead.
	var held, clean []string
	for _, r := range rows {
		if r[1] == "poisoned" {
			held = r
		} else {
			clean = r
		}
	}
	require.NotNil(t, held)
	require.NotNil(t, clean)

	assert.Contains(t, held[4], "1 changed held", "the status column must name the hold")
	assert.NotEqual(t, "✅", held[0], "a server with a held tool must not render the all-clear emoji")
	assert.Contains(t, held[5], "tools list --server=poisoned", "the action must point at the hold evidence")

	assert.Equal(t, "✅", clean[0], "a server with no holds is unchanged")
	assert.Equal(t, "Connected (4 tools)", clean[4])
}

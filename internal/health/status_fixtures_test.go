package health

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusFixturesMatchConstants pins testdata/status_fixtures.json — the
// golden vocabulary the macOS tray's HealthVocabularyTests also reads — to the
// Go source of truth in constants.go. Together the two tests make a label or
// value change in constants.go fail on the Swift side too, instead of leaving
// macOS silently rendering the old wording (Spec 109 FR-090).
func TestStatusFixturesMatchConstants(t *testing.T) {
	raw, err := os.ReadFile("testdata/status_fixtures.json")
	require.NoError(t, err)

	var fixture struct {
		StatusOrder    []string          `json:"status_order"`
		StatusLabels   map[string]string `json:"status_labels"`
		ActionPriority []string          `json:"action_priority"`
		ActionLabels   map[string]string `json:"action_labels"`
	}
	require.NoError(t, json.Unmarshal(raw, &fixture))

	assert.Equal(t, StatusOrder, fixture.StatusOrder, "status_order drifted from constants.go")
	assert.Equal(t, StatusLabels, fixture.StatusLabels, "status_labels drifted from constants.go")
	assert.Equal(t, ActionPriority, fixture.ActionPriority, "action_priority drifted from constants.go")
	assert.Equal(t, ActionLabels, fixture.ActionLabels, "action_labels drifted from constants.go")
}

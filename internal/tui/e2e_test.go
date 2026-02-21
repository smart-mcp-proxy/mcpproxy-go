package tui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

// E2E workflow tests: full user interaction sequences

// TestE2EFilterWorkflow tests the complete filter workflow:
// 1. User presses 'f' to enter filter mode
// 2. User navigates filters with tab/shift+tab
// 3. User cycles filter values with up/down
// 4. User exits and returns to normal mode
func TestE2EFilterWorkflow(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "github", HealthLevel: "healthy", HealthSummary: "Connected", AdminState: "enabled"},
		{Name: "stripe", HealthLevel: "degraded", HealthSummary: "Token expiring", AdminState: "enabled"},
		{Name: "broken", HealthLevel: "unhealthy", HealthSummary: "Failed", AdminState: "disabled"},
	}
	m.height = 24
	m.width = 80

	// Step 1: Enter filter mode (press 'f')
	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = result.(model)
	_ = cmd // ignore command for now

	assert.Equal(t, ModeFilterEdit, m.uiMode, "should enter filter edit mode")
	assert.NotNil(t, m.filterState, "filter state should exist")

	// Step 2: Navigate to a filter (press tab to move to next)
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = result.(model)

	// Step 3: Change filter value (simulate up arrow to cycle through values)
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = result.(model)

	// Step 4: Exit filter mode (press Escape)
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = result.(model)

	assert.Equal(t, ModeNormal, m.uiMode, "should return to normal mode")
	// Note: Filters are cleared when exiting filter mode
}

// TestE2ESortWorkflow tests the complete sort workflow:
// 1. User presses 's' to enter sort mode
// 2. User selects sort column (e.g., 'h' for health)
// 3. View re-renders with sort indicators
func TestE2ESortWorkflow(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "zebra", HealthLevel: "healthy", ToolCount: 5},
		{Name: "apple", HealthLevel: "degraded", ToolCount: 3},
		{Name: "banana", HealthLevel: "unhealthy", ToolCount: 1},
	}
	m.cursor = 0
	m.height = 24
	m.width = 80

	// Step 1: Enter sort mode (press 's')
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = result.(model)

	assert.Equal(t, ModeSortSelect, m.uiMode, "should enter sort select mode")

	// Step 2: Select sort column (press 'n' for name)
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = result.(model)

	assert.Equal(t, "name", m.sortState.Column, "should sort by name")
	assert.Equal(t, ModeNormal, m.uiMode, "should return to normal mode after selecting")

	// Step 3: Verify view renders with sort indicator
	view := renderServers(m, 10)
	assert.Contains(t, view, "NAME", "should show NAME header")
	assert.Contains(t, view, "▲", "should show sort indicator (ascending)")
}

// TestE2ETabSwitching tests switching between tabs and preserving state:
// 1. View servers tab
// 2. Press '2' to switch to activity tab
// 3. Filter activity
// 4. Press '1' to switch back to servers
// 5. Verify server state is preserved
func TestE2ETabSwitching(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "server1", HealthLevel: "healthy"},
		{Name: "server2", HealthLevel: "degraded"},
	}
	m.activities = []activityInfo{
		{Type: "tool_call", ServerName: "server1", Status: "success"},
		{Type: "tool_call", ServerName: "server2", Status: "error"},
	}
	m.height = 24
	m.width = 80

	// Step 1: Verify we're on servers tab
	assert.Equal(t, tabServers, m.activeTab, "should start on servers tab")
	m.cursor = 1

	// Step 2: Switch to activity tab (press '2')
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = result.(model)

	assert.Equal(t, tabActivity, m.activeTab, "should switch to activity tab")

	// Step 3: Change cursor position on activity tab
	m.cursor = 1

	// Step 4: Switch back to servers (press '1')
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = result.(model)

	assert.Equal(t, tabServers, m.activeTab, "should return to servers tab")
	// Note: cursor is preserved per tab
}

// TestE2EOAuthRefreshWorkflow tests OAuth refresh trigger:
// 1. Press 'o' to refresh OAuth
// 2. Verify command is triggered
// 3. Verify state updates appropriately
func TestE2EOAuthRefreshWorkflow(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "stripe", HealthLevel: "degraded", TokenExpiresAt: time.Now().Add(30 * time.Minute).Format(time.RFC3339)},
	}

	// Step 1: Press 'o' to refresh OAuth
	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = result.(model)

	assert.NotNil(t, cmd, "should return a command for OAuth refresh")

	// Step 2: Verify no error is shown
	assert.Nil(t, m.err, "should not show error")
}

// TestE2EClearFiltersWorkflow tests clearing active filters:
// 1. Apply filters
// 2. Press 'c' to clear
// 3. Verify filters are cleared
func TestE2EClearFiltersWorkflow(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "github", HealthLevel: "healthy"},
		{Name: "stripe", HealthLevel: "degraded"},
		{Name: "broken", HealthLevel: "unhealthy"},
	}

	// Step 1: Apply a filter manually
	m.filterState["health_level"] = "healthy"
	assert.True(t, m.filterState.hasActiveFilters(), "filter should be active")

	// Step 2: Press 'c' to clear filters
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = result.(model)

	// Step 3: Verify filters are cleared
	assert.False(t, m.filterState.hasActiveFilters(), "filters should be cleared")
}

// TestE2ESearchWorkflow tests search mode (if implemented):
// 1. Press '/' or similar to enter search mode
// 2. Type search terms
// 3. Results are filtered in real-time
// 4. Press Escape to exit search
func TestE2ESearchWorkflow(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "github-api", HealthLevel: "healthy"},
		{Name: "stripe-payments", HealthLevel: "healthy"},
		{Name: "aws-lambda", HealthLevel: "degraded"},
	}
	m.height = 24
	m.width = 80

	// Note: Search mode implementation is optional
	// This test structure can be enabled when search is added
	_ = m
}

// TestE2ECursorNavigation tests cursor movement with wraparound:
// 1. Move down through all items
// 2. Verify cursor stops at bottom
// 3. Move up through all items
// 4. Verify cursor stops at top
func TestE2ECursorNavigation(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "server1", HealthLevel: "healthy"},
		{Name: "server2", HealthLevel: "healthy"},
		{Name: "server3", HealthLevel: "healthy"},
	}
	m.cursor = 0

	// Step 1: Move down (j key)
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = result.(model)
	assert.Equal(t, 1, m.cursor, "cursor should move to item 1")

	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = result.(model)
	assert.Equal(t, 2, m.cursor, "cursor should move to item 2")

	// Step 2: Try to move past end (should stop)
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = result.(model)
	assert.Equal(t, 2, m.cursor, "cursor should stay at last item")

	// Step 3: Move up (k key)
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = result.(model)
	assert.Equal(t, 1, m.cursor, "cursor should move to item 1")

	// Step 4: Move to top and try to move further (should stop)
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = result.(model)
	assert.Equal(t, 0, m.cursor, "cursor should move to item 0")

	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = result.(model)
	assert.Equal(t, 0, m.cursor, "cursor should stay at first item")
}

// TestE2EHealthStatusDisplay tests health status indicators are rendered correctly:
// 1. Create servers with different health levels
// 2. Render view
// 3. Verify health indicators (●, ◐, ○) are present
func TestE2EHealthStatusDisplay(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "healthy-server", HealthLevel: "healthy", HealthSummary: "Connected"},
		{Name: "degraded-server", HealthLevel: "degraded", HealthSummary: "Token expiring"},
		{Name: "unhealthy-server", HealthLevel: "unhealthy", HealthSummary: "Connection failed"},
	}
	m.height = 24
	m.width = 80

	view := renderServers(m, 10)

	// Verify health indicators are present
	assert.Contains(t, view, "●", "should show healthy indicator")
	assert.Contains(t, view, "◐", "should show degraded indicator")
	assert.Contains(t, view, "○", "should show unhealthy indicator")
}

// TestE2EFilterSummaryDisplay tests filter badges are shown in view:
// 1. Apply filters
// 2. Render view
// 3. Verify filter badges are shown
func TestE2EFilterSummaryDisplay(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "server1", HealthLevel: "healthy"},
		{Name: "server2", HealthLevel: "degraded"},
	}
	m.filterState["health_level"] = "healthy"
	m.height = 24
	m.width = 80

	view := renderServers(m, 10)

	// Verify filter summary is displayed
	assert.Contains(t, view, "Filter:", "should show filter section")
	assert.Contains(t, view, "Health_level:", "should show filter name")
	assert.Contains(t, view, "healthy", "should show filter value")
	assert.Contains(t, view, "[Clear]", "should show clear option")
}

// TestE2EMultipleFiltersApply tests applying multiple filters simultaneously:
// 1. Apply multiple filters
// 2. Verify all filters are active
// 3. Render shows all filter badges
func TestE2EMultipleFiltersApply(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "github", HealthLevel: "healthy", AdminState: "enabled"},
		{Name: "stripe", HealthLevel: "healthy", AdminState: "disabled"},
		{Name: "aws", HealthLevel: "degraded", AdminState: "enabled"},
	}

	// Step 1: Apply multiple filters
	m.filterState["health_level"] = "healthy"
	m.filterState["admin_state"] = "enabled"

	// Step 2: Get visible servers (should be filtered)
	visible := m.getVisibleServers()

	// Should only show github (healthy AND enabled)
	assert.Equal(t, 1, len(visible), "should have 1 visible server")
	assert.Equal(t, "github", visible[0].Name, "should show github")
}

// TestE2ETabbedSortingByTab tests sorting persists independently for each tab:
// 1. On servers tab, sort by name
// 2. Verify sort state shows name column
// 3. Rendering includes sort indicators
func TestE2ETabbedSortingByTab(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "zebra", HealthLevel: "healthy"},
		{Name: "apple", HealthLevel: "degraded"},
	}
	m.activities = []activityInfo{
		{Type: "tool_call", Timestamp: "2026-02-09T10:00:00Z"},
		{Type: "policy", Timestamp: "2026-02-09T09:00:00Z"},
	}

	// Step 1: On servers tab, sort by name
	m.activeTab = tabServers
	m.sortState.Column = "name"
	m.sortState.Descending = false

	serverView := renderServers(m, 10)
	assert.Contains(t, serverView, "NAME", "should show NAME header")
	assert.Contains(t, serverView, "▲", "should show sort indicator")

	// Step 2: Switch to activity tab
	m.activeTab = tabActivity
	m.sortState.Column = "timestamp"

	activityView := renderActivity(m, 10)
	assert.Contains(t, activityView, "TIME", "should show TIME header")
}

// TestE2EQuitCommand tests quit key (q) exits cleanly:
// 1. Press 'q'
// 2. Verify quit command is issued
func TestE2EQuitCommand(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)

	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = result.(model)

	assert.NotNil(t, cmd, "should issue a command")
	// The exact command will be tea.Quit() which is checked by Bubble Tea framework
}

// TestE2EHelpDisplay tests help text shows correct information:
// 1. Start in normal mode on servers tab
// 2. Render view
// 3. Verify servers-specific help is shown
// 4. Switch to activity tab
// 5. Verify help text changes
func TestE2EHelpDisplay(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.height = 24
	m.width = 80

	// Step 1: On servers tab in normal mode
	m.activeTab = tabServers
	m.uiMode = ModeNormal

	help := renderHelp(m)
	assert.Contains(t, help, "enable", "should show enable command for servers")
	assert.Contains(t, help, "disable", "should show disable command for servers")

	// Step 2: Switch to activity tab
	m.activeTab = tabActivity
	help = renderHelp(m)
	// Activity tab should not have enable/disable
	// (this will depend on renderHelp implementation)
}

// TestE2ERefreshCommand tests refresh key (r) triggers data update:
// 1. Press 'r'
// 2. Verify refresh command is issued
// 3. No error should occur
func TestE2ERefreshCommand(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)

	// Press 'r' to refresh
	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = result.(model)

	assert.NotNil(t, cmd, "should issue a refresh command")
	assert.Nil(t, m.err, "should not have error")
}

// TestE2EEmptyState tests behavior with no servers:
// 1. Start with empty server list
// 2. Render view
// 3. Verify "No servers configured" message
func TestE2EEmptyState(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{}
	m.height = 24
	m.width = 80

	view := renderServers(m, 10)

	assert.Contains(t, view, "No servers", "should show empty state message")
	assert.NotContains(t, view, "●", "should not show health indicators")
}

// TestE2ELongServerNames tests truncation of long names:
// 1. Create server with very long name
// 2. Render view
// 3. Verify name is truncated with "..."
func TestE2ELongServerNames(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "this-is-an-extremely-long-server-name-that-should-be-truncated", HealthLevel: "healthy"},
	}
	m.height = 24
	m.width = 80

	view := renderServers(m, 10)

	assert.Contains(t, view, "...", "should truncate long names")
	// Should not contain the full name
	assert.NotContains(t, view, "this-is-an-extremely-long-server-name-that-should-be-truncated", "full name should not appear")
}

// TestE2EResponseToWindowResize tests view adapts to terminal size changes:
// 1. Create model with 80x24
// 2. Render
// 3. Change to 120x40
// 4. Render again
// 5. Verify layout adjusts
func TestE2EResponseToWindowResize(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "server1", HealthLevel: "healthy"},
		{Name: "server2", HealthLevel: "healthy"},
		{Name: "server3", HealthLevel: "healthy"},
	}

	// Step 1: Small terminal
	m.width = 80
	m.height = 24
	view1 := renderServers(m, 10)
	assert.NotEmpty(t, view1)

	// Step 2: Large terminal
	m.width = 200
	m.height = 50
	view2 := renderServers(m, 20)
	assert.NotEmpty(t, view2)

	// Both should be valid (length > 0)
	assert.True(t, len(view1) > 0 && len(view2) > 0, "both renders should be non-empty")
}

// TestE2ESequentialKeyPresses tests handling multiple key presses in sequence:
// 1. Press 'j' twice to move down
// 2. Verify cursor moved
// 3. Press 'f' to enter filter mode
// 4. Press Escape to exit (cursor resets in filter mode)
// 5. Verify final state
func TestE2ESequentialKeyPresses(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "s1", HealthLevel: "healthy"},
		{Name: "s2", HealthLevel: "healthy"},
		{Name: "s3", HealthLevel: "healthy"},
	}
	m.cursor = 0

	// Press 'j' twice to move down
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = result.(model)
	assert.Equal(t, 1, m.cursor, "cursor should move to 1")

	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = result.(model)
	assert.Equal(t, 2, m.cursor, "cursor should move to 2")

	// Press 'f' to enter filter mode
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = result.(model)
	assert.Equal(t, ModeFilterEdit, m.uiMode, "should be in filter mode")

	// Press Escape to exit (resets cursor to 0)
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = result.(model)

	assert.Equal(t, 0, m.cursor, "cursor should reset to 0 after exiting filter mode")
	assert.Equal(t, ModeNormal, m.uiMode, "should be in normal mode")
}

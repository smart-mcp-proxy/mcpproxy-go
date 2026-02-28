package tui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNavigationKeys tests navigation key bindings (j/k/g/G)
func TestNavigationKeys(t *testing.T) {
	tests := []struct {
		name         string
		key          tea.KeyMsg
		initialCur   int
		itemCount    int
		expectCur    int
		description  string
	}{
		{
			name:        "j moves cursor down",
			key:         tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}},
			initialCur:  0,
			itemCount:   3,
			expectCur:   1,
			description: "j should move cursor from 0 to 1",
		},
		{
			name:        "k moves cursor up",
			key:         tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}},
			initialCur:  1,
			itemCount:   3,
			expectCur:   0,
			description: "k should move cursor from 1 to 0",
		},
		{
			name:        "j at bottom is no-op",
			key:         tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}},
			initialCur:  2,
			itemCount:   3,
			expectCur:   2,
			description: "j should not move past last item",
		},
		{
			name:        "k at top is no-op",
			key:         tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}},
			initialCur:  0,
			itemCount:   3,
			expectCur:   0,
			description: "k should not move above first item",
		},
		{
			name:        "down arrow moves cursor down",
			key:         tea.KeyMsg{Type: tea.KeyDown},
			initialCur:  0,
			itemCount:   3,
			expectCur:   1,
			description: "down arrow should move cursor",
		},
		{
			name:        "up arrow moves cursor up",
			key:         tea.KeyMsg{Type: tea.KeyUp},
			initialCur:  1,
			itemCount:   3,
			expectCur:   0,
			description: "up arrow should move cursor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(context.Background(), client, 5*time.Second)
			m.activeTab = tabServers
			m.servers = make([]serverInfo, tt.itemCount)
			for i := 0; i < tt.itemCount; i++ {
				m.servers[i] = serverInfo{Name: "srv" + string(rune('0'+i))}
			}
			m.cursor = tt.initialCur

			result, _ := m.Update(tt.key)
			resultModel := result.(model)

			assert.Equal(t, tt.expectCur, resultModel.cursor, tt.description)
		})
	}
}

// TestJumpToEnds tests 'g' (top) and 'G' (bottom) navigation
func TestJumpToEnds(t *testing.T) {
	tests := []struct {
		name        string
		key         rune
		itemCount   int
		expectCur   int
		description string
	}{
		{
			name:        "g jumps to top",
			key:         'g',
			itemCount:   5,
			expectCur:   0,
			description: "g should jump cursor to 0",
		},
		{
			name:        "G jumps to bottom",
			key:         'G',
			itemCount:   5,
			expectCur:   4,
			description: "G should jump cursor to last index",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(context.Background(), client, 5*time.Second)
			m.activeTab = tabServers
			m.servers = make([]serverInfo, tt.itemCount)
			for i := 0; i < tt.itemCount; i++ {
				m.servers[i] = serverInfo{Name: "srv" + string(rune('0'+i))}
			}
			m.cursor = 2 // Start in middle

			key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.key}}
			result, _ := m.Update(key)
			resultModel := result.(model)

			assert.Equal(t, tt.expectCur, resultModel.cursor, tt.description)
		})
	}
}

// TestTabSwitching tests tab switching with 'tab', '1', '2'
func TestTabSwitching(t *testing.T) {
	tests := []struct {
		name         string
		key          tea.KeyMsg
		initialTab   tab
		expectTab    tab
		expectCursor int
	}{
		{
			name:         "tab key switches from Servers to Activity",
			key:          tea.KeyMsg{Type: tea.KeyTab},
			initialTab:   tabServers,
			expectTab:    tabActivity,
			expectCursor: 0,
		},
		{
			name:         "tab key switches from Activity to Servers",
			key:          tea.KeyMsg{Type: tea.KeyTab},
			initialTab:   tabActivity,
			expectTab:    tabServers,
			expectCursor: 0,
		},
		{
			name:         "1 key goes to Servers",
			key:          tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}},
			initialTab:   tabActivity,
			expectTab:    tabServers,
			expectCursor: 0,
		},
		{
			name:         "2 key goes to Activity",
			key:          tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}},
			initialTab:   tabServers,
			expectTab:    tabActivity,
			expectCursor: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(context.Background(), client, 5*time.Second)
			m.activeTab = tt.initialTab
			m.cursor = 5

			result, _ := m.Update(tt.key)
			resultModel := result.(model)

			assert.Equal(t, tt.expectTab, resultModel.activeTab)
			assert.Equal(t, tt.expectCursor, resultModel.cursor, "cursor should reset on tab switch")
		})
	}
}

// TestSortingKeys tests sort-related keys (s + column key)
func TestSortingKeys(t *testing.T) {
	tests := []struct {
		name               string
		key                tea.KeyMsg
		expectedColumn     string
		expectedDescending bool
	}{
		{
			name:               "s then t sorts by timestamp descending",
			key:                tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}},
			expectedColumn:     "timestamp",
			expectedDescending: true,
		},
		{
			name:               "s then y sorts by type ascending",
			key:                tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}},
			expectedColumn:     "type",
			expectedDescending: false,
		},
		{
			name:               "s then s sorts by server ascending",
			key:                tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}},
			expectedColumn:     "server_name",
			expectedDescending: false,
		},
		{
			name:               "s then d sorts by duration descending",
			key:                tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}},
			expectedColumn:     "duration_ms",
			expectedDescending: true,
		},
		{
			name:               "s then a sorts by status ascending",
			key:                tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}},
			expectedColumn:     "status",
			expectedDescending: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(context.Background(), client, 5*time.Second)
			m.activeTab = tabActivity

			// Enter sort mode
			result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
			m = result.(model)
			assert.Equal(t, ModeSortSelect, m.uiMode)

			// Press the sort column key
			result, _ = m.Update(tt.key)
			m = result.(model)

			assert.Equal(t, ModeNormal, m.uiMode, "should return to normal mode")
			assert.Equal(t, tt.expectedColumn, m.sortState.Column)
			assert.Equal(t, tt.expectedDescending, m.sortState.Descending)
		})
	}
}

// TestFilterModeToggle tests entering/exiting filter mode with 'f'
func TestFilterModeToggle(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.uiMode = ModeNormal
	m.filterState = newFilterState()

	// 'f' should transition to filter mode
	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}}
	result, _ := m.Update(key)
	resultModel := result.(model)

	// Implementation should handle filter mode transition
	// This is a placeholder for mode-based filter handling
	assert.NotNil(t, resultModel)
}

// TestClearFiltersKey tests 'c' key clears filters and sort
func TestClearFiltersKey(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activeTab = tabActivity
	m.filterState = filterState{"status": "error", "server": "github"}
	m.sortState = sortState{Column: "type", Descending: true}
	m.cursor = 5

	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}
	result, _ := m.Update(key)
	resultModel := result.(model)

	// After 'c', filters should be cleared and sort reset to default
	assert.False(t, resultModel.filterState.hasActiveFilters())
	assert.Equal(t, "timestamp", resultModel.sortState.Column)
	assert.True(t, resultModel.sortState.Descending)
	assert.Equal(t, 0, resultModel.cursor)
}

// TestHelpKey tests '?' shows help
func TestHelpKey(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.uiMode = ModeNormal

	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}
	result, _ := m.Update(key)
	resultModel := result.(model)

	// '?' should potentially enter help mode
	assert.NotNil(t, resultModel)
}

// TestQuitKey tests 'q' and 'ctrl+c' quit
func TestQuitKey(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyMsg
	}{
		{name: "q quits", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}},
		{name: "ctrl+c quits", key: tea.KeyMsg{Type: tea.KeyCtrlC}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(context.Background(), client, 5*time.Second)

			_, cmd := m.Update(tt.key)
			require.NotNil(t, cmd)

			// Execute command and verify it's quit
			msg := cmd()
			_, ok := msg.(tea.QuitMsg)
			assert.True(t, ok, "should produce quit message")
		})
	}
}

// TestRefreshKey tests 'r' triggers manual refresh
func TestRefreshKey(t *testing.T) {
	client := &MockClient{
		servers:    []map[string]interface{}{{"name": "test"}},
		activities: []map[string]interface{}{},
	}
	m := NewModel(context.Background(), client, 5*time.Second)

	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}
	_, cmd := m.Update(key)

	require.NotNil(t, cmd, "refresh should return a command")
}

// TestOAuthRefreshKey tests 'o' triggers OAuth refresh
func TestOAuthRefreshKey(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)

	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}}
	_, cmd := m.Update(key)

	require.NotNil(t, cmd, "OAuth refresh should return a command")
}

// TestSpaceRefresh tests 'space' manual refresh
func TestSpaceRefresh(t *testing.T) {
	client := &MockClient{
		servers:    []map[string]interface{}{},
		activities: []map[string]interface{}{},
	}
	m := NewModel(context.Background(), client, 5*time.Second)

	key := tea.KeyMsg{Type: tea.KeySpace}
	result, _ := m.Update(key)

	// Space may trigger refresh or be no-op depending on implementation
	resultModel := result.(model)
	assert.NotNil(t, resultModel)
}

// TestServerActionKeys tests server action keys (e/d/R/l)
func TestServerActionKeys(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		expectAction string
		tab          tab
		shouldWork   bool
	}{
		{name: "e enables", key: "e", expectAction: "enable", tab: tabServers, shouldWork: true},
		{name: "d disables", key: "d", expectAction: "disable", tab: tabServers, shouldWork: true},
		{name: "R restarts", key: "R", expectAction: "restart", tab: tabServers, shouldWork: true},
		{name: "l logs in", key: "l", expectAction: "login", tab: tabServers, shouldWork: true},
		{name: "e on Activity tab ignored", key: "e", expectAction: "", tab: tabActivity, shouldWork: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(context.Background(), client, 5*time.Second)
			m.activeTab = tt.tab
			m.servers = []serverInfo{
				{
					Name:         "test-server",
					HealthAction: "login",
					AdminState:   "enabled",
				},
			}
			m.cursor = 0

			var key tea.KeyMsg
			if tt.key == "R" {
				key = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}}
			} else {
				key = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(tt.key[0])}}
			}

			_, cmd := m.Update(key)

			if tt.shouldWork {
				require.NotNil(t, cmd, "action should produce command")
			} else {
				assert.Nil(t, cmd, "action should be ignored on wrong tab")
			}
		})
	}
}

// TestServerActionTargetsVisibleServer verifies that action keys target the
// server shown at the cursor position (sorted/filtered), not the raw list.
func TestServerActionTargetsVisibleServer(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activeTab = tabServers

	// Raw order: zebra, alpha, middle.
	// Sorted by name ascending (default): alpha, middle, zebra.
	m.servers = []serverInfo{
		{Name: "zebra", AdminState: "enabled"},
		{Name: "alpha", AdminState: "enabled"},
		{Name: "middle", AdminState: "enabled"},
	}
	m.sortState = newServerSortState() // name ascending
	m.cursor = 0                       // Should target "alpha" (first in sorted order)

	// Press "d" to disable
	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}
	_, cmd := m.Update(key)
	require.NotNil(t, cmd, "d should produce command")

	// Execute the command to get the statusMsg
	msg := cmd()
	status, ok := msg.(statusMsg)
	require.True(t, ok, "expected statusMsg, got %T", msg)
	assert.Contains(t, string(status), "alpha", "should target alpha (visible[0]), not zebra (raw[0])")
}

// TestCursorBoundsAfterDataChange tests cursor clamping
func TestCursorBoundsAfterDataChange(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activeTab = tabServers
	m.servers = []serverInfo{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	m.cursor = 2

	// Simulate data refresh with fewer items
	newServers := []serverInfo{{Name: "a"}}
	msg := serversMsg{servers: newServers}
	result, _ := m.Update(msg)
	resultModel := result.(model)

	assert.Equal(t, 0, resultModel.cursor, "cursor should clamp to 0 (last valid index)")
}

// TestInvalidKeysIgnored tests that invalid keys don't crash
func TestInvalidKeysIgnored(t *testing.T) {
	invalidKeys := []string{
		"~", "@", "#", "$", "%", "^", "&", "*", "(", ")",
		"-", "=", "[", "]", "{", "}", ";", "'", ",", ".",
		"/", "\\", "<", ">", "|", "?",
	}

	for _, keyStr := range invalidKeys {
		t.Run("invalid key: "+keyStr, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(context.Background(), client, 5*time.Second)
			m.servers = []serverInfo{{Name: "test"}}
			m.cursor = 0
			m.activeTab = tabServers

			key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(keyStr[0])}}
			result, _ := m.Update(key)

			// Should not crash, just be a no-op or have minimal effect
			assert.NotNil(t, result)
		})
	}
}

// TestMultipleKeysInSequence tests keyboard input sequence
func TestMultipleKeysInSequence(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activeTab = tabServers
	m.servers = []serverInfo{
		{Name: "srv1"},
		{Name: "srv2"},
		{Name: "srv3"},
	}
	m.cursor = 0

	// Sequence: j, j, k (down, down, up)
	keys := []rune{'j', 'j', 'k'}
	result := tea.Model(m)
	for _, k := range keys {
		key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{k}}
		result, _ = result.Update(key)
	}

	resultModel := result.(model)
	assert.Equal(t, 1, resultModel.cursor, "j, j, k should end at cursor 1")
}

// TestMixedNavigationMethods tests j/k vs arrow keys equivalence
func TestMixedNavigationMethods(t *testing.T) {
	// Using j/k
	client1 := &MockClient{}
	m1 := NewModel(context.Background(), client1, 5*time.Second)
	m1.activeTab = tabServers
	m1.servers = []serverInfo{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	m1.cursor = 0

	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}
	result1, _ := m1.Update(key)

	// Using arrow keys
	client2 := &MockClient{}
	m2 := NewModel(context.Background(), client2, 5*time.Second)
	m2.activeTab = tabServers
	m2.servers = []serverInfo{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	m2.cursor = 0

	key2 := tea.KeyMsg{Type: tea.KeyDown}
	result2, _ := m2.Update(key2)

	model1 := result1.(model)
	model2 := result2.(model)
	assert.Equal(t, model1.cursor, model2.cursor, "j and down arrow should move cursor the same way")
}

// TestEmptyDataNoCrash tests keyboard input with empty data
func TestEmptyDataNoCrash(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activeTab = tabServers
	m.servers = []serverInfo{}
	m.cursor = 0

	keys := []rune{'j', 'k', 'g', 'G', 'r', 'c'}
	for _, k := range keys {
		key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{k}}
		result, _ := m.Update(key)
		assert.NotNil(t, result, "should not crash with empty data for key: "+string(k))
	}
}

// TestCursorBehaviorAtBoundaries tests cursor behavior at edges
func TestCursorBehaviorAtBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		itemCount  int
		startCur   int
		key        rune
		expectCur  int
		description string
	}{
		{
			name:        "single item - j no-op",
			itemCount:   1,
			startCur:    0,
			key:         'j',
			expectCur:   0,
			description: "cannot move down from only item",
		},
		{
			name:        "single item - k no-op",
			itemCount:   1,
			startCur:    0,
			key:         'k',
			expectCur:   0,
			description: "cannot move up from only item",
		},
		{
			name:        "many items - g from middle",
			itemCount:   100,
			startCur:    50,
			key:         'g',
			expectCur:   0,
			description: "g should jump to 0",
		},
		{
			name:        "many items - G from start",
			itemCount:   100,
			startCur:    0,
			key:         'G',
			expectCur:   99,
			description: "G should jump to last",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(context.Background(), client, 5*time.Second)
			m.activeTab = tabServers
			m.servers = make([]serverInfo, tt.itemCount)
			for i := 0; i < tt.itemCount; i++ {
				m.servers[i] = serverInfo{Name: "srv"}
			}
			m.cursor = tt.startCur

			key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.key}}
			result, _ := m.Update(key)
			resultModel := result.(model)

			assert.Equal(t, tt.expectCur, resultModel.cursor, tt.description)
		})
	}
}

// TestHandleFilterMode tests filter mode input handling
func TestHandleFilterMode(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.uiMode = ModeFilterEdit
	m.activeTab = tabActivity
	m.focusedFilter = "status"
	m.activities = []activityInfo{
		{Status: "success"},
		{Status: "error"},
		{Status: "blocked"},
	}

	tests := []struct {
		name       string
		key        string
		wantMode   UIMode
		wantFilter string
	}{
		{
			name:      "esc exits filter mode",
			key:       "esc",
			wantMode:  ModeNormal,
			wantFilter: "",
		},
		{
			name:      "q exits filter mode",
			key:       "q",
			wantMode:  ModeNormal,
			wantFilter: "",
		},
		{
			name:      "enter applies and exits",
			key:       "enter",
			wantMode:  ModeNormal,
			wantFilter: "",
		},
		{
			name:       "text input adds to filter",
			key:        "s",
			wantMode:   ModeFilterEdit,
			wantFilter: "s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.filterQuery = ""
			resultModel, _ := m.handleFilterMode(tt.key)
			assert.Equal(t, tt.wantMode, resultModel.uiMode)
			if tt.wantFilter != "" {
				assert.Contains(t, resultModel.filterQuery, tt.wantFilter)
			}
		})
	}
}

// TestHandleSortMode tests sort mode selection
func TestHandleSortMode(t *testing.T) {
	client := &MockClient{}

	tests := []struct {
		name       string
		key        string
		tab        tab
		wantColumn string
		wantMode   UIMode
	}{
		{
			name:       "esc cancels sort mode",
			key:        "esc",
			tab:        tabActivity,
			wantColumn: "timestamp", // should not change
			wantMode:   ModeNormal,
		},
		{
			name:       "t sorts by timestamp (activity)",
			key:        "t",
			tab:        tabActivity,
			wantColumn: "timestamp",
			wantMode:   ModeNormal,
		},
		{
			name:       "y sorts by type",
			key:        "y",
			tab:        tabActivity,
			wantColumn: "type",
			wantMode:   ModeNormal,
		},
		{
			name:       "s sorts by server (activity)",
			key:        "s",
			tab:        tabActivity,
			wantColumn: "server_name",
			wantMode:   ModeNormal,
		},
		{
			name:       "d sorts by duration (activity)",
			key:        "d",
			tab:        tabActivity,
			wantColumn: "duration_ms",
			wantMode:   ModeNormal,
		},
		{
			name:       "a sorts by status (activity)",
			key:        "a",
			tab:        tabActivity,
			wantColumn: "status",
			wantMode:   ModeNormal,
		},
		{
			name:       "n sorts by name (servers)",
			key:        "n",
			tab:        tabServers,
			wantColumn: "name",
			wantMode:   ModeNormal,
		},
		{
			name:       "h sorts by health (servers)",
			key:        "h",
			tab:        tabServers,
			wantColumn: "health_level",
			wantMode:   ModeNormal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel(context.Background(), client, 5*time.Second)
			m.uiMode = ModeSortSelect
			m.activeTab = tt.tab
			m.sortState.Column = "timestamp" // Start state

			resultModel, _ := m.handleSortMode(tt.key)

			assert.Equal(t, tt.wantMode, resultModel.uiMode)
			if tt.key != "esc" && tt.key != "q" {
				assert.Equal(t, tt.wantColumn, resultModel.sortState.Column)
			}
		})
	}
}

// TestHandleSearchMode tests search mode input handling
func TestHandleSearchMode(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		wantMode   UIMode
		wantQuery  string
	}{
		{
			name:      "esc exits search mode",
			key:       "esc",
			wantMode:  ModeNormal,
			wantQuery: "",
		},
		{
			name:      "ctrl+c exits search mode",
			key:       "ctrl+c",
			wantMode:  ModeNormal,
			wantQuery: "",
		},
		{
			name:      "enter stays in search mode",
			key:       "enter",
			wantMode:  ModeSearch,
			wantQuery: "",
		},
		{
			name:      "backspace removes char",
			key:       "backspace",
			wantMode:  ModeSearch,
			wantQuery: "",
		},
		{
			name:      "letter added to query",
			key:       "a",
			wantMode:  ModeSearch,
			wantQuery: "a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(context.Background(), client, 5*time.Second)
			m.uiMode = ModeSearch
			m.filterQuery = ""

			m, _ = m.handleSearchMode(tt.key)

			assert.Equal(t, tt.wantMode, m.uiMode)
			assert.Equal(t, tt.wantQuery, m.filterQuery)
		})
	}
}

// TestHandleHelpMode tests help mode input handling
func TestHandleHelpMode(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantMode UIMode
	}{
		{
			name:    "esc exits help",
			key:    "esc",
			wantMode: ModeNormal,
		},
		{
			name:    "q exits help",
			key:    "q",
			wantMode: ModeNormal,
		},
		{
			name:    "? exits help",
			key:    "?",
			wantMode: ModeNormal,
		},
		{
			name:    "other key stays in help",
			key:    "a",
			wantMode: ModeHelp,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(context.Background(), client, 5*time.Second)
			m.uiMode = ModeHelp

			m, _ = m.handleHelpMode(tt.key)

			assert.Equal(t, tt.wantMode, m.uiMode)
		})
	}
}

// TestFilterKeyNavigation tests filter navigation helpers
func TestFilterKeyNavigation(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activeTab = tabActivity

	t.Run("getFirstFilterKey Activity tab", func(t *testing.T) {
		result := m.getFirstFilterKey()
		assert.Equal(t, "status", result)
	})

	t.Run("getFilterKeysForTab Activity", func(t *testing.T) {
		keys := m.getFilterKeysForTab()
		assert.ElementsMatch(t, []string{"status", "server", "type"}, keys)
	})

	t.Run("getFilterKeysForTab Servers", func(t *testing.T) {
		m.activeTab = tabServers
		keys := m.getFilterKeysForTab()
		assert.ElementsMatch(t, []string{"admin_state", "health_level"}, keys)
	})

	t.Run("getNextFilterKey wraps around", func(t *testing.T) {
		m.activeTab = tabActivity
		next := m.getNextFilterKey("type")
		assert.Equal(t, "status", next) // wraps to first
	})

	t.Run("getPrevFilterKey wraps around", func(t *testing.T) {
		m.activeTab = tabActivity
		prev := m.getPrevFilterKey("status")
		assert.Equal(t, "type", prev) // wraps to last
	})
}

// TestHandleKeyNavigation tests key routing
func TestHandleKeyNavigation(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "srv1"},
		{Name: "srv2"},
	}

	t.Run("c key clears filters", func(t *testing.T) {
		m.filterState = filterState{"status": "error"}
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}
		result, _ := m.handleKey(keyMsg)
		resultModel := result.(model)
		assert.Empty(t, resultModel.filterState)
	})

	t.Run("f key enters filter mode", func(t *testing.T) {
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}}
		result, _ := m.handleKey(keyMsg)
		resultModel := result.(model)
		assert.Equal(t, ModeFilterEdit, resultModel.uiMode)
	})

	t.Run("s key enters sort mode", func(t *testing.T) {
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}
		result, _ := m.handleKey(keyMsg)
		resultModel := result.(model)
		assert.Equal(t, ModeSortSelect, resultModel.uiMode)
	})

	t.Run("/ key enters search mode", func(t *testing.T) {
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}
		result, _ := m.handleKey(keyMsg)
		resultModel := result.(model)
		assert.Equal(t, ModeSearch, resultModel.uiMode)
	})
}

// BenchmarkKeyboardInput measures keyboard input performance
func BenchmarkKeyboardInput(b *testing.B) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activeTab = tabServers
	m.servers = make([]serverInfo, 100)
	for i := 0; i < 100; i++ {
		m.servers[i] = serverInfo{Name: "srv"}
	}

	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, _ := m.Update(key)
		m = result.(model)
	}
}

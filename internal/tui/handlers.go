package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// handleNormalMode handles all input when in normal navigation mode
func (m model) handleNormalMode(key string) (model, tea.Cmd) {
	switch key {
	// Quit
	case "q":
		return m, tea.Quit

	// OAuth refresh
	case "o", "O":
		return m, m.triggerOAuthRefresh()

	// Tab switching
	case "1":
		m.activeTab = tabServers
		m.cursor = 0
		m.sortState = newServerSortState()
		return m, nil

	case "2":
		m.activeTab = tabActivity
		m.cursor = 0
		m.sortState = newActivitySortState()
		return m, nil

	case "tab":
		if m.activeTab == tabServers {
			m.activeTab = tabActivity
		} else {
			m.activeTab = tabServers
		}
		m.cursor = 0
		return m, nil

	// Help
	case "?":
		m.uiMode = ModeHelp
		return m, nil

	// Manual refresh
	case "space", "r":
		return m, tea.Batch(
			fetchServers(m.client, m.ctx),
			fetchActivities(m.client, m.ctx),
		)

	// Navigation
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		maxIdx := m.maxIndex()
		if m.cursor < maxIdx {
			m.cursor++
		}
		return m, nil

	case "g":
		m.cursor = 0
		return m, nil

	case "G":
		m.cursor = m.maxIndex()
		return m, nil

	case "pageup", "pgup":
		pageSize := 10
		if m.cursor > pageSize {
			m.cursor -= pageSize
		} else {
			m.cursor = 0
		}
		return m, nil

	case "pagedown", "pgdn":
		pageSize := 10
		maxIdx := m.maxIndex()
		if m.cursor+pageSize <= maxIdx {
			m.cursor += pageSize
		} else {
			m.cursor = maxIdx
		}
		return m, nil

	// Mode switching
	case "f", "F":
		m.uiMode = ModeFilterEdit
		m.focusedFilter = m.getFirstFilterKey()
		m.filterQuery = ""
		return m, nil

	case "s":
		m.uiMode = ModeSortSelect
		return m, nil

	case "/":
		m.uiMode = ModeSearch
		m.filterQuery = ""
		return m, nil

	case "c", "C":
		m.clearFilters()
		return m, nil

	// Server actions (Servers tab only)
	// Use getVisibleServers() so cursor matches the displayed (sorted/filtered) list.
	case "e":
		visible := m.getVisibleServers()
		if m.activeTab == tabServers && m.cursor < len(visible) {
			name := visible[m.cursor].Name
			return m, serverActionCmd(m.client, m.ctx, name, "enable")
		}

	case "d":
		visible := m.getVisibleServers()
		if m.activeTab == tabServers && m.cursor < len(visible) {
			name := visible[m.cursor].Name
			return m, serverActionCmd(m.client, m.ctx, name, "disable")
		}

	case "R":
		visible := m.getVisibleServers()
		if m.activeTab == tabServers && m.cursor < len(visible) {
			name := visible[m.cursor].Name
			return m, serverActionCmd(m.client, m.ctx, name, "restart")
		}

	case "l":
		visible := m.getVisibleServers()
		if m.activeTab == tabServers && m.cursor < len(visible) {
			s := visible[m.cursor]
			if s.HealthAction == "login" {
				return m, oauthLoginCmd(m.client, m.ctx, s.Name)
			}
		}

	case "L":
		var cmds []tea.Cmd
		for _, s := range m.servers {
			if s.HealthAction == "login" {
				cmds = append(cmds, oauthLoginCmd(m.client, m.ctx, s.Name))
			}
		}
		if len(cmds) > 0 {
			cmds = append(cmds, func() tea.Msg { return tickMsg(time.Now()) })
			return m, tea.Batch(cmds...)
		}
	}

	return m, nil
}

// handleFilterMode handles input when in filter edit mode
func (m model) handleFilterMode(key string) (model, tea.Cmd) {
	switch key {
	case "esc", "q":
		m.uiMode = ModeNormal
		m.focusedFilter = ""
		m.filterQuery = ""
		m.cursor = 0
		return m, nil

	case "tab":
		m.focusedFilter = m.getNextFilterKey(m.focusedFilter)
		m.filterQuery = ""
		return m, nil

	case "shift+tab":
		m.focusedFilter = m.getPrevFilterKey(m.focusedFilter)
		m.filterQuery = ""
		return m, nil

	case "up", "k":
		values := m.getAvailableFilterValues(m.focusedFilter)
		if len(values) > 0 {
			current := m.filterState[m.focusedFilter]
			for i, v := range values {
				if v == current {
					if i > 0 {
						m.filterState[m.focusedFilter] = values[i-1]
					}
					break
				}
			}
		}
		return m, nil

	case "down", "j":
		values := m.getAvailableFilterValues(m.focusedFilter)
		if len(values) > 0 {
			current := m.filterState[m.focusedFilter]
			for i, v := range values {
				if v == current {
					if i < len(values)-1 {
						m.filterState[m.focusedFilter] = values[i+1]
					}
					break
				}
			}
		}
		return m, nil

	case "enter":
		m.uiMode = ModeNormal
		m.focusedFilter = ""
		m.filterQuery = ""
		m.cursor = 0
		return m, nil

	case "backspace":
		if len(m.filterQuery) > 0 {
			runes := []rune(m.filterQuery)
			m.filterQuery = string(runes[:len(runes)-1])
		} else {
			delete(m.filterState, m.focusedFilter)
		}
		return m, nil

	default:
		if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			m.filterQuery += key
			m.filterState[m.focusedFilter] = m.filterQuery
		}
		return m, nil
	}
}

// handleSortMode handles input when in sort selection mode
func (m model) handleSortMode(key string) (model, tea.Cmd) {
	switch key {
	case "esc", "q":
		m.uiMode = ModeNormal
		return m, nil

	case "t":
		if m.activeTab == tabActivity {
			m.sortState.Column = "timestamp"
			m.sortState.Descending = true
		}
		m.uiMode = ModeNormal
		m.cursor = 0
		return m, nil

	case "y":
		m.sortState.Column = "type"
		m.sortState.Descending = false
		m.uiMode = ModeNormal
		m.cursor = 0
		return m, nil

	case "s":
		if m.activeTab == tabActivity {
			m.sortState.Column = "server_name"
		} else {
			m.sortState.Column = "admin_state"
		}
		m.sortState.Descending = false
		m.uiMode = ModeNormal
		m.cursor = 0
		return m, nil

	case "d":
		if m.activeTab == tabActivity {
			m.sortState.Column = "duration_ms"
			m.sortState.Descending = true
		}
		m.uiMode = ModeNormal
		m.cursor = 0
		return m, nil

	case "a":
		if m.activeTab == tabActivity {
			m.sortState.Column = "status"
		} else {
			m.sortState.Column = "admin_state"
		}
		m.sortState.Descending = false
		m.uiMode = ModeNormal
		m.cursor = 0
		return m, nil

	case "n":
		if m.activeTab == tabServers {
			m.sortState.Column = "name"
			m.sortState.Descending = false
		}
		m.uiMode = ModeNormal
		m.cursor = 0
		return m, nil

	case "h":
		if m.activeTab == tabServers {
			m.sortState.Column = "health_level"
			m.sortState.Descending = false
		}
		m.uiMode = ModeNormal
		m.cursor = 0
		return m, nil
	}

	return m, nil
}

// handleSearchMode handles input when in search mode
func (m model) handleSearchMode(key string) (model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+c":
		m.uiMode = ModeNormal
		m.filterQuery = ""
		m.cursor = 0
		return m, nil

	case "enter":
		m.cursor = 0
		return m, nil

	case "backspace":
		if len(m.filterQuery) > 0 {
			runes := []rune(m.filterQuery)
			m.filterQuery = string(runes[:len(runes)-1])
		}
		return m, nil

	default:
		if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			m.filterQuery += key
		}
		return m, nil
	}
}

// handleHelpMode handles input when in help mode
func (m model) handleHelpMode(key string) (model, tea.Cmd) {
	switch key {
	case "esc", "q", "?":
		m.uiMode = ModeNormal
		return m, nil
	}
	return m, nil
}

// getFirstFilterKey returns the first available filter key for the current tab
func (m *model) getFirstFilterKey() string {
	if m.activeTab == tabActivity {
		return "status"
	}
	return "admin_state"
}

// getNextFilterKey returns the next filter key
func (m *model) getNextFilterKey(current string) string {
	filterKeys := m.getFilterKeysForTab()
	for i, key := range filterKeys {
		if key == current && i < len(filterKeys)-1 {
			return filterKeys[i+1]
		}
	}
	return filterKeys[0]
}

// getPrevFilterKey returns the previous filter key
func (m *model) getPrevFilterKey(current string) string {
	filterKeys := m.getFilterKeysForTab()
	for i, key := range filterKeys {
		if key == current && i > 0 {
			return filterKeys[i-1]
		}
	}
	return filterKeys[len(filterKeys)-1]
}

// getFilterKeysForTab returns available filter keys for the current tab
func (m *model) getFilterKeysForTab() []string {
	if m.activeTab == tabActivity {
		return []string{"status", "server", "type"}
	}
	return []string{"admin_state", "health_level"}
}

package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// handleNormalMode handles input when in normal navigation mode
func (m model) handleNormalMode(key string) (model, tea.Cmd) {
	switch key {
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

	case "f", "F":
		// Enter filter mode
		m.uiMode = ModeFilterEdit
		m.focusedFilter = m.getFirstFilterKey()
		m.filterQuery = ""
		return m, nil

	case "s":
		// Enter sort mode
		m.uiMode = ModeSortSelect
		return m, nil

	case "/":
		// Enter search mode
		m.uiMode = ModeSearch
		m.filterQuery = ""
		return m, nil

	case "c", "C":
		// Clear all filters and reset sort
		m.clearFilters()
		return m, nil
	}

	return m, nil
}

// handleFilterMode handles input when in filter edit mode
func (m model) handleFilterMode(key string) (model, tea.Cmd) {
	switch key {
	case "esc", "q":
		// Exit filter mode and return to normal
		m.uiMode = ModeNormal
		m.focusedFilter = ""
		m.filterQuery = ""
		m.cursor = 0
		return m, nil

	case "tab":
		// Move to next filter
		m.focusedFilter = m.getNextFilterKey(m.focusedFilter)
		m.filterQuery = ""
		return m, nil

	case "shift+tab":
		// Move to previous filter
		m.focusedFilter = m.getPrevFilterKey(m.focusedFilter)
		m.filterQuery = ""
		return m, nil

	case "up", "k":
		// Cycle to previous filter value
		values := m.getAvailableFilterValues(m.focusedFilter)
		if len(values) > 0 {
			current, _ := m.filterState[m.focusedFilter].(string)
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
		// Cycle to next filter value
		values := m.getAvailableFilterValues(m.focusedFilter)
		if len(values) > 0 {
			current, _ := m.filterState[m.focusedFilter].(string)
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
		// Apply and exit filter mode
		m.uiMode = ModeNormal
		m.focusedFilter = ""
		m.filterQuery = ""
		m.cursor = 0
		return m, nil

	case "backspace":
		// Clear current filter value
		if len(m.filterQuery) > 0 {
			m.filterQuery = m.filterQuery[:len(m.filterQuery)-1]
		} else {
			delete(m.filterState, m.focusedFilter)
		}
		return m, nil

	default:
		// Text input for filter search
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
		// Cancel, return to normal mode without changing sort
		m.uiMode = ModeNormal
		return m, nil

	case "t":
		// Sort by timestamp (activity only)
		if m.activeTab == tabActivity {
			m.sortState.Column = "timestamp"
			m.sortState.Descending = true
		}
		m.uiMode = ModeNormal
		m.cursor = 0
		return m, nil

	case "y":
		// Sort by type
		m.sortState.Column = "type"
		m.sortState.Descending = false
		m.uiMode = ModeNormal
		m.cursor = 0
		return m, nil

	case "s":
		// Sort by server (activity) or state (servers)
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
		// Sort by duration (activity only)
		if m.activeTab == tabActivity {
			m.sortState.Column = "duration_ms"
			m.sortState.Descending = true
		}
		m.uiMode = ModeNormal
		m.cursor = 0
		return m, nil

	case "a":
		// Sort by status/admin_state
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
		// Sort by name (servers only)
		if m.activeTab == tabServers {
			m.sortState.Column = "name"
			m.sortState.Descending = false
		}
		m.uiMode = ModeNormal
		m.cursor = 0
		return m, nil

	case "h":
		// Sort by health (servers only)
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
		// Cancel search, return to normal
		m.uiMode = ModeNormal
		m.filterQuery = ""
		m.cursor = 0
		return m, nil

	case "enter":
		// Apply search as filter, stay in search mode for refinement
		m.cursor = 0
		return m, nil

	case "backspace":
		// Remove last character from search
		if len(m.filterQuery) > 0 {
			m.filterQuery = m.filterQuery[:len(m.filterQuery)-1]
		}
		return m, nil

	default:
		// Add character to search
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
		// Exit help, return to normal
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

// triggerOAuthRefresh triggers non-blocking OAuth refresh for all servers
func (m model) triggerOAuthRefresh() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()

		// Trigger OAuth login for all servers needing auth (empty string means all)
		err := m.client.TriggerOAuthLogin(ctx, "")
		if err != nil {
			return errMsg{fmt.Errorf("oauth refresh failed: %w", err)}
		}

		// Refresh data after OAuth completes
		return tickMsg(time.Now())
	}
}

package tui

import (
	"slices"
	"strings"
)

// filterState represents active filter values as string key-value pairs
type filterState map[string]string

// newFilterState creates an empty filter state
func newFilterState() filterState {
	return make(filterState)
}

// hasActiveFilters returns true if any filters are set
func (f filterState) hasActiveFilters() bool {
	for _, v := range f {
		if v != "" {
			return true
		}
	}
	return false
}

// matchesAllFilters checks if an activity matches all active filters
func (m *model) matchesAllFilters(a activityInfo) bool {
	for filterKey, filterVal := range m.filterState {
		if !m.matchesActivityFilter(a, filterKey, filterVal) {
			return false
		}
	}
	return true
}

// matchesActivityFilter checks if activity matches a single filter
func (m *model) matchesActivityFilter(a activityInfo, key, value string) bool {
	if value == "" {
		return true
	}
	switch key {
	case "status":
		return a.Status == value
	case "server":
		return strings.Contains(strings.ToLower(a.ServerName), strings.ToLower(value))
	case "type":
		return strings.Contains(strings.ToLower(a.Type), strings.ToLower(value))
	case "tool":
		return strings.Contains(strings.ToLower(a.ToolName), strings.ToLower(value))
	}
	return true
}

// matchesServerFilter checks if server matches a single filter
func (m *model) matchesServerFilter(s serverInfo, key, value string) bool {
	if value == "" {
		return true
	}
	switch key {
	case "admin_state":
		return s.AdminState == value
	case "health_level":
		return s.HealthLevel == value
	case "oauth_status":
		return s.OAuthStatus == value
	}
	return true
}

// matchesAllServerFilters checks if a server matches all active filters
func (m *model) matchesAllServerFilters(s serverInfo) bool {
	for filterKey, filterVal := range m.filterState {
		if !m.matchesServerFilter(s, filterKey, filterVal) {
			return false
		}
	}
	return true
}

// filterActivities applies all active filters to activities
func (m *model) filterActivities() []activityInfo {
	if !m.filterState.hasActiveFilters() {
		return m.activities
	}

	filtered := make([]activityInfo, 0, len(m.activities))
	for _, a := range m.activities {
		if m.matchesAllFilters(a) {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

// filterServers applies all active filters to servers
func (m *model) filterServers() []serverInfo {
	if !m.filterState.hasActiveFilters() {
		return m.servers
	}

	filtered := make([]serverInfo, 0, len(m.servers))
	for _, s := range m.servers {
		if m.matchesAllServerFilters(s) {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// getVisibleActivities returns filtered and sorted activities
func (m *model) getVisibleActivities() []activityInfo {
	filtered := m.filterActivities()
	result := make([]activityInfo, len(filtered))
	copy(result, filtered)
	sortActivitiesByState(result, m.sortState)
	return result
}

// getVisibleServers returns filtered and sorted servers
func (m *model) getVisibleServers() []serverInfo {
	filtered := m.filterServers()
	result := make([]serverInfo, len(filtered))
	copy(result, filtered)
	sortServersByState(result, m.sortState)
	return result
}

// clearFilters resets all filters and sort to defaults
func (m *model) clearFilters() {
	m.filterState = newFilterState()
	if m.activeTab == tabActivity {
		m.sortState = newActivitySortState()
	} else {
		m.sortState = newServerSortState()
	}
	m.cursor = 0
}

// getAvailableFilterValues returns possible values for a given filter, sorted deterministically
func (m *model) getAvailableFilterValues(filterKey string) []string {
	values := make(map[string]bool)

	switch filterKey {
	case "status":
		for _, a := range m.activities {
			if a.Status != "" {
				values[a.Status] = true
			}
		}
	case "server":
		for _, a := range m.activities {
			if a.ServerName != "" {
				values[a.ServerName] = true
			}
		}
	case "type":
		for _, a := range m.activities {
			if a.Type != "" {
				values[a.Type] = true
			}
		}
	case "admin_state":
		for _, s := range m.servers {
			if s.AdminState != "" {
				values[s.AdminState] = true
			}
		}
	case "health_level":
		for _, s := range m.servers {
			if s.HealthLevel != "" {
				values[s.HealthLevel] = true
			}
		}
	}

	result := make([]string, 0, len(values))
	for v := range values {
		result = append(result, v)
	}
	slices.Sort(result)
	return result
}

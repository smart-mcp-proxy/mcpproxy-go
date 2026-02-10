package tui

import (
	"strings"
)

// filterState represents active filter values
type filterState map[string]interface{}

// newFilterState creates an empty filter state
func newFilterState() filterState {
	return make(map[string]interface{})
}

// hasActiveFilters returns true if any filters are set
func (f filterState) hasActiveFilters() bool {
	for _, v := range f {
		switch val := v.(type) {
		case string:
			if val != "" {
				return true
			}
		case []string:
			if len(val) > 0 {
				return true
			}
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
func (m *model) matchesActivityFilter(a activityInfo, key string, value interface{}) bool {
	switch key {
	case "status":
		if status, ok := value.(string); ok && status != "" {
			return a.Status == status
		}
	case "server":
		if server, ok := value.(string); ok && server != "" {
			return strings.Contains(strings.ToLower(a.ServerName), strings.ToLower(server))
		}
	case "type":
		if typeVal, ok := value.(string); ok && typeVal != "" {
			return strings.Contains(strings.ToLower(a.Type), strings.ToLower(typeVal))
		}
	case "tool":
		if tool, ok := value.(string); ok && tool != "" {
			return strings.Contains(strings.ToLower(a.ToolName), strings.ToLower(tool))
		}
	}
	return true // No filter means match all
}

// matchesServerFilter checks if server matches a single filter
func (m *model) matchesServerFilter(s serverInfo, key string, value interface{}) bool {
	switch key {
	case "admin_state":
		if state, ok := value.(string); ok && state != "" {
			return s.AdminState == state
		}
	case "health_level":
		if level, ok := value.(string); ok && level != "" {
			return s.HealthLevel == level
		}
	case "oauth_status":
		if status, ok := value.(string); ok && status != "" {
			return s.OAuthStatus == status
		}
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
	// Filter first
	filtered := m.filterActivities()

	// Then sort (using a copy to avoid modifying original)
	result := make([]activityInfo, len(filtered))
	copy(result, filtered)
	sliceModel := &model{
		activities: result,
		sortState:  m.sortState,
	}
	sliceModel.sortActivities()
	return sliceModel.activities
}

// getVisibleServers returns filtered and sorted servers
func (m *model) getVisibleServers() []serverInfo {
	// Filter first
	filtered := m.filterServers()

	// Then sort
	result := make([]serverInfo, len(filtered))
	copy(result, filtered)
	sliceModel := &model{
		servers:   result,
		sortState: m.sortState,
	}
	sliceModel.sortServers()
	return sliceModel.servers
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

// getAvailableFilterValues returns possible values for a given filter
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
	return result
}

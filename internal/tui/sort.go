package tui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// sortState represents the current sort configuration
type sortState struct {
	Column      string // Primary sort column: "timestamp", "type", "server_name", "status", "duration_ms", "name", "admin_state", "health_level"
	Descending  bool   // Sort direction
	Secondary   string // Fallback sort column (e.g., "id" for stable tiebreaking)
}

// newSortState creates default sort state for activity log (newest first)
func newActivitySortState() sortState {
	return sortState{
		Column:     "timestamp",
		Descending: true,
		Secondary:  "id",
	}
}

// newServerSortState creates default sort state for servers (alphabetical)
func newServerSortState() sortState {
	return sortState{
		Column:     "name",
		Descending: false,
		Secondary:  "id",
	}
}

// sortActivities applies stable sort to activities
func (m *model) sortActivities() {
	slices.SortStableFunc(m.activities, func(a, b activityInfo) int {
		return m.compareActivities(a, b)
	})
}

// compareActivities compares two activities according to sort state
// Returns -1 if a < b, 0 if equal, 1 if a > b
func (m *model) compareActivities(a, b activityInfo) int {
	// Compare by primary sort column
	cmp := m.compareActivityField(a, b, m.sortState.Column)

	// Tiebreak with secondary sort (ensures stable output)
	if cmp == 0 && m.sortState.Secondary != "" {
		cmp = m.compareActivityField(a, b, m.sortState.Secondary)
	}

	if m.sortState.Descending {
		return -cmp // Reverse for descending
	}
	return cmp
}

// compareActivityField compares a specific field between two activities
func (m *model) compareActivityField(a, b activityInfo, field string) int {
	switch field {
	case "timestamp":
		return cmp.Compare(a.Timestamp, b.Timestamp)
	case "type":
		return cmp.Compare(a.Type, b.Type)
	case "server_name":
		return cmp.Compare(a.ServerName, b.ServerName)
	case "status":
		return cmp.Compare(a.Status, b.Status)
	case "duration_ms":
		// Parse numeric duration values for proper comparison
		return cmp.Compare(parseDurationMs(a.DurationMs), parseDurationMs(b.DurationMs))
	case "id":
		return cmp.Compare(a.ID, b.ID)
	default:
		return cmp.Compare(a.ID, b.ID) // Default to ID
	}
}

// sortServers applies stable sort to servers
func (m *model) sortServers() {
	slices.SortStableFunc(m.servers, func(a, b serverInfo) int {
		return m.compareServers(a, b)
	})
}

// compareServers compares two servers according to sort state
func (m *model) compareServers(a, b serverInfo) int {
	// Compare by primary sort column
	cmp := m.compareServerField(a, b, m.sortState.Column)

	// Tiebreak with secondary sort
	if cmp == 0 && m.sortState.Secondary != "" {
		cmp = m.compareServerField(a, b, m.sortState.Secondary)
	}

	if m.sortState.Descending {
		return -cmp
	}
	return cmp
}

// compareServerField compares a specific field between two servers
func (m *model) compareServerField(a, b serverInfo, field string) int {
	switch field {
	case "name":
		return cmp.Compare(a.Name, b.Name)
	case "admin_state":
		return cmp.Compare(a.AdminState, b.AdminState)
	case "health_level":
		return cmp.Compare(a.HealthLevel, b.HealthLevel)
	case "token_expires_at":
		return cmp.Compare(a.TokenExpiresAt, b.TokenExpiresAt)
	case "oauth_status":
		return cmp.Compare(a.OAuthStatus, b.OAuthStatus)
	case "tool_count":
		// Numeric comparison for tool count
		if a.ToolCount != b.ToolCount {
			return cmp.Compare(a.ToolCount, b.ToolCount)
		}
		return 0
	default:
		return cmp.Compare(a.Name, b.Name) // Default to name
	}
}

// sortIndicator returns the visual indicator for sort direction
func sortIndicator(descending bool) string {
	if descending {
		return "▼"
	}
	return "▲"
}

// parseDurationMs extracts numeric value from duration string like "42ms"
func parseDurationMs(s string) int64 {
	// Remove "ms" suffix
	s = strings.TrimSuffix(s, "ms")
	// Parse as integer
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return val
}

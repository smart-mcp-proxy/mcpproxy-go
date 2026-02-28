package tui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// sortState represents the current sort configuration
type sortState struct {
	Column     string // Primary sort column
	Descending bool   // Sort direction
	Secondary  string // Fallback sort column for stable tiebreaking
}

// newActivitySortState creates default sort state for activity log (newest first)
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

// sortActivitiesByState applies stable sort to activities using the given sort state
func sortActivitiesByState(activities []activityInfo, state sortState) {
	slices.SortStableFunc(activities, func(a, b activityInfo) int {
		return compareActivities(a, b, state)
	})
}

// compareActivities compares two activities according to sort state
func compareActivities(a, b activityInfo, state sortState) int {
	c := compareActivityField(a, b, state.Column)
	if c == 0 && state.Secondary != "" {
		c = compareActivityField(a, b, state.Secondary)
	}
	if state.Descending {
		return -c
	}
	return c
}

// compareActivityField compares a specific field between two activities
func compareActivityField(a, b activityInfo, field string) int {
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
		return cmp.Compare(parseDurationMs(a.DurationMs), parseDurationMs(b.DurationMs))
	case "id":
		return cmp.Compare(a.ID, b.ID)
	default:
		return cmp.Compare(a.ID, b.ID)
	}
}

// sortServersByState applies stable sort to servers using the given sort state
func sortServersByState(servers []serverInfo, state sortState) {
	slices.SortStableFunc(servers, func(a, b serverInfo) int {
		return compareServers(a, b, state)
	})
}

// compareServers compares two servers according to sort state
func compareServers(a, b serverInfo, state sortState) int {
	c := compareServerField(a, b, state.Column)
	if c == 0 && state.Secondary != "" {
		c = compareServerField(a, b, state.Secondary)
	}
	if state.Descending {
		return -c
	}
	return c
}

// compareServerField compares a specific field between two servers
func compareServerField(a, b serverInfo, field string) int {
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
		return cmp.Compare(a.ToolCount, b.ToolCount)
	default:
		return cmp.Compare(a.Name, b.Name)
	}
}

// parseDurationMs extracts numeric value from duration string like "42ms"
func parseDurationMs(s string) int64 {
	s = strings.TrimSuffix(s, "ms")
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return val
}

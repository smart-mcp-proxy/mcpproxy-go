package tui

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFilterStateBasics tests basic filter state operations
func TestFilterStateBasics(t *testing.T) {
	tests := []struct {
		name         string
		filterState  filterState
		expectActive bool
	}{
		{
			name:         "empty filter is not active",
			filterState:  newFilterState(),
			expectActive: false,
		},
		{
			name: "filter with empty status is not active",
			filterState: filterState{
				"status": "",
			},
			expectActive: false,
		},
		{
			name: "filter with non-empty status is active",
			filterState: filterState{
				"status": "error",
			},
			expectActive: true,
		},
		{
			name: "multiple active filters",
			filterState: filterState{
				"status": "error",
				"server": "github",
			},
			expectActive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.filterState.hasActiveFilters()
			assert.Equal(t, tt.expectActive, result)
		})
	}
}

// TestFilterActivityByStatus tests filtering activities by status
func TestFilterActivityByStatus(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activities = []activityInfo{
		{ID: "1", Status: "success", Type: "tool_call"},
		{ID: "2", Status: "error", Type: "tool_call"},
		{ID: "3", Status: "success", Type: "server_event"},
		{ID: "4", Status: "blocked", Type: "tool_call"},
	}
	m.filterState = filterState{"status": "error"}

	result := m.matchesAllFilters(m.activities[1])
	assert.True(t, result, "should match error status")

	result = m.matchesAllFilters(m.activities[0])
	assert.False(t, result, "should not match success status")
}

// TestFilterActivityByServer tests filtering activities by server name
func TestFilterActivityByServer(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activities = []activityInfo{
		{ID: "1", ServerName: "github", Type: "tool_call"},
		{ID: "2", ServerName: "stripe", Type: "tool_call"},
		{ID: "3", ServerName: "GitHub-API", Type: "server_event"}, // Case variation
	}
	m.filterState = filterState{"server": "github"}

	// Should match case-insensitively
	result := m.matchesAllFilters(m.activities[0])
	assert.True(t, result, "should match github")

	result = m.matchesAllFilters(m.activities[2])
	assert.True(t, result, "should match GitHub-API (case-insensitive)")

	result = m.matchesAllFilters(m.activities[1])
	assert.False(t, result, "should not match stripe")
}

// TestFilterActivityByType tests filtering activities by type
func TestFilterActivityByType(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activities = []activityInfo{
		{ID: "1", Type: "tool_call"},
		{ID: "2", Type: "server_event"},
		{ID: "3", Type: "TOOL_CALL"}, // Case variation
	}
	m.filterState = filterState{"type": "tool_call"}

	result := m.matchesAllFilters(m.activities[0])
	assert.True(t, result, "should match tool_call")

	result = m.matchesAllFilters(m.activities[2])
	assert.True(t, result, "should match TOOL_CALL (case-insensitive)")

	result = m.matchesAllFilters(m.activities[1])
	assert.False(t, result, "should not match server_event")
}

// TestFilterActivityByTool tests filtering activities by tool name
func TestFilterActivityByTool(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activities = []activityInfo{
		{ID: "1", ToolName: "list_repositories"},
		{ID: "2", ToolName: "create_issue"},
		{ID: "3", ToolName: "LIST_REPOSITORIES"},
	}
	m.filterState = filterState{"tool": "list"}

	result := m.matchesAllFilters(m.activities[0])
	assert.True(t, result, "should match tool containing 'list'")

	result = m.matchesAllFilters(m.activities[2])
	assert.True(t, result, "should match LIST_REPOSITORIES (case-insensitive)")

	result = m.matchesAllFilters(m.activities[1])
	assert.False(t, result, "should not match create_issue")
}

// TestFilterActivitiesMultipleCombinations tests multiple filters combined
func TestFilterActivitiesMultipleCombinations(t *testing.T) {
	tests := []struct {
		name         string
		activities   []activityInfo
		filterState  filterState
		expectedIDs  []string
	}{
		{
			name: "status + server filter",
			activities: []activityInfo{
				{ID: "1", Status: "success", ServerName: "github"},
				{ID: "2", Status: "error", ServerName: "github"},
				{ID: "3", Status: "error", ServerName: "stripe"},
			},
			filterState: filterState{"status": "error", "server": "github"},
			expectedIDs: []string{"2"},
		},
		{
			name: "status + type + server filter",
			activities: []activityInfo{
				{ID: "1", Status: "success", Type: "tool_call", ServerName: "github"},
				{ID: "2", Status: "error", Type: "tool_call", ServerName: "github"},
				{ID: "3", Status: "error", Type: "server_event", ServerName: "github"},
				{ID: "4", Status: "error", Type: "tool_call", ServerName: "stripe"},
			},
			filterState: filterState{"status": "error", "type": "tool_call", "server": "github"},
			expectedIDs: []string{"2"},
		},
		{
			name: "empty filters match all",
			activities: []activityInfo{
				{ID: "1", Status: "success"},
				{ID: "2", Status: "error"},
			},
			filterState: filterState{},
			expectedIDs: []string{"1", "2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(context.Background(), client, 5*time.Second)
			m.activities = tt.activities
			m.filterState = tt.filterState

			filtered := m.filterActivities()
			require.Len(t, filtered, len(tt.expectedIDs))

			for i, expID := range tt.expectedIDs {
				assert.Equal(t, expID, filtered[i].ID)
			}
		})
	}
}

// TestFilterServerByAdminState tests filtering servers by admin state
func TestFilterServerByAdminState(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "github", AdminState: "enabled"},
		{Name: "stripe", AdminState: "disabled"},
		{Name: "glean", AdminState: "enabled"},
	}
	m.filterState = filterState{"admin_state": "enabled"}

	result := m.matchesAllServerFilters(m.servers[0])
	assert.True(t, result, "should match enabled state")

	result = m.matchesAllServerFilters(m.servers[1])
	assert.False(t, result, "should not match disabled state")
}

// TestFilterServerByHealthLevel tests filtering servers by health level
func TestFilterServerByHealthLevel(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "github", HealthLevel: "healthy"},
		{Name: "stripe", HealthLevel: "degraded"},
		{Name: "glean", HealthLevel: "unhealthy"},
	}
	m.filterState = filterState{"health_level": "degraded"}

	result := m.matchesAllServerFilters(m.servers[1])
	assert.True(t, result, "should match degraded health")

	result = m.matchesAllServerFilters(m.servers[0])
	assert.False(t, result, "should not match healthy")
}

// TestFilterServerByOAuthStatus tests filtering servers by OAuth status
func TestFilterServerByOAuthStatus(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "github", OAuthStatus: "authenticated"},
		{Name: "stripe", OAuthStatus: "expired"},
		{Name: "glean", OAuthStatus: "authenticated"},
	}
	m.filterState = filterState{"oauth_status": "expired"}

	result := m.matchesAllServerFilters(m.servers[1])
	assert.True(t, result, "should match expired status")

	result = m.matchesAllServerFilters(m.servers[0])
	assert.False(t, result, "should not match authenticated")
}

// TestFilterServersMultiple tests multiple server filters
func TestFilterServersMultiple(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "github", AdminState: "enabled", HealthLevel: "healthy"},
		{Name: "stripe", AdminState: "enabled", HealthLevel: "degraded"},
		{Name: "glean", AdminState: "disabled", HealthLevel: "healthy"},
	}
	m.filterState = filterState{"admin_state": "enabled", "health_level": "healthy"}

	filtered := m.filterServers()
	require.Len(t, filtered, 1)
	assert.Equal(t, "github", filtered[0].Name)
}

// TestGetVisibleActivitiesWithFilterAndSort tests filtering + sorting combined
func TestGetVisibleActivitiesWithFilterAndSort(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activities = []activityInfo{
		{ID: "1", Status: "success", Timestamp: "2026-02-09T14:02:00Z"},
		{ID: "2", Status: "error", Timestamp: "2026-02-09T14:00:00Z"},
		{ID: "3", Status: "success", Timestamp: "2026-02-09T14:01:00Z"},
		{ID: "4", Status: "error", Timestamp: "2026-02-09T14:03:00Z"},
	}
	m.filterState = filterState{"status": "success"}
	m.sortState = newActivitySortState() // Timestamp DESC by default

	visible := m.getVisibleActivities()

	// Should only have success activities (1, 3)
	require.Len(t, visible, 2)
	// Should be sorted newest first (14:02:00 before 14:01:00)
	assert.Equal(t, "1", visible[0].ID)
	assert.Equal(t, "3", visible[1].ID)
}

// TestGetVisibleServersWithFilterAndSort tests filtering + sorting for servers
func TestGetVisibleServersWithFilterAndSort(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "charlie", AdminState: "enabled", HealthLevel: "healthy"},
		{Name: "alpha", AdminState: "enabled", HealthLevel: "unhealthy"},
		{Name: "bravo", AdminState: "disabled", HealthLevel: "healthy"},
	}
	m.filterState = filterState{"admin_state": "enabled"}
	m.sortState = newServerSortState() // Name ASC by default

	visible := m.getVisibleServers()

	// Should only have enabled servers (charlie, alpha)
	require.Len(t, visible, 2)
	// Should be sorted alphabetically (alpha before charlie)
	assert.Equal(t, "alpha", visible[0].Name)
	assert.Equal(t, "charlie", visible[1].Name)
}

// TestClearFilters tests clearing all filters and resetting sort
func TestClearFilters(t *testing.T) {
	tests := []struct {
		name        string
		initialTab  tab
		expectCol   string
		expectDesc  bool
	}{
		{
			name:       "clear on Activity tab",
			initialTab: tabActivity,
			expectCol:  "timestamp",
			expectDesc: true,
		},
		{
			name:       "clear on Servers tab",
			initialTab: tabServers,
			expectCol:  "name",
			expectDesc: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(context.Background(), client, 5*time.Second)
			m.activeTab = tt.initialTab
			m.filterState = filterState{"status": "error", "server": "github"}
			m.sortState = sortState{Column: "type", Descending: true}
			m.cursor = 5

			m.clearFilters()

			assert.False(t, m.filterState.hasActiveFilters(), "filters should be cleared")
			assert.Equal(t, tt.expectCol, m.sortState.Column, "sort column should reset to default")
			assert.Equal(t, tt.expectDesc, m.sortState.Descending, "sort direction should reset to default")
			assert.Equal(t, 0, m.cursor, "cursor should reset to 0")
		})
	}
}

// TestGetAvailableFilterValuesStatus tests getting available status values
func TestGetAvailableFilterValuesStatus(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activities = []activityInfo{
		{Status: "success"},
		{Status: "error"},
		{Status: "success"},
		{Status: "blocked"},
		{Status: ""}, // Empty should be ignored
	}

	values := m.getAvailableFilterValues("status")
	assert.ElementsMatch(t, []string{"success", "error", "blocked"}, values)
}

// TestGetAvailableFilterValuesServer tests getting available server values
func TestGetAvailableFilterValuesServer(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activities = []activityInfo{
		{ServerName: "github"},
		{ServerName: "stripe"},
		{ServerName: "github"},
		{ServerName: ""},
	}

	values := m.getAvailableFilterValues("server")
	assert.ElementsMatch(t, []string{"github", "stripe"}, values)
}

// TestGetAvailableFilterValuesType tests getting available type values
func TestGetAvailableFilterValuesType(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activities = []activityInfo{
		{Type: "tool_call"},
		{Type: "server_event"},
		{Type: "tool_call"},
		{Type: ""},
	}

	values := m.getAvailableFilterValues("type")
	assert.ElementsMatch(t, []string{"tool_call", "server_event"}, values)
}

// TestGetAvailableFilterValuesAdminState tests getting available admin state values
func TestGetAvailableFilterValuesAdminState(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{AdminState: "enabled"},
		{AdminState: "disabled"},
		{AdminState: "enabled"},
		{AdminState: "quarantined"},
	}

	values := m.getAvailableFilterValues("admin_state")
	assert.ElementsMatch(t, []string{"enabled", "disabled", "quarantined"}, values)
}

// TestGetAvailableFilterValuesHealthLevel tests getting available health level values
func TestGetAvailableFilterValuesHealthLevel(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{
		{HealthLevel: "healthy"},
		{HealthLevel: "degraded"},
		{HealthLevel: "unhealthy"},
	}

	values := m.getAvailableFilterValues("health_level")
	assert.ElementsMatch(t, []string{"healthy", "degraded", "unhealthy"}, values)
}

// TestFilterActivitiesEmpty tests filtering empty activity list
func TestFilterActivitiesEmpty(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activities = []activityInfo{}
	m.filterState = filterState{"status": "error"}

	filtered := m.filterActivities()
	assert.Empty(t, filtered)
}

// TestFilterServersEmpty tests filtering empty server list
func TestFilterServersEmpty(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.servers = []serverInfo{}
	m.filterState = filterState{"admin_state": "enabled"}

	filtered := m.filterServers()
	assert.Empty(t, filtered)
}

// TestMatchesActivityFilterUnknownKey tests unknown filter keys
func TestMatchesActivityFilterUnknownKey(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	a := activityInfo{Status: "success"}

	// Unknown key should match all (return true)
	result := m.matchesActivityFilter(a, "unknown_key", "some_value")
	assert.True(t, result, "unknown filter key should match all")
}

// TestMatchesServerFilterUnknownKey tests unknown filter keys for servers
func TestMatchesServerFilterUnknownKey(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	s := serverInfo{Name: "github"}

	// Unknown key should match all (return true)
	result := m.matchesServerFilter(s, "unknown_key", "some_value")
	assert.True(t, result, "unknown filter key should match all")
}

// TestFilterActivitiesNoMatch tests when filter matches nothing
func TestFilterActivitiesNoMatch(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activities = []activityInfo{
		{ID: "1", Status: "success"},
		{ID: "2", Status: "error"},
	}
	m.filterState = filterState{"status": "blocked"}

	filtered := m.filterActivities()
	assert.Empty(t, filtered, "no activities should match 'blocked'")
}

// TestFilterWithSpecialCharacters tests filtering with special characters
func TestFilterWithSpecialCharacters(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activities = []activityInfo{
		{ID: "1", ServerName: "github-api"},
		{ID: "2", ServerName: "stripe_prod"},
		{ID: "3", ServerName: "api.example.com"},
	}
	m.filterState = filterState{"server": "api"}

	filtered := m.filterActivities()
	// Should match both "github-api" and "api.example.com"
	require.Len(t, filtered, 2)
	assert.Equal(t, "1", filtered[0].ID)
	assert.Equal(t, "3", filtered[1].ID)
}

// BenchmarkFilterActivities measures filter performance on 10k rows
func BenchmarkFilterActivities(b *testing.B) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activities = make([]activityInfo, 10000)
	for i := 0; i < 10000; i++ {
		m.activities[i] = activityInfo{
			ID:         string(rune(i)),
			Status:     []string{"success", "error", "blocked"}[i%3],
			ServerName: []string{"github", "stripe", "amplitude"}[i%3],
			Type:       []string{"tool_call", "server_event"}[i%2],
		}
	}
	m.filterState = filterState{"status": "error", "server": "github"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.filterActivities()
	}
}

// BenchmarkGetVisibleActivities measures combined filter + sort performance
func BenchmarkGetVisibleActivities(b *testing.B) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.activities = make([]activityInfo, 10000)
	for i := 0; i < 10000; i++ {
		m.activities[i] = activityInfo{
			ID:         string(rune(i)),
			Status:     []string{"success", "error"}[i%2],
			Timestamp:  time.Unix(int64(i), 0).UTC().Format(time.RFC3339),
		}
	}
	m.filterState = filterState{"status": "error"}
	m.sortState = newActivitySortState()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.getVisibleActivities()
	}
}

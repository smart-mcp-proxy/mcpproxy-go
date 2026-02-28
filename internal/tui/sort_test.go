package tui

import (
	"testing"
	"time"
)

// TestSortActivitiesByTimestamp verifies activities are sorted chronologically ascending
func TestSortActivitiesByTimestamp(t *testing.T) {
	activities := []activityInfo{
		{Timestamp: "2026-02-09T14:02:00Z", ServerName: "github", Type: "tool_call", ID: "aaa"},
		{Timestamp: "2026-02-09T14:00:00Z", ServerName: "glean", Type: "tool_call", ID: "bbb"},
		{Timestamp: "2026-02-09T14:01:00Z", ServerName: "amplitude", Type: "tool_call", ID: "ccc"},
	}

	sortActivitiesByState(activities, sortState{Column: "timestamp", Descending: false, Secondary: "id"})

	expected := []string{"14:00:00Z", "14:01:00Z", "14:02:00Z"}
	for i, exp := range expected {
		if !contains(activities[i].Timestamp, exp) {
			t.Errorf("At index %d: got %s, want timestamp containing %s", i, activities[i].Timestamp, exp)
		}
	}
}

// TestSortActivitiesByTimestampDescending verifies activities are sorted chronologically descending
func TestSortActivitiesByTimestampDescending(t *testing.T) {
	activities := []activityInfo{
		{Timestamp: "2026-02-09T14:00:00Z", ServerName: "amplitude", ID: "aaa"},
		{Timestamp: "2026-02-09T14:02:00Z", ServerName: "github", ID: "bbb"},
		{Timestamp: "2026-02-09T14:01:00Z", ServerName: "glean", ID: "ccc"},
	}

	sortActivitiesByState(activities, sortState{Column: "timestamp", Descending: true, Secondary: "id"})

	expected := []string{"14:02:00Z", "14:01:00Z", "14:00:00Z"}
	for i, exp := range expected {
		if !contains(activities[i].Timestamp, exp) {
			t.Errorf("At index %d: got %s, want timestamp containing %s", i, activities[i].Timestamp, exp)
		}
	}
}

// TestSortActivitiesByType verifies activities are sorted alphabetically by type
func TestSortActivitiesByType(t *testing.T) {
	activities := []activityInfo{
		{Type: "tool_call", ID: "aaa"},
		{Type: "error", ID: "bbb"},
		{Type: "server_event", ID: "ccc"},
	}

	sortActivitiesByState(activities, sortState{Column: "type", Descending: false, Secondary: "id"})

	expected := []string{"error", "server_event", "tool_call"}
	for i, exp := range expected {
		if activities[i].Type != exp {
			t.Errorf("At index %d: got %s, want %s", i, activities[i].Type, exp)
		}
	}
}

// TestSortActivitiesByServer verifies activities are sorted by server name
func TestSortActivitiesByServer(t *testing.T) {
	activities := []activityInfo{
		{ServerName: "github", ID: "aaa"},
		{ServerName: "amplitude", ID: "bbb"},
		{ServerName: "glean", ID: "ccc"},
	}

	sortActivitiesByState(activities, sortState{Column: "server_name", Descending: false, Secondary: "id"})

	expected := []string{"amplitude", "github", "glean"}
	for i, exp := range expected {
		if activities[i].ServerName != exp {
			t.Errorf("At index %d: got %s, want %s", i, activities[i].ServerName, exp)
		}
	}
}

// TestSortActivitiesByStatus verifies activities are sorted by status
func TestSortActivitiesByStatus(t *testing.T) {
	activities := []activityInfo{
		{Status: "success", ID: "aaa"},
		{Status: "blocked", ID: "bbb"},
		{Status: "error", ID: "ccc"},
	}

	sortActivitiesByState(activities, sortState{Column: "status", Descending: false, Secondary: "id"})

	expected := []string{"blocked", "error", "success"}
	for i, exp := range expected {
		if activities[i].Status != exp {
			t.Errorf("At index %d: got %s, want %s", i, activities[i].Status, exp)
		}
	}
}

// TestSortActivitiesByDuration verifies numeric duration sorting
func TestSortActivitiesByDuration(t *testing.T) {
	activities := []activityInfo{
		{DurationMs: "1023ms", ID: "aaa"},
		{DurationMs: "5ms", ID: "bbb"},
		{DurationMs: "42ms", ID: "ccc"},
	}

	sortActivitiesByState(activities, sortState{Column: "duration_ms", Descending: false, Secondary: "id"})

	expected := []string{"5ms", "42ms", "1023ms"}
	for i, exp := range expected {
		if activities[i].DurationMs != exp {
			t.Errorf("At index %d: got %s, want %s", i, activities[i].DurationMs, exp)
		}
	}
}

// TestSortActivitiesByDurationDescending verifies descending numeric sort
func TestSortActivitiesByDurationDescending(t *testing.T) {
	activities := []activityInfo{
		{DurationMs: "5ms", ID: "aaa"},
		{DurationMs: "1023ms", ID: "bbb"},
		{DurationMs: "42ms", ID: "ccc"},
	}

	sortActivitiesByState(activities, sortState{Column: "duration_ms", Descending: true, Secondary: "id"})

	expected := []string{"1023ms", "42ms", "5ms"}
	for i, exp := range expected {
		if activities[i].DurationMs != exp {
			t.Errorf("At index %d: got %s, want %s", i, activities[i].DurationMs, exp)
		}
	}
}

// TestStableSortWithSecondaryColumn verifies that identical primary sort values
// use secondary column for tiebreaking
func TestStableSortWithSecondaryColumn(t *testing.T) {
	activities := []activityInfo{
		{Timestamp: "2026-02-09T14:00:00Z", ID: "id-3", Type: "tool_call"},
		{Timestamp: "2026-02-09T14:00:00Z", ID: "id-1", Type: "tool_call"},
		{Timestamp: "2026-02-09T14:00:00Z", ID: "id-2", Type: "tool_call"},
	}

	sortActivitiesByState(activities, sortState{Column: "timestamp", Descending: false, Secondary: "id"})

	expected := []string{"id-1", "id-2", "id-3"}
	for i, exp := range expected {
		if activities[i].ID != exp {
			t.Errorf("At index %d: got %s, want %s", i, activities[i].ID, exp)
		}
	}
}

// TestSortServers tests server sorting by various columns
func TestSortServers(t *testing.T) {
	tests := []struct {
		name        string
		servers     []serverInfo
		sortState   sortState
		expectedIdx []int
	}{
		{
			name: "sort servers by name ascending",
			servers: []serverInfo{
				{Name: "glean"},
				{Name: "amplitude"},
				{Name: "github"},
			},
			sortState:   sortState{Column: "name", Descending: false},
			expectedIdx: []int{1, 2, 0},
		},
		{
			name: "sort servers by name descending",
			servers: []serverInfo{
				{Name: "amplitude"},
				{Name: "github"},
				{Name: "glean"},
			},
			sortState:   sortState{Column: "name", Descending: true},
			expectedIdx: []int{2, 1, 0},
		},
		{
			name: "sort servers by health level",
			servers: []serverInfo{
				{Name: "s1", HealthLevel: "unhealthy"},
				{Name: "s2", HealthLevel: "healthy"},
				{Name: "s3", HealthLevel: "degraded"},
			},
			sortState:   sortState{Column: "health_level", Descending: false},
			expectedIdx: []int{2, 1, 0},
		},
		{
			name: "sort servers by tool count descending",
			servers: []serverInfo{
				{Name: "s1", ToolCount: 5},
				{Name: "s2", ToolCount: 12},
				{Name: "s3", ToolCount: 8},
			},
			sortState:   sortState{Column: "tool_count", Descending: true},
			expectedIdx: []int{1, 2, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			servers := make([]serverInfo, len(tt.servers))
			copy(servers, tt.servers)

			sortServersByState(servers, tt.sortState)

			for i, expIdx := range tt.expectedIdx {
				if servers[i].Name != tt.servers[expIdx].Name {
					t.Errorf("At index %d: got %s, want %s", i, servers[i].Name, tt.servers[expIdx].Name)
				}
			}
		})
	}
}

// TestNewActivitySortState verifies default sort state for activities
func TestNewActivitySortState(t *testing.T) {
	s := newActivitySortState()
	if s.Column != "timestamp" {
		t.Errorf("Expected Column='timestamp', got '%s'", s.Column)
	}
	if !s.Descending {
		t.Errorf("Expected Descending=true, got false")
	}
	if s.Secondary != "id" {
		t.Errorf("Expected Secondary='id', got '%s'", s.Secondary)
	}
}

// TestNewServerSortState verifies default sort state for servers
func TestNewServerSortState(t *testing.T) {
	s := newServerSortState()
	if s.Column != "name" {
		t.Errorf("Expected Column='name', got '%s'", s.Column)
	}
	if s.Descending {
		t.Errorf("Expected Descending=false, got true")
	}
	if s.Secondary != "id" {
		t.Errorf("Expected Secondary='id', got '%s'", s.Secondary)
	}
}

// TestAddSortMark tests column header marking
func TestAddSortMark(t *testing.T) {
	tests := []struct {
		label      string
		currentCol string
		colKey     string
		mark       string
		expected   string
	}{
		{"TYPE", "type", "type", "▼", "TYPE ▼"},
		{"SERVER", "type", "server_name", "▼", "SERVER"},
		{"STATUS", "status", "status", "▲", "STATUS ▲"},
	}

	for _, tt := range tests {
		if result := addSortMark(tt.label, tt.currentCol, tt.colKey, tt.mark); result != tt.expected {
			t.Errorf("addSortMark(%s, %s, %s, %s) = %s, want %s", tt.label, tt.currentCol, tt.colKey, tt.mark, result, tt.expected)
		}
	}
}

// TestParseDurationMs tests duration parsing
func TestParseDurationMs(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		{"42ms", 42},
		{"1023ms", 1023},
		{"5ms", 5},
		{"0ms", 0},
		{"invalid", 0},
		{"", 0},
	}

	for _, tt := range tests {
		if result := parseDurationMs(tt.input); result != tt.expected {
			t.Errorf("parseDurationMs(%q) = %d, want %d", tt.input, result, tt.expected)
		}
	}
}

// BenchmarkSortActivities benchmarks sort performance
func BenchmarkSortActivities(b *testing.B) {
	activities := make([]activityInfo, 1000)
	for i := 0; i < 1000; i++ {
		activities[i] = activityInfo{
			ID:         string(rune(i)),
			Type:       []string{"tool_call", "server_event", "error"}[i%3],
			ServerName: []string{"glean", "github", "amplitude"}[i%3],
			Status:     []string{"success", "error", "blocked"}[i%3],
			Timestamp:  time.Unix(int64(i)*100, 0).UTC().Format(time.RFC3339),
			DurationMs: "42ms",
		}
	}

	state := sortState{Column: "timestamp", Descending: false, Secondary: "id"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		act := make([]activityInfo, len(activities))
		copy(act, activities)
		sortActivitiesByState(act, state)
	}
}

// Helper function
func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

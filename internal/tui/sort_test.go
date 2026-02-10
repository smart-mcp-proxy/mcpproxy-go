package tui

import (
	"context"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
)

// mockClient is a test implementation of the Client interface
type mockClient struct{}

func (m *mockClient) GetServers(ctx context.Context) ([]map[string]interface{}, error) {
	return nil, nil
}

func (m *mockClient) ListActivities(ctx context.Context, filter cliclient.ActivityFilterParams) ([]map[string]interface{}, int, error) {
	return nil, 0, nil
}

func (m *mockClient) ServerAction(ctx context.Context, name, action string) error {
	return nil
}

func (m *mockClient) TriggerOAuthLogin(ctx context.Context, name string) error {
	return nil
}

// TestSortActivitiesByTimestamp verifies activities are sorted chronologically ascending
func TestSortActivitiesByTimestamp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := &model{
		client: &mockClient{},
		ctx:    ctx,
		sortState: sortState{
			Column:     "timestamp",
			Descending: false,
			Secondary:  "id",
		},
		activities: []activityInfo{
			{Timestamp: "2026-02-09T14:02:00Z", ServerName: "github", Type: "tool_call", ID: "aaa"},
			{Timestamp: "2026-02-09T14:00:00Z", ServerName: "glean", Type: "tool_call", ID: "bbb"},
			{Timestamp: "2026-02-09T14:01:00Z", ServerName: "amplitude", Type: "tool_call", ID: "ccc"},
		},
	}

	model.sortActivities()

	// Should be: 14:00:00, 14:01:00, 14:02:00
	expected := []string{"14:00:00Z", "14:01:00Z", "14:02:00Z"}
	for i, exp := range expected {
		if !contains(model.activities[i].Timestamp, exp) {
			t.Errorf("At index %d: got %s, want timestamp containing %s", i, model.activities[i].Timestamp, exp)
		}
	}
}

// TestSortActivitiesByTimestampDescending verifies activities are sorted chronologically descending
func TestSortActivitiesByTimestampDescending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := &model{
		client: &mockClient{},
		ctx:    ctx,
		sortState: sortState{
			Column:     "timestamp",
			Descending: true,
			Secondary:  "id",
		},
		activities: []activityInfo{
			{Timestamp: "2026-02-09T14:00:00Z", ServerName: "amplitude", ID: "aaa"},
			{Timestamp: "2026-02-09T14:02:00Z", ServerName: "github", ID: "bbb"},
			{Timestamp: "2026-02-09T14:01:00Z", ServerName: "glean", ID: "ccc"},
		},
	}

	model.sortActivities()

	// Should be: 14:02:00, 14:01:00, 14:00:00
	expected := []string{"14:02:00Z", "14:01:00Z", "14:00:00Z"}
	for i, exp := range expected {
		if !contains(model.activities[i].Timestamp, exp) {
			t.Errorf("At index %d: got %s, want timestamp containing %s", i, model.activities[i].Timestamp, exp)
		}
	}
}

// TestSortActivitiesByType verifies activities are sorted alphabetically by type
func TestSortActivitiesByType(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := &model{
		client: &mockClient{},
		ctx:    ctx,
		sortState: sortState{
			Column:     "type",
			Descending: false,
			Secondary:  "id",
		},
		activities: []activityInfo{
			{Type: "tool_call", ID: "aaa"},
			{Type: "error", ID: "bbb"},
			{Type: "server_event", ID: "ccc"},
		},
	}

	model.sortActivities()

	// Should be: error, server_event, tool_call (alphabetical)
	expected := []string{"error", "server_event", "tool_call"}
	for i, exp := range expected {
		if model.activities[i].Type != exp {
			t.Errorf("At index %d: got %s, want %s", i, model.activities[i].Type, exp)
		}
	}
}

// TestSortActivitiesByServer verifies activities are sorted by server name
func TestSortActivitiesByServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := &model{
		client: &mockClient{},
		ctx:    ctx,
		sortState: sortState{
			Column:     "server_name",
			Descending: false,
			Secondary:  "id",
		},
		activities: []activityInfo{
			{ServerName: "github", ID: "aaa"},
			{ServerName: "amplitude", ID: "bbb"},
			{ServerName: "glean", ID: "ccc"},
		},
	}

	model.sortActivities()

	// Should be: amplitude, github, glean (alphabetical)
	expected := []string{"amplitude", "github", "glean"}
	for i, exp := range expected {
		if model.activities[i].ServerName != exp {
			t.Errorf("At index %d: got %s, want %s", i, model.activities[i].ServerName, exp)
		}
	}
}

// TestSortActivitiesByStatus verifies activities are sorted by status
func TestSortActivitiesByStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := &model{
		client: &mockClient{},
		ctx:    ctx,
		sortState: sortState{
			Column:     "status",
			Descending: false,
			Secondary:  "id",
		},
		activities: []activityInfo{
			{Status: "success", ID: "aaa"},
			{Status: "blocked", ID: "bbb"},
			{Status: "error", ID: "ccc"},
		},
	}

	model.sortActivities()

	// Should be: blocked, error, success (alphabetical)
	expected := []string{"blocked", "error", "success"}
	for i, exp := range expected {
		if model.activities[i].Status != exp {
			t.Errorf("At index %d: got %s, want %s", i, model.activities[i].Status, exp)
		}
	}
}

// TestSortActivitiesByDuration verifies numeric duration sorting
func TestSortActivitiesByDuration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := &model{
		client: &mockClient{},
		ctx:    ctx,
		sortState: sortState{
			Column:     "duration_ms",
			Descending: false,
			Secondary:  "id",
		},
		activities: []activityInfo{
			{DurationMs: "1023ms", ID: "aaa"},
			{DurationMs: "5ms", ID: "bbb"},
			{DurationMs: "42ms", ID: "ccc"},
		},
	}

	model.sortActivities()

	// Should be: 5ms, 42ms, 1023ms (numeric)
	expected := []string{"5ms", "42ms", "1023ms"}
	for i, exp := range expected {
		if model.activities[i].DurationMs != exp {
			t.Errorf("At index %d: got %s, want %s", i, model.activities[i].DurationMs, exp)
		}
	}
}

// TestSortActivitiesByDurationDescending verifies descending numeric sort
func TestSortActivitiesByDurationDescending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := &model{
		client: &mockClient{},
		ctx:    ctx,
		sortState: sortState{
			Column:     "duration_ms",
			Descending: true,
			Secondary:  "id",
		},
		activities: []activityInfo{
			{DurationMs: "5ms", ID: "aaa"},
			{DurationMs: "1023ms", ID: "bbb"},
			{DurationMs: "42ms", ID: "ccc"},
		},
	}

	model.sortActivities()

	// Should be: 1023ms, 42ms, 5ms (numeric descending)
	expected := []string{"1023ms", "42ms", "5ms"}
	for i, exp := range expected {
		if model.activities[i].DurationMs != exp {
			t.Errorf("At index %d: got %s, want %s", i, model.activities[i].DurationMs, exp)
		}
	}
}

// TestStableSortWithSecondaryColumn verifies that identical primary sort values
// use secondary column for tiebreaking
func TestStableSortWithSecondaryColumn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := &model{
		client: &mockClient{},
		ctx:    ctx,
		sortState: sortState{
			Column:     "timestamp",
			Descending: false,
			Secondary:  "id",
		},
		activities: []activityInfo{
			{Timestamp: "2026-02-09T14:00:00Z", ID: "id-3", Type: "tool_call"},
			{Timestamp: "2026-02-09T14:00:00Z", ID: "id-1", Type: "tool_call"},
			{Timestamp: "2026-02-09T14:00:00Z", ID: "id-2", Type: "tool_call"},
		},
	}

	model.sortActivities()

	// When sorted by timestamp (identical), secondary sort by ID should give us id-1, id-2, id-3
	expected := []string{"id-1", "id-2", "id-3"}
	for i, exp := range expected {
		if model.activities[i].ID != exp {
			t.Errorf("At index %d: got %s, want %s", i, model.activities[i].ID, exp)
		}
	}
}

// TestSortServers tests server sorting by various columns
func TestSortServers(t *testing.T) {
	tests := []struct {
		name        string
		servers     []serverInfo
		sortState   sortState
		expectedIdx []int // indices of expected order from original array
	}{
		{
			name: "sort servers by name ascending",
			servers: []serverInfo{
				{Name: "glean"},
				{Name: "amplitude"},
				{Name: "github"},
			},
			sortState:   sortState{Column: "name", Descending: false},
			expectedIdx: []int{1, 2, 0}, // amplitude, github, glean
		},
		{
			name: "sort servers by name descending",
			servers: []serverInfo{
				{Name: "amplitude"},
				{Name: "github"},
				{Name: "glean"},
			},
			sortState:   sortState{Column: "name", Descending: true},
			expectedIdx: []int{2, 1, 0}, // glean, github, amplitude
		},
		{
			name: "sort servers by health level",
			servers: []serverInfo{
				{Name: "s1", HealthLevel: "unhealthy"},
				{Name: "s2", HealthLevel: "healthy"},
				{Name: "s3", HealthLevel: "degraded"},
			},
			sortState:   sortState{Column: "health_level", Descending: false},
			expectedIdx: []int{2, 1, 0}, // degraded, healthy, unhealthy (alphabetical)
		},
		{
			name: "sort servers by tool count descending",
			servers: []serverInfo{
				{Name: "s1", ToolCount: 5},
				{Name: "s2", ToolCount: 12},
				{Name: "s3", ToolCount: 8},
			},
			sortState:   sortState{Column: "tool_count", Descending: true},
			expectedIdx: []int{1, 2, 0}, // 12, 8, 5
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			model := &model{
				client:    &mockClient{},
				ctx:       ctx,
				sortState: tt.sortState,
				servers:   make([]serverInfo, len(tt.servers)),
			}
			copy(model.servers, tt.servers)

			model.sortServers()

			for i, expIdx := range tt.expectedIdx {
				if model.servers[i].Name != tt.servers[expIdx].Name {
					t.Errorf("At index %d: got %s, want %s", i, model.servers[i].Name, tt.servers[expIdx].Name)
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

// TestSortIndicator tests the sort direction indicator
func TestSortIndicator(t *testing.T) {
	tests := []struct {
		descending bool
		expected   string
	}{
		{true, "▼"},
		{false, "▲"},
	}

	for _, tt := range tests {
		if result := sortIndicator(tt.descending); result != tt.expected {
			t.Errorf("sortIndicator(%v) = %s, want %s", tt.descending, result, tt.expected)
		}
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

// Benchmark sort performance
func BenchmarkSortActivities(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	model := &model{
		client: &mockClient{},
		ctx:    ctx,
		sortState: sortState{
			Column:     "timestamp",
			Descending: false,
			Secondary:  "id",
		},
		activities: activities,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		act := make([]activityInfo, len(model.activities))
		copy(act, model.activities)
		model.activities = act
		model.sortActivities()
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

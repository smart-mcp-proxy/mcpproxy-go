package tui

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRenderView(t *testing.T) {
	client := &MockClient{}
	m := NewModel(context.Background(), client, 5*time.Second)
	m.width = 80
	m.height = 24

	view := m.View()
	assert.NotEmpty(t, view)
	assert.Contains(t, view, "MCPProxy TUI")
}

func TestRenderTabs(t *testing.T) {
	tests := []struct {
		name     string
		active   tab
		wantServers bool
		wantActivity bool
	}{
		{
			name:     "Servers tab active",
			active:   tabServers,
			wantServers: true,
		},
		{
			name:     "Activity tab active",
			active:   tabActivity,
			wantActivity: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderTabs(tt.active)
			assert.NotEmpty(t, result)
			assert.Contains(t, result, "Servers")
			assert.Contains(t, result, "Activity")
		})
	}
}

func TestRenderServers(t *testing.T) {
	tests := []struct {
		name         string
		servers      []serverInfo
		cursor       int
		maxHeight    int
		wantRows     int
		wantSelected bool
	}{
		{
			name:      "empty servers",
			servers:   []serverInfo{},
			maxHeight: 10,
			wantRows:  0,
		},
		{
			name: "single server healthy",
			servers: []serverInfo{
				{
					Name:          "github",
					HealthLevel:   "healthy",
					HealthSummary: "Connected (12 tools)",
					ToolCount:     12,
				},
			},
			cursor:        0,
			maxHeight:     10,
			wantRows:      1,
			wantSelected:  true,
		},
		{
			name: "multiple servers with different health",
			servers: []serverInfo{
				{
					Name:          "github",
					HealthLevel:   "healthy",
					HealthSummary: "Connected (12 tools)",
					ToolCount:     12,
				},
				{
					Name:          "pagerduty",
					HealthLevel:   "degraded",
					HealthSummary: "Token expiring in 2h",
					ToolCount:     5,
				},
				{
					Name:          "broken-api",
					HealthLevel:   "unhealthy",
					HealthSummary: "Connection failed",
					ToolCount:     0,
				},
			},
			cursor:        1,
			maxHeight:     10,
			wantRows:      3,
			wantSelected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(context.Background(), client, 5*time.Second)
			m.servers = tt.servers
			m.cursor = tt.cursor
			m.width = 120

			result := renderServers(m, tt.maxHeight)
			assert.NotEmpty(t, result)

			if len(tt.servers) == 0 {
				// Empty case shows message
				assert.Contains(t, result, "No servers")
			} else {
				// Should contain server names
				for _, s := range tt.servers {
					assert.Contains(t, result, s.Name)
				}

				// Should contain health indicators
				assert.Contains(t, result, "●") // healthy
				if len(tt.servers) > 1 {
					assert.Contains(t, result, "◐") // degraded
				}
			}
		})
	}
}

func TestRenderActivity(t *testing.T) {
	tests := []struct {
		name      string
		activities []activityInfo
		cursor     int
		maxHeight int
		wantRows  int
	}{
		{
			name:       "empty activity",
			activities: []activityInfo{},
			maxHeight:  10,
			wantRows:   0,
		},
		{
			name: "single activity",
			activities: []activityInfo{
				{
					ID:         "act-123",
					Type:       "tool_call",
					ServerName: "github",
					ToolName:   "list_repositories",
					Status:     "success",
					Timestamp:  "2026-02-09T15:00:00Z",
					DurationMs: "145ms",
				},
			},
			cursor:       0,
			maxHeight:    10,
			wantRows:     1,
		},
		{
			name: "multiple activities with different status",
			activities: []activityInfo{
				{
					Type:       "tool_call",
					ServerName: "github",
					ToolName:   "get_user",
					Status:     "success",
					DurationMs: "50ms",
				},
				{
					Type:       "tool_call",
					ServerName: "stripe",
					ToolName:   "create_invoice",
					Status:     "error",
					DurationMs: "200ms",
				},
				{
					Type:       "policy_decision",
					ServerName: "mcpproxy",
					ToolName:   "quarantine_check",
					Status:     "blocked",
					DurationMs: "10ms",
				},
			},
			cursor:       2,
			maxHeight:    10,
			wantRows:     3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(context.Background(), client, 5*time.Second)
			m.activities = tt.activities
			m.cursor = tt.cursor
			m.width = 120

			result := renderActivity(m, tt.maxHeight)
			assert.NotEmpty(t, result)

			if len(tt.activities) > 0 {
				for _, a := range tt.activities {
					// Type might be truncated, check first few chars
					assert.Contains(t, result, a.Type[:min(len(a.Type), 3)])
					assert.Contains(t, result, a.ServerName)
				}
			}
		})
	}
}

func TestFormatTokenExpiry(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt string
		want      string
	}{
		{
			name:      "empty expiry",
			expiresAt: "",
			want:      "-",
		},
		{
			name:      "expired token",
			expiresAt: "2026-01-01T00:00:00Z",
			want:      "EXPIRED",
		},
		{
			name:      "token expiring soon",
			expiresAt: time.Now().Add(90 * time.Minute).Format(time.RFC3339),
			want:      "1h",
		},
		{
			name:      "token expires in 10+ hours",
			expiresAt: time.Now().Add(10*time.Hour + 30*time.Minute).Format(time.RFC3339),
			want:      "10h",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatTokenExpiry(tt.expiresAt)
			switch tt.want {
			case "EXPIRED":
				assert.Contains(t, result, "EXPIRED")
			case "-":
				assert.Equal(t, tt.want, result)
			default:
				// Duration result should contain the expected hour prefix
				assert.NotEmpty(t, result)
				assert.Contains(t, result, tt.want,
					"expected duration hint %q in formatted output %q", tt.want, result)
			}
		})
	}
}

func TestFormatTimestamp(t *testing.T) {
	tests := []struct {
		name      string
		timestamp string
		valid     bool
	}{
		{
			name:      "valid RFC3339",
			timestamp: "2026-02-09T15:30:45Z",
			valid:     true,
		},
		{
			name:      "valid RFC3339Nano",
			timestamp: "2026-02-09T15:30:45.123456789Z",
			valid:     true,
		},
		{
			name:      "invalid timestamp",
			timestamp: "invalid",
			valid:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatTimestamp(tt.timestamp)
			assert.NotEmpty(t, result)
			if tt.valid {
				// Should be formatted as HH:MM:SS
				assert.Regexp(t, `\d{2}:\d{2}:\d{2}`, result)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{
			name:     "less than minute",
			duration: 30 * time.Second,
			want:     "30s",
		},
		{
			name:     "less than hour",
			duration: 5 * time.Minute,
			want:     "5m",
		},
		{
			name:     "less than day",
			duration: 2 * time.Hour,
			want:     "2h0m",
		},
		{
			name:     "multiple days",
			duration: 3 * 24 * time.Hour,
			want:     "3d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.duration)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestHealthIndicator(t *testing.T) {
	tests := []struct {
		name  string
		level string
		want  string
	}{
		{name: "healthy", level: "healthy", want: "●"},
		{name: "degraded", level: "degraded", want: "◐"},
		{name: "unhealthy", level: "unhealthy", want: "○"},
		{name: "unknown", level: "unknown", want: "○"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just verify it returns a non-empty string with the indicator
			result := healthIndicator(tt.level)
			assert.NotEmpty(t, result)
			// The actual character might be wrapped in color codes
			assert.Contains(t, result, tt.want)
		})
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		max      int
		want     string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"needs truncation", "hello world", 8, "hello..."},
		{"unicode safe", "日本語サーバー", 5, "日本..."},
		{"emoji safe", "🔧🔨🔩🔪🔫", 4, "🔧..."},
		{"very small max", "hello", 3, "hel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateString(tt.input, tt.max)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestRenderHelpers(t *testing.T) {
	t.Run("RenderTitle", func(t *testing.T) {
		result := RenderTitle("Test Title")
		assert.Contains(t, result, "Test Title")
		assert.NotEmpty(t, result)
	})

	t.Run("RenderError with error", func(t *testing.T) {
		result := RenderError(fmt.Errorf("something failed"))
		assert.Contains(t, result, "something failed")
		assert.Contains(t, result, "Error:")
	})

	t.Run("RenderError with nil", func(t *testing.T) {
		result := RenderError(nil)
		assert.Empty(t, result)
	})

	t.Run("RenderHelp", func(t *testing.T) {
		result := RenderHelp("q: quit  r: refresh")
		assert.Contains(t, result, "q: quit")
		assert.Contains(t, result, "r: refresh")
	})
}

func TestRenderFilterSummary(t *testing.T) {
	tests := []struct {
		name       string
		filterState filterState
		wantEmpty  bool
		wantBadges []string
	}{
		{
			name:        "No active filters",
			filterState: filterState{},
			wantEmpty:   true,
		},
		{
			name:        "Single filter",
			filterState: filterState{"status": "error"},
			wantEmpty:   false,
			wantBadges:  []string{"[Status: error ✕]", "[Clear]"},
		},
		{
			name:        "Multiple filters",
			filterState: filterState{"status": "error", "server": "glean"},
			wantEmpty:   false,
			wantBadges:  []string{"[Status: error ✕]", "[Server: glean ✕]", "[Clear]"},
		},
		{
			name:        "Filter with empty value",
			filterState: filterState{"status": ""},
			wantEmpty:   true,
		},
		{
			name:        "Non-string filter value",
			filterState: filterState{"count": 5},
			wantEmpty:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model{filterState: tt.filterState}
			result := renderFilterSummary(m)

			if tt.wantEmpty {
				assert.Empty(t, result)
			} else {
				assert.NotEmpty(t, result)
				assert.Contains(t, result, "Filter:")
				for _, badge := range tt.wantBadges {
					assert.Contains(t, result, badge)
				}
			}
		})
	}
}

func TestGetSortMark(t *testing.T) {
	tests := []struct {
		name      string
		descending bool
		want      string
	}{
		{
			name:       "Descending sort",
			descending: true,
			want:       "▼",
		},
		{
			name:       "Ascending sort",
			descending: false,
			want:       "▲",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getSortMark(tt.descending)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestRenderHelp(t *testing.T) {
	tests := []struct {
		name    string
		uiMode  UIMode
		activeTab tab
		wantText []string
	}{
		{
			name:     "Normal mode help - servers tab",
			uiMode:   ModeNormal,
			activeTab: tabServers,
			wantText: []string{"j/k", "f", "s", "q", "enable", "disable", "restart"},
		},
		{
			name:     "Normal mode help - activity tab",
			uiMode:   ModeNormal,
			activeTab: tabActivity,
			wantText: []string{"j/k", "f", "s", "q", "quit"},
		},
		{
			name:      "Filter mode help",
			uiMode:    ModeFilterEdit,
			wantText: []string{"tab", "↑/↓", "esc: apply"},
		},
		{
			name:     "Sort mode help",
			uiMode:   ModeSortSelect,
			wantText: []string{"SORT MODE", "esc: cancel"},
		},
		{
			name:      "Default mode (not ModeHelp)",
			uiMode:    ModeSearch,
			wantText: []string{"quit", "tab"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model{uiMode: tt.uiMode, activeTab: tt.activeTab}
			result := renderHelp(m)
			assert.NotEmpty(t, result)
			for _, text := range tt.wantText {
				assert.Contains(t, result, text, "help text should contain %s", text)
			}
		})
	}
}

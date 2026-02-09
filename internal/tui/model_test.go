package tui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockClient mocks the Client interface for testing
type MockClient struct {
	servers    []map[string]interface{}
	activities []map[string]interface{}
	err        error
}

func (m *MockClient) GetServers(ctx context.Context) ([]map[string]interface{}, error) {
	return m.servers, m.err
}

func (m *MockClient) ListActivities(ctx context.Context, filter interface{}) ([]map[string]interface{}, int, error) {
	return m.activities, len(m.activities), m.err
}

func (m *MockClient) ServerAction(ctx context.Context, name, action string) error {
	return m.err
}

func (m *MockClient) TriggerOAuthLogin(ctx context.Context, name string) error {
	return m.err
}

func TestModelInit(t *testing.T) {
	client := &MockClient{}
	m := NewModel(client, 5*time.Second)

	cmd := m.Init()
	assert.NotNil(t, cmd)
}

func TestModelKeyboardHandling(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		activeTab    tab
		cursor       int
		servers      []serverInfo
		expectTab    tab
		expectCursor int
	}{
		{
			name:      "navigate to Servers tab with 1",
			key:       "1",
			activeTab: tabActivity,
			expectTab: tabServers,
		},
		{
			name:      "navigate to Activity tab with 2",
			key:       "2",
			activeTab: tabServers,
			expectTab: tabActivity,
		},
		{
			name:         "cursor j (down)",
			key:          "j",
			activeTab:    tabServers,
			cursor:       0,
			servers:      []serverInfo{{Name: "srv1"}, {Name: "srv2"}},
			expectCursor: 1,
		},
		{
			name:         "cursor k (up)",
			key:          "k",
			activeTab:    tabServers,
			cursor:       1,
			servers:      []serverInfo{{Name: "srv1"}, {Name: "srv2"}},
			expectCursor: 0,
		},
		{
			name:         "cursor down at end (no-op)",
			key:          "j",
			activeTab:    tabServers,
			cursor:       1,
			servers:      []serverInfo{{Name: "srv1"}, {Name: "srv2"}},
			expectCursor: 1,
		},
		{
			name:         "cursor up at start (no-op)",
			key:          "k",
			activeTab:    tabServers,
			cursor:       0,
			servers:      []serverInfo{{Name: "srv1"}},
			expectCursor: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(client, 5*time.Second)
			m.activeTab = tt.activeTab
			m.cursor = tt.cursor
			m.servers = tt.servers

			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(tt.key[0])}}

			result, _ := m.Update(msg)
			resultModel := result.(model)

			assert.Equal(t, tt.expectTab, resultModel.activeTab, "tab mismatch")
			assert.Equal(t, tt.expectCursor, resultModel.cursor, "cursor mismatch")
		})
	}
}

func TestModelDataFetching(t *testing.T) {
	tests := []struct {
		name     string
		servers  []map[string]interface{}
		wantName string
		wantLevel string
		wantTools int
	}{
		{
			name: "parse healthy server",
			servers: []map[string]interface{}{
				{
					"name": "github",
					"tool_count": 12.0,
					"health": map[string]interface{}{
						"level":      "healthy",
						"summary":    "Connected (12 tools)",
						"admin_state": "enabled",
					},
				},
			},
			wantName:  "github",
			wantLevel: "healthy",
			wantTools: 12,
		},
		{
			name: "parse degraded server",
			servers: []map[string]interface{}{
				{
					"name": "github-api",
					"tool_count": 5.0,
					"health": map[string]interface{}{
						"level":      "degraded",
						"summary":    "Token expiring in 2h",
						"admin_state": "enabled",
						"action":     "login",
					},
					"oauth_status": "expiring",
					"token_expires_at": "2026-02-10T15:00:00Z",
				},
			},
			wantName:  "github-api",
			wantLevel: "degraded",
			wantTools: 5,
		},
		{
			name: "parse unhealthy server",
			servers: []map[string]interface{}{
				{
					"name": "broken-server",
					"tool_count": 0.0,
					"health": map[string]interface{}{
						"level":      "unhealthy",
						"summary":    "Connection failed",
						"admin_state": "enabled",
					},
					"last_error": "failed to connect",
				},
			},
			wantName:  "broken-server",
			wantLevel: "unhealthy",
			wantTools: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{servers: tt.servers}
			m := NewModel(client, 5*time.Second)

			cmd := fetchServers(client, m.ctx)
			assert.NotNil(t, cmd)

			msg := cmd()
			assert.NotNil(t, msg)

			result, _ := m.Update(msg)
			resultModel := result.(model)

			require.Len(t, resultModel.servers, len(tt.servers))
			s := resultModel.servers[0]
			assert.Equal(t, tt.wantName, s.Name)
			assert.Equal(t, tt.wantLevel, s.HealthLevel)
			assert.Equal(t, tt.wantTools, s.ToolCount)
		})
	}
}

func TestModelMaxIndex(t *testing.T) {
	tests := []struct {
		name     string
		servers  []serverInfo
		activity []activityInfo
		tab      tab
		want     int
	}{
		{
			name:    "empty servers",
			servers: []serverInfo{},
			tab:     tabServers,
			want:    0,
		},
		{
			name:    "5 servers",
			servers: make([]serverInfo, 5),
			tab:     tabServers,
			want:    4,
		},
		{
			name:     "empty activity",
			activity: []activityInfo{},
			tab:      tabActivity,
			want:     0,
		},
		{
			name:     "3 activities",
			activity: make([]activityInfo, 3),
			tab:      tabActivity,
			want:     2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(client, 5*time.Second)
			m.servers = tt.servers
			m.activities = tt.activity
			m.activeTab = tt.tab

			got := m.maxIndex()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWindowResize(t *testing.T) {
	client := &MockClient{}
	m := NewModel(client, 5*time.Second)
	assert.Equal(t, 0, m.width)
	assert.Equal(t, 0, m.height)

	msg := tea.WindowSizeMsg{Width: 120, Height: 40}
	result, _ := m.Update(msg)
	resultModel := result.(model)

	assert.Equal(t, 120, resultModel.width)
	assert.Equal(t, 40, resultModel.height)
}

func TestErrorHandling(t *testing.T) {
	client := &MockClient{}
	m := NewModel(client, 5*time.Second)
	assert.Nil(t, m.err)

	msg := errMsg{err: ErrConnectionFailed}
	result, _ := m.Update(msg)
	resultModel := result.(model)

	assert.NotNil(t, resultModel.err)
	assert.Equal(t, ErrConnectionFailed, resultModel.err)
}

// Test error constants for consistency
var (
	ErrConnectionFailed = assert.AnError
)

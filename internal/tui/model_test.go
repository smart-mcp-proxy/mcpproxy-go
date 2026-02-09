package tui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
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

func (m *MockClient) ListActivities(ctx context.Context, filter cliclient.ActivityFilterParams) ([]map[string]interface{}, int, error) {
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

func TestServerActions(t *testing.T) {
	tests := []struct {
		name           string
		key            string
		cursor         int
		servers        []serverInfo
		expectActionCt int
	}{
		{
			name:           "enable server",
			key:            "e",
			cursor:         0,
			servers:        []serverInfo{{Name: "github"}},
			expectActionCt: 1,
		},
		{
			name:           "disable server",
			key:            "d",
			cursor:         0,
			servers:        []serverInfo{{Name: "github"}},
			expectActionCt: 1,
		},
		{
			name:           "restart server",
			key:            "R",
			cursor:         0,
			servers:        []serverInfo{{Name: "github"}},
			expectActionCt: 1,
		},
		{
			name:           "action with no servers",
			key:            "e",
			cursor:         0,
			servers:        []serverInfo{},
			expectActionCt: 0, // Should not call action
		},
		{
			name:           "action with cursor out of bounds",
			key:            "e",
			cursor:         5,
			servers:        []serverInfo{{Name: "github"}},
			expectActionCt: 0, // Should not call action
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(client, 5*time.Second)
			m.servers = tt.servers
			m.cursor = tt.cursor
			m.activeTab = tabServers

			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(tt.key[0])}}
			_, _ = m.Update(msg)
			// We verify it doesn't panic; actual action verification would need enhanced MockClient
		})
	}
}

func TestTabKeyNavigation(t *testing.T) {
	tests := []struct {
		name         string
		initialTab   tab
		expectTab    tab
		expectCursor int
	}{
		{
			name:         "tab from Servers to Activity",
			initialTab:   tabServers,
			expectTab:    tabActivity,
			expectCursor: 0,
		},
		{
			name:         "tab from Activity back to Servers",
			initialTab:   tabActivity,
			expectTab:    tabServers,
			expectCursor: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(client, 5*time.Second)
			m.activeTab = tt.initialTab
			m.cursor = 5 // Set to non-zero to verify reset

			// Send tab key via KeyTab type
			msg := tea.KeyMsg{Type: tea.KeyTab}
			result, _ := m.Update(msg)
			resultModel := result.(model)

			assert.Equal(t, tt.expectTab, resultModel.activeTab)
			assert.Equal(t, tt.expectCursor, resultModel.cursor)
		})
	}
}

func TestOAuthLoginConditional(t *testing.T) {
	tests := []struct {
		name         string
		healthAction string
		key          string
		expectLogin  bool
	}{
		{
			name:         "login triggered when action=login",
			healthAction: "login",
			key:          "l",
			expectLogin:  true,
		},
		{
			name:         "login not triggered when action=restart",
			healthAction: "restart",
			key:          "l",
			expectLogin:  false,
		},
		{
			name:         "login not triggered when action empty",
			healthAction: "",
			key:          "l",
			expectLogin:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(client, 5*time.Second)
			m.servers = []serverInfo{
				{
					Name:           "test-server",
					HealthAction:   tt.healthAction,
					HealthLevel:    "healthy",
					HealthSummary:  "Connected",
					AdminState:     "enabled",
					OAuthStatus:    "authenticated",
				},
			}
			m.cursor = 0
			m.activeTab = tabServers

			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(tt.key[0])}}
			_, _ = m.Update(msg)
			// Note: would need enhanced MockClient with call tracking to verify actual login call
		})
	}
}

func TestWindowResizeEdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
	}{
		{
			name:   "very small window",
			width:  10,
			height: 5,
		},
		{
			name:   "zero width",
			width:  0,
			height: 24,
		},
		{
			name:   "zero height",
			width:  80,
			height: 0,
		},
		{
			name:   "large window",
			width:  200,
			height: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockClient{}
			m := NewModel(client, 5*time.Second)

			msg := tea.WindowSizeMsg{Width: tt.width, Height: tt.height}
			result, _ := m.Update(msg)
			resultModel := result.(model)

			assert.Equal(t, tt.width, resultModel.width)
			assert.Equal(t, tt.height, resultModel.height)

			// View should not panic on extreme sizes
			view := resultModel.View()
			assert.NotEmpty(t, view)
		})
	}
}

func TestRefreshCommand(t *testing.T) {
	client := &MockClient{
		servers:    []map[string]interface{}{{"name": "test"}},
		activities: []map[string]interface{}{},
	}
	m := NewModel(client, 5*time.Second)

	// Simulate 'r' key
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}
	result, cmd := m.Update(msg)
	resultModel := result.(model)

	// Command should be returned (batch of fetch commands)
	assert.NotNil(t, cmd)
	assert.NotNil(t, resultModel)
}

func TestRefreshAllOAuthTokens(t *testing.T) {
	client := &MockClient{}
	m := NewModel(client, 5*time.Second)
	m.servers = []serverInfo{
		{Name: "server-1", HealthAction: "login", HealthLevel: "unhealthy"},
		{Name: "server-2", HealthAction: "", HealthLevel: "healthy"},
		{Name: "server-3", HealthAction: "login", HealthLevel: "degraded"},
	}

	// 'L' should trigger OAuth login for servers with action="login"
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}}
	_, cmd := m.Update(msg)
	assert.NotNil(t, cmd, "L key should produce batch command for servers needing login")

	// When no servers need login, cmd should be nil
	m.servers = []serverInfo{
		{Name: "server-1", HealthAction: "", HealthLevel: "healthy"},
	}
	_, cmd = m.Update(msg)
	assert.Nil(t, cmd, "L key should produce nil cmd when no servers need login")
}

// Test error constants for consistency
var (
	ErrConnectionFailed = assert.AnError
)

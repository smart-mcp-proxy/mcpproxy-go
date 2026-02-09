package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
)

// tab represents TUI tabs
type tab int

const (
	tabServers tab = iota
	tabActivity
)

// serverInfo holds parsed server data for display
type serverInfo struct {
	Name           string
	HealthLevel    string
	HealthSummary  string
	HealthAction   string
	AdminState     string
	ToolCount      int
	OAuthStatus    string
	TokenExpiresAt string
	LastError      string
}

// activityInfo holds parsed activity data for display
type activityInfo struct {
	ID         string
	Type       string
	ServerName string
	ToolName   string
	Status     string
	Timestamp  string
	DurationMs string
}

// Client defines the interface for API operations
type Client interface {
	GetServers(ctx context.Context) ([]map[string]interface{}, error)
	ListActivities(ctx context.Context, filter cliclient.ActivityFilterParams) ([]map[string]interface{}, int, error)
	ServerAction(ctx context.Context, name, action string) error
	TriggerOAuthLogin(ctx context.Context, name string) error
}

// model is the main Bubble Tea model
type model struct {
	client Client
	ctx    context.Context

	// UI state
	activeTab tab
	cursor    int
	width     int
	height    int

	// Data
	servers    []serverInfo
	activities []activityInfo
	lastUpdate time.Time
	err        error

	// Refresh
	refreshInterval time.Duration
}

// Messages

type serversMsg struct {
	servers []serverInfo
}

type activitiesMsg struct {
	activities []activityInfo
}

type errMsg struct {
	err error
}

type tickMsg time.Time

// Commands

func fetchServers(client Client, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		rawServers, err := client.GetServers(ctx)
		if err != nil {
			return errMsg{err}
		}

		servers := make([]serverInfo, 0, len(rawServers))
		for _, raw := range rawServers {
			s := serverInfo{
				Name: strVal(raw, "name"),
			}

			if health, ok := raw["health"].(map[string]interface{}); ok {
				s.HealthLevel = strVal(health, "level")
				s.HealthSummary = strVal(health, "summary")
				s.HealthAction = strVal(health, "action")
				s.AdminState = strVal(health, "admin_state")
			}

			if tc, ok := raw["tool_count"].(float64); ok {
				s.ToolCount = int(tc)
			}

			s.OAuthStatus = strVal(raw, "oauth_status")
			s.TokenExpiresAt = strVal(raw, "token_expires_at")
			s.LastError = strVal(raw, "last_error")

			servers = append(servers, s)
		}

		return serversMsg{servers}
	}
}

func fetchActivities(client Client, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		rawActivities, _, err := client.ListActivities(ctx, nil)
		if err != nil {
			return errMsg{err}
		}

		activities := make([]activityInfo, 0, len(rawActivities))
		for _, raw := range rawActivities {
			a := activityInfo{
				ID:         strVal(raw, "id"),
				Type:       strVal(raw, "type"),
				ServerName: strVal(raw, "server_name"),
				ToolName:   strVal(raw, "tool_name"),
				Status:     strVal(raw, "status"),
				Timestamp:  strVal(raw, "timestamp"),
			}
			if dur, ok := raw["duration_ms"].(float64); ok {
				a.DurationMs = fmt.Sprintf("%.0fms", dur)
			}
			activities = append(activities, a)
		}

		return activitiesMsg{activities}
	}
}

func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func strVal(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// NewModel creates a new TUI model
func NewModel(client Client, refreshInterval time.Duration) model {
	return model{
		client:          client,
		ctx:             context.Background(),
		activeTab:       tabServers,
		refreshInterval: refreshInterval,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		fetchServers(m.client, m.ctx),
		fetchActivities(m.client, m.ctx),
		tickCmd(m.refreshInterval),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case serversMsg:
		m.servers = msg.servers
		m.lastUpdate = time.Now()
		m.err = nil
		return m, nil

	case activitiesMsg:
		m.activities = msg.activities
		m.lastUpdate = time.Now()
		m.err = nil
		return m, nil

	case errMsg:
		m.err = msg.err
		return m, nil

	case tickMsg:
		return m, tea.Batch(
			fetchServers(m.client, m.ctx),
			fetchActivities(m.client, m.ctx),
			tickCmd(m.refreshInterval),
		)
	}

	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "tab":
		if m.activeTab == tabServers {
			m.activeTab = tabActivity
		} else {
			m.activeTab = tabServers
		}
		m.cursor = 0
		return m, nil

	case "1":
		m.activeTab = tabServers
		m.cursor = 0
		return m, nil

	case "2":
		m.activeTab = tabActivity
		m.cursor = 0
		return m, nil

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		maxIdx := m.maxIndex()
		if m.cursor < maxIdx {
			m.cursor++
		}
		return m, nil

	case "r":
		return m, tea.Batch(
			fetchServers(m.client, m.ctx),
			fetchActivities(m.client, m.ctx),
		)

	case "e":
		if m.activeTab == tabServers && m.cursor < len(m.servers) {
			name := m.servers[m.cursor].Name
			return m, func() tea.Msg {
				_ = m.client.ServerAction(m.ctx, name, "enable")
				return tickMsg(time.Now())
			}
		}

	case "d":
		if m.activeTab == tabServers && m.cursor < len(m.servers) {
			name := m.servers[m.cursor].Name
			return m, func() tea.Msg {
				_ = m.client.ServerAction(m.ctx, name, "disable")
				return tickMsg(time.Now())
			}
		}

	case "R":
		if m.activeTab == tabServers && m.cursor < len(m.servers) {
			name := m.servers[m.cursor].Name
			return m, func() tea.Msg {
				_ = m.client.ServerAction(m.ctx, name, "restart")
				return tickMsg(time.Now())
			}
		}

	case "l":
		if m.activeTab == tabServers && m.cursor < len(m.servers) {
			s := m.servers[m.cursor]
			if s.HealthAction == "login" {
				return m, func() tea.Msg {
					_ = m.client.TriggerOAuthLogin(m.ctx, s.Name)
					return tickMsg(time.Now())
				}
			}
		}

	case "L":
		// Refresh all OAuth tokens: trigger login for every server needing it
		var cmds []tea.Cmd
		for _, s := range m.servers {
			if s.HealthAction == "login" {
				name := s.Name
				cmds = append(cmds, func() tea.Msg {
					_ = m.client.TriggerOAuthLogin(m.ctx, name)
					return nil
				})
			}
		}
		if len(cmds) > 0 {
			cmds = append(cmds, func() tea.Msg { return tickMsg(time.Now()) })
			return m, tea.Batch(cmds...)
		}
	}

	return m, nil
}

func (m model) maxIndex() int {
	switch m.activeTab {
	case tabServers:
		if len(m.servers) == 0 {
			return 0
		}
		return len(m.servers) - 1
	case tabActivity:
		if len(m.activities) == 0 {
			return 0
		}
		return len(m.activities) - 1
	}
	return 0
}

func (m model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	return renderView(m)
}

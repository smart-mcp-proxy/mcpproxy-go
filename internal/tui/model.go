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

// UIMode represents the current interaction mode
type UIMode string

const (
	ModeNormal     UIMode = "normal"       // Navigate table
	ModeFilterEdit UIMode = "filter_edit"  // Edit filters
	ModeSortSelect UIMode = "sort_select"  // Choose sort column
	ModeSearch     UIMode = "search"       // Text search
	ModeHelp       UIMode = "help"         // Show keybindings
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
	uiMode    UIMode

	// Data
	servers    []serverInfo
	activities []activityInfo
	lastUpdate time.Time
	err        error

	// Sorting
	sortState sortState

	// Filtering
	filterState   filterState
	focusedFilter string // Which filter is currently being edited
	filterQuery   string // Temporary text input for filters

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

func serverActionCmd(client Client, ctx context.Context, name, action string) tea.Cmd {
	return func() tea.Msg {
		if err := client.ServerAction(ctx, name, action); err != nil {
			return errMsg{fmt.Errorf("%s %s: %w", action, name, err)}
		}
		return tickMsg(time.Now())
	}
}

func oauthLoginCmd(client Client, ctx context.Context, name string) tea.Cmd {
	return func() tea.Msg {
		if err := client.TriggerOAuthLogin(ctx, name); err != nil {
			return errMsg{fmt.Errorf("login %s: %w", name, err)}
		}
		return tickMsg(time.Now())
	}
}

// triggerOAuthRefresh triggers OAuth refresh for all servers
func (m model) triggerOAuthRefresh() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()

		// Trigger OAuth login for all servers needing auth
		err := m.client.TriggerOAuthLogin(ctx, "")
		if err != nil {
			return errMsg{fmt.Errorf("oauth refresh failed: %w", err)}
		}

		// Refresh data after OAuth completes
		return tickMsg(time.Now())
	}
}

func strVal(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// NewModel creates a new TUI model. The context controls the lifetime of all
// API calls; cancel it to cleanly abort in-flight requests on shutdown.
func NewModel(ctx context.Context, client Client, refreshInterval time.Duration) model {
	return model{
		client:          client,
		ctx:             ctx,
		activeTab:       tabServers,
		refreshInterval: refreshInterval,
		uiMode:          ModeNormal,
		sortState:       newServerSortState(),
		filterState:     newFilterState(),
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
		if m.activeTab == tabServers && len(m.servers) > 0 && m.cursor >= len(m.servers) {
			m.cursor = len(m.servers) - 1
		}
		return m, nil

	case activitiesMsg:
		m.activities = msg.activities
		m.lastUpdate = time.Now()
		m.err = nil
		if m.activeTab == tabActivity && len(m.activities) > 0 && m.cursor >= len(m.activities) {
			m.cursor = len(m.activities) - 1
		}
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
	key := msg.String()

	// Global shortcuts work in all modes
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "o", "O":
		return m, m.triggerOAuthRefresh()

	case "1":
		m.activeTab = tabServers
		m.cursor = 0
		m.uiMode = ModeNormal
		m.sortState = newServerSortState()
		return m, nil

	case "2":
		m.activeTab = tabActivity
		m.cursor = 0
		m.uiMode = ModeNormal
		m.sortState = newActivitySortState()
		return m, nil

	case "?":
		m.uiMode = ModeHelp
		return m, nil

	case "space":
		// Manual refresh
		return m, tea.Batch(
			fetchServers(m.client, m.ctx),
			fetchActivities(m.client, m.ctx),
		)
	}

	// Mode-specific handling
	switch m.uiMode {
	case ModeNormal:
		return m.handleKeyNormal(key)
	case ModeFilterEdit:
		m, cmd := m.handleFilterMode(key)
		return m, cmd
	case ModeSortSelect:
		m, cmd := m.handleSortMode(key)
		return m, cmd
	case ModeSearch:
		m, cmd := m.handleSearchMode(key)
		return m, cmd
	case ModeHelp:
		m, cmd := m.handleHelpMode(key)
		return m, cmd
	}

	return m, nil
}

// handleKeyNormal handles normal mode navigation and actions
func (m model) handleKeyNormal(key string) (tea.Model, tea.Cmd) {
	// Tab switching
	if key == "tab" {
		if m.activeTab == tabServers {
			m.activeTab = tabActivity
		} else {
			m.activeTab = tabServers
		}
		m.cursor = 0
		return m, nil
	}

	// Navigation
	switch key {
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
			return m, serverActionCmd(m.client, m.ctx, name, "enable")
		}

	case "d":
		if m.activeTab == tabServers && m.cursor < len(m.servers) {
			name := m.servers[m.cursor].Name
			return m, serverActionCmd(m.client, m.ctx, name, "disable")
		}

	case "R":
		if m.activeTab == tabServers && m.cursor < len(m.servers) {
			name := m.servers[m.cursor].Name
			return m, serverActionCmd(m.client, m.ctx, name, "restart")
		}

	case "l":
		if m.activeTab == tabServers && m.cursor < len(m.servers) {
			s := m.servers[m.cursor]
			if s.HealthAction == "login" {
				return m, oauthLoginCmd(m.client, m.ctx, s.Name)
			}
		}

	case "L":
		// Refresh all OAuth tokens: trigger login for every server needing it
		var cmds []tea.Cmd
		for _, s := range m.servers {
			if s.HealthAction == "login" {
				cmds = append(cmds, oauthLoginCmd(m.client, m.ctx, s.Name))
			}
		}
		if len(cmds) > 0 {
			cmds = append(cmds, func() tea.Msg { return tickMsg(time.Now()) })
			return m, tea.Batch(cmds...)
		}
	}

	// Delegate to mode-specific handler for extended features (sort, filter, etc)
	m, cmd := m.handleNormalMode(key)
	return m, cmd
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

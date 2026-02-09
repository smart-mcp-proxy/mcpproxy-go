package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func renderView(m model) string {
	var b strings.Builder

	// Title bar
	title := titleStyle.Render(" MCPProxy TUI ")
	b.WriteString(title)
	b.WriteString("\n\n")

	// Tabs
	b.WriteString(renderTabs(m.activeTab))
	b.WriteString("\n\n")

	// Content area (use remaining height minus header/footer)
	contentHeight := m.height - 8 // title + tabs + status + help
	if contentHeight < 5 {
		contentHeight = 5
	}

	switch m.activeTab {
	case tabServers:
		b.WriteString(renderServers(m, contentHeight))
	case tabActivity:
		b.WriteString(renderActivity(m, contentHeight))
	}

	// Error display
	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", m.err)))
		b.WriteString("\n")
	}

	// Status bar
	b.WriteString("\n")
	b.WriteString(renderStatusBar(m))
	b.WriteString("\n")

	// Help
	b.WriteString(renderHelp(m.activeTab))

	return b.String()
}

func renderTabs(active tab) string {
	tabs := []struct {
		label string
		key   string
		t     tab
	}{
		{"Servers", "1", tabServers},
		{"Activity", "2", tabActivity},
	}

	var parts []string
	for _, t := range tabs {
		label := fmt.Sprintf("[%s] %s", t.key, t.label)
		if t.t == active {
			parts = append(parts, tabActiveStyle.Render(label))
		} else {
			parts = append(parts, tabInactiveStyle.Render(label))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func renderServers(m model, maxHeight int) string {
	if len(m.servers) == 0 {
		return mutedStyle.Render("  No servers configured")
	}

	var b strings.Builder

	// Header
	header := fmt.Sprintf("  %-3s %-24s %-10s %-6s %-36s %s",
		"", "NAME", "STATE", "TOOLS", "STATUS", "TOKEN EXPIRES")
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	// Server rows
	visible := maxHeight - 2 // header + spacing
	if visible > len(m.servers) {
		visible = len(m.servers)
	}

	// Scroll offset
	offset := 0
	if m.cursor >= visible {
		offset = m.cursor - visible + 1
	}

	for i := offset; i < offset+visible && i < len(m.servers); i++ {
		s := m.servers[i]

		indicator := healthIndicator(s.HealthLevel)
		name := s.Name
		if len(name) > 24 {
			name = name[:21] + "..."
		}

		state := s.AdminState
		if state == "" {
			state = "enabled"
		}

		tools := fmt.Sprintf("%d", s.ToolCount)

		summary := s.HealthSummary
		if len(summary) > 36 {
			summary = summary[:33] + "..."
		}

		tokenExpiry := formatTokenExpiry(s.TokenExpiresAt)

		row := fmt.Sprintf("  %s %-24s %-10s %-6s %-36s %s",
			indicator, name, state, tools, summary, tokenExpiry)

		if i == m.cursor {
			b.WriteString(selectedStyle.Render(row))
		} else {
			stateStyle := healthStyle(s.HealthLevel)
			// Apply health coloring to summary portion
			prefix := fmt.Sprintf("  %s %-24s %-10s %-6s ", indicator, name, state, tools)
			b.WriteString(normalStyle.Render(prefix))
			b.WriteString(stateStyle.Render(fmt.Sprintf("%-36s", summary)))
			b.WriteString(mutedStyle.Render(fmt.Sprintf(" %s", tokenExpiry)))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func renderActivity(m model, maxHeight int) string {
	if len(m.activities) == 0 {
		return mutedStyle.Render("  No recent activity")
	}

	var b strings.Builder

	// Header
	header := fmt.Sprintf("  %-12s %-16s %-28s %-10s %-10s %s",
		"TYPE", "SERVER", "TOOL", "STATUS", "DURATION", "TIME")
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	visible := maxHeight - 2
	if visible > len(m.activities) {
		visible = len(m.activities)
	}

	offset := 0
	if m.cursor >= visible {
		offset = m.cursor - visible + 1
	}

	for i := offset; i < offset+visible && i < len(m.activities); i++ {
		a := m.activities[i]

		actType := a.Type
		if len(actType) > 12 {
			actType = actType[:9] + "..."
		}

		server := a.ServerName
		if len(server) > 16 {
			server = server[:13] + "..."
		}

		tool := a.ToolName
		if len(tool) > 28 {
			tool = tool[:25] + "..."
		}

		status := a.Status
		duration := a.DurationMs
		ts := formatTimestamp(a.Timestamp)

		row := fmt.Sprintf("  %-12s %-16s %-28s %-10s %-10s %s",
			actType, server, tool, status, duration, ts)

		if i == m.cursor {
			b.WriteString(selectedStyle.Render(row))
		} else {
			var statusStyle lipgloss.Style
			switch a.Status {
			case "success":
				statusStyle = healthyStyle
			case "error":
				statusStyle = unhealthyStyle
			case "blocked":
				statusStyle = degradedStyle
			default:
				statusStyle = normalStyle
			}

			prefix := fmt.Sprintf("  %-12s %-16s %-28s ", actType, server, tool)
			b.WriteString(normalStyle.Render(prefix))
			b.WriteString(statusStyle.Render(fmt.Sprintf("%-10s", status)))
			b.WriteString(mutedStyle.Render(fmt.Sprintf(" %-10s %s", duration, ts)))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func renderStatusBar(m model) string {
	left := fmt.Sprintf(" %d servers", len(m.servers))
	right := ""
	if !m.lastUpdate.IsZero() {
		right = fmt.Sprintf("Updated %s ago ", formatDuration(time.Since(m.lastUpdate)))
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}

	bar := left + strings.Repeat(" ", gap) + right
	return statusBarStyle.Width(m.width).Render(bar)
}

func renderHelp(active tab) string {
	common := "q: quit  tab: switch  r: refresh"
	switch active {
	case tabServers:
		return helpStyle.Render(" " + common + "  e: enable  d: disable  R: restart  l: login")
	case tabActivity:
		return helpStyle.Render(" " + common + "  j/k: navigate")
	}
	return helpStyle.Render(" " + common)
}

func formatTokenExpiry(expiresAt string) string {
	if expiresAt == "" {
		return "-"
	}

	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return expiresAt
	}

	remaining := time.Until(t)
	if remaining <= 0 {
		return unhealthyStyle.Render("EXPIRED")
	}

	formatted := formatDuration(remaining)
	if remaining < 2*time.Hour {
		return degradedStyle.Render(formatted)
	}
	return formatted
}

func formatTimestamp(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			return ts
		}
	}
	return t.Local().Format("15:04:05")
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

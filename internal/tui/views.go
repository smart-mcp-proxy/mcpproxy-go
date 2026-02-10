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
	b.WriteString(RenderTitle(" MCPProxy TUI "))
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
		b.WriteString(RenderError(m.err))
		b.WriteString("\n")
	}

	// Status bar
	b.WriteString("\n")
	b.WriteString(renderStatusBar(m))
	b.WriteString("\n")

	// Help
	b.WriteString(renderHelp(m))

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
		return MutedStyle.Render("  No servers configured")
	}

	var b strings.Builder

	// Filter summary line (if any filters active)
	filterSummary := renderFilterSummary(m)
	if filterSummary != "" {
		b.WriteString(MutedStyle.Render(filterSummary))
		b.WriteString("\n")
	}

	// Sort line (if not default sort)
	if m.sortState.Column != "name" {
		sortMark := "▼"
		if !m.sortState.Descending {
			sortMark = "▲"
		}
		b.WriteString(MutedStyle.Render(fmt.Sprintf("Sort: %s %s", m.sortState.Column, sortMark)))
		b.WriteString("\n")
	}

	// Header with sort indicators
	sortMark := getSortMark(m.sortState.Descending)
	header := fmt.Sprintf("  %-3s %-24s %-10s %-6s %-36s %s",
		"",
		addSortMark("NAME", m.sortState.Column, "name", sortMark),
		addSortMark("STATE", m.sortState.Column, "admin_state", sortMark),
		addSortMark("TOOLS", m.sortState.Column, "tool_count", sortMark),
		addSortMark("STATUS", m.sortState.Column, "health_level", sortMark),
		addSortMark("TOKEN EXPIRES", m.sortState.Column, "token_expires_at", sortMark))
	b.WriteString(HeaderStyle.Render(header))
	b.WriteString("\n")

	// Server rows
	visible := maxHeight - 4 // header + spacing + filter summary (if present)
	if filterSummary != "" {
		visible = maxHeight - 5
	}
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
		name := truncateString(s.Name, 24)

		state := s.AdminState
		if state == "" {
			state = "enabled"
		}

		tools := fmt.Sprintf("%d", s.ToolCount)

		summary := truncateString(s.HealthSummary, 36)

		tokenExpiry := formatTokenExpiry(s.TokenExpiresAt)

		row := fmt.Sprintf("  %s %-24s %-10s %-6s %-36s %s",
			indicator, name, state, tools, summary, tokenExpiry)

		if i == m.cursor {
			b.WriteString(SelectedStyle.Render(row))
		} else {
			stateStyle := healthStyle(s.HealthLevel)
			// Apply health coloring to summary portion
			prefix := fmt.Sprintf("  %s %-24s %-10s %-6s ", indicator, name, state, tools)
			b.WriteString(BaseStyle.Render(prefix))
			b.WriteString(stateStyle.Render(fmt.Sprintf("%-36s", summary)))
			b.WriteString(MutedStyle.Render(fmt.Sprintf(" %s", tokenExpiry)))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func renderActivity(m model, maxHeight int) string {
	if len(m.activities) == 0 {
		return MutedStyle.Render("  No recent activity")
	}

	var b strings.Builder

	// Filter summary line (if any filters active)
	filterSummary := renderFilterSummary(m)
	if filterSummary != "" {
		b.WriteString(MutedStyle.Render(filterSummary))
		b.WriteString("\n")
	}

	// Sort line (if not default sort)
	if m.sortState.Column != "timestamp" {
		sortMark := "▼"
		if !m.sortState.Descending {
			sortMark = "▲"
		}
		b.WriteString(MutedStyle.Render(fmt.Sprintf("Sort: %s %s", m.sortState.Column, sortMark)))
		b.WriteString("\n")
	}

	// Header with sort indicators
	sortMark := getSortMark(m.sortState.Descending)
	header := fmt.Sprintf("  %-12s %-16s %-28s %-10s %-10s %s",
		addSortMark("TYPE", m.sortState.Column, "type", sortMark),
		addSortMark("SERVER", m.sortState.Column, "server_name", sortMark),
		addSortMark("TOOL", m.sortState.Column, "tool_name", sortMark),
		addSortMark("STATUS", m.sortState.Column, "status", sortMark),
		addSortMark("DURATION", m.sortState.Column, "duration_ms", sortMark),
		addSortMark("TIME", m.sortState.Column, "timestamp", sortMark))
	b.WriteString(HeaderStyle.Render(header))
	b.WriteString("\n")

	visible := maxHeight - 4
	if filterSummary != "" {
		visible = maxHeight - 5
	}
	if visible > len(m.activities) {
		visible = len(m.activities)
	}

	offset := 0
	if m.cursor >= visible {
		offset = m.cursor - visible + 1
	}

	for i := offset; i < offset+visible && i < len(m.activities); i++ {
		a := m.activities[i]

		actType := truncateString(a.Type, 12)
		server := truncateString(a.ServerName, 16)
		tool := truncateString(a.ToolName, 28)

		status := a.Status
		duration := a.DurationMs
		ts := formatTimestamp(a.Timestamp)

		row := fmt.Sprintf("  %-12s %-16s %-28s %-10s %-10s %s",
			actType, server, tool, status, duration, ts)

		if i == m.cursor {
			b.WriteString(SelectedStyle.Render(row))
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
				statusStyle = BaseStyle
			}

			prefix := fmt.Sprintf("  %-12s %-16s %-28s ", actType, server, tool)
			b.WriteString(BaseStyle.Render(prefix))
			b.WriteString(statusStyle.Render(fmt.Sprintf("%-10s", status)))
			b.WriteString(MutedStyle.Render(fmt.Sprintf(" %-10s %s", duration, ts)))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func renderStatusBar(m model) string {
	// Left side: current tab and item count
	var left string
	if m.activeTab == tabServers {
		left = fmt.Sprintf(" [Servers] %d servers", len(m.servers))
	} else {
		left = fmt.Sprintf(" [Activity] %d activities", len(m.activities))
	}

	// Center: sort, filter, and mode info
	var center []string

	// Add sort status
	if m.sortState.Column != "" {
		sortDir := "↑"
		if m.sortState.Descending {
			sortDir = "↓"
		}
		center = append(center, fmt.Sprintf("Sort: %s %s", m.sortState.Column, sortDir))
	}

	// Add filter count
	filterCount := 0
	for _, v := range m.filterState {
		if str, ok := v.(string); ok && str != "" {
			filterCount++
		}
	}
	if filterCount > 0 {
		center = append(center, fmt.Sprintf("Filters: %d", filterCount))
	}

	// Add mode indicator
	if m.uiMode != ModeNormal {
		center = append(center, fmt.Sprintf("Mode: %s", m.uiMode))
	}

	centerStr := strings.Join(center, " | ")

	// Right side: last update time and cursor position
	var right string
	if m.activeTab == tabServers {
		right = fmt.Sprintf("Row %d/%d  ", m.cursor+1, len(m.servers))
	} else {
		right = fmt.Sprintf("Row %d/%d  ", m.cursor+1, len(m.activities))
	}

	if !m.lastUpdate.IsZero() {
		right = fmt.Sprintf("%sUpdated %s ago ", right, formatDuration(time.Since(m.lastUpdate)))
	}

	// Calculate gaps
	leftWidth := lipgloss.Width(left)
	centerWidth := lipgloss.Width(centerStr)
	rightWidth := lipgloss.Width(right)

	// Try to fit center in the middle
	availableWidth := m.width - leftWidth - rightWidth
	if availableWidth >= centerWidth {
		gap1 := (availableWidth - centerWidth) / 2
		gap2 := availableWidth - centerWidth - gap1
		bar := left + strings.Repeat(" ", gap1) + centerStr + strings.Repeat(" ", gap2) + right
		return StatusBarStyle.Width(m.width).Render(bar)
	}

	// Fallback: left + center + right with minimal spacing
	gap := m.width - leftWidth - centerWidth - rightWidth
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + centerStr + right
	return StatusBarStyle.Width(m.width).Render(bar)
}

func renderHelp(m model) string {
	common := "q: quit  1/2: tabs  r: refresh  o: oauth  j/k: nav  s: sort  f: filter  c: clear  ?: help"

	var modeHelp string
	switch m.uiMode {
	case ModeNormal:
		switch m.activeTab {
		case tabServers:
			modeHelp = common + "  e: enable  d: disable  R: restart  l: login"
		case tabActivity:
			modeHelp = common
		}

	case ModeSortSelect:
		modeHelp = "SORT MODE: t=type  y=type  s=server  d=duration  st=status  ts=timestamp  esc: cancel"

	case ModeFilterEdit:
		modeHelp = "FILTER MODE: tab/shift+tab=move  ↑/↓=cycle  esc: apply  c: clear"

	default:
		modeHelp = common
	}

	return RenderHelp(" " + modeHelp)
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

// truncateString truncates s to maxRunes runes, appending "..." if truncated.
// Uses []rune to avoid splitting multi-byte UTF-8 characters.
func truncateString(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

// addSortMark appends a sort indicator to a column label if it's the active sort column
func addSortMark(label, currentCol, colKey, sortMark string) string {
	if currentCol != colKey {
		return label
	}
	return label + " " + sortMark
}

// getSortMark returns the appropriate sort indicator based on direction
func getSortMark(descending bool) string {
	if descending {
		return "▼"
	}
	return "▲"
}

// renderFilterSummary returns a string showing active filters as badges
func renderFilterSummary(m model) string {
	if !m.filterState.hasActiveFilters() {
		return ""
	}

	var parts []string
	for key, val := range m.filterState {
		if str, ok := val.(string); ok && str != "" {
			// Capitalize first letter of filter key
			keyDisplay := strings.ToUpper(string(key[0])) + key[1:]
			badge := fmt.Sprintf("[%s: %s ✕]", keyDisplay, str)
			parts = append(parts, badge)
		}
	}

	if len(parts) > 0 {
		parts = append(parts, "[Clear]")
		return "Filter: " + strings.Join(parts, " ")
	}
	return ""
}

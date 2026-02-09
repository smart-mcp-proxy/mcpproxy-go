package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	colorHealthy   = lipgloss.Color("#22c55e") // green
	colorDegraded  = lipgloss.Color("#eab308") // yellow
	colorUnhealthy = lipgloss.Color("#ef4444") // red
	colorDisabled  = lipgloss.Color("#6b7280") // gray
	colorAccent    = lipgloss.Color("#3b82f6") // blue
	colorMuted     = lipgloss.Color("#9ca3af") // light gray
	colorWhite     = lipgloss.Color("#f9fafb")

	// Styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWhite).
			Background(colorAccent).
			Padding(0, 1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWhite).
			Background(lipgloss.Color("#374151"))

	normalStyle = lipgloss.NewStyle()

	mutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	healthyStyle = lipgloss.NewStyle().
			Foreground(colorHealthy)

	degradedStyle = lipgloss.NewStyle().
			Foreground(colorDegraded)

	unhealthyStyle = lipgloss.NewStyle().
			Foreground(colorUnhealthy)

	disabledStyle = lipgloss.NewStyle().
			Foreground(colorDisabled)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Background(lipgloss.Color("#1f2937")).
			Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorUnhealthy).
			Bold(true)

	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWhite).
			Background(colorAccent).
			Padding(0, 1)

	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Padding(0, 1)
)

func healthStyle(level string) lipgloss.Style {
	switch level {
	case "healthy":
		return healthyStyle
	case "degraded":
		return degradedStyle
	case "unhealthy":
		return unhealthyStyle
	default:
		return disabledStyle
	}
}

func healthIndicator(level string) string {
	switch level {
	case "healthy":
		return healthyStyle.Render("●")
	case "degraded":
		return degradedStyle.Render("◐")
	case "unhealthy":
		return unhealthyStyle.Render("○")
	default:
		return disabledStyle.Render("○")
	}
}

package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// Semantic color palette using AdaptiveColor for light/dark terminal support
var (
	colorHealthy   = lipgloss.AdaptiveColor{Light: "28", Dark: "42"}   // green
	colorDegraded  = lipgloss.AdaptiveColor{Light: "136", Dark: "214"} // yellow
	colorUnhealthy = lipgloss.AdaptiveColor{Light: "160", Dark: "196"} // red
	colorDisabled  = lipgloss.AdaptiveColor{Light: "245", Dark: "243"} // gray
	colorAccent    = lipgloss.AdaptiveColor{Light: "25", Dark: "75"}   // blue
	colorMuted     = lipgloss.AdaptiveColor{Light: "245", Dark: "244"} // light gray
	colorBgDark = lipgloss.AdaptiveColor{Light: "254", Dark: "236"} // dark bg
	colorHighlight = lipgloss.AdaptiveColor{Light: "141", Dark: "57"}  // selection bg
)

// Shared reusable styles

var (
	// TitleStyle renders top-level titles with bold accent background
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "255", Dark: "255"}).
			Background(colorAccent).
			Padding(0, 1)

	// HeaderStyle renders table/section headers
	HeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	// SelectedStyle highlights the currently selected row
	SelectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "232", Dark: "229"}).
			Background(colorHighlight)

	// BaseStyle is the default unstyled base
	BaseStyle = lipgloss.NewStyle()

	// MutedStyle renders secondary/less important text
	MutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	// ErrorStyle renders error messages
	ErrorStyle = lipgloss.NewStyle().
			Foreground(colorUnhealthy).
			Bold(true)

	// SuccessStyle renders success/status messages
	SuccessStyle = lipgloss.NewStyle().
			Foreground(colorHealthy)

	// Health-level styles
	healthyStyle   = lipgloss.NewStyle().Foreground(colorHealthy)
	degradedStyle  = lipgloss.NewStyle().Foreground(colorDegraded)
	unhealthyStyle = lipgloss.NewStyle().Foreground(colorUnhealthy)
	disabledStyle  = lipgloss.NewStyle().Foreground(colorDisabled)

	// StatusBarStyle renders the bottom status bar
	StatusBarStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Background(colorBgDark).
			Padding(0, 1)

	// HelpStyle renders keybinding hints
	HelpStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	// Tab styles
	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "255", Dark: "255"}).
			Background(colorAccent).
			Padding(0, 1)

	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Padding(0, 1)
)

// RenderTitle wraps text with TitleStyle
func RenderTitle(text string) string {
	return TitleStyle.Render(text)
}

// RenderError formats an error with ErrorStyle
func RenderError(err error) string {
	if err == nil {
		return ""
	}
	return ErrorStyle.Render(fmt.Sprintf("Error: %v", err))
}

// RenderHelp wraps help text with HelpStyle
func RenderHelp(text string) string {
	return HelpStyle.Render(text)
}

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

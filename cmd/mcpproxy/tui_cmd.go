package main

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tui"
)

// GetTUICommand creates the TUI subcommand.
func GetTUICommand() *cobra.Command {
	var refreshSeconds int

	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Launch the terminal UI dashboard",
		Long:  "Launch an interactive terminal UI for monitoring servers, OAuth tokens, and activity.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmdLogLevel, _ := cmd.Flags().GetString("log-level")
			cmdLogToFile, _ := cmd.Flags().GetBool("log-to-file")
			cmdLogDir, _ := cmd.Flags().GetString("log-dir")

			logger, err := logs.SetupCommandLogger(false, cmdLogLevel, cmdLogToFile, cmdLogDir)
			if err != nil {
				return fmt.Errorf("failed to setup logger: %w", err)
			}
			defer func() { _ = logger.Sync() }()

			// Load config to find daemon connection
			cfg, err := config.Load()
			if err != nil {
				cfg = config.DefaultConfig()
			}

			// Detect socket or fall back to TCP
			socketPath := socket.DetectSocketPath(cfg.DataDir)
			var endpoint string
			if socket.IsSocketAvailable(socketPath) {
				endpoint = socketPath
			} else {
				endpoint = fmt.Sprintf("http://%s", cfg.Listen)
			}

			client := cliclient.NewClientWithAPIKey(endpoint, cfg.APIKey, logger.Sugar())

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			refreshInterval := time.Duration(refreshSeconds) * time.Second
			if refreshInterval < 1*time.Second {
				return fmt.Errorf("--refresh must be at least 1 (got %d)", refreshSeconds)
			}
			m := tui.NewModel(ctx, client, refreshInterval)

			p := tea.NewProgram(m, tea.WithAltScreen())
			if _, err := p.Run(); err != nil {
				return fmt.Errorf("TUI error: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&refreshSeconds, "refresh", 5, "Refresh interval in seconds")

	return cmd
}

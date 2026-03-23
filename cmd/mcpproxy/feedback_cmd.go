package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

var (
	feedbackCategory string
	feedbackEmail    string
)

// GetFeedbackCommand returns the feedback submission command.
func GetFeedbackCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "feedback \"message\"",
		Short: "Submit feedback (bug report, feature request, or general)",
		Long: `Submit feedback to the MCPProxy team. Your message is sent to the
telemetry endpoint along with anonymous system context (version, OS, etc.).

Examples:
  mcpproxy feedback "The search results could be more relevant" --category feature
  mcpproxy feedback "Server crashes when adding OAuth server" --category bug
  mcpproxy feedback "Great tool, thanks!" --category other --email me@example.com`,
		Args: cobra.ExactArgs(1),
		RunE: runFeedback,
	}

	cmd.Flags().StringVar(&feedbackCategory, "category", "other", "Feedback category: bug, feature, other")
	cmd.Flags().StringVar(&feedbackEmail, "email", "", "Optional email for follow-up")

	return cmd
}

func runFeedback(cmd *cobra.Command, args []string) error {
	message := args[0]

	// Validate inputs before sending
	if !telemetry.ValidateCategory(feedbackCategory) {
		return fmt.Errorf("invalid category %q: must be bug, feature, or other", feedbackCategory)
	}
	if err := telemetry.ValidateMessage(message); err != nil {
		return fmt.Errorf("invalid message: %w", err)
	}

	cfg, err := loadFeedbackConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	logger, _ := zap.NewProduction()
	defer logger.Sync()

	cfgPath := config.GetConfigPath(cfg.DataDir)
	svc := telemetry.New(cfg, cfgPath, version, Edition, logger)

	req := &telemetry.FeedbackRequest{
		Category: feedbackCategory,
		Message:  message,
		Email:    feedbackEmail,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := svc.SubmitFeedback(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to submit feedback: %w", err)
	}

	format := clioutput.ResolveFormat(globalOutputFormat, globalJSONOutput)
	switch format {
	case "json":
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	default:
		if resp.Success {
			fmt.Println("Feedback submitted successfully!")
			if resp.IssueURL != "" {
				fmt.Printf("Track your feedback: %s\n", resp.IssueURL)
			}
		} else {
			fmt.Printf("Feedback submission failed: %s\n", resp.Error)
		}
	}

	return nil
}

func loadFeedbackConfig() (*config.Config, error) {
	if configFile != "" {
		cfg, err := config.LoadFromFile(configFile)
		if err != nil {
			return nil, err
		}
		if dataDir != "" {
			cfg.DataDir = dataDir
		}
		return cfg, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	return cfg, nil
}

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
)

// GetAttentionCommand returns the `mcpproxy attention` cobra command
// (Spec 109 FR-001/FR-003): the one needs-attention list, read verbatim from
// GET /api/v1/attention — the same list the Web UI Home page, the macOS tray
// and Home section, and the first line of `status`/first section of `doctor`
// all render.
func GetAttentionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attention",
		Short: "Show the needs-attention list",
		Long: `Show the one needs-attention list every MCPProxy surface reads from:
sign-in prompts, quarantine/tool reviews, connection errors, missing secrets,
configuration errors, and clients that connected but were never seen.

This is a report, not a health check: it always exits 0, whether the list is
empty or not. Use 'mcpproxy doctor' for a full diagnostic pass.

Examples:
  mcpproxy attention
  mcpproxy attention -o json`,
		RunE: runAttention,
	}
	return cmd
}

func runAttention(_ *cobra.Command, _ []string) error {
	cfg, err := loadCLIConfig(configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client, ok := newDaemonClient(cfg, nil)
	if !ok {
		return fmt.Errorf("attention requires running daemon. Start with: mcpproxy serve")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.GetAttention(ctx)
	if err != nil {
		return fmt.Errorf("failed to get attention list from daemon: %w", err)
	}

	return printAttentionOutput(resp)
}

func printAttentionOutput(resp *cliclient.AttentionResponse) error {
	format := ResolveOutputFormat()
	if format == "json" || format == "yaml" {
		formatter, err := GetOutputFormatter()
		if err != nil {
			return err
		}
		out, err := formatter.Format(resp)
		if err != nil {
			return fmt.Errorf("failed to format output: %w", err)
		}
		fmt.Println(out)
		return nil
	}

	if resp.Count == 0 {
		fmt.Println("All clear")
		return nil
	}

	headers := []string{"#", "KIND", "SUBJECT", "SUMMARY", "FIX"}
	rows := make([][]string, len(resp.Items))
	for i, item := range resp.Items {
		rows[i] = []string{
			fmt.Sprintf("%d", i+1),
			item.Kind,
			item.Subject.Name,
			item.Summary,
			item.Fix.Label,
		}
	}

	formatter, err := GetOutputFormatter()
	if err != nil {
		return clioutput.NewStructuredError(clioutput.ErrCodeInvalidOutputFormat, err.Error()).
			WithGuidance("Use -o table, -o json, or -o yaml")
	}
	out, err := formatter.FormatTable(headers, rows)
	if err != nil {
		return fmt.Errorf("failed to format table: %w", err)
	}
	fmt.Print(out)
	return nil
}

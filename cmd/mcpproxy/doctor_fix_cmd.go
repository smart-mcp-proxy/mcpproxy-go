// doctor_fix_cmd.go implements `mcpproxy doctor fix <CODE> --server <name>`
// (spec 044). It's a thin CLI wrapper around POST /api/v1/diagnostics/fix
// that looks up the fixer_key from the diagnostics catalog based on the
// supplied error code so the caller doesn't have to know about the
// internal fixer key registry.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"
)

var (
	doctorFixServer    string
	doctorFixFixerKey  string
	doctorFixExecute   bool
	doctorFixOutput    string
	doctorFixLogLevel  string
	doctorFixConfigPth string
)

var doctorFixCmd = &cobra.Command{
	Use:   "fix <CODE>",
	Short: "Run a registered fixer for a diagnostic code",
	Long: `Invoke a registered diagnostics fixer for a specific (server, code)
pair. By default the fix runs in dry_run mode; pass --execute to apply
mutating changes.

The <CODE> argument is a stable diagnostics code such as
MCPX_OAUTH_REFRESH_EXPIRED. The appropriate fixer_key is resolved from
the diagnostics catalog automatically; use --fixer-key to override when
a code defines more than one button-type fix step.

Examples:
  mcpproxy doctor fix MCPX_OAUTH_REFRESH_EXPIRED --server github
  mcpproxy doctor fix MCPX_STDIO_SPAWN_ENOENT --server my-stdio --execute
  mcpproxy doctor fix MCPX_STDIO_SPAWN_ENOENT --server my-stdio -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runDoctorFix,
}

func init() {
	doctorFixCmd.Flags().StringVar(&doctorFixServer, "server", "", "Upstream server name (required)")
	doctorFixCmd.Flags().StringVar(&doctorFixFixerKey, "fixer-key", "", "Override the auto-resolved fixer_key")
	doctorFixCmd.Flags().BoolVar(&doctorFixExecute, "execute", false, "Apply the fix (default: dry_run)")
	doctorFixCmd.Flags().StringVarP(&doctorFixOutput, "output", "o", "pretty", "Output format (pretty, json)")
	doctorFixCmd.Flags().StringVarP(&doctorFixLogLevel, "log-level", "l", "warn", "Log level")
	doctorFixCmd.Flags().StringVarP(&doctorFixConfigPth, "config", "c", "", "Path to config file")
	_ = doctorFixCmd.MarkFlagRequired("server")
	doctorCmd.AddCommand(doctorFixCmd)
}

func runDoctorFix(_ *cobra.Command, args []string) error {
	codeArg := args[0]

	// Validate the code exists in the catalog.
	entry, ok := diagnostics.Get(diagnostics.Code(codeArg))
	if !ok {
		return fmt.Errorf("unknown diagnostics code: %q (run `mcpproxy doctor list-codes` for the full catalog)", codeArg)
	}

	// Resolve the fixer_key. If the user passed one explicitly, validate it
	// against the catalog. Otherwise pick the first Button-type fix step.
	fixerKey := doctorFixFixerKey
	resolvedStep := resolveFixerKey(entry, &fixerKey)
	if resolvedStep == nil {
		return fmt.Errorf("code %s has no Button fix steps; nothing to invoke", codeArg)
	}

	mode := diagnostics.ModeDryRun
	if doctorFixExecute {
		mode = diagnostics.ModeExecute
	}

	// Resolve the data directory used to locate the daemon socket. Order of
	// precedence: --data-dir (root persistent flag) > config-file data_dir >
	// platform default (~/.mcpproxy).
	resolvedDataDir := dataDir
	if resolvedDataDir == "" {
		if globalConfig, err := loadDoctorConfig(); err == nil && globalConfig.DataDir != "" {
			resolvedDataDir = globalConfig.DataDir
		}
	}

	logger, err := createDoctorLogger(doctorFixLogLevel)
	if err != nil {
		return err
	}
	if !shouldUseDoctorDaemon(resolvedDataDir) {
		return fmt.Errorf("doctor fix requires a running daemon. Start with: mcpproxy serve")
	}
	socketPath := socket.DetectSocketPath(resolvedDataDir)
	client := cliclient.NewClient(socketPath, logger.Sugar())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := client.InvokeDiagnosticFix(ctx, doctorFixServer, codeArg, fixerKey, mode)
	if err != nil {
		return fmt.Errorf("fix invocation failed: %w", err)
	}

	return printDoctorFixResult(entry, resolvedStep, fixerKey, mode, result)
}

// resolveFixerKey finds the FixStep that matches the supplied fixer_key, or
// the first Button-type step if fixerKey is empty. Mutates *fixerKey so the
// caller can echo the resolved value in the output.
func resolveFixerKey(entry diagnostics.CatalogEntry, fixerKey *string) *diagnostics.FixStep {
	for i := range entry.FixSteps {
		step := &entry.FixSteps[i]
		if step.Type != diagnostics.FixStepButton {
			continue
		}
		if *fixerKey == "" {
			*fixerKey = step.FixerKey
			return step
		}
		if step.FixerKey == *fixerKey {
			return step
		}
	}
	return nil
}

func printDoctorFixResult(
	entry diagnostics.CatalogEntry,
	step *diagnostics.FixStep,
	fixerKey string,
	mode string,
	result *cliclient.DiagnosticFixResult,
) error {
	switch doctorFixOutput {
	case "json":
		combined := map[string]interface{}{
			"code":        entry.Code,
			"severity":    entry.Severity,
			"server":      doctorFixServer,
			"fixer_key":   fixerKey,
			"label":       step.Label,
			"destructive": step.Destructive,
			"mode":        mode,
			"result":      result,
		}
		out, err := json.MarshalIndent(combined, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to format output: %w", err)
		}
		fmt.Println(string(out))
	default:
		// pretty
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Printf("🛠  Doctor Fix: %s\n", entry.Code)
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Printf("Server:      %s\n", doctorFixServer)
		fmt.Printf("Fix step:    %s\n", step.Label)
		fmt.Printf("Fixer key:   %s\n", fixerKey)
		destructive := "no"
		if step.Destructive {
			destructive = "yes"
		}
		fmt.Printf("Destructive: %s\n", destructive)
		fmt.Printf("Mode:        %s\n", mode)
		fmt.Println()

		outcome := "(no outcome)"
		if result != nil {
			outcome = result.Outcome
		}
		switch outcome {
		case diagnostics.OutcomeSuccess:
			fmt.Printf("Outcome:     ✅ %s\n", outcome)
		case diagnostics.OutcomeFailed:
			fmt.Printf("Outcome:     ❌ %s\n", outcome)
		case diagnostics.OutcomeBlocked:
			fmt.Printf("Outcome:     ⛔ %s\n", outcome)
		default:
			fmt.Printf("Outcome:     %s\n", outcome)
		}
		if result != nil {
			if result.DurationMs > 0 {
				fmt.Printf("Duration:    %dms\n", result.DurationMs)
			}
			if result.Preview != "" {
				fmt.Println()
				fmt.Println("Preview:")
				fmt.Printf("  %s\n", result.Preview)
			}
			if result.FailureMsg != "" {
				fmt.Println()
				fmt.Println("Failure:")
				fmt.Printf("  %s\n", result.FailureMsg)
			}
		}
		fmt.Println()
		if !doctorFixExecute && step.Destructive {
			fmt.Println("💡 This was a dry run. Re-run with --execute to apply the change.")
		}
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	}
	return nil
}

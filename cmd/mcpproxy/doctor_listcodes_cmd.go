package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
)

var doctorListCodesOutput string

var doctorListCodesCmd = &cobra.Command{
	Use:   "list-codes",
	Short: "List every registered diagnostic error code",
	Long: `Print the full diagnostics catalog: code, severity, short message,
and docs URL. Useful for AI agents consuming the stable taxonomy and for
writing docs / release notes.

Output formats: pretty (default), json.

Spec 044.

Examples:
  mcpproxy doctor list-codes
  mcpproxy doctor list-codes -o json | jq '.[] | select(.severity == "error")'`,
	RunE: runDoctorListCodes,
}

func init() {
	doctorListCodesCmd.Flags().StringVarP(&doctorListCodesOutput, "output", "o", "pretty", "Output format: pretty, json")
	doctorCmd.AddCommand(doctorListCodesCmd)
}

func runDoctorListCodes(_ *cobra.Command, _ []string) error {
	entries := diagnostics.All()

	switch doctorListCodesOutput {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)

	case "pretty", "":
		fmt.Printf("%d diagnostic codes registered:\n\n", len(entries))
		for _, e := range entries {
			fmt.Printf("  %-36s  %-5s  %s\n", e.Code, e.Severity, e.UserMessage)
			fmt.Printf("    docs: %s\n", e.DocsURL)
			for _, s := range e.FixSteps {
				switch s.Type {
				case diagnostics.FixStepLink:
					fmt.Printf("    fix (link):    %s  %s\n", s.Label, s.URL)
				case diagnostics.FixStepCommand:
					fmt.Printf("    fix (command): %s  %s\n", s.Label, s.Command)
				case diagnostics.FixStepButton:
					dr := ""
					if s.Destructive {
						dr = " [destructive -> dry-run default]"
					}
					fmt.Printf("    fix (button):  %s  key=%s%s\n", s.Label, s.FixerKey, dr)
				}
			}
			fmt.Println()
		}
		return nil

	default:
		return fmt.Errorf("unknown output format %q (expected: pretty, json)", doctorListCodesOutput)
	}
}

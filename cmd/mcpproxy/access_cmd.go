package main

// `mcpproxy access explain` (Spec 108-g, FR-035, FR-036): why a tool is or is
// not reachable for one subject. The verdict is data, not an error: exit 0
// whenever an explanation is produced.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// GetAccessCommand returns the `access` command group.
func GetAccessCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Explain what a client, token or profile can reach",
	}
	cmd.AddCommand(newAccessExplainCmd())
	silenceUsageOnRun(cmd)
	return cmd
}

func newAccessExplainCmd() *cobra.Command {
	var tool, client, token, prof string
	var anonymous bool
	cmd := &cobra.Command{
		Use:   "explain",
		Short: "Walk the access chain for one tool and one subject",
		Long: `Walk the gates a real call meets (credential, profile, server in scope, tool
rule, tier cap, token permission, global gate, server state, tool approval) for
one upstream tool and exactly one subject, print the verdict and the fixes in
preference order. Exit code 0 whenever an explanation is produced; 1 for a
request error (unknown subject, daemon unreachable). Administrators only.

Examples:
  mcpproxy access explain --tool github:create_issue --client cursor
  mcpproxy access explain --tool github:list_issues --token ci-bot
  mcpproxy access explain --tool github:create_issue --profile work-readonly
  mcpproxy access explain --tool github:create_issue --anonymous`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			subjects := 0
			q := url.Values{}
			for k, v := range map[string]string{"client": client, "token": token, "profile": prof} {
				if v != "" {
					subjects++
					q.Set(k, v)
				}
			}
			if anonymous {
				subjects++
				q.Set("anonymous", "true")
			}
			if subjects != 1 {
				return newFlagValidationError("exactly one of --client, --token, --profile, --anonymous is required")
			}
			if tool == "" {
				return newFlagValidationError("--tool <server:tool> is required")
			}
			q.Set("tool", tool)
			data, err := restDo("access", http.MethodGet, "/api/v1/access/explain?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			if structuredOutput() {
				return printData(data)
			}
			var ex runtime.AccessExplanation
			if err := json.Unmarshal(data, &ex); err != nil {
				return err
			}
			printExplanation(ex)
			return nil
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "", "Upstream tool as server:tool (required)")
	cmd.Flags().StringVar(&client, "client", "", "Explain a client's credential")
	cmd.Flags().StringVar(&token, "token", "", "Explain a regular agent token")
	cmd.Flags().StringVar(&prof, "profile", "", "Explain a profile")
	cmd.Flags().BoolVar(&anonymous, "anonymous", false, "Explain a caller that sends no credential")
	return cmd
}

func printExplanation(ex runtime.AccessExplanation) {
	subject := string(ex.Subject.Kind)
	if ex.Subject.Name != "" {
		subject += " " + ex.Subject.Name
	}
	fmt.Printf("Tool: %s\nSubject: %s\n", ex.Tool, subject)
	if ex.Profile.Name != "" {
		fmt.Printf("Profile: %s (%s)\n", ex.Profile.Name, ex.Profile.Source)
	}
	firstFix := map[profile.ExplainStep]string{}
	for _, f := range ex.Fixes {
		if _, ok := firstFix[f.Step]; !ok {
			firstFix[f.Step] = f.Label
		}
	}
	rows := make([][]string, 0, len(ex.Steps))
	for _, s := range ex.Steps {
		rows = append(rows, []string{string(s.Step), string(s.Status), dash(s.Detail), dash(firstFix[s.Step])})
	}
	_ = printTable([]string{"STEP", "STATUS", "DETAIL", "FIX"}, rows)
	if ex.FirstFailure != "" {
		fmt.Printf("VERDICT: %s at %s\n", ex.Verdict, ex.FirstFailure)
	} else {
		fmt.Printf("VERDICT: %s\n", ex.Verdict)
	}
	if len(ex.Fixes) == 0 {
		return
	}
	fmt.Println("Fixes:")
	for i, f := range ex.Fixes {
		fmt.Printf("  %d. %s\n", i+1, f.Label)
		if hint := fixCommand(f, ex.Tool); hint != "" {
			fmt.Printf("     %s\n", hint)
		}
	}
}

// fixCommand is the command (or setting) that performs one explain fix.
func fixCommand(f runtime.Fix, tool string) string {
	server := tool
	if i := strings.Index(tool, ":"); i > 0 {
		server = tool[:i]
	}
	switch f.Action {
	case profile.FixAllowInProfile:
		return fmt.Sprintf("mcpproxy profile update %s --add-allow %s", f.Target, tool)
	case profile.FixClassifyInProfile:
		return fmt.Sprintf("mcpproxy profile classify %s %s <read|write|destructive>", f.Target, tool)
	case profile.FixAddServerToProfile:
		return fmt.Sprintf("mcpproxy profile update %s --add-server %s", f.Target, server)
	case profile.FixMoveClient:
		dest := f.Profile
		if dest == "" {
			dest = "<profile>" // a daemon that predates the field
		}
		return fmt.Sprintf("mcpproxy client set-profile %s %s", f.Target, dest)
	case profile.FixEditToken:
		return "mcpproxy token create --name <new-name> --profile <profile>"
	case profile.FixEnableServer:
		return "mcpproxy upstream enable " + f.Target
	case profile.FixApproveTool:
		// A tool-approval target is "server:tool": approve only that tool (a bare
		// `approve <server>` approves every pending tool of the server). A
		// quarantined-server target is the server name alone, where approving
		// the whole server is the intended action.
		if server, tool, ok := strings.Cut(f.Target, ":"); ok && server != "" && tool != "" {
			return "mcpproxy upstream approve " + server + " " + tool
		}
		return "mcpproxy upstream approve " + f.Target
	case profile.FixChangeSetting:
		if f.Target == "require_mcp_auth" {
			return "set require_mcp_auth: true (config or Settings → Security)"
		}
		return "mcpproxy profile anonymous <profile>"
	case profile.FixReconnectClient:
		return "mcpproxy connect " + f.Target
	}
	return ""
}

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
	cmd.AddCommand(newAccessSuggestCmd())
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

func newAccessSuggestCmd() *cobra.Command {
	var since, token, prof string
	var patch bool
	cmd := &cobra.Command{
		Use:   "suggest",
		Short: "Suggest profile changes from calls the profiles blocked",
		Long: `Read the profile refusals in the audit log, group them by profile, tool and
block reason, and print one reviewed suggestion per group with the command that
would apply it. Each group is re-checked with the access explainer, so a tool
that is reachable now no longer appears.

Nothing is applied. A deny rule is reported and never offered for widening, and
an unannotated tool is never given a tier: you choose it. Blocks that no profile
change fixes (quarantine, token permissions) are counted; use "access explain".
Administrators only.

--since is a duration (90m, 24h, 7d); the default is 24h and the audit-log
retention is the only limit. --patch also prints, per profile, the proposed
profile document. That is mcpproxy's own profile format (what PUT
/api/v1/profiles/{name} takes, and a profiles[] entry in the config file), not a
client permission file.

Examples:
  mcpproxy access suggest
  mcpproxy access suggest --since 7d --profile work-readonly
  mcpproxy access suggest --token ci-bot --patch
  mcpproxy access suggest -o json`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			q := url.Values{}
			q.Set("since", since)
			if token != "" {
				q.Set("token", token)
			}
			if prof != "" {
				q.Set("profile", prof)
			}
			if patch {
				q.Set("patch", "true")
			}
			data, err := restDo("access", http.MethodGet, "/api/v1/access/suggest?"+q.Encode(), nil)
			if err != nil {
				return err
			}
			if structuredOutput() {
				return printData(data)
			}
			var res runtime.AccessSuggestions
			if err := json.Unmarshal(data, &res); err != nil {
				return err
			}
			return printSuggestions(res, patch)
		},
	}
	cmd.Flags().StringVar(&since, "since", "24h", "Look-back window: a duration such as 90m, 24h or 7d")
	cmd.Flags().StringVar(&token, "token", "", "Only blocks of this token")
	cmd.Flags().StringVar(&prof, "profile", "", "Only blocks under this profile")
	cmd.Flags().BoolVar(&patch, "patch", false, "Also print the proposed profile document per profile")
	return cmd
}

func printSuggestions(res runtime.AccessSuggestions, patch bool) error {
	fmt.Printf("Profile refusals in the last %s: %d\n", res.Window, res.BlockedCalls)
	if len(res.Suggestions) == 0 {
		fmt.Println("Nothing to suggest.")
	} else {
		rows := make([][]string, 0, len(res.Suggestions))
		for _, s := range res.Suggestions {
			rows = append(rows, []string{
				s.Profile, s.Tool, string(s.BlockReason), fmt.Sprint(s.Count),
				dash(strings.Join(s.Tokens, ",")), s.LastSeen.Local().Format("2006-01-02 15:04"), string(s.Suggestion),
			})
		}
		if err := printTable([]string{"PROFILE", "TOOL", "REASON", "BLOCKED", "TOKENS", "LAST SEEN", "SUGGESTION"}, rows); err != nil {
			return err
		}
		for _, s := range res.Suggestions {
			printSuggestionDetail(s)
		}
	}
	var skipped []string
	add := func(n int, what string) {
		if n > 0 {
			skipped = append(skipped, fmt.Sprintf("%d %s", n, what))
		}
	}
	add(res.AlreadyFixed, "group(s) already reachable")
	add(res.ProfileRemoved, "group(s) of a removed profile")
	add(res.NonProfileBlocked, "blocked call(s) not caused by a profile (see 'mcpproxy access explain')")
	add(res.Unattributed, "refusal(s) naming no profile")
	if len(skipped) > 0 {
		fmt.Println("Not listed: " + strings.Join(skipped, "; ") + ".")
	}
	if patch {
		if len(res.Patches) == 0 {
			fmt.Println("No apply-ready profile change (classify needs a tier from you).")
		}
		for _, p := range res.Patches {
			doc, err := json.MarshalIndent(p.Proposed, "", "  ")
			if err != nil {
				return err
			}
			fmt.Printf("\nProposed profile %s (%s):\n%s\n", p.Profile, strings.Join(p.Changes, "; "), doc)
		}
	}
	return nil
}

// printSuggestionDetail prints the command, note and warning of one row.
func printSuggestionDetail(s runtime.AccessSuggestion) {
	var want profile.FixAction
	switch s.Suggestion {
	case runtime.SuggestAllow:
		want = profile.FixAllowInProfile
	case runtime.SuggestAddServer:
		want = profile.FixAddServerToProfile
	case runtime.SuggestClassify:
		want = profile.FixClassifyInProfile
	}
	fmt.Printf("\n%s  %s  (%s, %d blocked)\n", s.Profile, s.Tool, s.BlockReason, s.Count)
	for _, f := range s.Fixes {
		if f.Action == want {
			fmt.Printf("  %s\n", fixCommand(f, s.Tool))
		}
	}
	if s.Note != "" {
		fmt.Printf("  %s\n", s.Note)
	}
	if s.Warning != "" {
		fmt.Printf("  WARNING: %s\n", s.Warning)
	}
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

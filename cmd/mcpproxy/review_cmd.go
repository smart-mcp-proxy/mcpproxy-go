package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/spf13/cobra"
)

// GetReviewCommand exposes the same review payload and scan-gated decisions as
// REST and MCP. It deliberately never uses the legacy unquarantine endpoint.
func GetReviewCommand() *cobra.Command {
	return newReviewCommand(promptConfirmation)
}

func newReviewCommand(confirm func(string) (bool, error)) *cobra.Command {
	var except []string
	var tools []string
	var force bool
	var yes bool
	var all bool
	cmd := &cobra.Command{Use: "review", Short: "Inspect and decide quarantined server reviews"}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List servers needing review", RunE: func(_ *cobra.Command, _ []string) error {
		return runReviewRead("/api/v1/review")
	}})
	show := &cobra.Command{Use: "show <server>", Short: "Show captured tool definitions and scan verdicts", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		full, _ := c.Flags().GetBool("full")
		return runReviewRead("/api/v1/servers/"+url.PathEscape(args[0])+"/review", full)
	}}
	show.Flags().Bool("full", false, "Show full captured schemas and descriptions")
	cmd.AddCommand(show)
	approve := &cobra.Command{Use: "approve <server>", Short: "Approve a server through the scan gate", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		server := args[0]
		if all && len(tools) > 0 {
			return errReviewAllWithTools
		}
		state, err := reviewServerState(server)
		if err != nil {
			return err
		}
		if !state.quarantined {
			if len(except) > 0 {
				return fmt.Errorf("--except applies only while approving a quarantined server")
			}
			if !yes {
				confirmed, err := confirm(fmt.Sprintf("Approve review for server '%s'?", server))
				if err != nil || !confirmed {
					return reviewDeclined("Approval cancelled.", err)
				}
			}
			body := map[string]interface{}{"approve_all": true}
			if len(tools) > 0 {
				body = map[string]interface{}{"tools": tools}
			}
			return runReviewWrite(server, "tools/approve", body)
		}
		body := map[string]interface{}{"force": force}
		prompt := fmt.Sprintf("Approve server '%s' without seeing tools?", server)
		var summary string
		if len(state.tools) > 0 {
			block, allowed, err := reviewApproveSelection(state.tools, all, tools, except)
			if err != nil {
				return fmt.Errorf("%w for server '%s'", err, server)
			}
			if len(block) > 0 {
				body["block"] = block
			}
			prompt, summary = reviewApproveWording(server, len(state.tools), allowed, block)
		}
		if !yes {
			confirmed, err := confirm(prompt)
			if err != nil || !confirmed {
				return reviewDeclined("Approval cancelled.", err)
			}
		}
		if summary != "" && ResolveOutputFormat() == "table" {
			fmt.Println(summary)
		}
		return runReviewWrite(server, "security/approve", body)
	}}
	approve.Flags().BoolVar(&all, "all", false, "Approve every tool (default: only read-only tools with a clean scan, as on the Web and macOS review screens)")
	approve.Flags().StringSliceVar(&tools, "tools", nil, "Approve exactly these tools and block the rest (trusted server: only these pending or changed tools)")
	approve.Flags().StringSliceVar(&except, "except", nil, "Block these tools while approving the server")
	approve.Flags().BoolVar(&force, "force", false, "Force approval when the scan verdict is dangerous")
	approve.Flags().BoolVar(&yes, "yes", false, "Confirm the approval")
	cmd.AddCommand(approve)
	reject := &cobra.Command{Use: "reject <server>", Short: "Keep a server quarantined", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		if !yes {
			confirmed, err := confirm(fmt.Sprintf("Reject review for server '%s'?", args[0]))
			if err != nil {
				return err
			}
			if !confirmed {
				fmt.Println("Rejection cancelled.")
				return nil
			}
		}
		if len(tools) > 0 {
			return runReviewWrite(args[0], "tools/block", map[string]interface{}{"tools": tools})
		}
		return runReviewWrite(args[0], "security/reject", nil)
	}}
	reject.Flags().StringSliceVar(&tools, "tools", nil, "Block these pending or changed tools")
	reject.Flags().BoolVar(&yes, "yes", false, "Confirm the rejection")
	cmd.AddCommand(reject)
	return cmd
}

func runReviewRead(path string, full ...bool) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := client.DoRaw(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return parseAPIError(body, resp.StatusCode, "read review")
	}
	return formatReviewResponse(ResolveOutputFormat(), body, len(full) > 0 && full[0])
}

func runReviewWrite(server, operation string, value interface{}) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}
	var body []byte
	if value != nil {
		body, err = json.Marshal(value)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := client.DoRaw(ctx, http.MethodPost, "/api/v1/servers/"+url.PathEscape(server)+"/"+operation, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return parseAPIError(response, resp.StatusCode, strings.ReplaceAll(operation, "/", " "))
	}
	return formatReviewResponse(ResolveOutputFormat(), response, false)
}

var errReviewAllWithTools = fmt.Errorf("--all cannot be combined with --tools")

// reviewDeclined reports a declined confirmation as a clean exit.
func reviewDeclined(message string, err error) error {
	if err != nil {
		return err
	}
	fmt.Println(message)
	return nil
}

// reviewToolState is the part of a review tool the approval selection needs.
// DefaultAllowed is nil when the core predates the field, which counts as
// false: a mismatched core fails closed (D41.1).
type reviewToolState struct {
	Name           string `json:"name"`
	Tier           string `json:"tier"`
	DefaultAllowed *bool  `json:"default_allowed"`
}

type reviewServerStateResult struct {
	quarantined bool
	tools       []reviewToolState
}

func reviewServerState(server string) (reviewServerStateResult, error) {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return reviewServerStateResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/servers/"+url.PathEscape(server)+"/review", nil)
	if err != nil {
		return reviewServerStateResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return reviewServerStateResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return reviewServerStateResult{}, parseAPIError(body, resp.StatusCode, "read review")
	}
	var envelope struct {
		Data struct {
			Server struct {
				Quarantined bool `json:"quarantined"`
			} `json:"server"`
			Tools []reviewToolState `json:"tools"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return reviewServerStateResult{}, err
	}
	return reviewServerStateResult{quarantined: envelope.Data.Server.Quarantined, tools: envelope.Data.Tools}, nil
}

// reviewApproveSelection resolves which tools a quarantined-server approval
// allows, the same rule the Web and macOS review screens use. The base is the
// core's default selection, every tool (all) or exactly the named tools (only);
// except then subtracts. block is every other tool, in review order. A name
// that is not in the review is an error before anything is written.
func reviewApproveSelection(tools []reviewToolState, all bool, only, except []string) (block []string, allowed int, err error) {
	if all && len(only) > 0 {
		return nil, 0, errReviewAllWithTools
	}
	known := make(map[string]bool, len(tools))
	for _, tool := range tools {
		known[tool.Name] = true
	}
	toSet := func(names []string) (map[string]bool, error) {
		set := make(map[string]bool, len(names))
		for _, name := range names {
			if !known[name] {
				return nil, fmt.Errorf("unknown tool '%s'", name)
			}
			set[name] = true
		}
		return set, nil
	}
	onlySet, err := toSet(only)
	if err != nil {
		return nil, 0, err
	}
	exceptSet, err := toSet(except)
	if err != nil {
		return nil, 0, err
	}
	for _, tool := range tools {
		var allow bool
		switch {
		case all:
			allow = true
		case len(only) > 0:
			allow = onlySet[tool.Name]
		default:
			allow = tool.DefaultAllowed != nil && *tool.DefaultAllowed
		}
		if allow && !exceptSet[tool.Name] {
			allowed++
		} else {
			block = append(block, tool.Name)
		}
	}
	return block, allowed, nil
}

// reviewApproveWording builds the confirmation prompt and the table-mode
// summary line, both naming the exact count.
func reviewApproveWording(server string, total, allowed int, block []string) (prompt, summary string) {
	noun := func(n int) string {
		if n == 1 {
			return "tool"
		}
		return "tools"
	}
	if len(block) == 0 {
		return fmt.Sprintf("Approve server '%s' with all %d %s?", server, total, noun(total)),
			fmt.Sprintf("Allowing %d of %d %s; blocking none", allowed, total, noun(total))
	}
	list := strings.Join(block, ", ")
	return fmt.Sprintf("Approve server '%s' with %d of %d %s? Blocked: %s.", server, allowed, total, noun(total), list),
		fmt.Sprintf("Allowing %d of %d %s; blocking %d: %s", allowed, total, noun(total), len(block), list)
}

// formatReviewResponse drops the REST envelope so CLI JSON/YAML is exactly the
// shared review data object, as required by the CLI contract.
func formatReviewResponse(format string, raw []byte, full bool) error {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if len(envelope.Data) == 0 {
		return fmt.Errorf("response has no data")
	}
	if format != "table" {
		return formatAndPrintRaw(format, envelope.Data)
	}
	var value map[string]interface{}
	if err := json.Unmarshal(envelope.Data, &value); err != nil {
		return err
	}
	table := &clioutput.TableFormatter{}
	if message, _ := value["message"].(string); message != "" {
		fmt.Println(message)
		return nil
	}
	if status, _ := value["status"].(string); status != "" {
		if serverName, _ := value["server_name"].(string); serverName != "" {
			fmt.Printf("%s server %s\n", strings.ToUpper(status[:1])+status[1:], serverName)
			return nil
		}
	}
	if servers, ok := value["servers"].([]interface{}); ok {
		rows := make([][]string, 0, len(servers))
		for _, item := range servers {
			row, _ := item.(map[string]interface{})
			rows = append(rows, []string{fmt.Sprint(row["server"]), fmt.Sprint(row["kind"]), fmt.Sprint(row["quarantined"]), fmt.Sprint(row["pending"]), fmt.Sprint(row["changed"]), fmt.Sprint(row["tier_counts"]), fmt.Sprint(row["scan"])})
		}
		out, err := table.FormatTable([]string{"SERVER", "KIND", "QUARANTINED", "PENDING", "CHANGED", "TIERS", "SCAN"}, rows)
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	}
	server, _ := value["server"].(map[string]interface{})
	fmt.Printf("Server: %v\n", server["name"])
	if line := reviewScanLine(server); line != "" {
		fmt.Println(line)
	}
	rows := make([][]string, 0)
	if tools, ok := value["tools"].([]interface{}); ok {
		for _, item := range tools {
			tool, _ := item.(map[string]interface{})
			desc := fmt.Sprint(tool["description"])
			if !full {
				desc = strings.Split(desc, "\n")[0]
			} else {
				desc = "from the server, not verified:\n" + desc
				desc += reviewSchemaText("Input schema", tool["input_schema"])
				desc += reviewSchemaText("Output schema", tool["output_schema"])
				if diff := tool["diff"]; diff != nil {
					desc += "\nChanges from the previous approved definition:\n" + fmt.Sprint(diff)
				}
			}
			rows = append(rows, []string{fmt.Sprint(tool["name"]), fmt.Sprint(tool["tier"]), fmt.Sprint(tool["approval_status"]), fmt.Sprint(tool["scan_verdict"]), desc})
		}
	}
	out, err := table.FormatTable([]string{"TOOL", "TIER", "APPROVAL", "SCAN", "DESCRIPTION"}, rows)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

// reviewScanLine renders the scan coverage of `review show` in the same words
// as the Web and macOS review screens. It returns "" when the payload carries
// no scan or predates coverage.
func reviewScanLine(server map[string]interface{}) string {
	scan, _ := server["scan"].(map[string]interface{})
	coverage, _ := scan["coverage"].(string)
	if coverage == "" {
		return ""
	}
	rescan := "run: mcpproxy security rescan " + fmt.Sprint(server["name"])
	switch coverage {
	case "current":
		risk, _ := scan["risk_score"].(float64)
		scanned, _ := scan["tools_scanned"].(float64)
		return fmt.Sprintf("Scan: %v · risk %d/100 · covers all %d tools", scan["verdict"], int(risk), int(scanned))
	case "stale":
		var tools []string
		if list, ok := scan["unscanned_tools"].([]interface{}); ok {
			for _, item := range list {
				tools = append(tools, fmt.Sprint(item))
			}
		}
		noun := "tools"
		if len(tools) == 1 {
			noun = "tool"
		}
		return fmt.Sprintf("Scan: out of date (%d %s changed or added after the last scan: %s); %s", len(tools), noun, strings.Join(tools, ", "), rescan)
	case "not_captured":
		return "Scan: not checked against tool definitions: they have not been captured yet; fetch them with Fetch tool definitions on the Web or macOS review screen"
	case "tools_not_scanned":
		return "Scan: the last scan did not analyse tool definitions (0 exported); " + rescan
	case "scanning":
		return "Scan: in progress"
	default:
		return "Scan: not scanned yet; " + rescan
	}
}

func reviewSchemaText(label string, schema interface{}) string {
	if schema == nil {
		return ""
	}
	encoded, err := json.Marshal(schema)
	if err != nil || string(encoded) == "null" {
		return ""
	}
	return "\n" + label + ":\n" + string(encoded)
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
	var fromReview string
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
	cmd.AddCommand(newReviewFetchCommand())
	approve := &cobra.Command{Use: "approve <server>", Short: "Approve a server through the scan gate", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		// Flags parsed: a failure from here on (a refusal, an out-of-date
		// review) is not a usage error, so cobra must not print the usage.
		c.SilenceUsage = true
		server := args[0]
		if all && len(tools) > 0 {
			return errReviewAllWithTools
		}
		// Spec 117 PR-2 (#1566): the approval is bound to the review the
		// operator saw — the saved `review show -o json` (--from-review) or
		// the fetch listed in the confirmation prompt below — so a
		// definition that changes before the approval lands fails as out of
		// date instead of being approved unseen.
		var state reviewServerStateResult
		var err error
		if fromReview != "" {
			state, err = reviewStateFromFile(fromReview, server)
		} else {
			state, err = reviewServerState(server)
		}
		if err != nil {
			return err
		}
		if !state.quarantined {
			if len(except) > 0 {
				return fmt.Errorf("--except applies only while approving a quarantined server")
			}
			body := map[string]interface{}{"approve_all": true}
			reachable := reviewHeldTools(state.tools)
			if len(tools) > 0 {
				body = map[string]interface{}{"tools": tools}
				reachable = reviewNamedTools(state.tools, tools)
			}
			if expected := reviewExpectedHashes(state.tools, reachable); expected != nil {
				body["expected_hashes"] = expected
			}
			if state.incarnation > 0 {
				body["expected_incarnation"] = state.incarnation
			}
			if !yes {
				printReviewReachable(server, reachable)
				confirmed, err := confirm(fmt.Sprintf("Approve review for server '%s'?", server))
				if err != nil || !confirmed {
					return reviewDeclined("Approval cancelled.", err)
				}
			}
			return runReviewWrite(server, "tools/approve", body)
		}
		body := map[string]interface{}{"force": force}
		// The binding covers every reviewed tool (blocked ones included;
		// extra names are ignored by the core), {} for an empty review.
		if expected := reviewExpectedHashes(state.tools, state.tools); expected != nil {
			body["expected_hashes"] = expected
		}
		if state.incarnation > 0 {
			body["expected_incarnation"] = state.incarnation
		}
		// Spec 117 FR-026: the approval records the server's tool baseline,
		// so nothing it lists later is auto-approved.
		prompt := fmt.Sprintf("Approve server '%s' without seeing tools? Tools the server lists later will wait for review.", server)
		var summary string
		if len(state.tools) > 0 {
			block, allowed, err := reviewApproveSelection(state.tools, all, tools, except)
			if err != nil {
				return fmt.Errorf("%w for server '%s'", err, server)
			}
			if len(block) > 0 {
				body["block"] = block
			}
			prompt, summary = reviewApproveWording(server, len(state.tools), allowed, block, all)
			if !yes {
				printReviewReachable(server, reviewReachableAfterServerApproval(state.tools, block))
			}
		} else if len(tools) > 0 || len(except) > 0 {
			// Nothing to select from: dropping --tools/--except would approve blind.
			return fmt.Errorf("no tool definitions captured for server '%s'; fetch them first (mcpproxy review fetch %s) before using --tools or --except", server, server)
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
	approve.Flags().BoolVar(&all, "all", false, "Approve every pending or changed tool; tools blocked earlier stay blocked (default: only read-only tools with a clean scan, as on the Web and macOS review screens)")
	approve.Flags().StringSliceVar(&tools, "tools", nil, "Approve exactly these tools and block the rest (trusted server: only these pending or changed tools)")
	approve.Flags().StringSliceVar(&except, "except", nil, "Block these tools while approving the server")
	approve.Flags().BoolVar(&force, "force", false, "Force approval when the scan verdict is dangerous")
	approve.Flags().BoolVar(&yes, "yes", false, "Confirm the approval (without --from-review: approve what the server serves now, still refused if it changes during the call)")
	approve.Flags().StringVar(&fromReview, "from-review", "", "Bind the approval to a review saved with 'mcpproxy review show <server> -o json'; it fails as out of date when a definition changed since")
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
		if err := reviewOutOfDateError(server, resp.StatusCode, response); err != nil {
			return err
		}
		return parseAPIError(response, resp.StatusCode, strings.ReplaceAll(operation, "/", " "))
	}
	if err := formatReviewResponse(ResolveOutputFormat(), response, false); err != nil {
		return err
	}
	warnApprovalHolds(os.Stderr, server, response)
	return nil
}

// warnApprovalHolds prints a warning to w when an approval response reports
// tools that still need review (UX-02): a partial outcome must not read as a
// completed approval. It goes to stderr so JSON/YAML stdout stays parseable.
// Responses from cores that predate the counts carry no fields and print
// nothing.
func warnApprovalHolds(w io.Writer, server string, raw []byte) {
	var envelope struct {
		Data struct {
			StillPending int      `json:"still_pending"`
			StillChanged int      `json:"still_changed"`
			HeldTools    []string `json:"held_tools"`
			Unavailable  bool     `json:"outcome_unavailable"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return
	}
	d := envelope.Data
	if d.Unavailable {
		fmt.Fprintf(w, "Warning: the resulting tool approval state on server '%s' could not be read back; some tools may still need review. Check with: mcpproxy review show %s\n", server, server)
		return
	}
	if d.StillPending+d.StillChanged == 0 {
		return
	}
	fmt.Fprintf(w, "Warning: %d tool(s) on server '%s' still need review (%d pending, %d changed)", d.StillPending+d.StillChanged, server, d.StillPending, d.StillChanged)
	if len(d.HeldTools) > 0 {
		fmt.Fprintf(w, ": %s", strings.Join(d.HeldTools, ", "))
	}
	fmt.Fprintf(w, ". Inspect them with: mcpproxy review show %s\n", server)
}

// approvalCountsSuffix renders the applied approval counts of an approval
// response (UX-02) as ": N tools approved, ..." for table output, or "" when
// the response predates the counts.
func approvalCountsSuffix(value map[string]interface{}) string {
	if counts := approvalCounts(value); counts != "" {
		return ": " + counts
	}
	return ""
}

// approvalCounts renders "N tools approved, B blocked, P still pending, C
// changed", or "" when the response carries no counts.
func approvalCounts(value map[string]interface{}) string {
	approvedCount, ok := value["approved_count"].(float64)
	if !ok {
		return ""
	}
	blocked, _ := value["blocked_count"].(float64)
	pending, _ := value["still_pending"].(float64)
	changed, _ := value["still_changed"].(float64)
	return fmt.Sprintf("%d tools approved, %d blocked, %d still pending, %d changed", int(approvedCount), int(blocked), int(pending), int(changed))
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
// false: a mismatched core fails closed (D43.1).
type reviewToolState struct {
	Name           string          `json:"name"`
	Tier           string          `json:"tier"`
	DefaultAllowed *bool           `json:"default_allowed"`
	ApprovalStatus string          `json:"approval_status"`
	Disabled       bool            `json:"disabled"`
	Description    string          `json:"description"`
	Previous       json.RawMessage `json:"previous"`
	// CurrentHash is the review fingerprint (Spec 117 PR-2): the approval
	// sends it back in expected_hashes. Empty on a core that predates it.
	CurrentHash string `json:"current_hash"`
}

type reviewServerStateResult struct {
	quarantined bool
	// incarnation is the reviewed server's incarnation (Spec 116 FR-017); 0
	// when the core predates it. The approval sends it as expected_incarnation
	// next to expected_hashes: the two refusals (server_replaced,
	// review_out_of_date) stay distinct.
	incarnation int64
	tools       []reviewToolState
}

// reviewExpectedHashes builds the review binding for an approval that makes
// `tools` reachable, from the review `review` the operator saw (the same rule
// as the Web review screen and the macOS tray): every tool of `tools` that
// carries a fingerprint; {} for an empty review; nil (an unbound request)
// only when the review has tools and none carries a fingerprint, i.e. the
// core predates the binding.
func reviewExpectedHashes(review, tools []reviewToolState) map[string]string {
	if len(review) > 0 {
		hashed := false
		for _, t := range review {
			if t.CurrentHash != "" {
				hashed = true
				break
			}
		}
		if !hashed {
			return nil
		}
	}
	out := make(map[string]string, len(tools))
	for _, t := range tools {
		if t.CurrentHash != "" {
			out[t.Name] = t.CurrentHash
		}
	}
	return out
}

// reviewHeldTools is what a trusted server's default approve_all makes
// reachable: every pending or changed tool.
func reviewHeldTools(tools []reviewToolState) []reviewToolState {
	var out []reviewToolState
	for _, t := range tools {
		if t.ApprovalStatus == "pending" || t.ApprovalStatus == "changed" {
			out = append(out, t)
		}
	}
	return out
}

// reviewNamedTools returns the review entries of names (unknown names are
// left to the core, which reports them as not approved).
func reviewNamedTools(tools []reviewToolState, names []string) []reviewToolState {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []reviewToolState
	for _, t := range tools {
		if want[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// reviewReachableAfterServerApproval is what a server approval with block
// makes callable: pending tools it promotes and enabled approved tools it
// activates (changed tools stay held, blocked ones disabled).
func reviewReachableAfterServerApproval(tools []reviewToolState, block []string) []reviewToolState {
	blocked := make(map[string]bool, len(block))
	for _, n := range block {
		blocked[n] = true
	}
	var out []reviewToolState
	for _, t := range tools {
		if blocked[t.Name] {
			continue
		}
		if t.ApprovalStatus == "pending" || (t.ApprovalStatus == "approved" && !t.Disabled) {
			out = append(out, t)
		}
	}
	return out
}

// printReviewReachable lists, before the confirmation, every tool the
// approval will make reachable, so the binding is to a review the operator
// actually saw (Spec 117 FR-015).
func printReviewReachable(server string, tools []reviewToolState) {
	// stdout in table mode, stderr otherwise so JSON/YAML stdout stays parseable.
	var w io.Writer = os.Stdout
	if ResolveOutputFormat() != "table" {
		w = os.Stderr
	}
	if len(tools) == 0 {
		fmt.Fprintf(w, "This approval makes no tool of server '%s' callable.\n", server)
		return
	}
	fmt.Fprintf(w, "This approval makes %d tool(s) of server '%s' callable:\n", len(tools), server)
	for _, t := range tools {
		marker := ""
		if t.ApprovalStatus == "changed" || (len(t.Previous) > 0 && string(t.Previous) != "null") {
			marker = " [changed since approval]"
		}
		desc := strings.TrimSpace(strings.SplitN(t.Description, "\n", 2)[0])
		fmt.Fprintf(w, "  - %s (%s, %s)%s: %s\n", t.Name, t.Tier, t.ApprovalStatus, marker, desc)
	}
}

// reviewOutOfDateHint is printed after the core's out-of-date message.
func reviewOutOfDateHint(server string) string {
	return fmt.Sprintf("Run 'mcpproxy review show %s' and approve again.", server)
}

// reviewOutOfDateError maps the core's 409 review_out_of_date (Spec 117
// FR-008) to an exit-1 error carrying the core's message and the hint. Under
// -o json/yaml the error envelope is printed to stdout first. It returns nil
// for any other response.
func reviewOutOfDateError(server string, status int, body []byte) error {
	if status != http.StatusConflict {
		return nil
	}
	var envelope struct {
		Error     string `json:"error"`
		ErrorCode string `json:"error_code"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil
	}
	if envelope.ErrorCode != "review_out_of_date" && !strings.Contains(envelope.Error, "out of date") {
		return nil
	}
	if format := ResolveOutputFormat(); format != "table" {
		_ = formatAndPrintRaw(format, body)
	}
	return cliRefusalError{fmt.Errorf("%s\n%s", envelope.Error, reviewOutOfDateHint(server))}
}

// reviewStateFromFile loads a review saved with `mcpproxy review show
// <server> -o json` (the review data object; the REST success envelope is
// accepted too). The file must describe server; otherwise nothing is sent
// (Spec 117 FR-015, design review r6 #2).
func reviewStateFromFile(path, server string) (reviewServerStateResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return reviewServerStateResult{}, newFlagValidationError("--from-review: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return reviewServerStateResult{}, newFlagValidationError("--from-review %s is not a saved review (mcpproxy review show %s -o json): %v", path, server, err)
	}
	payload := data
	if successRaw, isEnvelope := probe["success"]; isEnvelope {
		var success bool
		if json.Unmarshal(successRaw, &success) != nil || !success || len(probe["data"]) == 0 {
			return reviewServerStateResult{}, newFlagValidationError("--from-review %s holds an error response, not a review", path)
		}
		payload = probe["data"]
	}
	var review struct {
		Server *struct {
			Name        string `json:"name"`
			Quarantined bool   `json:"quarantined"`
			Incarnation int64  `json:"incarnation"`
		} `json:"server"`
		Tools *[]reviewToolState `json:"tools"`
	}
	if err := json.Unmarshal(payload, &review); err != nil || review.Server == nil || review.Tools == nil {
		return reviewServerStateResult{}, newFlagValidationError("--from-review %s is not a saved review (mcpproxy review show %s -o json)", path, server)
	}
	if review.Server.Name != server {
		return reviewServerStateResult{}, newFlagValidationError("--from-review %s is the review of server '%s', not '%s'", path, review.Server.Name, server)
	}
	return reviewServerStateResult{quarantined: review.Server.Quarantined, incarnation: review.Server.Incarnation, tools: *review.Tools}, nil
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
				Quarantined bool  `json:"quarantined"`
				Incarnation int64 `json:"incarnation"`
			} `json:"server"`
			Tools []reviewToolState `json:"tools"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return reviewServerStateResult{}, err
	}
	return reviewServerStateResult{quarantined: envelope.Data.Server.Quarantined, incarnation: envelope.Data.Server.Incarnation, tools: envelope.Data.Tools}, nil
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
func reviewApproveWording(server string, total, allowed int, block []string, all bool) (prompt, summary string) {
	noun := func(n int) string {
		if n == 1 {
			return "tool"
		}
		return "tools"
	}
	if len(block) == 0 && all {
		// --all approves pending or changed tools only; tools blocked earlier
		// stay blocked, so "N of N; blocking none" would overstate it.
		return fmt.Sprintf("Approve server '%s' with all %d %s?", server, total, noun(total)),
			"Allowing all pending or changed tools; previously blocked tools stay blocked"
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
		if counts := approvalCounts(value); counts != "" {
			message += " (server now: " + counts + ")"
		}
		fmt.Println(message)
		return nil
	}
	if status, _ := value["status"].(string); status != "" {
		if serverName, _ := value["server_name"].(string); serverName != "" {
			fmt.Printf("%s server %s%s\n", strings.ToUpper(status[:1])+status[1:], serverName, approvalCountsSuffix(value))
			return nil
		}
	}
	if servers, ok := value["servers"].([]interface{}); ok {
		rows := make([][]string, 0, len(servers))
		for _, item := range servers {
			row, _ := item.(map[string]interface{})
			// A core that predates the field omits "enabled"; read that as enabled.
			enabled := "true"
			if v, ok := row["enabled"].(bool); ok {
				enabled = fmt.Sprint(v)
			}
			rows = append(rows, []string{fmt.Sprint(row["server"]), fmt.Sprint(row["kind"]), fmt.Sprint(row["quarantined"]), enabled, fmt.Sprint(row["pending"]), fmt.Sprint(row["changed"]), fmt.Sprint(row["tier_counts"]), fmt.Sprint(row["scan"])})
		}
		out, err := table.FormatTable([]string{"SERVER", "KIND", "QUARANTINED", "ENABLED", "PENDING", "CHANGED", "TIERS", "SCAN"}, rows)
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
					desc += "\nChanges from the previous approved definition:\n" + reviewDiffText(diff)
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
	printReviewChangedTools(os.Stdout, value)
	return nil
}

// reviewDiffSections is the order and heading of the sections of a changed tool's diff.
var reviewDiffSections = []struct{ key, heading string }{
	{"description", "Description"},
	{"input_schema", "Input schema"},
	{"output_schema", "Output schema"},
	{"annotations", "Safety hints"},
}

// reviewDiffText renders the per-section diff of a changed tool; a section
// that did not change is left out. A core that sends a single string is
// printed as it is.
func reviewDiffText(diff interface{}) string {
	switch d := diff.(type) {
	case string:
		return strings.TrimSpace(d)
	case map[string]interface{}:
		var b strings.Builder
		for _, section := range reviewDiffSections {
			text, _ := d[section.key].(string)
			if strings.TrimSpace(text) == "" {
				continue
			}
			fmt.Fprintf(&b, "  %s:\n", section.heading)
			for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
				fmt.Fprintf(&b, "    %s\n", line)
			}
		}
		return strings.TrimRight(b.String(), "\n")
	}
	return ""
}

// printReviewChangedTools prints, for every tool held as changed since its
// approval, what changed against the approved definition, then how to approve
// the update and the per-server opt-out for servers the operator trusts to
// update freely (maintainer decision 2026-10-11: scan mode holds every
// changed contract; the friction is the diff and a one-step approval).
func printReviewChangedTools(w io.Writer, value map[string]interface{}) {
	tools, _ := value["tools"].([]interface{})
	var changed []string
	server, _ := value["server"].(map[string]interface{})
	for _, item := range tools {
		tool, _ := item.(map[string]interface{})
		if tool["approval_status"] != "changed" {
			continue
		}
		name := fmt.Sprint(tool["name"])
		changed = append(changed, name)
		fmt.Fprintf(w, "\nChanged since approval: %s\n", name)
		if text := reviewDiffText(tool["diff"]); text != "" {
			fmt.Fprintln(w, text)
		} else {
			fmt.Fprintln(w, "  (the approved definition is not stored; compare with 'mcpproxy review show --full')")
		}
	}
	if len(changed) == 0 {
		return
	}
	name := fmt.Sprint(server["name"])
	fmt.Fprintf(w, "\n%d tool(s) are held until you approve the update: mcpproxy review approve %s --tools %s\n", len(changed), name, strings.Join(changed, ","))
	fmt.Fprintf(w, "To let a server you trust update its tools without review, set auto_approve_tool_changes: true on it (https://docs.mcpproxy.app/features/security-quarantine).\n")
}

// reviewScanLine renders the scan coverage of `review show` in the same words
// as the Web and macOS review screens. It returns "" when the payload carries
// no scan or predates coverage.
func reviewScanLine(server map[string]interface{}) string {
	scan, hasScan := server["scan"].(map[string]interface{})
	if !hasScan {
		return ""
	}
	coverage, _ := scan["coverage"].(string)
	if coverage == "" {
		// An older core sends no coverage: say "none" like the Web and macOS screens.
		return "Scan: none"
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
		return "Scan: not checked against tool definitions: they have not been captured yet; fetch them with: mcpproxy review fetch " + fmt.Sprint(server["name"]) + " (or Fetch tool definitions on the Web or macOS review screen)"
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

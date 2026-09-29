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
		if !yes {
			confirmed, err := confirm(fmt.Sprintf("Approve review for server '%s'?", args[0]))
			if err != nil {
				return err
			}
			if !confirmed {
				fmt.Println("Approval cancelled.")
				return nil
			}
		}
		quarantined, err := reviewServerQuarantined(args[0])
		if err != nil {
			return err
		}
		if quarantined {
			if len(tools) > 0 {
				return fmt.Errorf("--tools cannot select a quarantined server approval; use --except to block tools")
			}
			body := map[string]interface{}{"force": force}
			if len(except) > 0 {
				body["block"] = except
			}
			return runReviewWrite(args[0], "security/approve", body)
		}
		if len(except) > 0 {
			return fmt.Errorf("--except applies only while approving a quarantined server")
		}
		body := map[string]interface{}{"approve_all": true}
		if len(tools) > 0 {
			body = map[string]interface{}{"tools": tools}
		}
		return runReviewWrite(args[0], "tools/approve", body)
	}}
	approve.Flags().StringSliceVar(&tools, "tools", nil, "Approve only these pending or changed tools on a trusted server")
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

func reviewServerQuarantined(server string) (bool, error) {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/servers/"+url.PathEscape(server)+"/review", nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, parseAPIError(body, resp.StatusCode, "read review")
	}
	var envelope struct {
		Data struct {
			Server struct {
				Quarantined bool `json:"quarantined"`
			} `json:"server"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false, err
	}
	return envelope.Data.Server.Quarantined, nil
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

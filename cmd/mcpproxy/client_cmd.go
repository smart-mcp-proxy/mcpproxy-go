package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/spf13/cobra"
)

// GetClientCommand exposes the same presence DTO as the Clients hub. Keeping
// this daemon-backed avoids a second, stale interpretation of client state in
// the CLI.
func GetClientCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "client", Short: "Inspect connected AI clients"}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List client presence", RunE: runClientList})
	cmd.AddCommand(&cobra.Command{Use: "show <id>", Short: "Show one client and its sessions", Args: cobra.ExactArgs(1), RunE: runClientShow})
	return cmd
}

func runClientList(_ *cobra.Command, _ []string) error {
	return runClientPath("/api/v1/clients", false)
}
func runClientShow(_ *cobra.Command, args []string) error {
	return runClientPath("/api/v1/clients/"+args[0], true)
}

func runClientPath(path string, detail bool) error {
	cfg, err := loadCLIConfig(configFile)
	if err != nil {
		return err
	}
	client, ok := newDaemonClient(cfg, nil)
	if !ok {
		return fmt.Errorf("client requires running daemon. Start with: mcpproxy serve")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := client.DoRaw(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned status %d: %s", response.StatusCode, string(body))
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   string          `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	if !envelope.Success {
		return fmt.Errorf("%s", envelope.Error)
	}
	formatter, err := GetOutputFormatter()
	if err != nil {
		return clioutput.NewStructuredError(clioutput.ErrCodeInvalidOutputFormat, err.Error())
	}
	if ResolveOutputFormat() != "table" {
		out, err := formatter.Format(json.RawMessage(envelope.Data))
		if err != nil {
			return err
		}
		fmt.Println(out)
		return nil
	}
	if detail {
		var row struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			State       string `json:"state"`
			ConfigPath  string `json:"config_path"`
			ReloadHint  string `json:"reload_hint"`
			Sessions    []struct {
				ID string `json:"id"`
			} `json:"sessions"`
		}
		if err := json.Unmarshal(envelope.Data, &row); err != nil {
			return err
		}
		fmt.Printf("Client: %s\nState: %s\nConfig path: %s\nReload: %s\nSessions: %d\n", row.DisplayName, row.State, row.ConfigPath, row.ReloadHint, len(row.Sessions))
		return nil
	}
	var list struct {
		Clients []struct {
			DisplayName    string     `json:"display_name"`
			State          string     `json:"state"`
			LastSeen       *time.Time `json:"last_seen"`
			ActiveSessions int        `json:"active_sessions"`
			Calls24h       int        `json:"calls_24h"`
			DisplayPath    string     `json:"display_path"`
		} `json:"clients"`
	}
	if err := json.Unmarshal(envelope.Data, &list); err != nil {
		return err
	}
	rows := make([][]string, len(list.Clients))
	for i, row := range list.Clients {
		last := "Never"
		if row.LastSeen != nil {
			last = row.LastSeen.Format(time.RFC3339)
		}
		rows[i] = []string{row.DisplayName, row.State, last, fmt.Sprint(row.ActiveSessions), fmt.Sprint(row.Calls24h), row.DisplayPath}
	}
	out, err := formatter.FormatTable([]string{"CLIENT", "STATE", "LAST SEEN", "SESSIONS", "CALLS 24H", "CONFIG PATH"}, rows)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

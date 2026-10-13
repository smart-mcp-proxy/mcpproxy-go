package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// GetClientCommand exposes the same presence DTO as the Clients hub. Keeping
// this daemon-backed avoids a second, stale interpretation of client state in
// the CLI. Spec 108-g adds the binding subcommands (client_binding_cmd.go) and
// appends the credential/binding columns to list and show.
func GetClientCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "client", Short: "Inspect connected AI clients and manage their profile bindings"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List client presence",
		Long: `List every known client with its presence, credential and profile binding.
--profile filters by the client's current binding ("-" is All servers).

Examples:
  mcpproxy client list
  mcpproxy client list --profile work-readonly
  mcpproxy client list --client cursor -o json`,
		Args: cobra.NoArgs,
		RunE: runClientList,
	}
	list.Flags().String("profile", "", `Only clients currently bound to this profile ("-" = All servers)`)
	list.Flags().String("client", "", "Only the client with this id")

	cmd.AddCommand(list)
	cmd.AddCommand(&cobra.Command{Use: "show <id>", Short: "Show one client and its sessions", Args: clientShowArgs, RunE: runClientShow})
	addClientBindingCommands(cmd)
	silenceUsageOnRun(cmd)
	return cmd
}

// clientShowArgs lets the inherited --help-json hook run without a positional
// id. Cobra validates Args before PersistentPreRunE, so ExactArgs(1) would
// otherwise make machine-readable discovery of this command impossible.
func clientShowArgs(cmd *cobra.Command, args []string) error {
	if helpJSON, err := cmd.Flags().GetBool("help-json"); err == nil && helpJSON {
		return nil
	}
	return cobra.ExactArgs(1)(cmd, args)
}

// clientRow is the part of a client row the CLI tables render (109-h presence
// plus the 108-f binding decoration).
type clientRow struct {
	ID              string     `json:"id"`
	DisplayName     string     `json:"display_name"`
	Kind            string     `json:"kind"`
	State           string     `json:"state"`
	ConfigPath      string     `json:"config_path"`
	DisplayPath     string     `json:"display_path"`
	ReloadHint      string     `json:"reload_hint"`
	LastSeen        *time.Time `json:"last_seen"`
	ActiveSessions  int        `json:"active_sessions"`
	Calls24h        int        `json:"calls_24h"`
	Blocked24h      int        `json:"blocked_24h"`
	CredentialState string     `json:"credential_state"`
	TokenName       string     `json:"token_name"`
	Profile         string     `json:"profile"`
	ProfileTitle    string     `json:"profile_title"`
	ProfileMode     string     `json:"profile_mode"`
	ProfileSource   string     `json:"profile_source"`
	ProfileMissing  bool       `json:"profile_missing"`
	ExpiresAt       *time.Time `json:"expires_at"`
	RotationPending bool       `json:"rotation_pending"`
	Sessions        []struct {
		ID            string    `json:"id"`
		WorkSessionID string    `json:"work_session_id"`
		StartedAt     time.Time `json:"started_at"`
		LastActivity  time.Time `json:"last_activity"`
		Profile       string    `json:"profile"`
		ProfileSource string    `json:"profile_source"`
	} `json:"sessions"`
}

// bound reports whether the row carries an active client credential (a binding
// exists only then).
func (r clientRow) bound() bool { return r.CredentialState == "client" }

// profileCell is the PROFILE column: the bound profile, "all" for All servers,
// "-" when the client holds no client credential.
func (r clientRow) profileCell() string {
	if !r.bound() {
		return "-"
	}
	name := r.Profile
	if name == "" {
		name = "all"
	}
	if r.ProfileMissing {
		name += " (missing)"
	}
	return name
}

// profileLine is `client show`'s Profile line.
func (r clientRow) profileLine() string {
	label := "all servers"
	if r.Profile != "" {
		label = r.Profile
		if r.ProfileTitle != "" {
			label = fmt.Sprintf("%s (%s)", r.ProfileTitle, r.Profile)
		}
	}
	parts := []string{label}
	if r.ProfileMode != "" {
		parts = append(parts, r.ProfileMode)
	}
	if r.ProfileSource != "" {
		parts = append(parts, "source "+r.ProfileSource)
	}
	line := strings.Join(parts, ", ")
	if r.ProfileMissing {
		line += " — profile no longer exists; the client is denied everything"
	}
	return line
}

func runClientList(cmd *cobra.Command, _ []string) error {
	q := url.Values{}
	if cmd != nil {
		for _, name := range []string{"profile", "client"} {
			if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
				q.Set(name, f.Value.String())
			}
		}
	}
	path := "/api/v1/clients"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	data, err := restDo("client", http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if structuredOutput() {
		return printData(data)
	}
	var list struct {
		Clients  []clientRow       `json:"clients"`
		Warnings []runtime.Warning `json:"warnings"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	rows := make([][]string, len(list.Clients))
	for i, row := range list.Clients {
		last := "Never"
		if row.LastSeen != nil {
			last = row.LastSeen.Format(time.RFC3339)
		}
		rows[i] = []string{
			row.DisplayName, row.State, last, strconv.Itoa(row.ActiveSessions), strconv.Itoa(row.Calls24h), row.DisplayPath,
			dash(row.CredentialState), row.profileCell(), dash(row.ProfileMode), dash(row.ProfileSource), strconv.Itoa(row.Blocked24h),
		}
	}
	if err := printTable([]string{"CLIENT", "STATE", "LAST SEEN", "SESSIONS", "CALLS 24H", "CONFIG PATH", "CREDENTIAL", "PROFILE", "MODE", "SOURCE", "BLOCKED 24H"}, rows); err != nil {
		return err
	}
	printWarnings(list.Warnings)
	return nil
}

func runClientShow(_ *cobra.Command, args []string) error {
	data, err := restDo("client", http.MethodGet, "/api/v1/clients/"+url.PathEscape(args[0]), nil)
	if err != nil {
		return err
	}
	if structuredOutput() {
		return printData(data)
	}
	var row clientRow
	if err := json.Unmarshal(data, &row); err != nil {
		return err
	}
	fmt.Printf("Client: %s\nState: %s\nConfig path: %s\nReload: %s\n", row.DisplayName, row.State, row.ConfigPath, row.ReloadHint)
	if row.CredentialState != "" {
		credential := row.CredentialState
		switch {
		case row.TokenName != "":
			credential += " (token " + row.TokenName + ")"
		case row.CredentialState == "admin_key":
			credential += " (holds the admin API key; fix: mcpproxy client upgrade-admin-key-holders)"
		}
		fmt.Printf("Credential: %s\n", credential)
		if row.bound() {
			fmt.Printf("Profile: %s\n", row.profileLine())
		}
		if row.ExpiresAt != nil && row.bound() {
			fmt.Printf("Expires: %s\n", row.ExpiresAt.Format(time.RFC3339))
		}
		if row.RotationPending {
			fmt.Printf("Rotation: pending (finish with: mcpproxy client rotate %s --finalize)\n", row.ID)
		}
		fmt.Printf("Blocked 24h: %d\n", row.Blocked24h)
	}
	fmt.Printf("Sessions: %d\n", len(row.Sessions))
	if len(row.Sessions) > 0 {
		rows := make([][]string, 0, len(row.Sessions))
		for _, session := range row.Sessions {
			rows = append(rows, []string{
				session.ID, session.WorkSessionID, session.StartedAt.Format(time.RFC3339), session.LastActivity.Format(time.RFC3339),
				dash(session.Profile), dash(session.ProfileSource),
			})
		}
		return printTable([]string{"SESSION", "WORK SESSION", "STARTED AT", "LAST ACTIVITY", "PROFILE", "SOURCE"}, rows)
	}
	return nil
}

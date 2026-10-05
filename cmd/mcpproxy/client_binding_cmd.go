package main

// Binding subcommands of `mcpproxy client` (Spec 108-g, FR-025, FR-026,
// FR-021, FR-021a): set-profile, lock, unlock, add, rotate,
// upgrade-admin-key-holders, forget. Each is a thin client of the /clients
// routes; the FR-008a guard refusal, the credential preconditions and the
// validators all live server-side.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// adminKeyDocsURL explains rotating api_key (published docs page).
const adminKeyDocsURL = "https://docs.mcpproxy.app/configuration/config-file"

func addClientBindingCommands(parent *cobra.Command) {
	parent.AddCommand(newClientSetProfileCmd(), newClientLockCmd(true), newClientLockCmd(false),
		newClientAddCmd(), newClientRotateCmd(), newClientUpgradeAdminKeyHoldersCmd(), newClientForgetCmd())
}

func clientPath(id string) string { return "/api/v1/clients/" + url.PathEscape(id) }

// profileArg turns the command-line profile into the wire value: `all` is
// All servers (the empty profile) and is never sent as a name.
func profileArg(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), "all") {
		return ""
	}
	return strings.TrimSpace(v)
}

// bindingMode returns the requested mode, or "" when neither flag is given.
func bindingMode(lock, switchable bool) string {
	switch {
	case lock:
		return auth.ProfileModeLocked
	case switchable:
		return auth.ProfileModeSwitchable
	}
	return ""
}

// clientRefusal renders a refusal of a client operation: the REST text, plus
// the connect hint when the client holds no client credential.
func clientRefusal(ref *restRefusal, err error, id, prof string) error {
	if err != nil {
		return err
	}
	if ref == nil {
		return nil
	}
	text := ref.errorText()
	if ref.Code == profile.ErrorCodeNoClientCredential {
		if prof == "" {
			prof = "<profile>"
		}
		text += fmt.Sprintf("\nhint: mcpproxy connect %s --profile %s", id, prof)
	}
	return flagValidationError{fmt.Errorf("%s", text)}
}

// --- set-profile ---------------------------------------------------------------

func newClientSetProfileCmd() *cobra.Command {
	var from, to string
	var lock, switchable bool
	cmd := &cobra.Command{
		Use:   "set-profile <id> <profile|all>",
		Short: "Bind a client to a profile (or move every client of one profile to another)",
		Long: `Change the profile a client's credential is bound to. <profile|all>: "all" is
All servers (always switchable). Without --lock/--switchable the current mode is
kept. The client's config file is never touched: its credential stays valid.

Bulk form: --from-profile <p|all> --to-profile <p|all> moves every client bound
to the first onto the second; clients without a client credential are skipped
(listed on stderr) and the command still exits 0.

Examples:
  mcpproxy client set-profile cursor work-readonly --lock
  mcpproxy client set-profile cursor all
  mcpproxy client set-profile --from-profile work-full --to-profile work-readonly`,
		Args: argsOrHelpJSON(func(cmd *cobra.Command, args []string) error {
			bulk := cmd.Flags().Changed("from-profile") || cmd.Flags().Changed("to-profile")
			switch {
			case bulk && len(args) > 0:
				return newFlagValidationError("<id> <profile> and --from-profile/--to-profile are mutually exclusive")
			case bulk && (!cmd.Flags().Changed("from-profile") || !cmd.Flags().Changed("to-profile")):
				return newFlagValidationError("--from-profile and --to-profile must be given together")
			case !bulk && len(args) != 2:
				return newFlagValidationError("set-profile takes <id> <profile|all>, or --from-profile and --to-profile")
			}
			return nil
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := bindingMode(lock, switchable)
			sess, err := newRESTSession("client")
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("from-profile") {
				body := map[string]any{"from_profile": profileArg(from), "to_profile": profileArg(to)}
				if mode != "" {
					body["mode"] = mode
				}
				data, ref, err := sess.call(http.MethodPost, "/api/v1/clients/bulk-assign", body)
				if e := clientRefusal(ref, err, "", ""); e != nil {
					return e
				}
				return printBulkAssign(data)
			}
			body := map[string]any{"profile": profileArg(args[1])}
			if mode != "" {
				body["mode"] = mode
			}
			data, ref, err := sess.call(http.MethodPut, clientPath(args[0])+"/binding", body)
			if e := clientRefusal(ref, err, args[0], profileArg(args[1])); e != nil {
				return e
			}
			return printBinding(data, args[0])
		},
	}
	cmd.Flags().StringVar(&from, "from-profile", "", "Bulk: the profile whose clients move (all = All servers)")
	cmd.Flags().StringVar(&to, "to-profile", "", "Bulk: the profile they move to (all = All servers)")
	cmd.Flags().BoolVar(&lock, "lock", false, "Lock the client to the profile: it can never switch")
	cmd.Flags().BoolVar(&switchable, "switchable", false, "Let the client switch within the profile's switchable_to")
	cmd.MarkFlagsMutuallyExclusive("lock", "switchable")
	return cmd
}

// printBinding prints the outcome of a binding write: the client's new binding
// line, with its warnings on stderr.
func printBinding(data json.RawMessage, id string) error {
	if structuredOutput() {
		return printData(data)
	}
	var res struct {
		Client   clientRow         `json:"client"`
		Warnings []runtime.Warning `json:"warnings"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}
	fmt.Printf("Client %s: %s\n", id, res.Client.profileLine())
	printWarnings(res.Warnings)
	return nil
}

func printBulkAssign(data json.RawMessage) error {
	if structuredOutput() {
		return printData(data)
	}
	var res struct {
		Moved   []string          `json:"moved"`
		Skipped []runtime.Skipped `json:"skipped"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}
	fmt.Printf("Moved: %s\n", dash(strings.Join(res.Moved, ", ")))
	for _, s := range res.Skipped {
		fmt.Fprintf(os.Stderr, "skipped %s: %s", s.ClientID, s.Code)
		if s.Error != "" {
			fmt.Fprintf(os.Stderr, " (%s)", s.Error)
		}
		if s.Code == profile.ErrorCodeNoClientCredential {
			fmt.Fprintf(os.Stderr, "\n  fix: mcpproxy connect %s --profile <profile>", s.ClientID)
		}
		fmt.Fprintln(os.Stderr)
	}
	return nil
}

// --- lock / unlock -------------------------------------------------------------

func newClientLockCmd(lock bool) *cobra.Command {
	use, short, mode, example := "unlock <id>", "Let a client switch profiles again", auth.ProfileModeSwitchable,
		"mcpproxy client unlock cursor"
	if lock {
		use, short, mode, example = "lock <id>", "Lock a client to its current profile", auth.ProfileModeLocked,
			"mcpproxy client lock cursor"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Long: short + `. The client keeps its current profile; only the mode changes. The client needs
an active client credential (mcpproxy connect <id> --profile <p>). A client on
All servers cannot be locked: give it a profile first.

Example:
  ` + example,
		Args: argsOrHelpJSON(cobra.ExactArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			sess, err := newRESTSession("client")
			if err != nil {
				return err
			}
			data, ref, err := sess.call(http.MethodGet, clientPath(id), nil)
			if e := clientRefusal(ref, err, id, ""); e != nil {
				return e
			}
			var row clientRow
			if err := json.Unmarshal(data, &row); err != nil {
				return err
			}
			data, ref, err = sess.call(http.MethodPut, clientPath(id)+"/binding", map[string]any{"profile": row.Profile, "mode": mode})
			if e := clientRefusal(ref, err, id, row.Profile); e != nil {
				if ref != nil && lock && ref.Field == "mode" {
					return flagValidationError{fmt.Errorf("%s\nhint: mcpproxy client set-profile %s <profile> --lock", e.Error(), id)}
				}
				return e
			}
			return printBinding(data, id)
		},
	}
}

// --- add -----------------------------------------------------------------------

func newClientAddCmd() *cobra.Command {
	var display, prof, expires string
	var lock, switchable bool
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Add a custom client (a script, a CI job) with its own credential",
		Long: `Create a per-client credential for a client that is not in the connect registry.
The secret is printed once, with a paste-ready config snippet that carries it in
the X-API-Key header. <id> is lower-case letters, digits, '-' or '_' (at most 56
characters) and must not be a supported client's id.

Examples:
  mcpproxy client add dev-laptop --display-name "Dev laptop" --profile work-readonly
  mcpproxy client add ci-runner --expires-in 90d`,
		Args: argsOrHelpJSON(cobra.ExactArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			body := map[string]any{"id": args[0]}
			if display != "" {
				body["display_name"] = display
			}
			if prof != "" {
				body["profile"] = profileArg(prof)
			}
			if mode := bindingMode(lock, switchable); mode != "" {
				body["mode"] = mode
			}
			if expires != "" {
				body["expires_in"] = expires
			}
			sess, err := newRESTSession("client")
			if err != nil {
				return err
			}
			data, ref, err := sess.call(http.MethodPost, "/api/v1/clients", body)
			if e := clientRefusal(ref, err, args[0], profileArg(prof)); e != nil {
				return e
			}
			if structuredOutput() {
				return printData(data)
			}
			var res struct {
				Client     clientRow `json:"client"`
				Credential string    `json:"credential"`
				Snippet    struct {
					GenericHTTP string `json:"generic_http"`
					HeaderName  string `json:"header_name"`
				} `json:"snippet"`
			}
			if err := json.Unmarshal(data, &res); err != nil {
				return err
			}
			fmt.Printf("Added client %s\n", args[0])
			fmt.Printf("Profile: %s\n", res.Client.profileLine())
			printCredentialOnce(res.Credential, res.Snippet.HeaderName, res.Snippet.GenericHTTP)
			return nil
		},
	}
	cmd.Flags().StringVar(&display, "display-name", "", "Human-readable name")
	cmd.Flags().StringVar(&prof, "profile", "", "Profile to bind (all = All servers)")
	cmd.Flags().BoolVar(&lock, "lock", false, "Lock the client to its profile (default for a named profile)")
	cmd.Flags().BoolVar(&switchable, "switchable", false, "Let the client switch within the profile's switchable_to")
	cmd.Flags().StringVar(&expires, "expires-in", "", `Credential lifetime, e.g. "90d" or "720h" (default and cap 365d)`)
	cmd.MarkFlagsMutuallyExclusive("lock", "switchable")
	return cmd
}

// printCredentialOnce prints a freshly minted secret to stdout (never a log).
func printCredentialOnce(credential, header, snippet string) {
	if header == "" {
		header = "X-API-Key"
	}
	fmt.Printf("Credential: %s\n", credential)
	fmt.Println("Store it now; it is not shown again.")
	fmt.Printf("Config snippet (header %s):\n%s\n", header, snippet)
}

// --- rotate --------------------------------------------------------------------

func newClientRotateCmd() *cobra.Command {
	var finalize, yes bool
	cmd := &cobra.Command{
		Use:   "rotate <id>",
		Short: "Replace a client's credential without cutting it off mid-flight",
		Long: `Rotate a client's credential. A supported client is rewritten through connect:
the command previews the change to its config file, asks to confirm (or --yes),
writes it and finalizes; if the write fails the old secret keeps working. A
custom client gets the new secret printed once and stays in a pending rotation
(both secrets work) until --finalize or 24 hours.

Examples:
  mcpproxy client rotate cursor --yes
  mcpproxy client rotate dev-laptop
  mcpproxy client rotate dev-laptop --finalize`,
		Args: argsOrHelpJSON(cobra.ExactArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			return runClientRotate(args[0], finalize, yes)
		},
	}
	cmd.Flags().BoolVar(&finalize, "finalize", false, "Finish a pending rotation: the old secret stops working")
	cmd.Flags().BoolVar(&yes, "yes", false, "Do not ask for confirmation of the config change")
	return cmd
}

func runClientRotate(id string, finalize, yes bool) error {
	sess, err := newRESTSession("client")
	if err != nil {
		return err
	}
	if finalize {
		data, ref, err := sess.call(http.MethodPost, clientPath(id)+"/rotate/finalize", map[string]any{})
		if e := clientRefusal(ref, err, id, ""); e != nil {
			return e
		}
		if structuredOutput() {
			return printData(data)
		}
		fmt.Println("Rotation: finalized")
		return nil
	}
	data, ref, err := sess.call(http.MethodGet, clientPath(id), nil)
	if e := clientRefusal(ref, err, id, ""); e != nil {
		return e
	}
	var row clientRow
	if err := json.Unmarshal(data, &row); err != nil {
		return err
	}
	if row.Kind != "supported" {
		return rotateCustom(sess, id)
	}

	// Supported: preview the config write, confirm, rotate bound to the preview.
	data, ref, err = sess.call(http.MethodGet, "/api/v1/connect/"+url.PathEscape(id)+"/preview", nil)
	if e := clientRefusal(ref, err, id, ""); e != nil {
		return e
	}
	var preview connect.ConnectPreview
	if err := json.Unmarshal(data, &preview); err != nil {
		return err
	}
	if preview.ConnectRefusal != "" {
		return flagValidationError{fmt.Errorf("%s", preview.ConnectRefusal)}
	}
	if !structuredOutput() {
		printRotationPreview(row, preview)
	}
	ok, err := confirmYes(fmt.Sprintf("Rewrite the %s entry in %s and rotate its credential?", row.DisplayName, preview.DisplayPath), yes)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("Cancelled; nothing changed.")
		return nil
	}
	body := map[string]any{}
	if preview.PreconditionToken != "" {
		body["precondition_token"] = preview.PreconditionToken
	}
	data, ref, err = sess.callWithTimeout(restLongTimeout, http.MethodPost, clientPath(id)+"/rotate", body)
	if ref != nil && ref.Code == "precondition_failed" {
		return flagValidationError{fmt.Errorf("the client config changed since the preview; run the command again")}
	}
	if e := clientRefusal(ref, err, id, ""); e != nil {
		return e
	}
	if structuredOutput() {
		return printData(data)
	}
	var res struct {
		Connect  connect.ConnectResult `json:"connect"`
		Rotation struct {
			State string `json:"state"`
		} `json:"rotation"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}
	fmt.Printf("Rotation: %s\n", res.Rotation.State)
	if res.Connect.Credential != "" {
		fmt.Printf("Credential: %s\n", res.Connect.Credential)
	}
	if res.Connect.ReloadHint != "" {
		fmt.Printf("Next: %s\n", res.Connect.ReloadHint)
	}
	return nil
}

func printRotationPreview(row clientRow, p connect.ConnectPreview) {
	fmt.Printf("Client: %s\n", row.DisplayName)
	fmt.Printf("Config: %s\n", p.DisplayPath)
	if p.EntryText != "" {
		fmt.Printf("New entry (credential masked):\n%s\n", p.EntryText)
	}
	fmt.Printf("Binding kept: %s\n", row.profileLine())
}

func rotateCustom(sess *restSession, id string) error {
	data, ref, err := sess.call(http.MethodPost, clientPath(id)+"/rotate", map[string]any{})
	if e := clientRefusal(ref, err, id, ""); e != nil {
		return e
	}
	if structuredOutput() {
		return printData(data)
	}
	var res struct {
		Credential string `json:"credential"`
		Snippet    struct {
			GenericHTTP string `json:"generic_http"`
			HeaderName  string `json:"header_name"`
		} `json:"snippet"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}
	printCredentialOnce(res.Credential, res.Snippet.HeaderName, res.Snippet.GenericHTTP)
	fmt.Printf("Rotation pending: both secrets work until 'mcpproxy client rotate %s --finalize' or 24 h\n", id)
	return nil
}

// --- upgrade-admin-key-holders -------------------------------------------------

func newClientUpgradeAdminKeyHoldersCmd() *cobra.Command {
	var prof string
	var lock, switchable, yes bool
	cmd := &cobra.Command{
		Use:   "upgrade-admin-key-holders",
		Short: "Give every client that holds the admin API key its own credential",
		Long: `Find every supported client whose config still holds the instance admin API key,
preview the config change for each, and (after confirmation or --yes) replace the
key with a per-client credential. --profile binds them all to one profile; when
that binding would be bypassable while require_mcp_auth is off the command
exits 1 after the preview and changes nothing. Afterwards rotate the admin key.

Examples:
  mcpproxy client upgrade-admin-key-holders
  mcpproxy client upgrade-admin-key-holders --profile work-readonly --lock --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body := map[string]any{}
			if cmd.Flags().Changed("profile") {
				body["profile"] = profileArg(prof)
			}
			if mode := bindingMode(lock, switchable); mode != "" {
				body["mode"] = mode
			}
			return runUpgradeAdminKeyHolders(body, yes)
		},
	}
	cmd.Flags().StringVar(&prof, "profile", "", "Bind every upgraded client to this profile (all = All servers)")
	cmd.Flags().BoolVar(&lock, "lock", false, "Lock the upgraded clients to the profile")
	cmd.Flags().BoolVar(&switchable, "switchable", false, "Make the upgraded clients switchable")
	cmd.Flags().BoolVar(&yes, "yes", false, "Do not ask for confirmation")
	cmd.MarkFlagsMutuallyExclusive("lock", "switchable")
	return cmd
}

func runUpgradeAdminKeyHolders(body map[string]any, yes bool) error {
	sess, err := newRESTSession("client")
	if err != nil {
		return err
	}
	data, ref, err := sess.callWithTimeout(restLongTimeout, http.MethodPost, "/api/v1/clients/upgrade-admin-key-holders", body)
	if e := clientRefusal(ref, err, "", ""); e != nil {
		return e
	}
	var preview runtime.UpgradePreview
	if err := json.Unmarshal(data, &preview); err != nil {
		return err
	}
	if structuredOutput() && (preview.Guard != nil || len(preview.Preview) == 0) {
		if err := printData(data); err != nil {
			return err
		}
	}
	if preview.Guard != nil {
		if !structuredOutput() {
			printUpgradePreviewTable(preview)
		}
		return (&restRefusal{Error: guardRefusalText(preview.Guard.Bindings), Code: preview.Guard.Code,
			Bindings: preview.Guard.Bindings, Fixes: preview.Guard.Fixes}).asError()
	}
	if len(preview.Preview) == 0 {
		if !structuredOutput() {
			fmt.Println("No supported client holds the admin key.")
			printAdminKeyRotationStep()
		}
		return nil
	}
	if !structuredOutput() {
		printUpgradePreviewTable(preview)
	}
	ok, err := confirmYes(fmt.Sprintf("Upgrade %d client(s) to their own credential?", len(preview.Preview)), yes)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("Cancelled; nothing changed.")
		return nil
	}
	body["apply"] = true
	body["precondition_token"] = preview.PreconditionToken
	data, ref, err = sess.callWithTimeout(restLongTimeout, http.MethodPost, "/api/v1/clients/upgrade-admin-key-holders", body)
	if ref != nil && ref.Code == "precondition_failed" {
		return flagValidationError{fmt.Errorf("the client configs changed since the preview; run the command again")}
	}
	if e := clientRefusal(ref, err, "", ""); e != nil {
		return e
	}
	var res runtime.UpgradeResult
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}
	if structuredOutput() {
		if err := printData(data); err != nil {
			return err
		}
	} else {
		fmt.Printf("Upgraded: %s\n", dash(strings.Join(res.Upgraded, ", ")))
		if len(res.Failed) > 0 {
			fmt.Println("Failed:")
			for _, f := range res.Failed {
				fmt.Printf("  %s: %s\n", f.ClientID, f.Error)
			}
		}
		if res.NextStep != "" {
			printAdminKeyRotationStep()
		}
	}
	if len(res.Failed) > 0 {
		return flagValidationError{fmt.Errorf("%d client(s) could not be upgraded", len(res.Failed))}
	}
	return nil
}

// guardRefusalText is the byte-stable FR-008a text for a preview-level guard.
func guardRefusalText(bindings []runtime.BindingRef) string {
	return (&runtime.BindingGuardError{Bindings: bindings}).Error()
}

func printUpgradePreviewTable(p runtime.UpgradePreview) {
	rows := make([][]string, 0, len(p.Preview))
	for _, r := range p.Preview {
		rows = append(rows, []string{r.ClientID, dash(r.DisplayPath), r.Credential, profileOrAll(r.Profile), dash(r.Mode)})
	}
	_ = printTable([]string{"CLIENT", "CONFIG PATH", "CREDENTIAL", "PROFILE", "MODE"}, rows)
}

func profileOrAll(p string) string {
	if p == "" {
		return "all"
	}
	return p
}

// printAdminKeyRotationStep prints what remains of the FR-025 remediation once
// every admin-key holder has its own credential: rotating the admin key itself.
func printAdminKeyRotationStep() {
	fmt.Printf("Next: rotate the admin API key (set a new api_key and restart): %s\n", adminKeyDocsURL)
}

// --- forget --------------------------------------------------------------------

func newClientForgetCmd() *cobra.Command {
	var disconnect bool
	cmd := &cobra.Command{
		Use:   "forget <id>",
		Short: "Revoke a client's credential",
		Long: `Revoke a client's credential; it stops authenticating at once. With --disconnect
a supported client's mcpproxy entry is removed from its config first, but the
credential is revoked either way: a failed config write leaves the client cut
off and is reported.

Examples:
  mcpproxy client forget dev-laptop
  mcpproxy client forget cursor --disconnect`,
		Args: argsOrHelpJSON(cobra.ExactArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			path := clientPath(args[0])
			if disconnect {
				path += "?disconnect=true"
			}
			sess, err := newRESTSession("client")
			if err != nil {
				return err
			}
			data, ref, err := sess.call(http.MethodDelete, path, nil)
			if e := clientRefusal(ref, err, args[0], ""); e != nil {
				return e
			}
			if structuredOutput() {
				return printData(data)
			}
			var res struct {
				Revoked         string `json:"revoked"`
				Disconnected    bool   `json:"disconnected"`
				DisconnectError string `json:"disconnect_error"`
			}
			if err := json.Unmarshal(data, &res); err != nil {
				return err
			}
			fmt.Printf("Revoked: %s\n", res.Revoked)
			switch {
			case res.Disconnected:
				fmt.Println("Config entry removed")
			case res.DisconnectError != "":
				fmt.Printf("Config entry kept (disconnect failed: %s)\n", res.DisconnectError)
			case disconnect:
				fmt.Println("Config entry kept")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&disconnect, "disconnect", false, "Also remove the entry from the client's config (supported clients)")
	return cmd
}

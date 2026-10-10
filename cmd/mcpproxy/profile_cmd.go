package main

// `mcpproxy profile ...` (Spec 108-g, FR-036). Every command is a thin client
// of the /api/v1/profiles routes; -o json|yaml prints the REST data object
// exactly.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// argsOrHelpJSON lets the inherited --help-json hook run without positional
// arguments: Cobra validates Args before PersistentPreRunE.
func argsOrHelpJSON(v cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if helpJSON, err := cmd.Flags().GetBool("help-json"); err == nil && helpJSON {
			return nil
		}
		return v(cmd, args)
	}
}

// GetProfileCommand returns the `profile` command group.
func GetProfileCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage profiles: named views over your servers with a tool policy",
		Long: `Create and inspect profiles. A profile names the servers a client or token may
reach and the highest tool tier (read, write, destructive) it may use.

The goal flow in four lines:
  mcpproxy profile create work-readonly --servers github,notion --max-tier read
  mcpproxy client set-profile cursor work-readonly --lock
  mcpproxy token create --name ci --profile work-readonly
  mcpproxy profile show work-readonly --effective

Every command needs a running daemon (mcpproxy serve) and honours -o json|yaml;
JSON output is the REST data object.`,
	}
	cmd.AddCommand(newProfileListCmd(), newProfileShowCmd(), newProfileCreateCmd(), newProfileUpdateCmd(),
		newProfileRenameCmd(), newProfileDeleteCmd(), newProfileClassifyCmd(), newProfileTryCmd(), newProfileAnonymousCmd())
	silenceUsageOnRun(cmd)
	return cmd
}

func profilePath(name string) string { return "/api/v1/profiles/" + url.PathEscape(name) }

// --- list / show -------------------------------------------------------------

func newProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List profiles",
		Long: `List every profile with its effective servers, tool counts by tier, who uses it
and its last-24h traffic. USED BY is shown to administrator credentials only.

Examples:
  mcpproxy profile list
  mcpproxy profile list -o json`,
		Args: cobra.NoArgs,
		RunE: runProfileList,
	}
}

func runProfileList(_ *cobra.Command, _ []string) error {
	data, err := restDo("profile", http.MethodGet, "/api/v1/profiles", nil)
	if err != nil {
		return err
	}
	if structuredOutput() {
		return printData(data)
	}
	var list runtime.ProfileList
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	rows := make([][]string, 0, len(list.Profiles))
	for _, p := range list.Profiles {
		rows = append(rows, []string{
			p.Name, dash(p.Title), shorten(dash(strings.Join(p.EffectiveServers, ",")), 30), dash(p.MaxTier),
			strconv.Itoa(p.ToolCounts.Read), strconv.Itoa(p.ToolCounts.Write), strconv.Itoa(p.ToolCounts.Destructive),
			strconv.Itoa(p.ToolCounts.UnannotatedHidden), usedBySummary(p.UsedBy),
			strconv.Itoa(p.Calls24h), strconv.Itoa(p.Blocked24h),
		})
	}
	return printTable([]string{"NAME", "TITLE", "SERVERS", "MAX TIER", "READ", "WRITE", "DESTR", "UNANNOTATED HIDDEN", "USED BY", "CALLS 24H", "BLOCKED 24H"}, rows)
}

// usedBySummary is the USED BY cell: an em dash when the REST omitted used_by
// (a non-administrator caller), "-" when nothing points at the profile.
func usedBySummary(u *runtime.UsedBy) string {
	if u == nil {
		return "—"
	}
	var parts []string
	if n := len(u.Clients); n > 0 {
		parts = append(parts, fmt.Sprintf("%d client%s", n, plural(n)))
	}
	if n := len(u.Tokens); n > 0 {
		parts = append(parts, fmt.Sprintf("%d token%s", n, plural(n)))
	}
	if u.AnonymousProfile {
		parts = append(parts, "anonymous")
	}
	return dash(strings.Join(parts, ", "))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func shorten(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

type profileShowOpts struct {
	effective bool
	client    string
	server    string
	reason    string
}

func newProfileShowCmd() *cobra.Command {
	o := &profileShowOpts{}
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show one profile, or its effective tools",
		Long: `Show a profile with the effective value of every policy field and where it comes
from. With --effective, list every tool with the verdict of the access chain
(callable, held or hidden) and the reason; a classification that no longer
applies (the tool is now annotated, or gone) is flagged, with the reason taken
from the server so a --server or --reason filter does not change it.

Examples:
  mcpproxy profile show work-readonly
  mcpproxy profile show work-readonly --effective
  mcpproxy profile show work-readonly --effective --client cursor --server github`,
		Args: argsOrHelpJSON(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error { return runProfileShow(cmd, args[0], o) },
	}
	cmd.Flags().BoolVar(&o.effective, "effective", false, "List the profile's tools with their access verdicts")
	cmd.Flags().StringVar(&o.client, "client", "", "With --effective: evaluate this client's credential under the profile (administrators)")
	cmd.Flags().StringVar(&o.server, "server", "", "With --effective: keep only this server's tools")
	cmd.Flags().StringVar(&o.reason, "reason", "", "With --effective: keep only tools with this access reason (administrators)")
	return cmd
}

func runProfileShow(cmd *cobra.Command, name string, o *profileShowOpts) error {
	if !o.effective && (o.client != "" || o.server != "" || o.reason != "") {
		return newFlagValidationError("--client, --server and --reason need --effective")
	}
	if o.effective {
		q := url.Values{}
		for k, v := range map[string]string{"client": o.client, "server": o.server, "reason": o.reason} {
			if v != "" {
				q.Set(k, v)
			}
		}
		path := profilePath(name) + "/effective-tools"
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		data, err := restDo("profile", http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		if structuredOutput() {
			return printData(data)
		}
		return printEffectiveTools(data, o.server != "" || o.reason != "")
	}
	data, err := restDo("profile", http.MethodGet, profilePath(name), nil)
	if err != nil {
		return err
	}
	if structuredOutput() {
		return printData(data)
	}
	var v runtime.ProfileView
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	fmt.Print(renderProfileBlock(v))
	return nil
}

// staleNote is the FR-005 row note for a classify entry that no longer applies.
const (
	staleNoteAnnotated = "classification ignored — tool is now annotated"
	staleNoteMissing   = "classification ignored — tool not found"
	// staleNoteUnknown is printed when the reason cannot be told: a filtered
	// request answered by a daemon that predates stale_classification_reasons.
	staleNoteUnknown = "classification ignored"
)

// staleNote is the note for one stale classification. The server's reason map
// is authoritative (it sees the unfiltered tool set). Without it (an older
// daemon) the listed rows are only a faithful view of the tool set when no
// --server/--reason filter was sent; otherwise the reason is not guessed.
func staleNote(res runtime.EffectiveToolsResult, id string, known map[string]bool, filtered bool) string {
	switch res.StaleClassificationReasons[id] {
	case profile.StaleClassificationAnnotated:
		return staleNoteAnnotated
	case profile.StaleClassificationMissing:
		return staleNoteMissing
	}
	if filtered {
		return staleNoteUnknown
	}
	if known[id] {
		return staleNoteAnnotated
	}
	return staleNoteMissing
}

// heldCount is the number of tools the profile allows but a later gate (tool
// approval, server state) still holds. It is only known to an administrator.
func heldCount(c runtime.EffectiveCounts) int {
	if c.Callable == nil || c.Visible <= *c.Callable {
		return 0
	}
	return c.Visible - *c.Callable
}

// effectiveCountsLine is the header of `profile show --effective`: the profile
// preview is policy-only, so "allowed by profile" is kept apart from "callable".
func effectiveCountsLine(res runtime.EffectiveToolsResult) string {
	line := fmt.Sprintf("Profile: %s (allowed by profile %d, hidden %d", res.Profile, res.Counts.Visible, res.Counts.Hidden)
	if res.Counts.Callable != nil {
		line += fmt.Sprintf(", callable %d, held %d", *res.Counts.Callable, heldCount(res.Counts))
	}
	return line + ")"
}

func printEffectiveTools(data json.RawMessage, filtered bool) error {
	var res runtime.EffectiveToolsResult
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}
	rows := make([][]string, 0, len(res.Tools))
	known := map[string]bool{}
	for _, t := range res.Tools {
		known[t.Server+":"+t.Tool] = true
		access := "hidden"
		switch {
		case t.Access.Callable:
			access = "callable"
		case t.Access.Visible:
			// Allowed by the profile, but a later gate (tool approval, server
			// state) still refuses the call.
			access = "held"
		}
		note := ""
		if t.ClassificationStale {
			note = staleNoteAnnotated
		}
		rows = append(rows, []string{t.Server, t.Tool, t.IntrinsicTier, t.ProfileTier, access, dash(t.Access.Reason), dash(note)})
	}
	fmt.Println(effectiveCountsLine(res))
	if err := printTable([]string{"SERVER", "TOOL", "TIER", "PROFILE TIER", "ACCESS", "REASON", "NOTE"}, rows); err != nil {
		return err
	}
	if res.Counts.Callable == nil {
		// A non-administrator caller: only visible rows are listed.
		fmt.Printf("Hidden: %d\n", res.Counts.Hidden)
	} else if held := heldCount(res.Counts); held > 0 {
		fmt.Printf("%d tool%s held: allowed by the profile but not callable yet. Run: mcpproxy access explain --profile %s --tool <server:tool>\n", held, plural(held), res.Profile)
	}
	if len(res.StaleClassifications) > 0 {
		fmt.Println("Stale classifications:")
		for _, id := range res.StaleClassifications {
			fmt.Printf("  %s: %s\n", id, staleNote(res, id, known, filtered))
		}
	}
	return nil
}

// renderProfileBlock is `profile show`: every policy field with the origin of
// its effective value.
func renderProfileBlock(v runtime.ProfileView) string {
	var b strings.Builder
	title := v.Name
	if v.Title != "" {
		title += " (" + v.Title + ")"
	}
	fmt.Fprintf(&b, "Profile: %s\n", title)
	if v.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", v.Description)
	}
	fmt.Fprintf(&b, "Servers: %s\n", dash(strings.Join(v.Servers, ", ")))
	if strings.Join(v.EffectiveServers, ",") != strings.Join(v.Servers, ",") {
		fmt.Fprintf(&b, "Effective servers: %s\n", dash(strings.Join(v.EffectiveServers, ", ")))
	}
	maxTier := v.MaxTier
	if maxTier == "" {
		maxTier = "none (no cap)"
	}
	fmt.Fprintf(&b, "Max tier: %s\n", maxTier)
	fmt.Fprintf(&b, "Unannotated: %s\n", unannotatedLabel(v))
	fmt.Fprintf(&b, "Code execution: %s\n", codeExecutionLabel(v))
	fmt.Fprintf(&b, "Management tools: %s\n", managementToolsLabel(v))
	switch {
	case v.SwitchableTo == nil:
		b.WriteString("Switchable to: unset (legacy)\n")
	case len(*v.SwitchableTo) == 0:
		b.WriteString("Switchable to: none\n")
	default:
		fmt.Fprintf(&b, "Switchable to: %s\n", strings.Join(*v.SwitchableTo, ", "))
	}
	if v.Tools != nil {
		if len(v.Tools.Allow) > 0 {
			fmt.Fprintf(&b, "Allow: %s\n", strings.Join(v.Tools.Allow, ", "))
		}
		if len(v.Tools.Deny) > 0 {
			fmt.Fprintf(&b, "Deny: %s\n", strings.Join(v.Tools.Deny, ", "))
		}
		for _, k := range sortedKeys(v.Tools.Classify) {
			fmt.Fprintf(&b, "Classify: %s → %s\n", k, v.Tools.Classify[k])
		}
	}
	fmt.Fprintf(&b, "Tools: %d read, %d write, %d destructive, %d unannotated hidden\n",
		v.ToolCounts.Read, v.ToolCounts.Write, v.ToolCounts.Destructive, v.ToolCounts.UnannotatedHidden)
	fmt.Fprintf(&b, "Last 24h: %d calls, %d blocked\n", v.Calls24h, v.Blocked24h)
	if v.IsLegacy {
		b.WriteString("Legacy: yes (no policy fields set; behaves as before Profiles v3)\n")
	}
	if v.UsedBy != nil {
		b.WriteString("Used by: " + usedByLine(v.UsedBy) + "\n")
	}
	return b.String()
}

func usedByLine(u *runtime.UsedBy) string {
	var parts []string
	for _, c := range u.Clients {
		parts = append(parts, fmt.Sprintf("client %s (%s)", c.ID, c.Mode))
	}
	for _, t := range u.Tokens {
		parts = append(parts, "token "+t)
	}
	if u.AnonymousProfile {
		parts = append(parts, "anonymous_profile")
	}
	return dash(strings.Join(parts, ", "))
}

func cappedTier(v runtime.ProfileView) bool {
	return v.MaxTier == config.ProfileTierRead || v.MaxTier == config.ProfileTierWrite
}

func unannotatedLabel(v runtime.ProfileView) string {
	if v.Unannotated != "" {
		return v.Unannotated
	}
	if cappedTier(v) {
		return fmt.Sprintf("%s (default under max tier %s)", v.EffectiveUnannotated, v.MaxTier)
	}
	return v.EffectiveUnannotated + " (default)"
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func codeExecutionLabel(v runtime.ProfileView) string {
	if v.CodeExecution != nil {
		return onOff(*v.CodeExecution)
	}
	if cappedTier(v) {
		return fmt.Sprintf("%s (default under max tier %s)", onOff(v.EffectiveCodeExecution), v.MaxTier)
	}
	return onOff(v.EffectiveCodeExecution) + " (inherited)"
}

func managementToolsLabel(v runtime.ProfileView) string {
	if v.ManagementTools != nil {
		return onOff(*v.ManagementTools)
	}
	return "inherited"
}

// --- create / update ---------------------------------------------------------

func newProfileCreateCmd() *cobra.Command {
	f := &profileFlags{}
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a profile",
		Long: `Create a profile over the listed servers. --max-tier caps the tool tier; under
read or write an unset --unannotated defaults to deny and an unset
--code-execution defaults to off. --switchable-to lists the profiles a session
may switch into ('' means none).

Examples:
  mcpproxy profile create work-readonly --servers github,notion --max-tier read --title "Work Read-only"
  mcpproxy profile create scripts --servers github --code-execution on --unannotated as_write`,
		Args: argsOrHelpJSON(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := config.ProfileConfig{Name: args[0]}
			if err := f.apply(cmd, &p, false); err != nil {
				return err
			}
			if p.Servers == nil {
				p.Servers = []string{}
			}
			return writeProfile(cmd, http.MethodPost, "/api/v1/profiles", "Created", p, nil)
		},
	}
	f.register(cmd, false)
	_ = cmd.MarkFlagRequired("servers")
	return cmd
}

func newProfileUpdateCmd() *cobra.Command {
	f := &profileFlags{}
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Change a profile (read-modify-write)",
		Long: `Change a profile. The command reads the stored profile, applies the flags (set,
then add/remove, then clear) and writes the whole document back. REST has no
ETag, so a concurrent edit between the read and the write is lost (last
writer wins).

Examples:
  mcpproxy profile update work-readonly --add-server linear
  mcpproxy profile update work-readonly --max-tier write --clear-unannotated
  mcpproxy profile update work-readonly --add-deny 'github:delete_*' --switchable-to work-full`,
		Args: argsOrHelpJSON(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := f.checkConflicts(cmd); err != nil {
				return err
			}
			sess, err := newRESTSession("profile")
			if err != nil {
				return err
			}
			p, err := fetchStoredProfile(sess, args[0])
			if err != nil {
				return err
			}
			if err := f.apply(cmd, &p, true); err != nil {
				return err
			}
			return writeProfile(cmd, http.MethodPut, profilePath(args[0]), "Updated", p, sess)
		},
	}
	f.register(cmd, true)
	return cmd
}

// fetchStoredProfile reads GET /profiles/{name} and keeps only the stored
// ProfileConfig fields (never the effective_* values).
func fetchStoredProfile(sess *restSession, name string) (config.ProfileConfig, error) {
	data, ref, err := sess.call(http.MethodGet, profilePath(name), nil)
	if e := refusalError(ref, err); e != nil {
		return config.ProfileConfig{}, e
	}
	var p config.ProfileConfig
	if err := json.Unmarshal(data, &p); err != nil {
		return config.ProfileConfig{}, err
	}
	if p.Name == "" {
		p.Name = name
	}
	if p.Servers == nil {
		p.Servers = []string{}
	}
	return p, nil
}

// writeProfile sends a create or update and prints its outcome.
func writeProfile(_ *cobra.Command, method, path, verb string, p config.ProfileConfig, sess *restSession) error {
	if sess == nil {
		var err error
		if sess, err = newRESTSession("profile"); err != nil {
			return err
		}
	}
	data, ref, err := sess.call(method, path, p)
	if e := refusalError(ref, err); e != nil {
		return e
	}
	if structuredOutput() {
		return printData(data)
	}
	var res runtime.WriteResult
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}
	fmt.Printf("%s profile %s\n", verb, p.Name)
	fmt.Print(renderProfileBlock(res.Profile))
	printValidatorWarnings(res.Warnings)
	return nil
}

// restDo is one request on a fresh session: the data on success, the refusal
// text (exit 1) otherwise.
func restDo(group, method, path string, body any) (json.RawMessage, error) {
	sess, err := newRESTSession(group)
	if err != nil {
		return nil, err
	}
	data, ref, err := sess.call(method, path, body)
	if e := refusalError(ref, err); e != nil {
		return nil, e
	}
	return data, nil
}

// --- rename / delete ---------------------------------------------------------

func newProfileRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a profile and move every client and token that uses it",
		Long: `Rename a profile. Every client binding and token pin that names it moves to the
new name; the command prints what moved.

Examples:
  mcpproxy profile rename work-readonly work-ro`,
		Args: argsOrHelpJSON(cobra.ExactArgs(2)),
		RunE: func(_ *cobra.Command, args []string) error {
			data, err := restDo("profile", http.MethodPost, profilePath(args[0])+"/rename", map[string]string{"new_name": args[1]})
			if err != nil {
				return err
			}
			if structuredOutput() {
				return printData(data)
			}
			var res runtime.RenameResult
			if err := json.Unmarshal(data, &res); err != nil {
				return err
			}
			fmt.Printf("Renamed profile %s → %s\n", args[0], res.Profile.Name)
			printMoved(res.Moved)
			return nil
		},
	}
}

func printMoved(m runtime.MovedRefs) {
	fmt.Printf("Moved clients: %s\n", dash(strings.Join(m.Clients, ", ")))
	fmt.Printf("Moved tokens: %s\n", dash(strings.Join(m.Tokens, ", ")))
}

func newProfileDeleteCmd() *cobra.Command {
	var reassign string
	var force bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a profile",
		Long: `Delete a profile. While clients or tokens use it the command exits 1 and prints
who; pass --reassign-to <profile> to move them, or --force to leave their pins
dangling (they then see nothing). The anonymous_profile can only be deleted
with --reassign-to; --force does not override that.

Examples:
  mcpproxy profile delete old-profile --reassign-to work-readonly
  mcpproxy profile delete scratch --force`,
		Args: argsOrHelpJSON(cobra.ExactArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			q := url.Values{}
			if reassign != "" {
				q.Set("reassign_to", reassign)
			}
			if force {
				q.Set("force", "true")
			}
			path := profilePath(args[0])
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			data, err := restDo("profile", http.MethodDelete, path, nil)
			if err != nil {
				return err
			}
			if structuredOutput() {
				return printData(data)
			}
			var res runtime.DeleteResult
			if err := json.Unmarshal(data, &res); err != nil {
				return err
			}
			fmt.Printf("Deleted profile %s\n", res.Deleted)
			printMoved(res.Moved)
			if res.AnonymousProfileMovedTo != "" {
				fmt.Printf("anonymous_profile moved to %s\n", res.AnonymousProfileMovedTo)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&reassign, "reassign-to", "", "Move every client and token that uses the profile to this profile")
	cmd.Flags().BoolVar(&force, "force", false, "Delete even while in use; pins are left dangling (they then see nothing)")
	return cmd
}

// --- classify ----------------------------------------------------------------

func newProfileClassifyCmd() *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "classify <name> <server:tool> [read|write|destructive]",
		Short: "Give an unannotated tool a tier in a profile",
		Long: `Assign a tier to a tool that declares none (under max tier read or write an
unannotated tool is hidden until classified). --clear removes the entry. A
classification is ignored once the tool carries annotations.

Examples:
  mcpproxy profile classify work-readonly github:search_code read
  mcpproxy profile classify work-readonly github:search_code --clear`,
		Args: argsOrHelpJSON(func(_ *cobra.Command, args []string) error {
			want := 3
			if clear {
				want = 2
			}
			if len(args) != want {
				if clear {
					return newFlagValidationError("classify --clear takes <name> <server:tool>")
				}
				return newFlagValidationError("classify takes <name> <server:tool> read|write|destructive (or --clear)")
			}
			return nil
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, err := newRESTSession("profile")
			if err != nil {
				return err
			}
			p, err := fetchStoredProfile(sess, args[0])
			if err != nil {
				return err
			}
			tool := args[1]
			if clear {
				if p.Tools != nil {
					delete(p.Tools.Classify, tool)
				}
			} else {
				t := ensureTools(&p)
				if t.Classify == nil {
					t.Classify = map[string]string{}
				}
				t.Classify[tool] = strings.TrimSpace(args[2])
			}
			normalizeTools(&p)
			return writeProfile(cmd, http.MethodPut, profilePath(args[0]), "Updated", p, sess)
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "Remove the classification of the tool")
	return cmd
}

// --- try ---------------------------------------------------------------------

func newProfileTryCmd() *cobra.Command {
	var query string
	var limit int
	var sets []string
	cmd := &cobra.Command{
		Use:   "try <name>",
		Short: "Preview what a draft profile would show for a query (nothing is saved)",
		Long: `Run a search as retrieve_tools would under a draft profile and show what it
hides. The draft is the saved profile (or a new empty one when the name does not
exist yet) with --set overrides applied; nothing is persisted.

--set keys are the REST field names: servers, title, description, max_tier,
unannotated, tools.allow, tools.deny, tools.classify.<server:tool>,
code_execution, management_tools, switchable_to. Lists are comma-separated;
code_execution=inherit clears the field.

Examples:
  mcpproxy profile try work-readonly --query issue --set max_tier=write
  mcpproxy profile try newdraft --query issue --set servers=github`,
		Args: argsOrHelpJSON(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if query == "" {
				return newFlagValidationError("--query is required")
			}
			sess, err := newRESTSession("profile")
			if err != nil {
				return err
			}
			draft := config.ProfileConfig{Name: args[0], Servers: []string{}}
			data, ref, err := sess.call(http.MethodGet, profilePath(args[0]), nil)
			switch {
			case err != nil:
				return err
			case ref != nil && ref.Status == http.StatusNotFound:
				// A draft can be tried before it exists.
			case ref != nil:
				return ref.asError()
			default:
				if err := json.Unmarshal(data, &draft); err != nil {
					return err
				}
				if draft.Servers == nil {
					draft.Servers = []string{}
				}
			}
			for _, kv := range sets {
				if err := applyDraftSet(&draft, kv); err != nil {
					return err
				}
			}
			body := map[string]any{"profile": draft, "query": query}
			if cmd.Flags().Changed("limit") {
				body["limit"] = limit
			}
			data, err = func() (json.RawMessage, error) {
				d, r, e := sess.call(http.MethodPost, "/api/v1/profiles/try", body)
				return d, refusalError(r, e)
			}()
			if err != nil {
				return err
			}
			if structuredOutput() {
				return printData(data)
			}
			return printTry(data)
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "Search query (required)")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum results")
	cmd.Flags().StringArrayVar(&sets, "set", nil, "Override a draft field: key=value (repeatable)")
	return cmd
}

func printTry(data json.RawMessage) error {
	var res runtime.TryResult
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}
	rows := make([][]string, 0, len(res.Results))
	for _, item := range res.Results {
		server, tool, tier := tryResultRow(item)
		rows = append(rows, []string{server, tool, tier})
	}
	if err := printTable([]string{"SERVER", "TOOL", "TIER"}, rows); err != nil {
		return err
	}
	fmt.Printf("Hidden by profile: %d\n", res.HiddenByProfile)
	if len(res.Hidden) == 0 {
		return nil
	}
	hidden := make([][]string, 0, len(res.Hidden))
	for _, h := range res.Hidden {
		hidden = append(hidden, []string{h.Server, h.Tool, h.Reason})
	}
	if err := printTable([]string{"SERVER", "TOOL", "REASON"}, hidden); err != nil {
		return err
	}
	if res.HiddenTruncated {
		fmt.Println("… truncated")
	}
	return nil
}

// tryResultRow reads one retrieve_tools hit: {tool: {name, server_name,
// annotations}, score}.
func tryResultRow(item map[string]interface{}) (server, tool, tier string) {
	t, _ := item["tool"].(map[string]interface{})
	name, _ := t["name"].(string)
	server, _ = t["server_name"].(string)
	if i := strings.Index(name, ":"); i > 0 {
		if server == "" {
			server = name[:i]
		}
		name = name[i+1:]
	}
	var ann *config.ToolAnnotations
	if raw, ok := t["annotations"]; ok {
		if b, err := json.Marshal(raw); err == nil {
			var a config.ToolAnnotations
			if json.Unmarshal(b, &a) == nil {
				ann = &a
			}
		}
	}
	return server, name, string(contracts.AnnotationTier(ann))
}

// --- anonymous ---------------------------------------------------------------

func newProfileAnonymousCmd() *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "anonymous [<name>]",
		Short: "Show or set the profile anonymous callers are confined to",
		Long: `With no argument, print the anonymous_profile. With a name, confine callers that
send no credential to that profile. --clear removes the confinement (anonymous
callers see every server while require_mcp_auth is off). A change that would
let a bound client escape its profile is refused (exit 1) with the fixes.

Examples:
  mcpproxy profile anonymous
  mcpproxy profile anonymous work-readonly
  mcpproxy profile anonymous --clear`,
		Args: argsOrHelpJSON(cobra.MaximumNArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			if clear && len(args) > 0 {
				return newFlagValidationError("a profile name and --clear cannot be combined")
			}
			if !clear && len(args) == 0 {
				data, err := restDo("profile", http.MethodGet, "/api/v1/profiles", nil)
				if err != nil {
					return err
				}
				if structuredOutput() {
					return printData(data)
				}
				var list runtime.ProfileList
				if err := json.Unmarshal(data, &list); err != nil {
					return err
				}
				fmt.Println(anonymousLine(list.AnonymousProfile))
				return nil
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			data, err := restDo("profile", http.MethodPatch, "/api/v1/config", map[string]string{"anonymous_profile": name})
			if err != nil {
				return err
			}
			if structuredOutput() {
				return printData(data)
			}
			if msg := configPatchProblems(data); msg != "" {
				return flagValidationError{errors.New(msg)}
			}
			fmt.Println(anonymousLine(name))
			return nil
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "Remove the anonymous_profile")
	return cmd
}

func anonymousLine(name string) string {
	if name == "" {
		return "Anonymous callers: unconfined (all servers when require_mcp_auth is off)"
	}
	return "Anonymous callers: " + name
}

// configPatchProblems reads validation_errors out of a PATCH /config result; a
// non-empty answer means the patch was not applied.
func configPatchProblems(data json.RawMessage) string {
	var res struct {
		Success          *bool `json:"success"`
		ValidationErrors []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"validation_errors"`
	}
	if json.Unmarshal(data, &res) != nil {
		return ""
	}
	if len(res.ValidationErrors) == 0 && (res.Success == nil || *res.Success) {
		return ""
	}
	parts := []string{}
	for _, v := range res.ValidationErrors {
		parts = append(parts, fmt.Sprintf("%s: %s", v.Field, v.Message))
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return "the configuration change was not applied"
	}
	return strings.Join(parts, "\n")
}

package main

// Shared transport for the Spec 108-g commands (profile, client bindings,
// access explain, doctor profile checks). Every command is daemon-only: the
// FR-008a guard delta needs the published (index, snapshot) pair, so there is no
// offline mode. One helper decodes the REST envelope and every refusal field so
// the commands cannot disagree about what a 409 means.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// restRefusal is every field a non-2xx REST body can carry.
type restRefusal struct {
	Status           int
	Error            string
	Code             string
	Field            string
	Action           string
	UsedBy           *runtime.UsedBy
	Bindings         []runtime.BindingRef
	Fixes            []runtime.GuardFix
	ConflictingToken string
	Remediation      string
	Skipped          json.RawMessage
	RequestID        string
}

// restSession is a daemon connection for one command group.
type restSession struct {
	client *cliclient.Client
}

// newRESTSession connects to the running daemon. With none it answers
// "<group> requires running daemon. Start with: mcpproxy serve" (exit 1).
func newRESTSession(group string) (*restSession, error) {
	cfg, err := loadCLIConfig(configFile)
	if err != nil {
		return nil, err
	}
	client, ok := newDaemonClient(cfg, nil)
	if !ok {
		return nil, flagValidationError{fmt.Errorf("%s requires running daemon. Start with: mcpproxy serve", group)}
	}
	return &restSession{client: client}, nil
}

// silenceUsageOnRun keeps Cobra from printing a command's usage block, and the
// error a second time on top of main's "Error: ..." line, after a runtime
// failure (a daemon refusal, a guard 409). PreRun runs only once the arguments
// and flags parsed, so a genuine usage mistake still shows usage.
func silenceUsageOnRun(root *cobra.Command) {
	for _, c := range root.Commands() {
		silenceUsageOnRun(c)
	}
	if root.Run != nil || root.RunE != nil {
		prev := root.PreRun
		root.PreRun = func(cmd *cobra.Command, args []string) {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			if prev != nil {
				prev(cmd, args)
			}
		}
	}
}

// restTimeout bounds an ordinary call; connect-backed calls (rotate, upgrade)
// write client config files and use restLongTimeout.
const (
	restTimeout     = 15 * time.Second
	restLongTimeout = 60 * time.Second
)

// call performs one REST request. A 2xx answer returns the envelope's data; any
// other status returns a refusal (err stays nil) so the caller can render it;
// err is reserved for transport and decoding failures.
func (s *restSession) call(method, path string, body any) (json.RawMessage, *restRefusal, error) {
	return s.callWithTimeout(restTimeout, method, path, body)
}

func (s *restSession) callWithTimeout(timeout time.Duration, method, path string, body any) (json.RawMessage, *restRefusal, error) {
	var payload []byte
	switch b := body.(type) {
	case nil:
	case json.RawMessage:
		payload = b
	case []byte:
		payload = b
	default:
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, nil, fmt.Errorf("encode request: %w", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	resp, err := s.client.DoRaw(ctx, method, path, payload)
	if err != nil {
		return nil, nil, flagValidationError{fmt.Errorf("request failed: %w", err)}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}
	return decodeRESTResponse(resp.StatusCode, raw)
}

// decodeRESTResponse unwraps {success, data, error} and every refusal field.
func decodeRESTResponse(status int, raw []byte) (json.RawMessage, *restRefusal, error) {
	var env struct {
		Success          bool                 `json:"success"`
		Data             json.RawMessage      `json:"data"`
		Error            string               `json:"error"`
		Code             string               `json:"code"`
		Field            string               `json:"field"`
		Action           string               `json:"action"`
		UsedBy           *runtime.UsedBy      `json:"used_by"`
		Bindings         []runtime.BindingRef `json:"bindings"`
		Fixes            []runtime.GuardFix   `json:"fixes"`
		ConflictingToken string               `json:"conflicting_token"`
		Remediation      string               `json:"remediation"`
		Skipped          json.RawMessage      `json:"skipped"`
		RequestID        string               `json:"request_id"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, nil, flagValidationError{fmt.Errorf("unexpected response (HTTP %d): %s", status, strings.TrimSpace(string(raw)))}
	}
	if status >= 200 && status < 300 && env.Success {
		return env.Data, nil, nil
	}
	ref := &restRefusal{
		Status: status, Error: env.Error, Code: env.Code, Field: env.Field, Action: env.Action,
		UsedBy: env.UsedBy, Bindings: env.Bindings, Fixes: env.Fixes,
		ConflictingToken: env.ConflictingToken, Remediation: env.Remediation,
		Skipped: env.Skipped, RequestID: env.RequestID,
	}
	// Connect-backed 409s discriminate on `action` (precondition_failed) rather
	// than `code`.
	if ref.Code == "" {
		ref.Code = ref.Action
	}
	if ref.Error == "" {
		ref.Error = fmt.Sprintf("HTTP %d", status)
	}
	return nil, ref, nil
}

// errorText renders a refusal for a terminal: the message, then whatever the
// refusal carries (used_by table, bindings and fixes, remediation).
func (r *restRefusal) errorText() string {
	var b strings.Builder
	switch {
	case r.Code == profile.ErrorCodeBindingBypassable:
		return renderGuardRefusal(r)
	case r.UsedBy != nil:
		b.WriteString(r.Error)
		b.WriteString("\n")
		b.WriteString(renderUsedBy(r.UsedBy))
		return strings.TrimRight(b.String(), "\n")
	}
	b.WriteString(r.Error)
	if r.Field != "" {
		fmt.Fprintf(&b, " (field: %s)", r.Field)
	}
	if r.ConflictingToken != "" {
		remediation := r.Remediation
		if remediation == "" {
			remediation = fmt.Sprintf("revoke or delete token %s, then try again", r.ConflictingToken)
		}
		b.WriteString("\n" + remediation)
	} else if r.Remediation != "" {
		b.WriteString("\n" + r.Remediation)
	}
	return b.String()
}

// asError wraps the refusal so it always exits 1: the refusal text mentions
// "config" (require_mcp_auth, anonymous_profile), which the string heuristics in
// classifyError would otherwise map to exit 4.
func (r *restRefusal) asError() error {
	return flagValidationError{errors.New(r.errorText())}
}

// refusalError is the standard "command failed" shape: a refusal becomes its
// text; a transport error passes through.
func refusalError(ref *restRefusal, err error) error {
	if err != nil {
		return err
	}
	if ref != nil {
		return ref.asError()
	}
	return nil
}

// renderGuardRefusal prints the FR-008a 409: the byte-stable text, the bindings
// it is about, and the fixes in the order the server offered them.
func renderGuardRefusal(r *restRefusal) string {
	var b strings.Builder
	b.WriteString(r.Error)
	if len(r.Bindings) > 0 {
		b.WriteString("\nBindings:")
		for _, bd := range r.Bindings {
			target := bd.Profile
			if target == "" {
				target = "all"
			}
			fmt.Fprintf(&b, "\n  - %s → %s (%s)", bd.ClientID, target, bd.Mode)
		}
	}
	b.WriteString(renderGuardFixes(r.Fixes, r.Bindings))
	return b.String()
}

func renderGuardFixes(fixes []runtime.GuardFix, bindings []runtime.BindingRef) string {
	if len(fixes) == 0 {
		return ""
	}
	name := ""
	if len(bindings) > 0 {
		name = bindings[0].Profile
	}
	var b strings.Builder
	b.WriteString("\nFixes:")
	for _, fix := range fixes {
		b.WriteString("\n  - ")
		b.WriteString(guardFixLine(fix, name))
	}
	return b.String()
}

// guardFixLine is the ONE wording of a guard fix (connect, profile, client and
// doctor all print it).
func guardFixLine(fix runtime.GuardFix, bindingProfile string) string {
	switch fix.Kind {
	case profile.GuardFixRequireMCPAuth:
		return "set require_mcp_auth: true (config or Settings → Security)"
	case profile.GuardFixSetAnonymousProfile:
		if fix.Target != "" {
			return "mcpproxy profile anonymous " + fix.Target
		}
		if bindingProfile == "" {
			bindingProfile = "<profile>"
		}
		return "mcpproxy profile anonymous <p> with a profile not wider than " + bindingProfile
	default:
		return fix.Kind
	}
}

// renderUsedBy prints what points at a profile.
func renderUsedBy(u *runtime.UsedBy) string {
	rows := [][]string{}
	for _, c := range u.Clients {
		rows = append(rows, []string{"client", c.ID, c.Mode})
	}
	for _, t := range u.Tokens {
		rows = append(rows, []string{"token", t, "pinned"})
	}
	if u.AnonymousProfile {
		rows = append(rows, []string{"anonymous", "anonymous_profile", "-"})
	}
	if len(rows) == 0 {
		return ""
	}
	out, err := tableText([]string{"KIND", "NAME", "MODE"}, rows)
	if err != nil {
		return ""
	}
	return "Used by:\n" + out
}

// --- output -------------------------------------------------------------------

// tableText renders a table through the table formatter regardless of -o (a
// json/yaml run never reaches here).
func tableText(headers []string, rows [][]string) (string, error) {
	f, err := clioutput.NewFormatter("table")
	if err != nil {
		return "", err
	}
	return f.FormatTable(headers, rows)
}

func printTable(headers []string, rows [][]string) error {
	out, err := tableText(headers, rows)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

// structuredOutput reports whether -o json|yaml is in effect.
func structuredOutput() bool { return ResolveOutputFormat() != "table" }

// printData prints the REST data object exactly: json re-indents the raw bytes
// (no Go struct in between, so unknown fields survive); yaml goes through a
// generic value.
func printData(data json.RawMessage) error {
	if len(bytes.TrimSpace(data)) == 0 {
		data = json.RawMessage("null")
	}
	switch ResolveOutputFormat() {
	case "yaml":
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		out, err := yaml.Marshal(v)
		if err != nil {
			return err
		}
		fmt.Print(string(out))
	default:
		var buf bytes.Buffer
		if err := json.Indent(&buf, data, "", "  "); err != nil {
			return err
		}
		fmt.Println(buf.String())
	}
	return nil
}

// printWarnings writes response warnings to stderr. Table mode only: in json and
// yaml mode they are already inside data.
func printWarnings(warnings []runtime.Warning) {
	if structuredOutput() {
		return
	}
	for _, w := range warnings {
		line := fmt.Sprintf("warning [%s]: %s", w.Code, w.Message)
		if fix := warningFix(w); fix != "" {
			line += "\n  fix: " + fix
		}
		fmt.Fprintln(os.Stderr, line)
	}
}

// printValidatorWarnings writes a profile write's validator findings to stderr.
func printValidatorWarnings(warnings []string) {
	if structuredOutput() {
		return
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning: "+w)
	}
}

// warningFix turns a warning's action into the command that fixes it.
func warningFix(w runtime.Warning) string {
	if len(w.Fixes) > 0 {
		var parts []string
		name := ""
		if len(w.Bindings) > 0 {
			name = w.Bindings[0].Profile
		}
		for _, f := range w.Fixes {
			parts = append(parts, guardFixLine(f, name))
		}
		return strings.Join(parts, "; or ")
	}
	if w.Action == nil {
		return ""
	}
	target := w.Action.Target
	switch w.Action.Kind {
	case string(profile.FixChangeSetting):
		if target == "require_mcp_auth" {
			return "set require_mcp_auth: true (config or Settings → Security)"
		}
		return "mcpproxy profile anonymous <p>"
	case string(profile.FixReconnectClient):
		if w.Code == profile.WarningClientRotationPending {
			return "mcpproxy client rotate " + target + " --finalize"
		}
		return "mcpproxy client rotate " + target
	case string(profile.FixMoveClient):
		return "mcpproxy client set-profile " + target + " <profile|all>"
	case string(profile.FixEditToken):
		return "mcpproxy token delete " + target
	case profile.WarningActionUpgradeAdminKeyHolders:
		return "mcpproxy client upgrade-admin-key-holders"
	}
	return ""
}

// confirmYes asks for confirmation on a terminal, or accepts --yes. A non-TTY
// stdin without --yes is refused (exit 1) so a script never proceeds blind.
func confirmYes(prompt string, yes bool) (bool, error) {
	if yes {
		return true, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false, flagValidationError{errors.New("confirmation required; pass --yes")}
	}
	fmt.Printf("%s [y/N]: ", prompt)
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("failed to read confirmation: %w", err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

// dash renders an empty cell.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

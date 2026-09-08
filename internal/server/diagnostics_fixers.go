package server

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
)

// Spec 044's self-heal buttons are backed by fixers registered in
// internal/diagnostics/builtin_fixers.go. That file's comment says the real
// implementations are "registered by higher layers at startup" — but until this
// file existed, NOTHING did: the only diagnostics.Register call outside the
// package was in a test. So every one of the four registered fixers was the
// placeholder, and the two that matter most in the field were dead:
//
//   - stdio_show_last_logs returned the canned string "log tail unavailable in
//     this build" with outcome=success. It is offered by
//     MCPX_STDIO_EXIT_BEFORE_INITIALIZE (270 installs) and
//     MCPX_STDIO_HANDSHAKE_TIMEOUT (315 installs), whose captured stderr
//     usually names the missing binary or env var outright — precisely what the
//     button promised and never delivered.
//   - oauth_reauth returned outcome=blocked for every execute. It is the ONLY
//     action MCPX_OAUTH_LOGIN_REQUIRED (208 installs) offers.
//
// Because the placeholder reported success, diagnostics.fix_succeeded_24h was
// counting placeholder no-ops. This file replaces both with the same calls the
// REST API already makes for the equivalent endpoints, so the counter starts
// measuring the product instead of the instrument.
//
// The two config-mutating placeholders (config_migrate_deprecated,
// server_disable_scanner) are deliberately left alone — mutating the user's
// config file from a one-click button is a larger design question than wiring
// an existing read path.

// diagnosticsLogTailLines is how many trailing log lines stdio_show_last_logs
// returns. It matches the default of the REST endpoint
// (GET /api/v1/servers/{id}/logs) and of the tail_log MCP tool.
const diagnosticsLogTailLines = 50

// diagnosticsPreviewMaxBytes bounds the rendered log tail.
//
// diagnosticsLogTailLines bounds the line COUNT, not the byte size, and a child
// MCP server is free to print a 60KB JSON blob on a single line (bufio.Scanner's
// default 64KB token limit is the only ceiling GetServerLogs imposes). This
// Preview is delivered as a Web-UI notification, not into a log viewer, so 50
// such lines would be a multi-megabyte toast. The cap keeps the NEWEST lines —
// a tail is read bottom-up — and the fixer states plainly what it dropped, so
// nothing goes missing silently.
//
// One documented exemption: the single newest line is never dropped or
// truncated, so a payload can exceed this when that one line does. That is
// deliberate (it is the line that explains the failure) and it is the ONLY way
// past the cap — the header and the omission notice are budgeted for.
const diagnosticsPreviewMaxBytes = 8 * 1024

// registerDiagnosticFixers installs the runtime-backed fixer implementations
// over the package-level placeholders. Called from NewServerWithConfigPath,
// where both dependencies (this *Server for logs, the Runtime for OAuth) are
// already constructed.
//
// diagnostics.Register is process-global and last-write-wins. Production builds
// one Server per process, so the binding is unambiguous there; in a test binary
// that constructs several, the most recently constructed Server owns the
// fixers. That is the same contract the package's own init() already had.
func (s *Server) registerDiagnosticFixers() {
	diagnostics.Register("stdio_show_last_logs", s.fixShowLastServerLogs)
	diagnostics.Register("oauth_reauth", s.fixOAuthReauth)
	s.logger.Debug("Registered runtime-backed diagnostics fixers",
		zap.Strings("fixers", []string{"stdio_show_last_logs", "oauth_reauth"}))
}

// fixShowLastServerLogs implements the "Show last server log lines" button.
//
// It reads through (*Server).GetServerLogs — the same call backing
// GET /api/v1/servers/{id}/logs — so the log text is masked by the identical
// scrubber (parseLogLine → scrubUpstreamText, issue #1148). That matters here
// because the FixResult crosses the REST API: the per-server log file is
// written by mcpproxy AND by the child process, and both put credentials in it.
//
// The step is non-destructive and read-only, so dry_run and execute do the same
// thing: there is no state to preview.
func (s *Server) fixShowLastServerLogs(_ context.Context, req diagnostics.FixRequest) (diagnostics.FixResult, error) {
	if req.ServerID == "" {
		return diagnostics.FixResult{
			Outcome:    diagnostics.OutcomeFailed,
			FailureMsg: "a server name is required to read a log tail",
		}, nil
	}

	entries, err := s.GetServerLogs(req.ServerID, diagnosticsLogTailLines)
	if err != nil {
		// Report the honest outcome. The placeholder answered "success" here
		// too, which is exactly how a fixer that showed the user nothing ended
		// up inflating fix_succeeded_24h.
		return diagnostics.FixResult{
			Outcome:    diagnostics.OutcomeFailed,
			FailureMsg: scrubUpstreamText(err.Error()),
		}, nil
	}

	if len(entries) == 0 {
		return diagnostics.FixResult{
			Outcome:    diagnostics.OutcomeFailed,
			FailureMsg: fmt.Sprintf("server %q has no log lines yet", req.ServerID),
		}, nil
	}

	// Render the scrubbed Message only. parseLogLine SYNTHESIZES Timestamp
	// (time.Now()) and Level ("INFO") for any line it cannot parse — which is
	// every raw stderr line piped from the child, i.e. the lines this button
	// exists to show — so echoing those fields back would fabricate data. For
	// an unparsed line Message is the whole original line, so nothing is lost.
	//
	// Individual lines are never truncated: scrubUpstreamText drops the
	// activity-store cap for live reads for this exact reason — a long line is
	// often precisely what an operator opened the log for. What IS bounded is
	// the total payload (diagnosticsPreviewMaxBytes), by dropping the OLDEST
	// lines, because the newest line is the one that explains the failure. The
	// newest line always survives whole, however long it is.
	//
	// The budget subtracts the framing the line loop does not measure — the
	// header and the omission notice, both of which interpolate the server
	// name. Without that, a tail whose lines exactly filled the cap shipped a
	// payload larger than the cap the constant advertises.
	overhead := len(fmt.Sprintf("Last %d log line(s) for %q:\n", len(entries), req.ServerID)) +
		len(fmt.Sprintf("\n(%d older line(s) omitted to keep this readable — run `mcpproxy upstream logs %s` for the full log.)\n",
			len(entries), req.ServerID))
	budget := diagnosticsPreviewMaxBytes - overhead
	if budget < 0 {
		budget = 0
	}

	start := 0
	size := 0
	for i := len(entries) - 1; i >= 0; i-- {
		size += len(entries[i].Message) + 1
		if size > budget && i != len(entries)-1 {
			start = i + 1
			break
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Last %d log line(s) for %q:\n", len(entries)-start, req.ServerID)
	for i := start; i < len(entries); i++ {
		b.WriteString(entries[i].Message)
		b.WriteByte('\n')
	}
	if start > 0 {
		fmt.Fprintf(&b, "\n(%d older line(s) omitted to keep this readable — run `mcpproxy upstream logs %s` for the full log.)\n",
			start, req.ServerID)
	}

	return diagnostics.FixResult{
		Outcome: diagnostics.OutcomeSuccess,
		Preview: b.String(),
	}, nil
}

// fixOAuthReauth implements the "Sign in" / "Log in again" button.
//
// It calls Runtime.TriggerOAuthLogin (async, hands off to
// Manager.StartManualOAuth, returns as soon as the flow's goroutine is
// launched) behind an explicit read_only_mode / disable_management check.
//
// Both halves of that are deliberate, and each was wrong on its own:
//
//   - WITHOUT the gate check, a one-click button started an OAuth flow on an
//     install whose owner had turned management off. POST
//     /api/v1/servers/{id}/login cannot: it goes through the management
//     service, whose checkWriteGates refuses both settings
//     (internal/management/service.go). The diagnostics route's middleware
//     checks caller authorization, not those config gates, so nothing else
//     was applying them here. The predicate is duplicated rather than called
//     because checkWriteGates is unexported and the only exported entry point
//     behind it is TriggerOAuthLoginQuick — which is the other half:
//   - Routing through TriggerOAuthLoginQuick to inherit the gate looked
//     tidier and is WRONG for this caller. StartManualOAuthQuick runs startup
//     synchronously under its own 30-minute background context, so the fix
//     endpoint's 15s deadline stops bounding the call; its watcher cancels
//     the callback context after ~120s, so a slow 2FA loses the flow it was
//     told had started; HasRecentOAuthCompletion can make that watcher exit
//     on its FIRST poll when the same server signed in within five minutes;
//     and it drops the SSE dispatch that ForceOAuthFlowWithResult performs,
//     so an OAuth-protected legacy SSE endpoint fails to initialize instead
//     of opening its flow. The REST login route accepts all of that; a
//     one-click fixer with a 15s budget must not.
//
// The message therefore does not claim a browser window opened — nothing on
// this path reports whether one did, and the placeholder this file replaced is
// exactly what asserting an unverified success looks like.
//
// Duplicate clicks: Manager.StartManualOAuth builds a fresh core client per
// invocation, so the isOAuthInProgress check is per-client. An earlier version
// of this comment claimed a second click "surfaces as an error string rather
// than a second browser tab"; that guarantee is stronger than the code
// provides. Whatever the real behaviour is, it is a pre-existing property of
// StartManualOAuth and is not changed here.
//
// Gating is unchanged: every catalog entry that offers this fixer marks the
// step Destructive (except MCPX_OAUTH_LOGIN_REQUIRED's first-time "Sign in",
// where there is no stored credential to lose), so handleInvokeFix still
// demands an explicit mode (409 otherwise) and the Web UI's ErrorPanel still
// gates Execute behind window.confirm().
func (s *Server) fixOAuthReauth(_ context.Context, req diagnostics.FixRequest) (diagnostics.FixResult, error) {
	if req.ServerID == "" {
		return diagnostics.FixResult{
			Outcome:    diagnostics.OutcomeFailed,
			FailureMsg: "a server name is required to start a sign-in",
		}, nil
	}

	if req.Mode == diagnostics.ModeDryRun {
		return diagnostics.FixResult{
			Outcome: diagnostics.OutcomeSuccess,
			Preview: fmt.Sprintf(
				"Would open a browser window to sign in to server %q and replace any stored token. No change has been made.",
				req.ServerID),
		}, nil
	}

	// Fail closed: if the config cannot be read, the gates cannot be applied,
	// and "I could not check" is not a reason to proceed.
	cfg, err := s.GetConfig()
	if err != nil || cfg == nil {
		return diagnostics.FixResult{
			Outcome:    diagnostics.OutcomeFailed,
			FailureMsg: "could not read the configuration, so sign-in cannot be started from here",
		}, nil
	}
	// Same two gates, same wording as management.checkWriteGates, so an
	// operator sees one answer whichever surface they used.
	if cfg.DisableManagement {
		return diagnostics.FixResult{
			Outcome:    diagnostics.OutcomeFailed,
			FailureMsg: "management operations are disabled (disable_management=true)",
		}, nil
	}
	if cfg.ReadOnlyMode {
		return diagnostics.FixResult{
			Outcome:    diagnostics.OutcomeFailed,
			FailureMsg: "management operations are disabled (read_only_mode=true)",
		}, nil
	}

	if err := s.runtime.TriggerOAuthLogin(req.ServerID); err != nil {
		return diagnostics.FixResult{
			Outcome:    diagnostics.OutcomeFailed,
			FailureMsg: scrubUpstreamText(err.Error()),
		}, nil
	}

	return diagnostics.FixResult{
		Outcome: diagnostics.OutcomeSuccess,
		Preview: oauthReauthStartedMessage(req.ServerID),
	}, nil
}

// oauthReauthStartedMessage is what the Sign in button reports once the
// asynchronous flow has been handed off. It must not claim a browser window
// opened — nothing on this path reports whether one did — and its fallback
// must be one the user can actually follow.
//
// An earlier wording sent a user with no browser window to "Show last server
// log lines". That button reads the PER-SERVER log file (GetServerLogs), but
// the authorization URL and the browser-launch failure are written by the
// client's main logger (connection_oauth.go, c.logger), which does not feed
// that file — and the OAuth catalog entries do not offer the log-tail button
// in the first place. The instruction pointed at a log that never held the
// URL, behind a button the user could not press.
//
// `mcpproxy auth login --server=<name>` is the path that works headless: it
// drives the same flow from a terminal and prints the authorization URL when
// the browser cannot be opened.
func oauthReauthStartedMessage(serverID string) string {
	return fmt.Sprintf(
		"Sign-in started for server %q. Finish it in the browser window mcpproxy opens; the server reconnects on its own once you do. "+
			"If no window appears, run `mcpproxy auth login --server=%s` from a terminal — it prints the authorization URL when the browser cannot be opened.",
		serverID, serverID)
}

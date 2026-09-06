package server

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
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
// It goes through the management service's TriggerOAuthLoginQuick — byte for
// byte the call POST /api/v1/servers/{id}/login makes
// (internal/httpapi/server.go handleServerLogin) — and NOT Runtime's method
// directly. That distinction is the whole point of this indirection:
//
//   - The management service applies checkWriteGates first
//     (internal/management/service.go), which refuses when read_only_mode or
//     disable_management is set. Calling the Runtime directly skipped both, so
//     a one-click button could start an OAuth flow on an install whose owner
//     had turned management off — the diagnostics route's own middleware only
//     checks caller authorization, not those config gates.
//   - It returns an OAuthStartResult carrying BrowserOpened / BrowserError, so
//     the success message can say what actually happened instead of asserting
//     that a browser window opened. The old wording claimed the window had
//     opened even when the launch failed, which is the same "a success that
//     means nothing" defect this whole file exists to remove.
//
// Duplicate clicks behave exactly as the REST login button does — no better,
// no worse — because it is now the same call. An earlier version of this
// comment claimed a stronger guarantee (that a second click "surfaces as an
// error string rather than a second browser tab") than the code provides:
// Manager.StartManualOAuth builds a fresh core client per invocation, so the
// isOAuthInProgress check is per-client. Whatever that path's real duplicate
// behaviour is, it is a pre-existing property of the login route and is not
// changed here.
//
// Gating is unchanged: every catalog entry that offers this fixer marks the
// step Destructive (except MCPX_OAUTH_LOGIN_REQUIRED's first-time "Sign in",
// where there is no stored credential to lose), so handleInvokeFix still
// demands an explicit mode (409 otherwise) and the Web UI's ErrorPanel still
// gates Execute behind window.confirm().
func (s *Server) fixOAuthReauth(ctx context.Context, req diagnostics.FixRequest) (diagnostics.FixResult, error) {
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

	// Fail closed if the gated path is unavailable rather than falling back to
	// the ungated Runtime call: "the write gates could not be applied" is not a
	// reason to skip them.
	mgmtSvc, ok := s.GetManagementService().(interface {
		TriggerOAuthLoginQuick(ctx context.Context, name string) (*core.OAuthStartResult, error)
	})
	if !ok {
		return diagnostics.FixResult{
			Outcome:    diagnostics.OutcomeFailed,
			FailureMsg: "the management service is not available, so sign-in cannot be started from here",
		}, nil
	}

	result, err := mgmtSvc.TriggerOAuthLoginQuick(ctx, req.ServerID)
	if err != nil {
		return diagnostics.FixResult{
			Outcome:    diagnostics.OutcomeFailed,
			FailureMsg: scrubUpstreamText(err.Error()),
		}, nil
	}

	// Say what happened, not what usually happens. A failed browser launch is
	// still a started flow — the URL is valid and the user can finish it — so
	// this is a success with an instruction, not a failure.
	msg := fmt.Sprintf(
		"Sign-in started for server %q. Complete it in the browser window that just opened; the server reconnects on its own once you do.",
		req.ServerID)
	if result != nil && !result.BrowserOpened {
		msg = fmt.Sprintf(
			"Sign-in started for server %q, but the browser could not be opened%s. Open this URL yourself to finish:\n%s",
			req.ServerID, browserErrSuffix(result.BrowserError), result.AuthURL)
	}

	return diagnostics.FixResult{
		Outcome: diagnostics.OutcomeSuccess,
		Preview: msg,
	}, nil
}

// browserErrSuffix renders the launcher's own error, scrubbed, when it gave one.
func browserErrSuffix(browserErr string) string {
	if strings.TrimSpace(browserErr) == "" {
		return ""
	}
	return " (" + scrubUpstreamText(browserErr) + ")"
}

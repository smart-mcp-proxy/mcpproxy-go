package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Spec 124 PR-2 (plan D8, FR-050..053): the work-session list and the failure
// timeline from the terminal.
//
//	mcpproxy activity sessions   one row per work session, with its failure facts
//	mcpproxy activity session    one session's timeline, around its failures
//
// Both read the doors of internal/httpapi/work_sessions.go and add nothing of
// their own: with -o json|yaml the output is the REST data object. The flags
// live in the constructors, not in package variables, so a command built for
// one run carries nothing over from another.

// The bounds the doors enforce, checked here so a mistyped flag fails before
// a request is made.
const (
	workSessionListLimitDefault = 25
	workSessionListLimitMax     = 100
	workSessionContextDefault   = 20
	workSessionContextMax       = 250
	// workSessionPageMax is the most rows one timeline page holds, the
	// positioned row included. The door shortens the forward context of a
	// larger request without saying so, so a larger request is refused here.
	workSessionPageMax = 500
	// workSessionWalkAttempts bounds how often `session --failures` starts
	// over when the stored failures change under it.
	workSessionWalkAttempts = 3
	// workSessionCallTimeout bounds one request to a door.
	workSessionCallTimeout = 30 * time.Second
	// workSessionReasonCell and workSessionDetailCell cap the free-text cells.
	workSessionReasonCell = 72
	workSessionDetailCell = 96
	workSessionNameCell   = 32
	// workSessionBodyRunes caps one argument or response body in table
	// output; -o json carries the whole body.
	workSessionBodyRunes = 2000
)

const workSessionTimeLayout = "2006-01-02 15:04:05Z"
const workSessionTimeLayoutMs = "2006-01-02 15:04:05.000Z"

func newActivitySessionsCmd() *cobra.Command {
	var (
		failed     bool
		noBlocked  bool
		token      string
		client     string
		profile    string
		run        string
		errorClass string
		since      string
		until      string
		sortBy     string
		limit      int
		offset     int
	)
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List work sessions with their failure facts",
		Long: `List work sessions: one row per run of work by one agent (one credential, one
client, one project, until 30 minutes of idle), with its calls, its failures
and the reason of the last failure.

An agent token sees only its own sessions, and only the part of each that
touched servers in its reach.

Examples:
  # The morning after: runs active in the last 24 hours that have failures,
  # newest failure first
  mcpproxy activity sessions --failed --since -24h

  # The same, without sessions whose only failures were policy blocks
  mcpproxy activity sessions --failed --no-blocked

  # The sessions of one agent token, or of one named REST run
  mcpproxy activity sessions --token ci-agent
  mcpproxy activity sessions --run nightly-build-417

  # Then open one at its first failure
  mcpproxy activity session <id>`,
		Args:   workSessionArgs(cobra.NoArgs),
		PreRun: silenceUsageAfterParse,
		RunE: func(_ *cobra.Command, _ []string) error {
			if limit < 1 || limit > workSessionListLimitMax {
				return newFlagValidationError("invalid --limit %d: use a number from 1 to %d", limit, workSessionListLimitMax)
			}
			if offset < 0 {
				return newFlagValidationError("invalid --offset %d: it cannot be negative", offset)
			}
			if sortBy != "" && sortBy != "last_activity" && sortBy != "last_failure" {
				return newFlagValidationError("invalid --sort %q: use last_activity or last_failure", sortBy)
			}
			if errorClass != "" && !containsString(validErrorClasses, errorClass) {
				return newFlagValidationError("invalid --error-class %q: use one of %s", errorClass, strings.Join(validErrorClasses, ", "))
			}
			if noBlocked && !failed {
				return newFlagValidationError("--no-blocked only changes what --failed counts: add --failed")
			}
			// User Story 4: the failed-only list is read newest failure first.
			if failed && sortBy == "" {
				sortBy = "last_failure"
			}
			now := time.Now()
			sinceAt, err := resolveActivityTime(since, now)
			if err != nil {
				return newFlagValidationError("--since: %v", err)
			}
			untilAt, err := resolveActivityTime(until, now)
			if err != nil {
				return newFlagValidationError("--until: %v", err)
			}
			params := cliclient.WorkSessionListParams{
				Failed: failed, Token: token, Client: client, Profile: profile, Run: run,
				ErrorClass: errorClass, Since: sinceAt, Until: untilAt, Sort: sortBy,
				Limit: limit, Offset: offset,
			}
			if noBlocked {
				include := false
				params.IncludeBlocked = &include
			}
			return runActivitySessions(params)
		},
	}
	cmd.SetFlagErrorFunc(workSessionFlagError)
	f := cmd.Flags()
	f.BoolVar(&failed, "failed", false, "Only sessions with at least one failure")
	f.BoolVar(&noBlocked, "no-blocked", false, "With --failed: do not count a policy block (a refusal by MCPProxy itself) as a failure")
	f.StringVar(&token, "token", "", "Filter by agent token name")
	f.StringVar(&client, "client", "", "Filter by client id")
	f.StringVar(&profile, "profile", "", "Filter by profile (administrators only)")
	f.StringVar(&run, "run", "", "Filter by run name (the X-MCPProxy-Run label of a REST or MCP caller)")
	f.StringVar(&errorClass, "error-class", "", "Filter by the error class of the last failure: "+strings.Join(validErrorClasses, ", "))
	f.StringVar(&since, "since", "", "Only sessions active at or after this time (RFC3339 or relative: -1h, -24h, -7d)")
	f.StringVar(&until, "until", "", "Only sessions started at or before this time (RFC3339 or relative)")
	f.StringVar(&sortBy, "sort", "", "Sort by last_activity or last_failure, newest first (default: last_activity; last_failure with --failed)")
	f.IntVarP(&limit, "limit", "n", workSessionListLimitDefault, "Max sessions to return (1-100)")
	f.IntVar(&offset, "offset", 0, "Pagination offset")
	return cmd
}

func newActivitySessionCmd() *cobra.Command {
	var (
		failures      bool
		before        int
		after         int
		at            string
		includeBodies bool
	)
	cmd := &cobra.Command{
		Use:   "session <id>",
		Short: "Show one work session's timeline around its failures",
		Long: `Show the timeline of one work session: its calls, the refusals by MCPProxy
itself, and the upstream events (a lost connection, an exited process, a login
that is required) of the servers it called, in time order.

Without --at the view opens at the first failure still stored, with 20 rows
before and after it. Argument and response bodies are left out unless
--include-bodies is given.

With --failures every stored failure is printed with the rows before it; rows
that are context of more than one failure are printed once. With -o json|yaml
that view is one object: the session, the rows in order (each once), and
"failures", the number and row index of each failure.

Examples:
  # What went wrong first, and what the agent did before it
  mcpproxy activity session ws-4f2a91c07b3d

  # Every failure of the run, each with the 5 rows before it
  mcpproxy activity session ws-4f2a91c07b3d --failures --before 5

  # The third failure, with the bodies of the calls around it
  mcpproxy activity session ws-4f2a91c07b3d --at failure:3 --include-bodies

  # The last row of the session and the 50 before it
  mcpproxy activity session ws-4f2a91c07b3d --at end --before 50

Find the id with: mcpproxy activity sessions --failed`,
		Args:   workSessionArgs(cobra.ExactArgs(1)),
		PreRun: silenceUsageAfterParse,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if strings.TrimSpace(id) == "" {
				return newFlagValidationError("the session id is empty. List sessions with: mcpproxy activity sessions")
			}
			for name, value := range map[string]int{"--before": before, "--after": after} {
				if value < 0 || value > workSessionContextMax {
					return newFlagValidationError("invalid %s %d: use a number from 0 to %d", name, value, workSessionContextMax)
				}
			}
			if before+after+1 > workSessionPageMax && (!failures || cmd.Flags().Changed("after")) {
				return newFlagValidationError("--before %d and --after %d do not fit one page: together they can be at most %d", before, after, workSessionPageMax-1)
			}
			if err := validateTimelinePosition(at); err != nil {
				return err
			}
			if failures {
				if at != "" {
					return newFlagValidationError("--failures walks every stored failure: it cannot be combined with --at")
				}
				// The rows after a failure belong to the next one, so the
				// default forward context of the walk is none.
				forward := 0
				if cmd.Flags().Changed("after") {
					forward = after
				}
				return runActivitySessionFailures(id, before, forward, includeBodies)
			}
			return runActivitySession(id, at, before, after, includeBodies)
		},
	}
	cmd.SetFlagErrorFunc(workSessionFlagError)
	f := cmd.Flags()
	f.BoolVar(&failures, "failures", false, "Print every stored failure with the rows before it (overlapping context once)")
	f.IntVar(&before, "before", workSessionContextDefault, "Rows before the position, or before each failure with --failures (0-250)")
	f.IntVar(&after, "after", workSessionContextDefault, "Rows after the position (0-250); with --failures the default is 0")
	f.StringVar(&at, "at", "", "Position: first_failure (default), failure:<n>, row:<activity id> or end")
	f.BoolVar(&includeBodies, "include-bodies", false, "Include argument and response bodies")
	return cmd
}

// silenceUsageAfterParse keeps Cobra's usage block for a command line that did
// not parse. PreRun runs once the arguments and flags are accepted; from there
// an error is a refused value or the daemon's answer, and the usage block
// under it would bury the one line that says what is wrong.
func silenceUsageAfterParse(cmd *cobra.Command, _ []string) { cmd.SilenceUsage = true }

// workSessionFlagError marks a flag Cobra could not parse as what it is, a
// mistyped command line (exit 1). Without it the error carries the value that
// was typed, and the exit-code heuristics read that value: `--limit config`
// would exit 4, the code of an unreadable configuration file.
func workSessionFlagError(_ *cobra.Command, err error) error {
	return flagValidationError{err}
}

// workSessionArgs is the positional-argument rule of both commands: v, let
// through under --help-json, with a refusal marked the same way and for the
// same reason as a flag's (the error quotes the argument that was typed).
func workSessionArgs(v cobra.PositionalArgs) cobra.PositionalArgs {
	inner := argsOrHelpJSON(v)
	return func(cmd *cobra.Command, args []string) error {
		if err := inner(cmd, args); err != nil {
			return flagValidationError{err}
		}
		return nil
	}
}

// validateTimelinePosition applies the door's rule for `at`.
func validateTimelinePosition(at string) error {
	switch {
	case at == "", at == "first_failure", at == "end":
		return nil
	case strings.HasPrefix(at, "failure:"):
		if n, err := strconv.Atoi(strings.TrimPrefix(at, "failure:")); err == nil && n >= 1 {
			return nil
		}
	case strings.HasPrefix(at, "row:") && len(at) > len("row:"):
		return nil
	}
	return newFlagValidationError("invalid --at %q: use first_failure, failure:<n>, row:<activity id> or end", at)
}

// workSessionError turns a client error into the command's error. Everything
// a door says exits 1: its text is the daemon's and must not be read by the
// exit-code heuristics.
func workSessionError(err error) error {
	var notFound *cliclient.WorkSessionNotFoundError
	if errors.As(err, &notFound) {
		return cliRefusalError{fmt.Errorf("work session %q not found: no session with this id is stored, or it is not yours to read. List sessions with: mcpproxy activity sessions",
			termSafeText(notFound.ID))}
	}
	// The text below is the daemon's, or a body that was not the daemon's JSON
	// (a proxy's error page). It is shown as text on one line: a line break
	// in it must not read as a second message of the CLI.
	var apiErr *cliclient.APIError
	if errors.As(err, &apiErr) {
		msg := termEscape(apiErr.Message, false)
		if apiErr.HasRequestID() {
			id := termEscape(apiErr.RequestID, false)
			msg += fmt.Sprintf("\n\nRequest ID: %s\nUse 'mcpproxy activity list --request-id %s' to find related logs.", id, id)
		}
		return cliRefusalError{errors.New(msg)}
	}
	return cliRefusalError{errors.New(termEscape(err.Error(), false))}
}

// workSessionLineSeparators shows the Unicode line and paragraph separators
// as text. sanitizeCell escapes control and format characters; these two are
// neither, and a terminal that honours them would break a table row in two.
var workSessionLineSeparators = strings.NewReplacer("\u2028", `\u2028`, "\u2029", `\u2029`)

// wsCell makes a client- or upstream-supplied name safe for one table cell.
func wsCell(s string, maxRunes int) string {
	return sanitizeCell(workSessionLineSeparators.Replace(s), maxRunes)
}

// wsText is wsCell for free text: whitespace runs become one space first.
func wsText(s string, maxRunes int) string {
	return sanitizeDescriptionCell(workSessionLineSeparators.Replace(s), maxRunes)
}

func runActivitySessions(params cliclient.WorkSessionListParams) error {
	s, err := newRESTSession("activity sessions")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), workSessionCallTimeout)
	defer cancel()
	list, err := s.client.ListWorkSessions(ctx, params)
	if err != nil {
		return workSessionError(err)
	}
	if list.Sessions == nil {
		list.Sessions = []contracts.WorkSession{}
	}
	if structuredOutput() {
		return printJSONValue(list)
	}
	if len(list.Sessions) == 0 {
		if list.Total > 0 {
			// The door answers with its own, clamped offset; the one that
			// was asked for is the one to name.
			fmt.Printf("No sessions at offset %d: %d sessions match. Lower --offset.\n", params.Offset, list.Total)
			return nil
		}
		fmt.Println("No work sessions found")
		return nil
	}
	headers, rows, truncated := workSessionListTable(list.Sessions)
	if err := printTable(headers, rows); err != nil {
		return err
	}
	fmt.Println()
	if truncated {
		fmt.Println("* the start of this session is no longer stored (retention removed its oldest rows)")
	}
	fmt.Printf("Showing %d of %d sessions", len(list.Sessions), list.Total)
	if list.Offset > 0 {
		fmt.Printf(" (from %d)", list.Offset+1)
	}
	fmt.Println()
	fmt.Println("Timeline of one session: mcpproxy activity session <id>")
	return nil
}

// workSessionListTable renders the list: the columns of Spec 124 User Story 4
// and the id the timeline command takes. RUN appears only when some session
// carries a run name. truncated reports whether a start was starred.
func workSessionListTable(sessions []contracts.WorkSession) (headers []string, rows [][]string, truncated bool) {
	withRun := false
	for i := range sessions {
		if sessions[i].RunName != "" {
			withRun = true
			break
		}
	}
	headers = []string{"ID", "TOKEN", "CLIENT", "PROJECT"}
	if withRun {
		headers = append(headers, "RUN")
	}
	headers = append(headers, "STARTED", "DURATION", "CALLS", "FAILURES", "LAST FAILURE", "REASON")

	for i := range sessions {
		ws := &sessions[i]
		started := ws.StartedAt.UTC().Format(workSessionTimeLayout)
		if ws.TruncatedStartAt != nil {
			started += "*"
			truncated = true
		}
		duration := formatSessionDuration(ws.DurationMs)
		if ws.Status == "active" {
			duration += " (active)"
		}
		lastFailure, reason := "-", "-"
		if ws.LastFailure != nil {
			lastFailure = ws.LastFailure.At.UTC().Format(workSessionTimeLayout)
			if ws.LastFailure.Reason != "" {
				reason = wsText(ws.LastFailure.Reason, workSessionReasonCell)
			}
		}
		row := []string{
			wsCell(ws.ID, 0),
			dashIfEmpty(wsCell(ws.TokenName, workSessionNameCell)),
			workSessionClientCell(ws),
			dashIfEmpty(wsCell(ws.ProjectName, workSessionNameCell)),
		}
		if withRun {
			row = append(row, dashIfEmpty(wsCell(ws.RunName, workSessionNameCell)))
		}
		row = append(row, started, duration, strconv.Itoa(ws.Attempts), workSessionFailuresCell(ws), lastFailure, reason)
		rows = append(rows, row)
	}
	return headers, rows, truncated
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// workSessionClientCell is the client id when the session carries a binding,
// else the client's self-reported name marked with "~" (a client can claim
// any name), else the surface the calls arrived on (rest, cli, webui, tray).
func workSessionClientCell(ws *contracts.WorkSession) string {
	switch {
	case ws.ClientID != "":
		return wsCell(ws.ClientID, workSessionNameCell)
	case ws.ClientName != "":
		return "~" + wsCell(ws.ClientName, workSessionNameCell)
	case ws.Surface != "":
		return wsCell(ws.Surface, workSessionNameCell)
	}
	return "-"
}

func workSessionFailuresCell(ws *contracts.WorkSession) string {
	if ws.PolicyBlocks > 0 {
		return fmt.Sprintf("%d (%d blocked)", ws.Failures, ws.PolicyBlocks)
	}
	return strconv.Itoa(ws.Failures)
}

// formatSessionDuration renders a session's length at the precision a person
// reads it: 850ms, 45s, 3m20s, 2h13m, 1d4h.
func formatSessionDuration(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", ms)
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", int(d/time.Minute), int(d%time.Minute/time.Second))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	default:
		return fmt.Sprintf("%dd%dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
	}
}

// --- one session ---------------------------------------------------------------

func runActivitySession(id, at string, before, after int, includeBodies bool) error {
	s, err := newRESTSession("activity session")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), workSessionCallTimeout)
	defer cancel()
	page, err := s.client.GetWorkSessionTimeline(ctx, id, cliclient.WorkSessionTimelineParams{
		At: at, Before: &before, After: &after, IncludeBodies: includeBodies,
	})
	if err != nil {
		return workSessionError(err)
	}
	if page.Rows == nil {
		page.Rows = []contracts.WorkSessionTimelineRow{}
	}
	if structuredOutput() {
		return printJSONValue(page)
	}

	printWorkSessionHeader(&page.Session)
	printStoredFailuresNote(page.LifetimeFailures, page.FailureTotal, page.EarlierFailuresNotStored)
	fmt.Println()
	if len(page.Rows) == 0 {
		if page.TotalRows > 0 {
			// The door found the position and then had no row to return for
			// it: removed in between, or not readable with this credential.
			fmt.Printf("No rows were returned at this position; %d rows of the session are stored. Try --at first_failure or --at end.\n", page.TotalRows)
			return nil
		}
		fmt.Println("No rows of this session are stored.")
		return nil
	}
	first, last := page.Rows[0].Index, page.Rows[len(page.Rows)-1].Index
	switch {
	case page.FailureTotal == 0:
		fmt.Printf("No stored failures · rows %d-%d of %d\n", first, last, page.TotalRows)
	case page.FailureIndex == 0:
		fmt.Printf("Before the first of %d failures · rows %d-%d of %d\n", page.FailureTotal, first, last, page.TotalRows)
	case at == "" || at == "first_failure" || strings.HasPrefix(at, "failure:"):
		// The position was asked for as a failure and is one, whether the
		// door returned its row or not.
		missing := ""
		if _, returned := timelinePositionRow(page); !returned {
			missing = " (its row was not returned)"
		}
		fmt.Printf("Failure %d of %d%s · rows %d-%d of %d\n", page.FailureIndex, page.FailureTotal, missing, first, last, page.TotalRows)
	case timelinePositionFailed(page):
		fmt.Printf("Failure %d of %d · rows %d-%d of %d\n", page.FailureIndex, page.FailureTotal, first, last, page.TotalRows)
	default:
		// failure_index is the last failure at or before the position; the
		// positioned row itself is not one.
		fmt.Printf("After failure %d of %d · rows %d-%d of %d\n", page.FailureIndex, page.FailureTotal, first, last, page.TotalRows)
	}
	if err := printTimelineRows(page.Rows, includeBodies); err != nil {
		return err
	}

	var more []string
	if page.HasBefore {
		more = append(more, "earlier rows: raise --before")
	}
	if page.HasAfter {
		more = append(more, "later rows: raise --after")
	}
	if len(more) > 0 {
		fmt.Printf("\nMore rows are stored (%s, up to %d; or move with --at failure:<n>, --at row:<id>, --at end).\n",
			strings.Join(more, "; "), workSessionContextMax)
	}
	if page.FailureTotal > 1 {
		fmt.Printf("Every failure with its context: mcpproxy activity session %s --failures\n", shellQuoteIfNeeded(id))
	}
	return nil
}

// timelinePositionRow returns the row the page is positioned at, when the
// door returned it.
func timelinePositionRow(page *contracts.WorkSessionTimelineResponse) (*contracts.WorkSessionTimelineRow, bool) {
	for i := range page.Rows {
		if page.Rows[i].Index == page.Position {
			return &page.Rows[i], true
		}
	}
	return nil, false
}

// timelinePositionFailed reports whether the row the page is positioned at is
// a failure.
func timelinePositionFailed(page *contracts.WorkSessionTimelineResponse) bool {
	row, ok := timelinePositionRow(page)
	return ok && row.Failed
}

// shellQuoteIfNeeded keeps a suggested command copyable when the id is not a
// plain word. Session ids are plain; this only guards the odd one.
func shellQuoteIfNeeded(s string) string {
	for _, r := range s {
		plain := r == '-' || r == '_' || r == '.' || r == ':' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !plain {
			return "'" + strings.ReplaceAll(termSafeText(s), "'", `'\''`) + "'"
		}
	}
	return s
}

// printWorkSessionHeader prints who ran the session and what it amounted to.
func printWorkSessionHeader(ws *contracts.WorkSession) {
	parts := []string{"Session " + wsCell(ws.ID, 0)}
	if ws.TokenName != "" {
		parts = append(parts, "token "+wsCell(ws.TokenName, workSessionNameCell))
	}
	if c := workSessionClientCell(ws); c != "-" {
		parts = append(parts, "client "+c)
	}
	if ws.ProjectName != "" {
		parts = append(parts, "project "+wsCell(ws.ProjectName, workSessionNameCell))
	}
	if ws.RunName != "" {
		parts = append(parts, "run "+wsCell(ws.RunName, workSessionNameCell))
	}
	if ws.Profile != "" {
		parts = append(parts, "profile "+wsCell(ws.Profile, workSessionNameCell))
	}
	parts = append(parts, wsCell(ws.Status, workSessionNameCell))
	fmt.Println(strings.Join(parts, " · "))

	calls := "calls"
	if ws.Attempts == 1 {
		calls = "call"
	}
	failures := fmt.Sprintf("%d failures", ws.Failures)
	if ws.Failures == 1 {
		failures = "1 failure"
	}
	if ws.PolicyBlocks > 0 {
		failures += fmt.Sprintf(" (%d blocked)", ws.PolicyBlocks)
	}
	fmt.Printf("Started %s · last activity %s · %s · %d %s · %s\n",
		ws.StartedAt.UTC().Format(workSessionTimeLayout), ws.LastActivityAt.UTC().Format(workSessionTimeLayout),
		formatSessionDuration(ws.DurationMs), ws.Attempts, calls, failures)
	if ws.TruncatedStartAt != nil {
		fmt.Printf("Note: the start of this session is no longer stored: retention removed its rows up to %s.\n",
			ws.TruncatedStartAt.UTC().Format(workSessionTimeLayout))
	}
}

// printStoredFailuresNote says when the session failed more often than the
// log still shows (FR-043).
func printStoredFailuresNote(lifetime, stored int, earlierGone bool) {
	if !earlierGone {
		return
	}
	fmt.Printf("Note: this session had %d failures in its lifetime, %d are still stored. The earlier ones were removed by retention.\n", lifetime, stored)
}

// printTimelineRows prints timeline rows as a table and, when bodies were
// asked for, the bodies under it.
func printTimelineRows(rows []contracts.WorkSessionTimelineRow, includeBodies bool) error {
	headers := []string{"", "#", "TIME", "KIND", "TARGET", "STATUS", "DURATION", "ID", "DETAIL"}
	table := make([][]string, 0, len(rows))
	for i := range rows {
		table = append(table, timelineTableRow(&rows[i]))
	}
	if err := printTable(headers, table); err != nil {
		return err
	}
	if includeBodies {
		printTimelineBodies(rows)
	}
	return nil
}

// timelineTableRow renders one row. The first cell marks a failure (FAIL) or
// a refusal by MCPProxy itself (BLOCK); rows that are not calls carry their
// kind in capitals so they stand apart.
func timelineTableRow(row *contracts.WorkSessionTimelineRow) []string {
	mark := ""
	switch {
	case row.PolicyBlock:
		mark = "BLOCK"
	case row.Failed:
		mark = "FAIL"
	}
	at := row.At.UTC().Format(workSessionTimeLayoutMs)
	index := strconv.Itoa(row.Index)

	switch row.Kind {
	case contracts.TimelineKindSessionStart:
		return []string{mark, index, at, "SESSION START", "-", "-", "-", "-", "-"}
	case contracts.TimelineKindSessionEnd:
		return []string{mark, index, at, "SESSION END", "-", "-", "-", "-", "-"}
	}
	act := row.Activity
	if act == nil {
		return []string{mark, index, at, wsCell(row.Kind, workSessionNameCell), "-", "-", "-", "-", "-"}
	}

	if row.Kind == contracts.TimelineKindUpstreamEvent {
		return []string{mark, index, at, "UPSTREAM EVENT",
			dashIfEmpty(wsCell(act.ServerName, 48)),
			dashIfEmpty(wsCell(act.Status, 16)),
			"-",
			wsCell(act.ID, 0),
			upstreamEventDetail(row, act),
		}
	}

	target := act.ToolName
	if act.ServerName != "" && act.ToolName != "" {
		target = act.ServerName + ":" + act.ToolName
	} else if act.ServerName != "" {
		target = act.ServerName
	}
	duration := "-"
	if act.DurationMs > 0 {
		duration = formatActivityDuration(act.DurationMs)
	}
	var detail []string
	if act.ErrorClass != "" {
		detail = append(detail, act.ErrorClass)
	}
	if act.BlockReason != "" {
		detail = append(detail, act.BlockReason)
	}
	if act.ErrorMessage != "" {
		detail = append(detail, act.ErrorMessage)
	} else if reason, _ := act.Metadata["reason"].(string); reason != "" && row.Kind == contracts.TimelineKindPolicyDecision {
		// A refusal by MCPProxy itself carries its reason here.
		detail = append(detail, reason)
	}
	return []string{mark, index, at,
		wsCell(row.Kind, workSessionNameCell),
		dashIfEmpty(wsCell(target, 64)),
		dashIfEmpty(wsCell(act.Status, 16)),
		duration,
		wsCell(act.ID, 0),
		dashIfEmpty(wsText(strings.Join(detail, ": "), workSessionDetailCell)),
	}
}

// upstreamEventDetail is the kind, code and fixed message of an upstream
// event, with the repeat count of a collapsed row.
func upstreamEventDetail(row *contracts.WorkSessionTimelineRow, act *contracts.ActivityRecord) string {
	var parts []string
	if row.PrecedesSession {
		parts = append(parts, "(before the session started)")
	}
	// The repeat count of a collapsed row goes with the kind, ahead of the
	// message: the cell is cut at its end, and "three times" must not be the
	// part that is lost.
	kind, _ := act.Metadata["kind"].(string)
	if count, _ := act.Metadata["count"].(float64); count > 1 {
		kind = strings.TrimSpace(fmt.Sprintf("%s x%d", kind, int(count)))
	}
	if kind != "" {
		parts = append(parts, kind)
	}
	if code, _ := act.Metadata["code"].(string); code != "" {
		parts = append(parts, code)
	}
	message := act.ErrorMessage
	if message == "" {
		message, _ = act.Metadata["message"].(string)
	}
	if message != "" {
		parts = append(parts, message)
	}
	return dashIfEmpty(wsText(strings.Join(parts, " · "), workSessionDetailCell))
}

// printTimelineBodies prints the argument and response bodies of the rows
// that carry them. A body is upstream or agent text: it is escaped and capped
// like every other cell; -o json carries it whole.
func printTimelineBodies(rows []contracts.WorkSessionTimelineRow) {
	printed := false
	heading := func() {
		if !printed {
			fmt.Println("\nBodies (capped; -o json has them whole):")
			printed = true
		}
	}
	for i := range rows {
		row := &rows[i]
		act := row.Activity
		if act == nil || row.Kind == contracts.TimelineKindUpstreamEvent {
			continue
		}
		if row.BodiesRemoved {
			heading()
			fmt.Printf("  #%d %s: bodies removed by retention\n", row.Index, wsCell(act.ID, 0))
			continue
		}
		if len(act.Arguments) == 0 && act.Response == "" {
			continue
		}
		heading()
		fmt.Printf("  #%d %s\n", row.Index, wsCell(act.ID, 0))
		if len(act.Arguments) > 0 {
			fmt.Printf("    arguments: %s\n", timelineBodyText(act.Arguments))
		}
		if act.Response != "" {
			fmt.Printf("    response:  %s\n", timelineBodyText(act.Response))
		}
	}
}

// timelineBodyText renders a body as one line of JSON: the arguments as their
// object, the response as a quoted string. JSON keeps every character of the
// value (a run of spaces in a file name stays a run of spaces, a line break is
// \n) and puts it on one line. What a terminal would still act on is shown
// escaped, and the line is capped at workSessionBodyRunes. It does not go
// through sanitizeCell, whose cap is the 200 runes of a table cell.
func timelineBodyText(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "(not printable: " + termEscape(err.Error(), false) + ")"
	}
	// What a terminal would still act on becomes its \uXXXX escape, which is
	// JSON too: a line that was not capped still parses.
	safe := string(jsonTermSafe(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))))
	if runes := []rune(safe); len(runes) > workSessionBodyRunes {
		return string(runes[:workSessionBodyRunes]) + "..."
	}
	return safe
}

// --- every failure of one session ----------------------------------------------

// workSessionFailureRef names one failure of the walk: its number among the
// stored failures and the index of its row.
type workSessionFailureRef struct {
	Number int `json:"number"`
	Index  int `json:"index"`
}

// workSessionFailuresView is the -o json|yaml shape of `session --failures`:
// the rows that are context of some failure, in order and each once.
type workSessionFailuresView struct {
	Session contracts.WorkSession              `json:"session"`
	Rows    []contracts.WorkSessionTimelineRow `json:"rows"`
	// Failures lists every failure that was walked.
	Failures []workSessionFailureRef `json:"failures"`
	// Before and After are the rows of context kept around each failure.
	Before       int `json:"before"`
	After        int `json:"after"`
	TotalRows    int `json:"total_rows"`
	FailureTotal int `json:"failure_total"`
	// FailuresNotReturned counts stored failures whose row the door did not
	// return: removed while the session was read, or not readable with the
	// caller's credential.
	FailuresNotReturned      int  `json:"failures_not_returned,omitempty"`
	LifetimeFailures         int  `json:"lifetime_failures"`
	EarlierFailuresNotStored bool `json:"earlier_failures_not_stored,omitempty"`
}

// timelineFetcher reads one page of the timeline positioned at `at`.
type timelineFetcher func(at string, before, after int) (*contracts.WorkSessionTimelineResponse, error)

// errWorkSessionChanged reports that the rows of a session moved while its
// failures were being walked.
var errWorkSessionChanged = errors.New("this session changed while its failures were being read (retention removed some of its rows): run the command again")

// walkRestart ends one pass of the walk because the session changed under it.
// cause is the door's refusal when that is how the change showed.
type walkRestart struct{ cause error }

func (e *walkRestart) Error() string {
	if e.cause != nil {
		return e.cause.Error()
	}
	return errWorkSessionChanged.Error()
}

// walkSessionFailures reads every stored failure of a session with `before`
// rows ahead of it and `after` rows behind it.
//
// Failures are addressed by their number among the stored ones, and rows by
// their index in the timeline. Both move when retention removes older rows in
// the middle of the walk. A pass that sees it happen is thrown away and the
// walk starts over, so a result never mixes two numberings.
func walkSessionFailures(fetch timelineFetcher, before, after, lookahead int) (*workSessionFailuresView, error) {
	for attempt := 1; ; attempt++ {
		view, err := walkSessionFailuresOnce(fetch, before, after, lookahead)
		var restart *walkRestart
		if !errors.As(err, &restart) {
			return view, err
		}
		if attempt == workSessionWalkAttempts {
			if restart.cause != nil {
				return nil, fmt.Errorf("%w. The server's last answer was: %s", errWorkSessionChanged, restart.cause)
			}
			return nil, errWorkSessionChanged
		}
	}
}

// walkSessionFailuresOnce is one pass of the walk.
//
// A page is asked for with as much forward context as the door gives, so the
// failures that follow closely arrive with it and need no request of their
// own: on a page positioned at a failure, a later failure has its whole
// backward context on the page (the page is contiguous and starts `before`
// rows ahead of the position), and its forward context when the page reaches
// far enough or the timeline ends there. The first failure that is not whole
// on the page is the next position. lookahead is that forward reach; with
// bodies it is `after`, so no body is fetched that will not be printed.
func walkSessionFailuresOnce(fetch timelineFetcher, before, after, lookahead int) (*workSessionFailuresView, error) {
	// One page holds workSessionPageMax rows; the door cuts the forward
	// context of a larger request.
	if room := workSessionPageMax - 1 - before; lookahead > room {
		lookahead = room
	}
	if lookahead < after {
		lookahead = after
	}
	page, err := fetch("first_failure", before, lookahead)
	if err != nil {
		return nil, err
	}
	view := &workSessionFailuresView{
		Session: page.Session, Rows: []contracts.WorkSessionTimelineRow{}, Failures: []workSessionFailureRef{},
		Before: before, After: after,
		TotalRows: page.TotalRows, FailureTotal: page.FailureTotal,
		LifetimeFailures: page.LifetimeFailures, EarlierFailuresNotStored: page.EarlierFailuresNotStored,
	}
	if page.FailureTotal == 0 {
		return view, nil
	}
	// Failures that are gone from the log: the lifetime count is exact, the
	// stored count is what the door walked. Retention removes the oldest rows,
	// so when this number changes every stored failure has a new number. A
	// failure recorded while the walk runs raises both counts and leaves it
	// as it was.
	removed := page.LifetimeFailures - page.FailureTotal

	kept := map[int]contracts.WorkSessionTimelineRow{}
	failureAt := map[int]int{} // row index -> failure number
	keep := func(page *contracts.WorkSessionTimelineResponse, failureIndex int) {
		for i := range page.Rows {
			if idx := page.Rows[i].Index; idx >= failureIndex-before && idx <= failureIndex+after {
				kept[idx] = page.Rows[i]
			}
		}
	}
	// Every stored row a page returns is remembered by index and by activity
	// id, whether it is kept as context or not. consistent reports false when
	// a page contradicts an earlier one: another row at the same index, or the
	// same row at another index. Rows were then removed ahead of them between
	// the two requests, and the indexes of the two pages do not describe the
	// same timeline. The two rows derived from the session (its start and its
	// end) are left out: the end row moves when the session goes on.
	idAt := map[int]string{}
	indexOf := map[string]int{}
	consistent := func(page *contracts.WorkSessionTimelineResponse) bool {
		for i := range page.Rows {
			row := &page.Rows[i]
			if row.Activity == nil || row.Activity.ID == "" {
				continue
			}
			id := row.Activity.ID
			if seen, ok := idAt[row.Index]; ok && seen != id {
				return false
			}
			if at, ok := indexOf[id]; ok && at != row.Index {
				return false
			}
			idAt[row.Index], indexOf[id] = id, row.Index
		}
		return true
	}
	if !consistent(page) {
		return nil, &walkRestart{}
	}

	requested := 1
	for {
		// The session, the totals and the lifetime facts are the latest read.
		view.Session, view.TotalRows, view.FailureTotal = page.Session, page.TotalRows, page.FailureTotal
		view.LifetimeFailures, view.EarlierFailuresNotStored = page.LifetimeFailures, page.EarlierFailuresNotStored

		number := page.FailureIndex
		if number < 1 {
			number = requested
		}
		last := page.Position
		anchored := false
		for i := range page.Rows {
			if idx := page.Rows[i].Index; idx > last {
				last = idx
			}
			if page.Rows[i].Index == page.Position {
				anchored = true
			}
		}
		// The position is the failure that was asked for, and the door gave
		// it its context. Its row can still be missing from the page: removed
		// since the door found it, or not readable with this credential. Then
		// there is nothing to show for it, and it is counted as not returned.
		covered := 1
		if anchored {
			failureAt[page.Position] = number
			keep(page, page.Position)
		} else if row, ok := kept[page.Position]; ok && row.Failed {
			// An earlier page returned this row as context of the failure
			// before it. It is shown, so it is numbered, not counted as
			// missing, and it keeps the context this page has for it.
			failureAt[page.Position] = number
			keep(page, page.Position)
		}
		previous := page.Position
		for i := range page.Rows {
			row := &page.Rows[i]
			if row.Index <= page.Position {
				continue
			}
			if row.Index != previous+1 {
				// A row is missing here. It may have been a failure, so the
				// number of the next one is not known from this page: it is
				// the next position, and the door says which it is.
				break
			}
			previous = row.Index
			if !row.Failed {
				continue
			}
			if row.Index+after > last && page.HasAfter {
				break // its forward context is cut: it is the next position
			}
			failureAt[row.Index] = number + covered
			keep(page, row.Index)
			covered++
		}

		// Always forward: a door that answers the same failure twice cannot
		// hold the walk.
		next := number + covered
		if next <= requested {
			next = requested + 1
		}
		if next > page.FailureTotal {
			break
		}
		requested = next
		rowsBefore := page.TotalRows
		if page, err = fetch("failure:"+strconv.Itoa(next), before, lookahead); err != nil {
			// The door knew this failure a moment ago. A refusal now (no such
			// failure) is how a session that lost rows in between answers.
			// Any other refusal (a read error of the daemon, for one) is the
			// command's error as it is.
			var apiErr *cliclient.APIError
			if errors.As(err, &apiErr) && apiErr.Message == contracts.WorkSessionNoSuchPositionMessage {
				return nil, &walkRestart{cause: err}
			}
			return nil, err
		}
		// Rows of a session are only ever added at its end or removed from its
		// start: fewer rows, or a new distance between the lifetime and the
		// stored failure count, means indexes and numbers have moved.
		if page.TotalRows < rowsBefore || page.LifetimeFailures-page.FailureTotal != removed || !consistent(page) {
			return nil, &walkRestart{}
		}
	}

	indexes := make([]int, 0, len(kept))
	for idx := range kept {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	for _, idx := range indexes {
		view.Rows = append(view.Rows, kept[idx])
	}
	failureRows := make([]int, 0, len(failureAt))
	for idx := range failureAt {
		failureRows = append(failureRows, idx)
	}
	sort.Ints(failureRows)
	for _, idx := range failureRows {
		view.Failures = append(view.Failures, workSessionFailureRef{Number: failureAt[idx], Index: idx})
	}
	if missing := view.FailureTotal - len(view.Failures); missing > 0 {
		view.FailuresNotReturned = missing
	}
	return view, nil
}

func runActivitySessionFailures(id string, before, after int, includeBodies bool) error {
	s, err := newRESTSession("activity session")
	if err != nil {
		return err
	}
	lookahead := workSessionContextMax
	if includeBodies {
		lookahead = after
	}
	view, err := walkSessionFailures(func(at string, before, after int) (*contracts.WorkSessionTimelineResponse, error) {
		ctx, cancel := context.WithTimeout(context.Background(), workSessionCallTimeout)
		defer cancel()
		return s.client.GetWorkSessionTimeline(ctx, id, cliclient.WorkSessionTimelineParams{
			At: at, Before: &before, After: &after, IncludeBodies: includeBodies,
		})
	}, before, after, lookahead)
	if err != nil {
		return workSessionError(err)
	}
	if structuredOutput() {
		return printJSONValue(view)
	}

	printWorkSessionHeader(&view.Session)
	printStoredFailuresNote(view.LifetimeFailures, view.FailureTotal, view.EarlierFailuresNotStored)
	if view.Session.Status == "active" && len(view.Failures) > 1 {
		// The failures were read in several requests, and nothing holds the
		// timeline still between them.
		fmt.Println("Note: this session is still active. A call made while it was read can add rows ahead of the later failures, so the row numbers (#) of different groups may not be from the same moment. Every row and every failure is still printed once.")
	}
	if view.FailuresNotReturned > 0 {
		fmt.Printf("Note: %d of the %d stored failures were not returned: their rows were removed while the session was read, or are not readable with this credential.\n",
			view.FailuresNotReturned, view.FailureTotal)
	}
	if len(view.Failures) == 0 {
		if view.FailureTotal == 0 {
			fmt.Println("\nNo stored failures in this session.")
		}
		return nil
	}
	for _, group := range groupFailureContexts(view) {
		fmt.Println()
		fmt.Println(failureGroupHeading(group.numbers, view.FailureTotal))
		if err := printTimelineRows(group.rows, includeBodies); err != nil {
			return err
		}
	}
	return nil
}

// failureContextGroup is a run of rows that is the context of one failure, or
// of several whose contexts overlap or touch.
type failureContextGroup struct {
	numbers []int // the failures shown in the group, ascending
	rows    []contracts.WorkSessionTimelineRow
}

// failureGroupHeading names the failures of a group: "Failure 3 of 7",
// "Failures 1-3 of 7" for a run, and "Failures 1, 3 of 7" when one in between
// is not shown, so the heading never claims a failure that is not under it.
func failureGroupHeading(numbers []int, total int) string {
	if len(numbers) == 1 {
		return fmt.Sprintf("Failure %d of %d", numbers[0], total)
	}
	first, last := numbers[0], numbers[len(numbers)-1]
	if last-first == len(numbers)-1 {
		return fmt.Sprintf("Failures %d-%d of %d", first, last, total)
	}
	parts := make([]string, len(numbers))
	for i, n := range numbers {
		parts[i] = strconv.Itoa(n)
	}
	return fmt.Sprintf("Failures %s of %d", strings.Join(parts, ", "), total)
}

// groupFailureContexts splits the walked rows where the context of one
// failure ends before the context of the next begins.
func groupFailureContexts(view *workSessionFailuresView) []failureContextGroup {
	var groups []failureContextGroup
	end := 0 // last row index of the current group's context
	next := 0
	for _, f := range view.Failures {
		from, to := f.Index-view.Before, f.Index+view.After
		if len(groups) == 0 || from > end+1 {
			groups = append(groups, failureContextGroup{})
			end = to
		}
		g := &groups[len(groups)-1]
		g.numbers = append(g.numbers, f.Number)
		if to > end {
			end = to
		}
		for next < len(view.Rows) && view.Rows[next].Index <= end {
			g.rows = append(g.rows, view.Rows[next])
			next++
		}
	}
	return groups
}

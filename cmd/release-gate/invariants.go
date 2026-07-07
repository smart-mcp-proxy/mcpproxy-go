package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/gatereport"
)

// ============================================================================
// FR-011 — activity-log completeness with request-id correlation
// ============================================================================

// activityCheckResult carries the empirical correlation-mode finding.
type activityCheckResult struct {
	CorrelationMode string   // "header" | "recorded-id"
	Resolved        int      // calls whose request id resolved via ?request_id=
	Missing         []string // nonces with no activity record
	Unresolvable    []string // request ids that failed to resolve
	Limitation      string   // recorded when header correlation is unsupported
}

// checkActivityRequestIDs asserts FR-011: 100% of the correlated tool calls
// issued during the matrix run appear in the activity log, and every
// recorded request id resolves via GET /api/v1/activity?request_id=.
//
// Empirical probe (per stage instructions): each issued call carried a
// caller-chosen X-Request-Id. If querying by that header id finds the call's
// activity record, header correlation works end-to-end and is used. If not
// (today internal/server/mcp.go synthesizes its own per-call request ids for
// both MCP-native and REST tool calls), the check falls back to locating
// each call by its unique argument nonce, then proves the RECORDED request
// id round-trips through the ?request_id= filter — and reports the
// limitation instead of hiding it (no middleware scope creep in this stage).
func checkActivityRequestIDs(ctx context.Context, c *Client, calls []issuedCall, timeout time.Duration) (*activityCheckResult, error) {
	if len(calls) == 0 {
		return nil, fmt.Errorf("no issued calls recorded by the matrix run; nothing to correlate")
	}
	res := &activityCheckResult{}

	// Empirical probe: does the caller's X-Request-Id land on activity records?
	headerHits := 0
	for _, call := range calls {
		if call.HeaderRequestID == "" {
			continue
		}
		recs, err := activityByRequestID(ctx, c, call.HeaderRequestID)
		if err != nil {
			return nil, err
		}
		if containsNonce(recs, call.Nonce) {
			headerHits++
		}
	}
	if headerHits == len(calls) {
		res.CorrelationMode = "header"
		res.Resolved = headerHits
		return res, nil
	}
	res.CorrelationMode = "recorded-id"
	res.Limitation = fmt.Sprintf(
		"caller-supplied X-Request-Id is not persisted on tool_call activity records "+
			"(%d/%d header lookups matched); correlation uses per-call argument nonces and the "+
			"core-recorded request ids instead", headerHits, len(calls))

	// The activity log write is asynchronous; poll until every nonce lands.
	deadline := time.Now().Add(timeout)
	for {
		recent, err := recentToolActivities(ctx, c)
		if err != nil {
			return nil, err
		}
		res.Missing = res.Missing[:0]
		res.Unresolvable = res.Unresolvable[:0]
		res.Resolved = 0
		for _, call := range calls {
			rec := findByNonce(recent, call.Nonce)
			if rec == nil {
				res.Missing = append(res.Missing, call.Nonce)
				continue
			}
			if rec.RequestID == "" {
				res.Unresolvable = append(res.Unresolvable, fmt.Sprintf("nonce %s: empty request_id", call.Nonce))
				continue
			}
			byID, err := activityByRequestID(ctx, c, rec.RequestID)
			if err != nil {
				return nil, err
			}
			if len(byID) == 0 {
				res.Unresolvable = append(res.Unresolvable, rec.RequestID)
				continue
			}
			res.Resolved++
		}
		if len(res.Missing) == 0 && len(res.Unresolvable) == 0 {
			return res, nil
		}
		if time.Now().After(deadline) {
			return res, fmt.Errorf("activity invariant failed: %d/%d calls resolved (missing nonces: %v; unresolvable request ids: %v)",
				res.Resolved, len(calls), res.Missing, res.Unresolvable)
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func activityByRequestID(ctx context.Context, c *Client, requestID string) ([]activityRecord, error) {
	q := url.Values{}
	q.Set("request_id", requestID)
	q.Set("limit", "50")
	recs, _, err := c.activities(ctx, q)
	return recs, err
}

// recentToolActivities fetches a window of recent activity records large
// enough to cover the matrix traffic.
func recentToolActivities(ctx context.Context, c *Client) ([]activityRecord, error) {
	q := url.Values{}
	q.Set("limit", "500")
	recs, _, err := c.activities(ctx, q)
	return recs, err
}

// findByNonce locates the activity record whose arguments embed the nonce.
func findByNonce(recs []activityRecord, nonce string) *activityRecord {
	for i := range recs {
		if activityMatchesNonce(&recs[i], nonce) {
			return &recs[i]
		}
	}
	return nil
}

func containsNonce(recs []activityRecord, nonce string) bool {
	return findByNonce(recs, nonce) != nil
}

func activityMatchesNonce(rec *activityRecord, nonce string) bool {
	if rec.Arguments == nil {
		return false
	}
	raw, err := json.Marshal(rec.Arguments)
	if err != nil {
		return false
	}
	return strings.Contains(string(raw), nonce)
}

// ============================================================================
// FR-012 — counters must move under traffic
// ============================================================================

// takeCounterSnapshot reads the pinned counters. The telemetry payload may
// legitimately be unavailable (503) when the telemetry service is absent;
// that is recorded, not fatal.
func takeCounterSnapshot(ctx context.Context, c *Client) (*counterSnapshot, error) {
	snap := &counterSnapshot{TakenAt: time.Now().UTC()}
	ts, err := c.tokenStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("token stats snapshot: %w", err)
	}
	snap.TokensToolListSize = ts.TotalServerToolListSize
	snap.TokensSaved = ts.SavedTokens

	u, err := c.usage(ctx)
	if err != nil {
		return nil, fmt.Errorf("usage snapshot: %w", err)
	}
	for _, t := range u.Tools {
		snap.UsageCalls += t.Calls
	}

	tp, available, err := c.telemetry(ctx)
	if err != nil {
		return nil, fmt.Errorf("telemetry snapshot: %w", err)
	}
	snap.TelemetryAvailable = available
	if available {
		for _, v := range tp.BuiltinToolCalls {
			snap.TelemetryBuiltin += v
		}
	}
	return snap, nil
}

// checkCountersMoved asserts FR-012 against the baseline snapshot taken at
// core boot (before the matrix upstreams connected and before any traffic):
//
//   - /api/v1/stats/tokens: total_server_tool_list_size must strictly
//     increase (five upstreams were indexed after the baseline);
//   - /api/v1/activity/usage: the per-tool call total must strictly increase
//     (matrix traffic); the usage aggregate is actor-owned and asynchronous,
//     so the check polls until movement or timeout;
//   - /api/v1/telemetry/payload: pinned to builtin_tool_calls only — pure
//     in-process counters that move with call_tool_*/retrieve_tools traffic
//     regardless of whether heartbeat SENDING is enabled. Network-dependent
//     fields are deliberately excluded. If the telemetry service is unavailable
//     (503) at the boot baseline, that sub-check is recorded as skipped with a
//     reason (FR-004 environment skip). If it is healthy at the baseline but
//     disappears under matrix traffic, that is a regression and the sub-check
//     FAILS rather than being masked as a skip.
func checkCountersMoved(ctx context.Context, c *Client, before *counterSnapshot, timeout time.Duration) ([]gatereport.Step, *counterSnapshot, error) {
	if before == nil {
		return nil, nil, fmt.Errorf("no baseline counter snapshot recorded by the matrix run")
	}
	deadline := time.Now().Add(timeout)
	var after *counterSnapshot
	var err error
	for {
		after, err = takeCounterSnapshot(ctx, c)
		if err != nil {
			return nil, nil, err
		}
		// The telemetry sub-check only skips when telemetry was unavailable at
		// the baseline (no reference point). If it was available at baseline we
		// must wait for it to actually move — a mid-run disappearance is a
		// regression, not a reason to stop polling early.
		telemetryMoved := !before.TelemetryAvailable ||
			(after.TelemetryAvailable && after.TelemetryBuiltin > before.TelemetryBuiltin)
		moved := after.TokensToolListSize > before.TokensToolListSize &&
			after.UsageCalls > before.UsageCalls &&
			telemetryMoved
		if moved || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, after, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	var steps []gatereport.Step
	var failures []string
	addStep := func(name string, ok bool, detail string) {
		st := gatereport.Step{Name: name, Status: gatereport.StatusPass, Reason: detail}
		if !ok {
			st.Status = gatereport.StatusFail
			failures = append(failures, name+": "+detail)
		}
		steps = append(steps, st)
	}

	addStep("tokens-strictly-increase",
		after.TokensToolListSize > before.TokensToolListSize,
		fmt.Sprintf("total_server_tool_list_size %d -> %d", before.TokensToolListSize, after.TokensToolListSize))
	addStep("usage-calls-strictly-increase",
		after.UsageCalls > before.UsageCalls,
		fmt.Sprintf("usage call total %d -> %d", before.UsageCalls, after.UsageCalls))

	switch {
	case !before.TelemetryAvailable:
		// Telemetry was already unavailable at the boot baseline: this
		// environment has telemetry disabled, so there is nothing to assert
		// (FR-004 environment skip).
		steps = append(steps, gatereport.Step{
			Name:   "telemetry-builtin-tool-calls-increase",
			Status: gatereport.StatusSkipped,
			Reason: "telemetry payload unavailable (503) at baseline — telemetry service disabled in this environment; counter not asserted",
		})
	case after.TelemetryAvailable:
		addStep("telemetry-builtin-tool-calls-increase",
			after.TelemetryBuiltin > before.TelemetryBuiltin,
			fmt.Sprintf("builtin_tool_calls total %d -> %d", before.TelemetryBuiltin, after.TelemetryBuiltin))
	default:
		// Telemetry was healthy at the baseline but the endpoint went
		// unavailable (503) under matrix traffic — a real regression, not an
		// environment skip. Fail rather than mask it.
		addStep("telemetry-builtin-tool-calls-increase", false,
			fmt.Sprintf("telemetry payload became unavailable after a healthy baseline (builtin_tool_calls baseline %d) — endpoint regressed under matrix traffic", before.TelemetryBuiltin))
	}

	if len(failures) > 0 {
		return steps, after, fmt.Errorf("flat counters under traffic: %s", strings.Join(failures, "; "))
	}
	return steps, after, nil
}

// ============================================================================
// FR-013 — quarantine + approval end-to-end (Spec 032)
// ============================================================================

type quarantineFlowDeps struct {
	FixtureBinary string
	WorkDir       string
	ServerName    string
	// timeouts, overridable in tests
	AppearTimeout  time.Duration
	ConnectTimeout time.Duration
}

// checkQuarantineFlow adds a fresh stdio fixture server mid-run via the
// management API and asserts the full Spec 032 lifecycle:
//
//  1. the server lands in quarantine (a pre-approved server FAILS the check);
//  2. tool calls are blocked while quarantined;
//  3. unquarantine + tool approval succeed;
//  4. a post-approval call round-trips.
//
// The fixture binary is copied to a unique path so cleanup can never touch
// other processes.
func checkQuarantineFlow(ctx context.Context, c *Client, deps quarantineFlowDeps) (steps []gatereport.Step, cleanup func(), err error) {
	if deps.ServerName == "" {
		deps.ServerName = "gate-fresh-" + randomNonce()[:6]
	}
	if deps.AppearTimeout == 0 {
		deps.AppearTimeout = 30 * time.Second
	}
	if deps.ConnectTimeout == 0 {
		deps.ConnectTimeout = 60 * time.Second
	}
	binPath := filepath.Join(deps.WorkDir, "bin", "mcpfixture-"+deps.ServerName)
	if err := copyFile(deps.FixtureBinary, binPath); err != nil {
		return nil, func() {}, fmt.Errorf("copy fixture for fresh server: %w", err)
	}
	cleanup = func() {
		ctx := context.Background()
		_ = c.removeServer(ctx, deps.ServerName)
		_, _ = killByPattern(binPath)
	}

	step := func(name string, fn func() error) error {
		start := time.Now()
		stepErr := fn()
		s := gatereport.Step{Name: name, Status: gatereport.StatusPass, DurationMS: time.Since(start).Milliseconds()}
		if stepErr != nil {
			s.Status = gatereport.StatusFail
			s.Reason = stepErr.Error()
		}
		steps = append(steps, s)
		if stepErr != nil {
			return fmt.Errorf("step %s: %w", name, stepErr)
		}
		return nil
	}

	// Add WITHOUT an explicit quarantined value: the default path must land
	// the server in quarantine (issue #370 semantics). Isolation is opted
	// out per-server: this fixture runs a host binary and the matrix run may
	// have global docker isolation enabled for the docker cell.
	if err := step("add-server", func() error {
		t, f := true, false
		return c.addServer(ctx, addServerRequest{
			Name:      deps.ServerName,
			Command:   binPath,
			Args:      []string{"--transport", "stdio"},
			Protocol:  "stdio",
			Enabled:   &t,
			Isolation: &isolationRequest{Enabled: &f},
		})
	}); err != nil {
		return steps, cleanup, err
	}

	if err := step("enters-quarantine", func() error {
		deadline := time.Now().Add(deps.AppearTimeout)
		for time.Now().Before(deadline) {
			if err := ctx.Err(); err != nil {
				return err
			}
			srv, err := c.server(ctx, deps.ServerName)
			if err == nil && srv != nil {
				if !srv.Quarantined {
					return fmt.Errorf("server %s was NOT quarantined on add (quarantine transition not observed — pre-approved servers must fail this invariant)", deps.ServerName)
				}
				return nil
			}
			time.Sleep(time.Second)
		}
		return fmt.Errorf("server %s never appeared after add", deps.ServerName)
	}); err != nil {
		return steps, cleanup, err
	}

	if err := step("call-blocked-while-quarantined", func() error {
		text, callErr := c.callToolREST(ctx, deps.ServerName+":echo", map[string]any{"text": "must-not-execute"}, "")
		if callErr != nil {
			// An error surface is also a valid block.
			return nil
		}
		// The block response is the structured security payload with
		// status QUARANTINED_SERVER_BLOCKED. It legitimately echoes the
		// requested tool name and args back (requestedArgs), so the check
		// keys on the status marker, not on argument absence. A successful
		// execution would instead return the fixture's {"echo":{...}} with
		// no such marker.
		if strings.Contains(text, "QUARANTINED_SERVER_BLOCKED") {
			return nil
		}
		return fmt.Errorf("tool call was NOT blocked while quarantined: %s", truncateStr(text, 200))
	}); err != nil {
		return steps, cleanup, err
	}

	if err := step("approve", func() error {
		if err := c.unquarantineServer(ctx, deps.ServerName); err != nil {
			return fmt.Errorf("unquarantine: %w", err)
		}
		// Wait for the server to connect, then approve its tools (Spec 032
		// tool-level baseline).
		deadline := time.Now().Add(deps.ConnectTimeout)
		for time.Now().Before(deadline) {
			if err := ctx.Err(); err != nil {
				return err
			}
			srv, err := c.server(ctx, deps.ServerName)
			if err == nil && srv != nil && srv.Connected && srv.ToolCount >= 2 {
				return c.approveAllTools(ctx, deps.ServerName)
			}
			time.Sleep(time.Second)
		}
		return fmt.Errorf("server %s did not connect within %s after unquarantine", deps.ServerName, deps.ConnectTimeout)
	}); err != nil {
		return steps, cleanup, err
	}

	if err := step("post-approval-call", func() error {
		nonce := "gate-fresh-" + randomNonce()
		deadline := time.Now().Add(deps.ConnectTimeout)
		var lastErr error
		for time.Now().Before(deadline) {
			if err := ctx.Err(); err != nil {
				return err
			}
			text, callErr := c.callToolREST(ctx, deps.ServerName+":echo", map[string]any{"text": nonce}, "")
			if callErr == nil && strings.Contains(text, nonce) {
				return nil
			}
			if callErr != nil {
				lastErr = callErr
			} else {
				lastErr = fmt.Errorf("response missing nonce: %s", truncateStr(text, 200))
			}
			time.Sleep(2 * time.Second)
		}
		return fmt.Errorf("post-approval call did not round-trip: %v", lastErr)
	}); err != nil {
		return steps, cleanup, err
	}

	return steps, cleanup, nil
}

// ============================================================================
// shared: search assertion used by the upgrade check (FR-014)
// ============================================================================

// assertSearchHasResults verifies /api/v1/index/search returns at least one
// result for the query, containing wantSubstring in a tool name; polls until
// timeout (index open is asynchronous at startup). Fails on corrupted or
// absent indexes.
func assertSearchHasResults(ctx context.Context, c *Client, query, wantSubstring string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastDetail string
	for {
		resp, err := c.searchIndex(ctx, query, 20)
		if err == nil && len(resp.Results) > 0 {
			if wantSubstring == "" {
				return nil
			}
			for _, r := range resp.Results {
				if strings.Contains(r.Tool.Name, wantSubstring) || strings.Contains(r.Tool.ServerName, wantSubstring) {
					return nil
				}
			}
			lastDetail = fmt.Sprintf("%d results but none matched %q", len(resp.Results), wantSubstring)
		} else if err != nil {
			lastDetail = err.Error()
		} else {
			lastDetail = "0 results"
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("index search %q returned no usable results after %s: %s", query, timeout, lastDetail)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

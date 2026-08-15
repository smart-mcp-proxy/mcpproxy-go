package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolannotations"
)

// Spec 098 — POST /api/v1/preflight.
//
// The contract this file implements, in one place because the pieces are easy
// to break independently:
//
//   - HTTP status reports whether the CHECK executed, never what it found. A
//     fully blocked set is a 200 carrying verdict "blocked"; only a malformed
//     request (400) or a proxy that cannot answer honestly (503) is non-200.
//   - The activity record is written SYNCHRONOUSLY before the 200 (FR-014). A
//     failed write is a 503: a preflight nobody can audit afterwards breaks the
//     transparency guarantee the feature exists to provide.
//   - Rejected requests (400/503) execute no preflight and therefore write no
//     record.
const (
	// preflightMaxTools bounds the RAW tools array, before dedup (FR-008): the
	// limit is about request size, not about how much work the evaluator does.
	preflightMaxTools = 100
	// preflightMaxWaitMS is the wait_ms cap. Over it is a 400 rather than a
	// silent clamp, so a caller asking for a 60s wait learns that it will not
	// get one.
	preflightMaxWaitMS = 10000
	// preflightPollFloor is the minimum interval between re-evaluations during
	// a wait (FR-012). Waiting must add no meaningful load.
	preflightPollFloor = 250 * time.Millisecond
	// preflightWaitSlots is the dedicated wait budget: the number of preflights
	// that may be parked in their poll loop at once. Small and fixed — a flood
	// of waiting preflights must not tie up the HTTP server, and spec 093's
	// admission control is scoped to upstream tool calls, not to this.
	preflightWaitSlots = 4
)

// preflightValidationError is a caller-input error: it becomes a 400 with its
// own message, which names the exact rule that was broken.
type preflightValidationError struct {
	message string
}

func (e *preflightValidationError) Error() string { return e.message }

func newPreflightValidationError(format string, args ...interface{}) *preflightValidationError {
	return &preflightValidationError{message: fmt.Sprintf(format, args...)}
}

// normalizePreflightTools validates the raw tools array and deduplicates it,
// preserving first-occurrence order (FR-008).
//
// Deduplication is by id. Two entries for the same id carrying different
// pin_hash values are a validation error rather than a "last one wins": the
// request states two incompatible expectations about the same tool and guessing
// which one the caller meant would silently answer a question they did not ask.
// An omitted pin counts as a value here — {id} and {id, pin} disagree about
// whether the tool is pinned at all.
func normalizePreflightTools(raw []contracts.PreflightToolRef) ([]preflight.ToolRef, error) {
	if len(raw) == 0 {
		return nil, newPreflightValidationError("tools must contain at least one entry: an empty preflight is a caller bug, not a trivially-green check")
	}
	if len(raw) > preflightMaxTools {
		return nil, newPreflightValidationError("tools contains %d entries, which exceeds the limit of %d per request", len(raw), preflightMaxTools)
	}

	seen := make(map[string]int, len(raw))
	out := make([]preflight.ToolRef, 0, len(raw))
	for _, ref := range raw {
		id := strings.TrimSpace(ref.ID)
		pin := strings.TrimSpace(ref.PinHash)
		if idx, ok := seen[id]; ok {
			if out[idx].PinHash != pin {
				return nil, newPreflightValidationError("duplicate tool id %q carries conflicting pin_hash values (%q and %q)", id, out[idx].PinHash, pin)
			}
			continue
		}
		seen[id] = len(out)
		out = append(out, preflight.ToolRef{ID: id, PinHash: pin})
	}
	return out, nil
}

// validatePreflightWait enforces the wait_ms range (FR-012 cap).
func validatePreflightWait(waitMS int) error {
	if waitMS < 0 {
		return newPreflightValidationError("wait_ms must not be negative")
	}
	if waitMS > preflightMaxWaitMS {
		return newPreflightValidationError("wait_ms is %d, which exceeds the cap of %d", waitMS, preflightMaxWaitMS)
	}
	return nil
}

// preflightParams turns the authenticated request plus its validated body into
// the evaluator's parameters.
//
// Tier detection is the security-relevant half (FR-013): everything the auth
// middleware authenticated as admin — API key over TCP, the Unix socket, the
// Windows named pipe — is the operator tier; an agent token is the scoped tier
// and carries its allowed_servers and profile pin into the evaluation. Tier is
// always set explicitly: an empty Tier reads as operator to the evaluator, so a
// call site that forgets it would silently get the more permissive disclosure.
func preflightParams(r *http.Request, req *contracts.PreflightRequest, tools []preflight.ToolRef) preflight.Params {
	params := preflight.Params{
		Tools:   tools,
		Tier:    preflight.TierOperator,
		Profile: strings.TrimSpace(req.Profile),
	}
	if req.Policy != nil {
		params.Filters = toolannotations.Filters{
			ReadOnlyOnly:       req.Policy.ReadOnlyOnly,
			ExcludeDestructive: req.Policy.ExcludeDestructive,
			ExcludeOpenWorld:   req.Policy.ExcludeOpenWorld,
		}
	}
	if tier, authCtx := disclosureTier(r); tier == preflight.TierAgentToken {
		params.Tier = preflight.TierAgentToken
		params.TokenServers = authCtx.AllowedServers
		params.TokenProfilePin = authCtx.ProfilePin
	}
	return params
}

// disclosureTier maps an authenticated request to the Spec 098 disclosure tier.
// It is the single source of that mapping: preflight uses it for the evaluator
// tier, and the tool-listing endpoints use it to decide whether a tool's hash
// pin may be published (T020, FR-011 + FR-013). The returned AuthContext is
// non-nil only for the agent-token tier, whose scope the caller needs.
func disclosureTier(r *http.Request) (preflight.Tier, *auth.AuthContext) {
	if authCtx := auth.AuthContextFromContext(r.Context()); authCtx != nil && authCtx.Type == auth.AuthTypeAgent {
		return preflight.TierAgentToken, authCtx
	}
	return preflight.TierOperator, nil
}

// handlePreflight handles POST /api/v1/preflight
// @Summary Preflight required tools
// @Description Deterministic, side-effect-free availability check for a caller-supplied list of tool IDs (Spec 098). Performs zero upstream calls and mutates no runtime state. HTTP status reports whether the CHECK executed: a fully blocked set is still 200, with the availability verdict in the body. Every executed preflight writes an activity record before the response is returned.
// @Tags tools
// @Accept json
// @Produce json
// @Param request body contracts.PreflightRequest true "Tool IDs (1-100 before dedup), optional profile, annotation policy filters and wait budget"
// @Success 200 {object} contracts.APIResponse{data=contracts.PreflightResponse} "Preflight verdict and per-tool results"
// @Failure 400 {object} contracts.APIResponse "Validation error (empty or oversized tool list, conflicting duplicate pins, unknown profile, wait_ms out of range)"
// @Failure 401 {object} contracts.APIResponse "Missing or invalid credentials"
// @Failure 503 {object} contracts.APIResponse "Runtime unavailable, evaluator infrastructure read failure, or the activity record could not be persisted"
// @Security ApiKeyHeader
// @Security ApiKeyQuery
// @Router /api/v1/preflight [post]
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	var req contracts.PreflightRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	tools, err := normalizePreflightTools(req.Tools)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePreflightWait(req.WaitMS); err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	params := preflightParams(r, &req, tools)

	outcome, waitedMS, err := s.runPreflightWithWait(r.Context(), params, req.WaitMS)
	if err != nil {
		switch {
		case errors.Is(err, preflight.ErrUnknownProfile):
			// A profile the config does not define is a caller mistake, not
			// proxy state: inventing a verdict for it would put a request bug
			// into the reason taxonomy.
			s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("Unknown profile %q", strings.TrimSpace(req.Profile)))
		case errors.Is(err, preflight.ErrRuntimeUnavailable):
			s.writeError(w, r, http.StatusServiceUnavailable, "Preflight unavailable: the proxy runtime is not ready to evaluate")
		default:
			// An index/storage/snapshot read failed. Reduced-fidelity verdicts
			// are worse than no verdict: the caller would gate a pipeline on a
			// guess (FR-006).
			s.getRequestLogger(r).Errorw("Preflight evaluation failed", "error", err)
			s.writeError(w, r, http.StatusServiceUnavailable, "Preflight unavailable: local state could not be read")
		}
		return
	}

	response := preflightResponse(outcome, req.WaitMS, waitedMS)

	// FR-014: durable BEFORE the 200. This is the whole reason RecordPreflight
	// is synchronous and returns its error instead of going through the bounded
	// async activity channel, which drops under load.
	if err := s.controller.RecordPreflight(preflightActivityRecord(r, outcome)); err != nil {
		s.getRequestLogger(r).Errorw("Preflight activity record could not be persisted", "error", err)
		s.writeError(w, r, http.StatusServiceUnavailable, "Preflight unavailable: the activity record could not be persisted")
		return
	}

	s.writeSuccess(w, response)
}

// runPreflightWithWait evaluates once and, when a wait budget was requested and
// every current failure is retryable, keeps re-evaluating local state until the
// set is ready, a non-retryable failure appears, or the deadline passes
// (FR-012). It returns the final outcome and the milliseconds actually waited.
//
// It never blocks indefinitely and never queues: with the wait budget exhausted
// the request degrades to an immediate answer with waited_ms 0.
func (s *Server) runPreflightWithWait(ctx context.Context, params preflight.Params, waitMS int) (preflight.Outcome, int, error) {
	outcome, err := s.controller.RunPreflight(ctx, params)
	if err != nil || waitMS <= 0 {
		return outcome, 0, err
	}
	// Nothing to wait for: the set is ready, or it is blocked by something that
	// waiting cannot fix. Both terminate before a single sleep.
	if outcome.Verdict != preflight.VerdictDegradedRetryable {
		return outcome, 0, nil
	}
	if !s.acquirePreflightWaitSlot() {
		// Graceful degradation, not an error and not a queue: the caller gets
		// the current verdict and waited_ms 0, and can retry.
		return outcome, 0, nil
	}
	defer s.releasePreflightWaitSlot()

	interval := s.preflightPollInterval()
	start := time.Now()
	deadline := start.Add(time.Duration(waitMS) * time.Millisecond)
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		sleep := interval
		if sleep > remaining {
			sleep = remaining
		}
		timer.Reset(sleep)
		select {
		case <-ctx.Done():
			// The caller went away. Resolve with what we have rather than
			// manufacturing an error — "always resolves" (FR-012).
			return outcome, elapsedMS(start), nil
		case <-timer.C:
		}

		next, err := s.controller.RunPreflight(ctx, params)
		if err != nil {
			return preflight.Outcome{}, elapsedMS(start), err
		}
		outcome = next
		if outcome.Verdict != preflight.VerdictDegradedRetryable {
			break
		}
	}
	return outcome, elapsedMS(start), nil
}

func elapsedMS(start time.Time) int {
	ms := int(time.Since(start).Round(time.Millisecond) / time.Millisecond)
	if ms < 0 {
		return 0
	}
	return ms
}

// preflightPollInterval is the re-evaluation interval, floored at 250 ms
// (FR-012). Tests lower it via preflightPollOverride; production never does.
func (s *Server) preflightPollInterval() time.Duration {
	if s.preflightPollOverride > 0 {
		return s.preflightPollOverride
	}
	return preflightPollFloor
}

// acquirePreflightWaitSlot takes one of the dedicated wait slots without
// blocking. A nil semaphore (a Server built without NewServer) reports
// exhausted, which degrades to "answer immediately" — the safe direction.
func (s *Server) acquirePreflightWaitSlot() bool {
	if s.preflightWaitSem == nil {
		return false
	}
	select {
	case s.preflightWaitSem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releasePreflightWaitSlot() {
	if s.preflightWaitSem == nil {
		return
	}
	select {
	case <-s.preflightWaitSem:
	default:
	}
}

// preflightResponse serializes one outcome. waited_ms is present whenever a
// wait was REQUESTED — including the 0 that says "the wait budget was exhausted
// (or nothing was worth waiting for), here is the current state" — and absent
// when no wait was asked for at all.
func preflightResponse(outcome preflight.Outcome, waitMS, waitedMS int) contracts.PreflightResponse {
	response := contracts.PreflightResponse{
		Verdict:   outcome.Verdict,
		CheckedAt: time.Now().UTC(),
		Tools:     make([]contracts.PreflightToolResult, 0, len(outcome.Results)),
	}
	if response.Verdict == "" {
		response.Verdict = preflight.VerdictReady
	}
	if waitMS > 0 {
		waited := waitedMS
		response.WaitedMS = &waited
	}
	for i := range outcome.Results {
		result := outcome.Results[i]
		entry := contracts.PreflightToolResult{
			ID:         result.ID,
			Status:     result.Status,
			Hash:       result.Hash,
			DidYouMean: result.DidYouMean,
		}
		// A ready result carries no failure fields at all — `ready` is a
		// status, not a reason, and an emitted `retryable: false` on a ready
		// tool would read as a failure.
		if result.Status != preflight.StatusReady {
			retryable := result.Retryable
			entry.Reason = result.Reason
			entry.Retryable = &retryable
			entry.Action = result.Action
			entry.Detail = result.Detail
			entry.Remediation = result.Remediation
		}
		response.Tools = append(response.Tools, entry)
	}
	return response
}

// preflightActivityRecord builds the FR-014 payload: enum codes, counts and
// tool ids only. No descriptions, no arguments, no hashes.
func preflightActivityRecord(r *http.Request, outcome preflight.Outcome) internalRuntime.PreflightActivity {
	record := internalRuntime.PreflightActivity{
		RequestID: reqcontext.GetRequestID(r.Context()),
		Verdict:   outcome.Verdict,
		Source:    preflightActivitySource(r),
		Tools:     make([]internalRuntime.PreflightToolOutcome, 0, len(outcome.Results)),
	}
	if record.Verdict == "" {
		record.Verdict = preflight.VerdictReady
	}
	if authCtx := auth.AuthContextFromContext(r.Context()); authCtx != nil {
		record.UserID = authCtx.UserID
		record.UserEmail = authCtx.Email
	}
	for i := range outcome.Results {
		result := outcome.Results[i]
		outcomeEntry := internalRuntime.PreflightToolOutcome{
			ID:     result.ID,
			Status: result.Status,
		}
		if result.Status != preflight.StatusReady {
			outcomeEntry.Reason = result.Reason
		}
		record.Tools = append(record.Tools, outcomeEntry)
	}
	return record
}

// preflightActivitySource attributes the record to the surface that made the
// call, so a cron job's preflight is distinguishable from a Web-UI one. The
// CLI announces itself with the same client header tool calls use.
func preflightActivitySource(r *http.Request) storage.ActivitySource {
	if strings.HasPrefix(strings.ToLower(r.Header.Get(XMCPProxyClientHeader)), "cli/") {
		return storage.ActivitySourceCLI
	}
	return storage.ActivitySourceAPI
}

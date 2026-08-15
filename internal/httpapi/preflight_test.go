package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

const preflightTestAPIKey = "preflight-test-api-key"

// preflightStep is one scripted answer from the controller's RunPreflight. A
// script lets the wait-loop tests describe a state that CHANGES between polls,
// which is the only thing wait_ms exists for.
type preflightStep struct {
	outcome preflight.Outcome
	err     error
}

type preflightController struct {
	baseController

	mu      sync.Mutex
	script  []preflightStep
	last    preflightStep
	calls   []preflight.Params
	records []internalRuntime.PreflightActivity
	// deadlines records the deadline (zero when none) of the context each
	// evaluation ran under, so the wait loop's bounding can be asserted.
	deadlines []time.Time

	recordErr error
}

func (c *preflightController) GetCurrentConfig() interface{} {
	return &config.Config{APIKey: preflightTestAPIKey}
}

func (c *preflightController) RunPreflight(ctx context.Context, params preflight.Params) (preflight.Outcome, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, params)
	deadline, _ := ctx.Deadline()
	c.deadlines = append(c.deadlines, deadline)
	step := c.last
	if len(c.script) > 0 {
		step = c.script[0]
		if len(c.script) > 1 {
			c.script = c.script[1:]
		} else {
			// The last scripted step repeats forever, so a deadline test does
			// not have to guess how many polls will fit in the budget.
			c.last = step
			c.script = nil
		}
	}
	return step.outcome, step.err
}

func (c *preflightController) RecordPreflight(rec internalRuntime.PreflightActivity) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.recordErr != nil {
		return c.recordErr
	}
	c.records = append(c.records, rec)
	return nil
}

func (c *preflightController) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *preflightController) capturedParams() []preflight.Params {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]preflight.Params, len(c.calls))
	copy(out, c.calls)
	return out
}

func (c *preflightController) capturedDeadlines() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Time, len(c.deadlines))
	copy(out, c.deadlines)
	return out
}

func (c *preflightController) recordedActivity() []internalRuntime.PreflightActivity {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]internalRuntime.PreflightActivity, len(c.records))
	copy(out, c.records)
	return out
}

func readyOutcome(ids ...string) preflight.Outcome {
	results := make([]preflight.Result, 0, len(ids))
	for _, id := range ids {
		results = append(results, preflight.Result{ID: id, Status: preflight.StatusReady})
	}
	return preflight.Outcome{Verdict: preflight.VerdictForResults(results), Results: results}
}

func unavailableOutcome(id, reason string) preflight.Outcome {
	results := []preflight.Result{{
		ID:          id,
		Status:      preflight.StatusUnavailable,
		Reason:      reason,
		Retryable:   preflight.Retryable(reason),
		Action:      preflight.DefaultAction(reason),
		Detail:      "detail for " + id,
		Remediation: preflight.DefaultRemediation(reason),
	}}
	return preflight.Outcome{Verdict: preflight.VerdictForResults(results), Results: results}
}

func newPreflightServer(t *testing.T, ctrl *preflightController) *Server {
	t.Helper()
	return NewServer(ctrl, zap.NewNop().Sugar(), nil)
}

// doPreflight posts a body (any JSON-marshalable value, or a raw string for the
// malformed-payload case) with the admin API key.
func doPreflight(t *testing.T, srv *Server, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	switch typed := body.(type) {
	case string:
		payload = []byte(typed)
	default:
		var err error
		payload, err = json.Marshal(typed)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight", bytes.NewReader(payload))
	req.Header.Set("X-API-Key", preflightTestAPIKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func decodePreflightResponse(t *testing.T, w *httptest.ResponseRecorder) contracts.PreflightResponse {
	t.Helper()
	var envelope struct {
		Success bool                        `json:"success"`
		Data    contracts.PreflightResponse `json:"data"`
		Error   string                      `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.True(t, envelope.Success, "expected a success envelope, got error %q", envelope.Error)
	return envelope.Data
}

func decodePreflightError(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	assert.False(t, envelope.Success)
	return envelope.Error
}

// --- T015: happy path & envelope -------------------------------------------

func TestPreflight_ReadySetIs200WithStandardEnvelope(t *testing.T) {
	ctrl := &preflightController{last: preflightStep{outcome: readyOutcome("ctl:echo", "ctl:add")}}
	srv := newPreflightServer(t, ctrl)

	w := doPreflight(t, srv, contracts.PreflightRequest{
		Tools: []contracts.PreflightToolRef{{ID: "ctl:echo"}, {ID: "ctl:add"}},
	})

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodePreflightResponse(t, w)
	assert.Equal(t, preflight.VerdictReady, resp.Verdict)
	assert.False(t, resp.CheckedAt.IsZero())
	assert.Nil(t, resp.WaitedMS, "waited_ms is absent when no wait was requested")
	require.Len(t, resp.Tools, 2)
	assert.Equal(t, "ctl:echo", resp.Tools[0].ID)
	assert.Equal(t, preflight.StatusReady, resp.Tools[0].Status)
	assert.Empty(t, resp.Tools[0].Reason)
	assert.Nil(t, resp.Tools[0].Retryable, "a ready result carries no retryable flag")
	assert.Empty(t, resp.Tools[0].Action)
}

// A fully blocked set still executed the check, so it is a 200 carrying the
// verdict — HTTP status reports whether the check RAN, not what it found.
func TestPreflight_BlockedVerdictIsStill200(t *testing.T) {
	ctrl := &preflightController{last: preflightStep{outcome: unavailableOutcome("ctl:echo", preflight.ReasonServerDisabled)}}
	srv := newPreflightServer(t, ctrl)

	w := doPreflight(t, srv, contracts.PreflightRequest{Tools: []contracts.PreflightToolRef{{ID: "ctl:echo"}}})

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodePreflightResponse(t, w)
	assert.Equal(t, preflight.VerdictBlocked, resp.Verdict)
	require.Len(t, resp.Tools, 1)
	assert.Equal(t, preflight.ReasonServerDisabled, resp.Tools[0].Reason)
	require.NotNil(t, resp.Tools[0].Retryable)
	assert.False(t, *resp.Tools[0].Retryable)
	assert.Equal(t, "enable", resp.Tools[0].Action)
	assert.NotEmpty(t, resp.Tools[0].Remediation)
}

// The endpoint sits inside the authenticated /api/v1 group: it discloses which
// tools exist and why they are unavailable, which is exactly the inventory an
// unauthenticated caller must not enumerate.
func TestPreflight_RequiresAuthentication(t *testing.T) {
	ctrl := &preflightController{last: preflightStep{outcome: readyOutcome("ctl:echo")}}
	srv := newPreflightServer(t, ctrl)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight",
		strings.NewReader(`{"tools":[{"id":"ctl:echo"}]}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Zero(t, ctrl.callCount())
}

// --- T015: validation (every 400 rule) --------------------------------------

// oversizedPreflightBody is a syntactically valid request whose padding pushes
// it past the body cap. It is a single JSON value, so only the cap can reject
// it — and the cap has to fail the decode rather than truncate, or the padding
// could hide a second value from the trailing check.
func oversizedPreflightBody() string {
	return fmt.Sprintf(`{"tools":[{"id":"ctl:%s"}]}`, strings.Repeat("x", preflightMaxBody))
}

func TestPreflight_ValidationRejections(t *testing.T) {
	oversized := make([]contracts.PreflightToolRef, 0, preflightMaxTools+1)
	for i := 0; i <= preflightMaxTools; i++ {
		// Every entry is the SAME id: dedup would collapse this to one tool, so
		// a 400 here proves the cap applies to the raw array (FR-008).
		oversized = append(oversized, contracts.PreflightToolRef{ID: "ctl:echo"})
	}

	tests := []struct {
		name        string
		body        interface{}
		wantMessage string
	}{
		{
			name:        "malformed json",
			body:        "{not json",
			wantMessage: "Invalid JSON payload",
		},
		{
			name:        "empty tool list",
			body:        contracts.PreflightRequest{},
			wantMessage: "at least one entry",
		},
		{
			name:        "raw list over the limit",
			body:        contracts.PreflightRequest{Tools: oversized},
			wantMessage: "exceeds the limit of 100",
		},
		{
			name: "duplicate id with conflicting pins",
			body: contracts.PreflightRequest{Tools: []contracts.PreflightToolRef{
				{ID: "ctl:echo", PinHash: "sha256/v1:aaaa"},
				{ID: "ctl:echo", PinHash: "sha256/v1:bbbb"},
			}},
			wantMessage: "conflicting pin_hash",
		},
		{
			name: "duplicate id where only one carries a pin",
			body: contracts.PreflightRequest{Tools: []contracts.PreflightToolRef{
				{ID: "ctl:echo"},
				{ID: "ctl:echo", PinHash: "sha256/v1:bbbb"},
			}},
			wantMessage: "conflicting pin_hash",
		},
		{
			name:        "wait_ms over the cap",
			body:        contracts.PreflightRequest{Tools: []contracts.PreflightToolRef{{ID: "ctl:echo"}}, WaitMS: preflightMaxWaitMS + 1},
			wantMessage: "exceeds the cap of 10000",
		},
		{
			name:        "negative wait_ms",
			body:        contracts.PreflightRequest{Tools: []contracts.PreflightToolRef{{ID: "ctl:echo"}}, WaitMS: -1},
			wantMessage: "must not be negative",
		},
		{
			// A gate is written once and trusted for months, so a mistyped key
			// must fail loudly instead of silently weakening the check: `wait`
			// would be dropped and the caller would never learn it waits 0ms.
			name:        "unknown field",
			body:        `{"tools":[{"id":"ctl:echo"}],"wait":5000}`,
			wantMessage: "Invalid JSON payload",
		},
		{
			name:        "unknown field inside a tool ref",
			body:        `{"tools":[{"id":"ctl:echo","pin":"sha256/v1:aaaa"}]}`,
			wantMessage: "Invalid JSON payload",
		},
		{
			name:        "trailing json after the request object",
			body:        `{"tools":[{"id":"ctl:echo"}]}{"tools":[{"id":"ctl:other"}]}`,
			wantMessage: "exactly one JSON object",
		},
		{
			name:        "trailing garbage after the request object",
			body:        `{"tools":[{"id":"ctl:echo"}]} nonsense`,
			wantMessage: "exactly one JSON object",
		},
		{
			name:        "body over the size cap",
			body:        oversizedPreflightBody(),
			wantMessage: "Invalid JSON payload",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := &preflightController{last: preflightStep{outcome: readyOutcome("ctl:echo")}}
			srv := newPreflightServer(t, ctrl)

			w := doPreflight(t, srv, tc.body)

			require.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, decodePreflightError(t, w), tc.wantMessage)
			// A rejected request executed no preflight...
			assert.Zero(t, ctrl.callCount(), "a 400 must not run the evaluator")
			// ...and therefore wrote no activity record.
			assert.Empty(t, ctrl.recordedActivity(), "a rejected request must not write an activity record")
		})
	}
}

// An unknown profile is a caller mistake, not proxy state: 400, and no record.
func TestPreflight_UnknownProfileIs400AndWritesNoRecord(t *testing.T) {
	ctrl := &preflightController{last: preflightStep{
		err: fmt.Errorf("%w: %q", preflight.ErrUnknownProfile, "ghost"),
	}}
	srv := newPreflightServer(t, ctrl)

	w := doPreflight(t, srv, contracts.PreflightRequest{
		Tools:   []contracts.PreflightToolRef{{ID: "ctl:echo"}},
		Profile: "ghost",
	})

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodePreflightError(t, w), "ghost")
	assert.Empty(t, ctrl.recordedActivity())
}

func TestPreflight_DedupPreservesFirstOccurrenceOrder(t *testing.T) {
	ctrl := &preflightController{last: preflightStep{outcome: readyOutcome("ctl:echo", "ctl:add")}}
	srv := newPreflightServer(t, ctrl)

	w := doPreflight(t, srv, contracts.PreflightRequest{Tools: []contracts.PreflightToolRef{
		{ID: "ctl:echo", PinHash: "sha256/v1:aaaa"},
		{ID: "ctl:add"},
		{ID: "ctl:echo", PinHash: "sha256/v1:aaaa"},
	}})

	require.Equal(t, http.StatusOK, w.Code)
	params := ctrl.capturedParams()
	require.Len(t, params, 1)
	require.Len(t, params[0].Tools, 2, "the duplicate id must be collapsed")
	assert.Equal(t, "ctl:echo", params[0].Tools[0].ID)
	assert.Equal(t, "sha256/v1:aaaa", params[0].Tools[0].PinHash)
	assert.Equal(t, "ctl:add", params[0].Tools[1].ID)
}

// --- T015: 503 rules --------------------------------------------------------

func TestPreflight_ServiceUnavailableRules(t *testing.T) {
	tests := []struct {
		name        string
		step        preflightStep
		recordErr   error
		wantMessage string
		wantRecords int
	}{
		{
			name:        "runtime unavailable",
			step:        preflightStep{err: preflight.ErrRuntimeUnavailable},
			wantMessage: "runtime is not ready",
		},
		{
			name:        "evaluator infrastructure read failure",
			step:        preflightStep{err: fmt.Errorf("index read for server %q: %w", "ctl", errPreflightInfraRead)},
			wantMessage: "local state could not be read",
		},
		{
			name:        "activity record could not be persisted",
			step:        preflightStep{outcome: readyOutcome("ctl:echo")},
			recordErr:   internalRuntime.ErrActivityUnavailable,
			wantMessage: "activity record could not be persisted",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := &preflightController{last: tc.step, recordErr: tc.recordErr}
			srv := newPreflightServer(t, ctrl)

			w := doPreflight(t, srv, contracts.PreflightRequest{Tools: []contracts.PreflightToolRef{{ID: "ctl:echo"}}})

			require.Equal(t, http.StatusServiceUnavailable, w.Code)
			body := decodePreflightError(t, w)
			assert.Contains(t, body, "Preflight unavailable")
			assert.Contains(t, body, tc.wantMessage)
			assert.Len(t, ctrl.recordedActivity(), tc.wantRecords)
			assert.NotContains(t, w.Body.String(), "\"verdict\"", "a 503 must not carry a reduced-fidelity verdict")
		})
	}
}

var errPreflightInfraRead = fmt.Errorf("bbolt: read failed")

// --- T015: activity record (FR-014) -----------------------------------------

func TestPreflight_WritesActivityRecordBefore200(t *testing.T) {
	blocked := unavailableOutcome("ctl:echo", preflight.ReasonToolChanged)
	blocked.Results = append(blocked.Results, preflight.Result{ID: "ctl:add", Status: preflight.StatusReady})
	blocked.Verdict = preflight.VerdictForResults(blocked.Results)

	ctrl := &preflightController{last: preflightStep{outcome: blocked}}
	srv := newPreflightServer(t, ctrl)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight",
		strings.NewReader(`{"tools":[{"id":"ctl:echo"},{"id":"ctl:add"}]}`))
	req.Header.Set("X-API-Key", preflightTestAPIKey)
	req.Header.Set(XMCPProxyClientHeader, "cli/0.55.0")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	records := ctrl.recordedActivity()
	require.Len(t, records, 1)
	rec := records[0]
	assert.Equal(t, preflight.VerdictBlocked, rec.Verdict)
	assert.Equal(t, storage.ActivitySourceCLI, rec.Source, "the CLI client header must attribute the record to the CLI")
	assert.Equal(t, w.Header().Get("X-Request-Id"), rec.RequestID, "the record must correlate with the response request id")
	require.Len(t, rec.Tools, 2)
	assert.Equal(t, preflight.ReasonToolChanged, rec.Tools[0].Reason)
	assert.Empty(t, rec.Tools[1].Reason, "a ready tool contributes no reason code")
}

// --- T015: tier detection ---------------------------------------------------

func TestPreflightParams_TierDetection(t *testing.T) {
	body := &contracts.PreflightRequest{
		Profile: "work",
		Policy:  &contracts.PreflightPolicy{ReadOnlyOnly: true, ExcludeOpenWorld: true},
	}
	tools := []preflight.ToolRef{{ID: "ctl:echo"}}

	t.Run("api key is the operator tier", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight", nil)
		req = req.WithContext(auth.WithAuthContext(req.Context(), auth.AdminContext()))

		params := preflightParams(req, body, tools)

		assert.Equal(t, preflight.TierOperator, params.Tier)
		assert.Nil(t, params.TokenServers)
		assert.Empty(t, params.TokenProfilePin)
		assert.Equal(t, "work", params.Profile)
		assert.True(t, params.Filters.ReadOnlyOnly)
		assert.True(t, params.Filters.ExcludeOpenWorld)
		assert.False(t, params.Filters.ExcludeDestructive)
	})

	t.Run("socket connections are the operator tier", func(t *testing.T) {
		// The socket/named-pipe path authenticates as admin, so it takes the
		// same branch as an API key — asserted explicitly because FR-013 names
		// the socket and pipe as operator surfaces.
		req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight", nil)
		req = req.WithContext(auth.WithAuthContext(req.Context(), auth.AdminContext()))
		assert.Equal(t, preflight.TierOperator, preflightParams(req, body, tools).Tier)
	})

	t.Run("no auth context defaults to operator", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight", nil)
		assert.Equal(t, preflight.TierOperator, preflightParams(req, body, tools).Tier)
	})

	t.Run("agent token is the scoped tier and carries its scope", func(t *testing.T) {
		authCtx := (&auth.AgentToken{
			Name:           "cron",
			AllowedServers: []string{"ctl"},
			ProfilePin:     "pinned",
		}).AuthContext()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight", nil)
		req = req.WithContext(auth.WithAuthContext(req.Context(), authCtx))

		params := preflightParams(req, body, tools)

		assert.Equal(t, preflight.TierAgentToken, params.Tier)
		assert.Equal(t, []string{"ctl"}, params.TokenServers)
		assert.Equal(t, "pinned", params.TokenProfilePin)
	})
}

// End-to-end proof that a real agent token reaches the evaluator as the scoped
// tier: the middleware, not just the helper, must classify it.
func TestPreflight_AgentTokenRequestIsScopedTier(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)
	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)

	ctrl := &preflightController{last: preflightStep{outcome: readyOutcome("ctl:echo")}}
	srv := newPreflightServer(t, ctrl)
	srv.SetTokenStore(&testTokenStore{
		validateFunc: func(token string, _ []byte) (*auth.AgentToken, error) {
			if token != rawToken {
				return nil, fmt.Errorf("token not found")
			}
			return &auth.AgentToken{
				Name:           "cron",
				TokenPrefix:    auth.TokenPrefix(rawToken),
				AllowedServers: []string{"ctl"},
				Permissions:    []string{auth.PermRead},
				ProfilePin:     "pinned",
				ExpiresAt:      time.Now().Add(time.Hour),
			}, nil
		},
	}, tmpDir)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/preflight",
		strings.NewReader(`{"tools":[{"id":"ctl:echo"}]}`))
	req.Header.Set("X-API-Key", rawToken)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	params := ctrl.capturedParams()
	require.Len(t, params, 1)
	assert.Equal(t, preflight.TierAgentToken, params[0].Tier)
	assert.Equal(t, []string{"ctl"}, params[0].TokenServers)
	assert.Equal(t, "pinned", params[0].TokenProfilePin)
}

// --- T016: wait_ms ----------------------------------------------------------

func TestPreflightWait_ResolvesAtDeadlineWithCurrentReasons(t *testing.T) {
	// State never improves: the loop must stop at the deadline and answer with
	// the current (retryable) reason rather than hanging.
	ctrl := &preflightController{last: preflightStep{outcome: unavailableOutcome("ctl:echo", preflight.ReasonServerInitializing)}}
	srv := newPreflightServer(t, ctrl)
	srv.preflightPollOverride = 5 * time.Millisecond

	start := time.Now()
	w := doPreflight(t, srv, contracts.PreflightRequest{
		Tools:  []contracts.PreflightToolRef{{ID: "ctl:echo"}},
		WaitMS: 60,
	})
	elapsed := time.Since(start)

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodePreflightResponse(t, w)
	assert.Equal(t, preflight.VerdictDegradedRetryable, resp.Verdict)
	require.NotNil(t, resp.WaitedMS)
	assert.GreaterOrEqual(t, *resp.WaitedMS, 40, "it should have waited close to the whole budget")
	assert.Less(t, elapsed, 5*time.Second, "the wait must be bounded by the requested budget")
	assert.Greater(t, ctrl.callCount(), 2, "it must re-evaluate local state while waiting")
}

// Every re-evaluation must be bounded by the WAIT deadline, not just by the
// request context: wait_ms is the promise the caller gets ("this resolves
// within N ms"), and an evaluation entered at the last poll could otherwise sit
// on a slow storage read long past the budget.
func TestPreflightWait_PollEvaluationsAreBoundedByTheWaitDeadline(t *testing.T) {
	ctrl := &preflightController{last: preflightStep{outcome: unavailableOutcome("ctl:echo", preflight.ReasonServerInitializing)}}
	srv := newPreflightServer(t, ctrl)
	srv.preflightPollOverride = 5 * time.Millisecond

	const waitMS = 60
	start := time.Now()
	w := doPreflight(t, srv, contracts.PreflightRequest{
		Tools:  []contracts.PreflightToolRef{{ID: "ctl:echo"}},
		WaitMS: waitMS,
	})
	require.Equal(t, http.StatusOK, w.Code)

	deadlines := ctrl.capturedDeadlines()
	require.Greater(t, len(deadlines), 2, "the loop must have polled")
	budgetEnd := start.Add(waitMS * time.Millisecond)
	for i, deadline := range deadlines[1:] {
		require.False(t, deadline.IsZero(), "poll %d ran on an unbounded context", i+1)
		assert.False(t, deadline.After(budgetEnd.Add(20*time.Millisecond)),
			"poll %d may not outlive the wait budget", i+1)
	}
}

func TestPreflightWait_StopsAsSoonAsEverythingIsReady(t *testing.T) {
	ctrl := &preflightController{script: []preflightStep{
		{outcome: unavailableOutcome("ctl:echo", preflight.ReasonServerInitializing)},
		{outcome: readyOutcome("ctl:echo")},
	}}
	srv := newPreflightServer(t, ctrl)
	srv.preflightPollOverride = 5 * time.Millisecond

	w := doPreflight(t, srv, contracts.PreflightRequest{
		Tools:  []contracts.PreflightToolRef{{ID: "ctl:echo"}},
		WaitMS: 5000,
	})

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodePreflightResponse(t, w)
	assert.Equal(t, preflight.VerdictReady, resp.Verdict)
	assert.Equal(t, 2, ctrl.callCount())
	require.NotNil(t, resp.WaitedMS)
	assert.Less(t, *resp.WaitedMS, 5000)
}

// Waiting cannot clear a non-retryable failure, so the loop must terminate the
// moment one appears (FR-012) instead of burning the whole budget.
func TestPreflightWait_TerminatesEarlyOnNonRetryableFailure(t *testing.T) {
	ctrl := &preflightController{script: []preflightStep{
		{outcome: unavailableOutcome("ctl:echo", preflight.ReasonServerInitializing)},
		{outcome: unavailableOutcome("ctl:echo", preflight.ReasonServerQuarantined)},
	}}
	srv := newPreflightServer(t, ctrl)
	srv.preflightPollOverride = 5 * time.Millisecond

	start := time.Now()
	w := doPreflight(t, srv, contracts.PreflightRequest{
		Tools:  []contracts.PreflightToolRef{{ID: "ctl:echo"}},
		WaitMS: 10000,
	})
	elapsed := time.Since(start)

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodePreflightResponse(t, w)
	assert.Equal(t, preflight.VerdictBlocked, resp.Verdict)
	assert.Equal(t, 2, ctrl.callCount())
	assert.Less(t, elapsed, 2*time.Second, "it must not wait out the 10s budget for a blocked tool")
}

// A blocked-on-arrival set never sleeps at all, and never takes a wait slot.
func TestPreflightWait_NotEnteredWhenNothingIsRetryable(t *testing.T) {
	ctrl := &preflightController{last: preflightStep{outcome: unavailableOutcome("ctl:echo", preflight.ReasonNotFound)}}
	srv := newPreflightServer(t, ctrl)

	w := doPreflight(t, srv, contracts.PreflightRequest{
		Tools:  []contracts.PreflightToolRef{{ID: "ctl:echo"}},
		WaitMS: 10000,
	})

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodePreflightResponse(t, w)
	assert.Equal(t, preflight.VerdictUnknownIDs, resp.Verdict)
	assert.Equal(t, 1, ctrl.callCount())
	require.NotNil(t, resp.WaitedMS)
	assert.Equal(t, 0, *resp.WaitedMS)
}

// With the dedicated wait budget exhausted the request degrades gracefully: it
// resolves immediately with waited_ms 0 instead of queueing or failing.
func TestPreflightWait_SemaphoreExhaustedDegradesToImmediateResolve(t *testing.T) {
	ctrl := &preflightController{last: preflightStep{outcome: unavailableOutcome("ctl:echo", preflight.ReasonServerInitializing)}}
	srv := newPreflightServer(t, ctrl)
	srv.preflightPollOverride = 5 * time.Millisecond

	for i := 0; i < preflightWaitSlots; i++ {
		require.True(t, srv.acquirePreflightWaitSlot())
	}
	require.False(t, srv.acquirePreflightWaitSlot(), "the budget is fixed and small")

	start := time.Now()
	w := doPreflight(t, srv, contracts.PreflightRequest{
		Tools:  []contracts.PreflightToolRef{{ID: "ctl:echo"}},
		WaitMS: 10000,
	})
	elapsed := time.Since(start)

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodePreflightResponse(t, w)
	assert.Equal(t, preflight.VerdictDegradedRetryable, resp.Verdict)
	require.NotNil(t, resp.WaitedMS)
	assert.Equal(t, 0, *resp.WaitedMS)
	assert.Equal(t, 1, ctrl.callCount(), "an exhausted budget must not poll")
	assert.Less(t, elapsed, 2*time.Second)
}

// The slot is returned when the wait finishes, or a handful of waiting cron
// jobs would permanently disable waiting for everyone.
func TestPreflightWait_SlotIsReleasedAfterWaiting(t *testing.T) {
	ctrl := &preflightController{last: preflightStep{outcome: unavailableOutcome("ctl:echo", preflight.ReasonServerInitializing)}}
	srv := newPreflightServer(t, ctrl)
	srv.preflightPollOverride = 5 * time.Millisecond

	for i := 0; i < 2; i++ {
		w := doPreflight(t, srv, contracts.PreflightRequest{
			Tools:  []contracts.PreflightToolRef{{ID: "ctl:echo"}},
			WaitMS: 20,
		})
		require.Equal(t, http.StatusOK, w.Code)
	}

	for i := 0; i < preflightWaitSlots; i++ {
		assert.True(t, srv.acquirePreflightWaitSlot(), "every slot must be free again")
	}
}

func TestPreflightPollInterval_DefaultsToTheSpecFloor(t *testing.T) {
	srv := newPreflightServer(t, &preflightController{})
	assert.Equal(t, 250*time.Millisecond, srv.preflightPollInterval())
	assert.Equal(t, 250*time.Millisecond, preflightPollFloor, "FR-012 floors polling at 250ms")
}

// --- unit-level validation helpers ------------------------------------------

func TestNormalizePreflightTools(t *testing.T) {
	t.Run("trims ids and pins", func(t *testing.T) {
		out, err := normalizePreflightTools([]contracts.PreflightToolRef{{ID: "  ctl:echo  ", PinHash: " sha256/v1:aa "}})
		require.NoError(t, err)
		require.Len(t, out, 1)
		assert.Equal(t, "ctl:echo", out[0].ID)
		assert.Equal(t, "sha256/v1:aa", out[0].PinHash)
	})

	t.Run("a malformed id is not a request error", func(t *testing.T) {
		// One bad entry must not mask the verdicts of the rest — it becomes a
		// per-ID not_found in the evaluator, so validation lets it through.
		out, err := normalizePreflightTools([]contracts.PreflightToolRef{{ID: "no-separator"}, {ID: "ctl:echo"}})
		require.NoError(t, err)
		assert.Len(t, out, 2)
	})

	t.Run("exactly the limit is accepted", func(t *testing.T) {
		refs := make([]contracts.PreflightToolRef, 0, preflightMaxTools)
		for i := 0; i < preflightMaxTools; i++ {
			refs = append(refs, contracts.PreflightToolRef{ID: fmt.Sprintf("ctl:tool%d", i)})
		}
		out, err := normalizePreflightTools(refs)
		require.NoError(t, err)
		assert.Len(t, out, preflightMaxTools)
	})
}

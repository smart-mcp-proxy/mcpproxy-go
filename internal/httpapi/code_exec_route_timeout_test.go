package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// codeExecDeadlineController records the deadline the router left on the
// request context by the time the code_execution tool is dispatched.
type codeExecDeadlineController struct {
	baseController
	cfg      *config.Config
	deadline time.Time
	hasDL    bool
}

func (c *codeExecDeadlineController) GetCurrentConfig() interface{} { return c.cfg }

func (c *codeExecDeadlineController) CallTool(ctx context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
	c.deadline, c.hasDL = ctx.Deadline()
	resultJSON, err := json.Marshal(map[string]interface{}{"ok": true, "value": nil})
	if err != nil {
		return nil, err
	}
	return []interface{}{map[string]interface{}{"type": "text", "text": string(resultJSON)}}, nil
}

// TestCodeExecRouteGetsItsOwnTimeoutBudget proves POST /api/v1/code/exec is not
// held to the blanket /api/v1 deadline. The handler derives its own context
// from the caller's timeout_ms (up to 600000ms), but a context can only shrink
// against its parent: under the shared 60s middleware every longer execution
// was cancelled at 60s no matter what the caller asked for.
func TestCodeExecRouteGetsItsOwnTimeoutBudget(t *testing.T) {
	const apiKey = "test-api-key"
	ctrl := &codeExecDeadlineController{cfg: &config.Config{APIKey: apiKey}}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	body, err := json.Marshal(map[string]interface{}{
		"code":    "({ result: 1 })",
		"options": map[string]interface{}{"timeout_ms": maxCodeExecTimeoutMS},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/code/exec", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	recorder := httptest.NewRecorder()

	start := time.Now()
	srv.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code, "body: %s", recorder.Body.String())
	require.True(t, ctrl.hasDL, "the request context carried no deadline")

	requested := time.Duration(maxCodeExecTimeoutMS) * time.Millisecond
	budget := ctrl.deadline.Sub(start)
	assert.Greater(t, budget, requested-time.Second,
		"a %v execution budget was clipped to the blanket %v /api/v1 deadline", requested, defaultAPIRequestTimeout)
	assert.LessOrEqual(t, budget, codeExecRequestTimeout,
		"the request outlives its route budget")
}

// TestAPIRequestTimeoutPerRouteBudgets covers the middleware itself with
// compressed durations: a path with its own budget survives work that the
// default deadline would cancel, and every other path keeps the default.
func TestAPIRequestTimeoutPerRouteBudgets(t *testing.T) {
	const (
		defaultBudget = 30 * time.Millisecond
		longBudget    = 5 * time.Second
		work          = 120 * time.Millisecond
	)

	var cancelled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			cancelled = true
		case <-time.After(work):
			cancelled = false
		}
		w.WriteHeader(http.StatusOK)
	})

	handler := apiRequestTimeout(defaultBudget, map[string]time.Duration{
		"/api/v1/long": longBudget,
	})(next)

	t.Run("a route with its own budget runs to completion", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/long", http.NoBody)
		handler.ServeHTTP(httptest.NewRecorder(), req)
		assert.False(t, cancelled, "the long-running route was cancelled by the default deadline")
	})

	t.Run("every other route keeps the default deadline", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", http.NoBody)
		handler.ServeHTTP(httptest.NewRecorder(), req)
		assert.True(t, cancelled, "an ordinary route escaped the default deadline")
	})
}

// TestCodeExecRouteBudgetCoversTheLongestExecution keeps the route budget ahead
// of the longest timeout_ms the code_execution tool accepts, so the parent
// deadline is never the thing that ends an execution.
func TestCodeExecRouteBudgetCoversTheLongestExecution(t *testing.T) {
	budgets := longRunningAPIBudgets()
	budget, ok := budgets[codeExecPath]
	require.True(t, ok, "%s has no timeout budget of its own", codeExecPath)
	assert.Greater(t, budget, time.Duration(maxCodeExecTimeoutMS)*time.Millisecond,
		"the route budget must leave slack above the maximum timeout_ms")
}

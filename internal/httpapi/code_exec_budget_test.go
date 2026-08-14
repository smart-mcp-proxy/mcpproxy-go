package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// dispatchCodeExecBudget runs one body through the handler and reports the
// deadline the handler left on the context it gave the code_execution tool,
// measured from just before the request was served.
func dispatchCodeExecBudget(t *testing.T, body map[string]interface{}) (budget time.Duration, status int) {
	t.Helper()

	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	ctrl := &codeExecDeadlineController{}
	req := httptest.NewRequest(http.MethodPost, codeExecPath, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	start := time.Now()
	NewCodeExecHandler(ctrl, zap.NewNop().Sugar()).ServeHTTP(recorder, req)

	require.True(t, ctrl.hasDL, "the dispatched context carried no deadline")
	return ctrl.deadline.Sub(start), recorder.Code
}

// TestCodeExecHandler_UnsetTimeoutCoversTheLargestResolvableBudget covers the
// case where the caller sends no timeout_ms. The tool then resolves the budget
// from code_execution_timeout_ms, which may legally be anything up to
// maxCodeExecTimeoutMS — but a context only ever shrinks against its parent, so
// a parent cut at this endpoint's own fallback silently capped every longer
// configured budget (a 300000ms config was cancelled at 120000ms).
func TestCodeExecHandler_UnsetTimeoutCoversTheLargestResolvableBudget(t *testing.T) {
	budget, status := dispatchCodeExecBudget(t, map[string]interface{}{
		"code": "({ result: 1 })",
	})

	require.Equal(t, http.StatusOK, status)
	assert.GreaterOrEqual(t, budget, time.Duration(maxCodeExecTimeoutMS)*time.Millisecond,
		"an omitted timeout_ms must leave room for the largest budget the tool can resolve from config")
	assert.LessOrEqual(t, budget, codeExecRequestTimeout,
		"the parent deadline must stay inside the route's own budget")
}

// TestCodeExecHandler_ExplicitTimeoutKeepsPreciseBudget is the other half of the
// contract: when the caller names a budget, that is what bounds the request —
// the ceiling above applies only to the resolve-from-config path.
func TestCodeExecHandler_ExplicitTimeoutKeepsPreciseBudget(t *testing.T) {
	budget, status := dispatchCodeExecBudget(t, map[string]interface{}{
		"code":    "({ result: 1 })",
		"options": map[string]interface{}{"timeout_ms": 5000},
	})

	require.Equal(t, http.StatusOK, status)
	assert.Less(t, budget, 6*time.Second,
		"a caller-named budget must not be widened to the resolve-from-config ceiling")
	assert.Greater(t, budget, 4*time.Second)
}

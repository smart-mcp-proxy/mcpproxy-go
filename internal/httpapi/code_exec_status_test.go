package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/codescripts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
)

// postFailingCodeExec runs one POST /api/v1/code/exec against a tool caller
// that fails with err, and returns the recorded response.
func postFailingCodeExec(t *testing.T, body map[string]interface{}, err error) (*httptest.ResponseRecorder, httpapi.CodeExecResponse) {
	t.Helper()

	recorder := postCodeExec(t, &mockController{
		callToolFunc: func(context.Context, string, map[string]interface{}) (interface{}, error) {
			return nil, err
		},
	}, body)

	var decoded httpapi.CodeExecResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &decoded))
	return recorder, decoded
}

// TestCodeExec_ScriptResolutionFailuresAreClientErrors pins the status CLASS of
// a stored-script rejection. Naming a script that does not exist is the
// documented discovery path (FR-004) and a mistyped name is a caller mistake,
// but both arrived as 500 EXECUTION_FAILED: agent retry policies treat 500 as
// retryable and re-POST a request that can never succeed, and monitoring counts
// typos as server faults. The handler already answers its own caller-mistake
// checks (XOR, language, option bounds) with 400, so the resolution errors have
// to join them.
func TestCodeExec_ScriptResolutionFailuresAreClientErrors(t *testing.T) {
	// Server.CallTool wraps whatever the tool dispatch returns; the typed
	// identity has to survive that wrapping for the mapping to be possible.
	wrap := func(err error) error { return fmt.Errorf("tool call failed: %w", err) }

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantInMsg  string
	}{
		{
			name: "not found carries the discovery listing",
			err: wrap(&codescripts.NotFoundError{
				Name:      "nope",
				Dir:       "/cfg/scripts",
				Available: []string{"daily-report"},
				Total:     1,
			}),
			wantStatus: http.StatusNotFound,
			wantCode:   "SCRIPT_NOT_FOUND",
			wantInMsg:  "daily-report",
		},
		{
			name:       "invalid name",
			err:        wrap(&codescripts.InvalidNameError{Name: "../etc/passwd", Reason: "character \"/\" is not allowed"}),
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_SCRIPT_NAME",
			wantInMsg:  "invalid script name",
		},
		{
			name:       "ambiguous",
			err:        wrap(&codescripts.AmbiguousError{Name: "dup", Paths: []string{"/cfg/scripts/dup.js", "/cfg/scripts/dup.ts"}}),
			wantStatus: http.StatusBadRequest,
			wantCode:   "SCRIPT_UNUSABLE",
			wantInMsg:  "ambiguous",
		},
		{
			name:       "oversized",
			err:        wrap(&codescripts.InvalidError{Name: "big", Path: "/cfg/scripts/big.js", Reason: codescripts.ReasonOversized}),
			wantStatus: http.StatusBadRequest,
			wantCode:   "SCRIPT_UNUSABLE",
			wantInMsg:  "limited to",
		},
		{
			name: "language contradicts the extension",
			err: wrap(&codescripts.LanguageMismatchError{
				Name: "typed", Extension: ".ts", Requested: "javascript", Derived: "typescript",
			}),
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_LANGUAGE",
			wantInMsg:  "typescript",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, decoded := postFailingCodeExec(t, map[string]interface{}{"script": "whatever"}, tc.err)

			assert.Equal(t, tc.wantStatus, w.Code)
			require.NotNil(t, decoded.Error)
			assert.Equal(t, tc.wantCode, decoded.Error.Code)
			assert.Contains(t, decoded.Error.Message, tc.wantInMsg,
				"the tool's own explanation must survive the status mapping — it is how a caller recovers")
		})
	}

	t.Run("a genuine execution fault is still a 500", func(t *testing.T) {
		w, decoded := postFailingCodeExec(t, map[string]interface{}{"code": "1"}, fmt.Errorf("tool call failed: js pool exhausted"))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		require.NotNil(t, decoded.Error)
		assert.Equal(t, "EXECUTION_FAILED", decoded.Error.Code)
	})
}

// TestCodeExec_DisabledFeatureIsForbidden pins the REST answer when the
// operator has switched code execution off: a refusal the caller cannot fix by
// retrying, not a server fault.
func TestCodeExec_DisabledFeatureIsForbidden(t *testing.T) {
	err := fmt.Errorf("tool call failed: %w", config.ErrCodeExecutionDisabled)

	for _, body := range []map[string]interface{}{
		{"script": "daily-report"},
		{"code": "({result: 1})"},
	} {
		w, decoded := postFailingCodeExec(t, body, err)
		assert.Equal(t, http.StatusForbidden, w.Code)
		require.NotNil(t, decoded.Error)
		assert.Equal(t, "FEATURE_DISABLED", decoded.Error.Code)
		assert.Contains(t, decoded.Error.Message, "enable_code_execution")
	}
}

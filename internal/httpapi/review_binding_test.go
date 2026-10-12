package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Spec 117 PR-2 (#1566, T211): expected_hashes on both approve endpoints and
// the 409 out-of-date mapping.

func postJSON(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", mockControllerAPIKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// requireStaleBody asserts the exact 409 contract of contracts/rest-approval-binding.md.
func requireStaleBody(t *testing.T, w *httptest.ResponseRecorder, changed, unreviewed, missing []string) {
	t.Helper()
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	for _, key := range []string{"success", "error", "error_code", "stale_tools"} {
		assert.Contains(t, raw, key)
	}
	var body StaleReviewErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.False(t, body.Success)
	assert.Equal(t, "review_out_of_date", body.ErrorCode)
	assert.Contains(t, body.Error, "out of date")
	assert.NotContains(t, strings.ToLower(body.Error), "dangerous")
	var lists map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["stale_tools"], &lists))
	for _, key := range []string{"changed", "unreviewed", "missing_blocks"} {
		require.Contains(t, lists, key)
		assert.NotEqual(t, "null", string(lists[key]), "%s is always a list", key)
	}
	norm := func(in []string) []string {
		if in == nil {
			return []string{}
		}
		return in
	}
	assert.Equal(t, norm(changed), body.StaleTools.Changed)
	assert.Equal(t, norm(unreviewed), body.StaleTools.Unreviewed)
	assert.Equal(t, norm(missing), body.StaleTools.MissingBlocks)
}

func TestSecurityApprove_BindingDecoding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		bound    bool
		expected map[string]string
	}{
		{"absent", `{"force":false}`, false, nil},
		{"null", `{"force":false,"expected_hashes":null}`, false, nil},
		{"empty object", `{"expected_hashes":{}}`, true, map[string]string{}},
		{"hashes", `{"block":["x"],"expected_hashes":{"read_note":"rv1-a"}}`, true, map[string]string{"read_note": "rv1-a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sec := &mockSecurityController{}
			srv := newTestServerWithSecurity(t, sec)
			w := postJSON(t, srv, "/api/v1/servers/my-server/security/approve", tc.body)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			if tc.bound {
				assert.Equal(t, 1, sec.reviewedCalls)
				assert.Equal(t, tc.expected, sec.reviewedExpected)
				assert.NotNil(t, sec.reviewedExpected)
			} else {
				assert.Zero(t, sec.reviewedCalls, "an unbound request keeps the legacy entry points")
			}
		})
	}
}

func TestSecurityApprove_StaleReview409(t *testing.T) {
	sec := &mockSecurityController{approveErr: toolreview.NewStaleToolReviewError("my-server", []string{"read_note"}, []string{"drop_db"}, nil)}
	srv := newTestServerWithSecurity(t, sec)
	w := postJSON(t, srv, "/api/v1/servers/my-server/security/approve", `{"expected_hashes":{"read_note":"rv1-old"}}`)
	requireStaleBody(t, w, []string{"read_note"}, []string{"drop_db"}, nil)
}

func TestSecurityApprove_MissingBlockBoundIs409UnboundIs400(t *testing.T) {
	sec := &mockSecurityController{approveErr: toolreview.NewStaleToolReviewError("my-server", nil, nil, []string{"gone"})}
	srv := newTestServerWithSecurity(t, sec)
	w := postJSON(t, srv, "/api/v1/servers/my-server/security/approve", `{"block":["gone"],"expected_hashes":{}}`)
	requireStaleBody(t, w, nil, nil, []string{"gone"})

	sec2 := &mockSecurityController{approveErr: fmt.Errorf("failed to save integrity baseline: %w: gone", scanner.ErrToolApprovalBlockNotFound)}
	srv2 := newTestServerWithSecurity(t, sec2)
	w = postJSON(t, srv2, "/api/v1/servers/my-server/security/approve", `{"block":["gone"]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSecurityApprove_DangerousStillPlain409(t *testing.T) {
	sec := &mockSecurityController{approveErr: fmt.Errorf("server has 1 dangerous (hard-tier) finding(s); resolve them or use --force to approve anyway")}
	srv := newTestServerWithSecurity(t, sec)
	w := postJSON(t, srv, "/api/v1/servers/my-server/security/approve", `{"expected_hashes":{}}`)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "dangerous")
	assert.NotContains(t, w.Body.String(), "review_out_of_date")
}

func toolApproveServer(t *testing.T, ctrl *mockToolQuarantineController) *Server {
	t.Helper()
	ctrl.apiKey = mockControllerAPIKey
	return NewServer(ctrl, zap.NewNop().Sugar(), nil)
}

func TestApproveTools_BindingDecodingAndPassThrough(t *testing.T) {
	pending := func() []*storage.ToolApprovalRecord {
		return []*storage.ToolApprovalRecord{{ServerName: "github", ToolName: "t1", Status: storage.ToolApprovalStatusPending}}
	}
	// Unbound: the legacy method, no binding.
	ctrl := &mockToolQuarantineController{approvals: pending()}
	w := postJSON(t, toolApproveServer(t, ctrl), "/api/v1/servers/github/tools/approve", `{"tools":["t1"],"expected_hashes":null}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Nil(t, ctrl.reviewedExpected)
	assert.Nil(t, ctrl.reviewedTools)
	assert.Equal(t, []string{"t1"}, ctrl.approvedTools)

	// Bound per-tool: the controller receives expected; duplicates collapse.
	ctrl = &mockToolQuarantineController{approvals: pending()}
	w = postJSON(t, toolApproveServer(t, ctrl), "/api/v1/servers/github/tools/approve", `{"tools":["t1","t1"],"expected_hashes":{"t1":"rv1-a"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, map[string]string{"t1": "rv1-a"}, ctrl.reviewedExpected)
	assert.Equal(t, []string{"t1"}, ctrl.reviewedTools)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, float64(1), data["approved"], "a bound duplicate is counted once")

	// Bound approve_all.
	ctrl = &mockToolQuarantineController{approvals: pending(), approvedCount: 1}
	w = postJSON(t, toolApproveServer(t, ctrl), "/api/v1/servers/github/tools/approve", `{"approve_all":true,"expected_hashes":{}}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, ctrl.reviewedAll)
	assert.Equal(t, map[string]string{}, ctrl.reviewedExpected)
}

func TestApproveTools_StaleReview409(t *testing.T) {
	stale := toolreview.NewStaleToolReviewError("github", []string{"t1"}, nil, nil)
	for _, body := range []string{`{"tools":["t1"],"expected_hashes":{"t1":"rv1-old"}}`, `{"approve_all":true,"expected_hashes":{"t1":"rv1-old"}}`} {
		ctrl := &mockToolQuarantineController{reviewedErr: stale}
		w := postJSON(t, toolApproveServer(t, ctrl), "/api/v1/servers/github/tools/approve", body)
		requireStaleBody(t, w, []string{"t1"}, nil, nil)
		assert.Nil(t, ctrl.approvedTools, "nothing approved")
	}
}

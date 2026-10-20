package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// bundleMock is the Spec 101 slice of mockSecurityController.
type bundleMock struct {
	info       scanner.BundleInfo
	history    []scanner.TPABundleActivation
	historyErr error

	rollbackErr   error
	rollbackCalls int
	rollbackReq   scanner.BundleRollbackRequest
	unpinErr      error
	unpinCalls    int
	reloadCalls   int
}

func (m *mockSecurityController) BundleStatus() scanner.BundleInfo { return m.bundle.info }

func (m *mockSecurityController) BundleHistory() ([]scanner.TPABundleActivation, error) {
	return m.bundle.history, m.bundle.historyErr
}

func (m *mockSecurityController) RollbackBundle(req scanner.BundleRollbackRequest) (scanner.BundleInfo, error) {
	m.bundle.rollbackCalls++
	m.bundle.rollbackReq = req
	return m.bundle.info, m.bundle.rollbackErr
}

func (m *mockSecurityController) UnpinBundle() (scanner.BundleInfo, error) {
	m.bundle.unpinCalls++
	return m.bundle.info, m.bundle.unpinErr
}

func (m *mockSecurityController) ReloadBundle() scanner.BundleInfo {
	m.bundle.reloadCalls++
	return m.bundle.info
}

const bundleFullDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func sampleBundleInfo() scanner.BundleInfo {
	return scanner.BundleInfo{
		Source:            scanner.BundleSourceFile,
		Path:              "/etc/mcpproxy/bundle.json",
		Fingerprint:       bundleFullDigest[:12],
		FullDigest:        bundleFullDigest,
		Sequence:          "42",
		Authority:         scanner.AuthorityPublisher,
		TrustEpoch:        3,
		SignatureVerified: true,
		RunnableRules:     10,
		CoverageDegraded:  true,
		DegradedReason:    "operator_bundle_missing",
		LoadedAt:          time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		LastRejection: &scanner.BundleRejection{
			Reason: "downgrade", Detail: "sequence 7 below watermark 42",
			At: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		},
	}
}

func bundleReq(t *testing.T, srv *Server, method, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Buffer
	if body != "" {
		rdr = bytes.NewBufferString(body)
	} else {
		rdr = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("X-API-Key", key)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func decodeBundleData(t *testing.T, rec *httptest.ResponseRecorder, into interface{}) {
	t.Helper()
	var env struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), rec.Body.String())
	require.True(t, env.Success, rec.Body.String())
	require.NoError(t, json.Unmarshal(env.Data, into), rec.Body.String())
}

func TestSecurityBundle_StatusReturnsInfo(t *testing.T) {
	sec := &mockSecurityController{bundle: bundleMock{info: sampleBundleInfo()}}
	srv := newTestServerWithSecurity(t, sec)

	rec := bundleReq(t, srv, http.MethodGet, "/api/v1/security/bundle", "", mockControllerAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var got scanner.BundleInfo
	decodeBundleData(t, rec, &got)
	assert.Equal(t, bundleFullDigest, got.FullDigest)
	assert.Equal(t, "42", got.Sequence)
	assert.True(t, got.CoverageDegraded)
	require.NotNil(t, got.LastRejection)
	assert.Equal(t, "downgrade", got.LastRejection.Reason)
}

func TestSecurityBundle_HistoryReturnsActivations(t *testing.T) {
	sec := &mockSecurityController{bundle: bundleMock{history: []scanner.TPABundleActivation{
		{FullDigest: bundleFullDigest, Sequence: "41", Source: "file"},
		{FullDigest: strings.Repeat("a", 64), Sequence: "42", Source: "embedded"},
	}}}
	srv := newTestServerWithSecurity(t, sec)

	rec := bundleReq(t, srv, http.MethodGet, "/api/v1/security/bundle/history", "", mockControllerAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got []scanner.TPABundleActivation
	decodeBundleData(t, rec, &got)
	require.Len(t, got, 2)
	assert.Equal(t, "41", got[0].Sequence, "oldest first, as the controller returned it")
}

func TestSecurityBundle_EmptyHistoryIsAnArray(t *testing.T) {
	srv := newTestServerWithSecurity(t, &mockSecurityController{})
	rec := bundleReq(t, srv, http.MethodGet, "/api/v1/security/bundle/history", "", mockControllerAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"data":[]`, "an empty history must be [] not null")
}

func TestSecurityBundle_HistoryErrorIs500(t *testing.T) {
	sec := &mockSecurityController{bundle: bundleMock{historyErr: errors.New("db down")}}
	srv := newTestServerWithSecurity(t, sec)
	rec := bundleReq(t, srv, http.MethodGet, "/api/v1/security/bundle/history", "", mockControllerAPIKey)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestSecurityBundle_RollbackSuccess(t *testing.T) {
	sec := &mockSecurityController{bundle: bundleMock{info: sampleBundleInfo()}}
	srv := newTestServerWithSecurity(t, sec)

	body := `{"path":"/tmp/known-good.json","digest":"` + bundleFullDigest + `"}`
	rec := bundleReq(t, srv, http.MethodPost, "/api/v1/security/bundle/rollback", body, mockControllerAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, sec.bundle.rollbackCalls)
	assert.Equal(t, "/tmp/known-good.json", sec.bundle.rollbackReq.Path)
	assert.Equal(t, bundleFullDigest, sec.bundle.rollbackReq.Digest)
	var got scanner.BundleInfo
	decodeBundleData(t, rec, &got)
	assert.Equal(t, bundleFullDigest, got.FullDigest)
}

func TestSecurityBundle_RollbackEmbeddedSuccess(t *testing.T) {
	sec := &mockSecurityController{bundle: bundleMock{info: sampleBundleInfo()}}
	srv := newTestServerWithSecurity(t, sec)
	body := `{"embedded":true,"digest":"` + bundleFullDigest + `"}`
	rec := bundleReq(t, srv, http.MethodPost, "/api/v1/security/bundle/rollback", body, mockControllerAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, sec.bundle.rollbackReq.Embedded)
}

func TestSecurityBundle_RollbackUsageErrorsAre400(t *testing.T) {
	cases := map[string]string{
		"both":          `{"embedded":true,"path":"/x","digest":"` + bundleFullDigest + `"}`,
		"neither":       `{"digest":"` + bundleFullDigest + `"}`,
		"short":         `{"embedded":true,"digest":"` + bundleFullDigest[:12] + `"}`,
		"uppercase":     `{"embedded":true,"digest":"` + strings.ToUpper(bundleFullDigest) + `"}`,
		"missingDigest": `{"embedded":true}`,
		"badJSON":       `{not json`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			// The real validator lives in the scanner package; the mock mirrors
			// the controller contract by returning its usage error.
			sec := &mockSecurityController{bundle: bundleMock{rollbackErr: scanner.ErrBundleRollbackUsage}}
			srv := newTestServerWithSecurity(t, sec)
			rec := bundleReq(t, srv, http.MethodPost, "/api/v1/security/bundle/rollback", body, mockControllerAPIKey)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
}

func TestSecurityBundle_ShortFingerprintRefusedWithoutReachingController(t *testing.T) {
	sec := &mockSecurityController{}
	srv := newTestServerWithSecurity(t, sec)
	body := `{"embedded":true,"digest":"` + bundleFullDigest[:12] + `"}`
	rec := bundleReq(t, srv, http.MethodPost, "/api/v1/security/bundle/rollback", body, mockControllerAPIKey)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "full digest is required")
	assert.Equal(t, 0, sec.bundle.rollbackCalls, "a bad request must not reach the controller")
}

func TestSecurityBundle_RollbackRefusedIs409WithReason(t *testing.T) {
	rej := scanner.BundleRejection{Reason: "rollback_target_mismatch", Detail: "named 0123456789ab, the file holds ffffffffffff",
		At: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)}
	sec := &mockSecurityController{bundle: bundleMock{info: sampleBundleInfo(), rollbackErr: &scanner.BundleRollbackRefused{Rejection: rej}}}
	srv := newTestServerWithSecurity(t, sec)

	body := `{"path":"/tmp/x.json","digest":"` + bundleFullDigest + `"}`
	rec := bundleReq(t, srv, http.MethodPost, "/api/v1/security/bundle/rollback", body, mockControllerAPIKey)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	var resp struct {
		Success   bool                    `json:"success"`
		Error     string                  `json:"error"`
		ErrorCode string                  `json:"error_code"`
		Rejection scanner.BundleRejection `json:"rejection"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.Success)
	assert.Equal(t, "rollback_target_mismatch", resp.ErrorCode)
	assert.Equal(t, "rollback_target_mismatch", resp.Rejection.Reason)
	assert.Contains(t, resp.Rejection.Detail, "named 0123456789ab")
	assert.Contains(t, resp.Error, "rollback_target_mismatch")
}

func TestSecurityBundle_RollbackUnexpectedErrorIs500(t *testing.T) {
	sec := &mockSecurityController{bundle: bundleMock{rollbackErr: errors.New("boom")}}
	srv := newTestServerWithSecurity(t, sec)
	body := `{"embedded":true,"digest":"` + bundleFullDigest + `"}`
	rec := bundleReq(t, srv, http.MethodPost, "/api/v1/security/bundle/rollback", body, mockControllerAPIKey)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestSecurityBundle_UnpinAndReload(t *testing.T) {
	sec := &mockSecurityController{bundle: bundleMock{info: sampleBundleInfo()}}
	srv := newTestServerWithSecurity(t, sec)

	rec := bundleReq(t, srv, http.MethodPost, "/api/v1/security/bundle/unpin", "", mockControllerAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, sec.bundle.unpinCalls)
	var got scanner.BundleInfo
	decodeBundleData(t, rec, &got)
	assert.Equal(t, bundleFullDigest, got.FullDigest)

	rec = bundleReq(t, srv, http.MethodPost, "/api/v1/security/bundle/reload", "", mockControllerAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, sec.bundle.reloadCalls)
	decodeBundleData(t, rec, &got)
	assert.Equal(t, "42", got.Sequence)
}

func TestSecurityBundle_UnpinErrorIs500(t *testing.T) {
	sec := &mockSecurityController{bundle: bundleMock{info: sampleBundleInfo(), unpinErr: errors.New("unpin not recorded")}}
	srv := newTestServerWithSecurity(t, sec)
	rec := bundleReq(t, srv, http.MethodPost, "/api/v1/security/bundle/unpin", "", mockControllerAPIKey)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestSecurityBundle_NotImplementedWithoutController(t *testing.T) {
	srv := newTestServerWithSecurity(t, nil)
	srv.securityController = nil
	for _, rt := range bundleRoutes {
		rec := bundleReq(t, srv, rt.method, rt.path, "{}", mockControllerAPIKey)
		assert.Equal(t, http.StatusNotImplemented, rec.Code, rt.path)
	}
}

// bundleRoutes is every Spec 101 bundle route; the agent-token tests below
// walk it so a new route cannot be added without being gated.
var bundleRoutes = []struct {
	name     string
	method   string
	path     string
	mutation bool
}{
	{"status", http.MethodGet, "/api/v1/security/bundle", false},
	{"history", http.MethodGet, "/api/v1/security/bundle/history", false},
	{"rollback", http.MethodPost, "/api/v1/security/bundle/rollback", true},
	{"unpin", http.MethodPost, "/api/v1/security/bundle/unpin", true},
	{"reload", http.MethodPost, "/api/v1/security/bundle/reload", true},
}

func TestSecurityBundle_AgentTokenRefusedOnEveryRoute(t *testing.T) {
	for _, rt := range bundleRoutes {
		t.Run(rt.name, func(t *testing.T) {
			sec := &mockSecurityController{bundle: bundleMock{info: sampleBundleInfo()}}
			srv, token := scopeRound10Server(t, sec)

			body := ""
			if rt.name == "rollback" {
				body = `{"embedded":true,"digest":"` + bundleFullDigest + `"}`
			}
			rec := bundleReq(t, srv, rt.method, rt.path, body, token)
			assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			assert.NotContains(t, rec.Body.String(), bundleFullDigest, "a refused caller must learn nothing")
			assert.Zero(t, sec.bundle.rollbackCalls+sec.bundle.unpinCalls+sec.bundle.reloadCalls,
				"a refused caller must not reach the mutation")

			// Positive control: the same route and fixture serve an admin.
			adminRec := bundleReq(t, srv, rt.method, rt.path, body, scopeAdminAPIKey)
			assert.Equal(t, http.StatusOK, adminRec.Code, adminRec.Body.String())
		})
	}
}

func TestSecurityBundle_UnauthenticatedRefused(t *testing.T) {
	srv := newTestServerWithSecurity(t, &mockSecurityController{})
	for _, rt := range bundleRoutes {
		rec := bundleReq(t, srv, rt.method, rt.path, "", "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code, rt.path)
	}
}

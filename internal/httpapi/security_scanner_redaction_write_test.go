package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// M3 (#1166 round 11) — the read doors redact every literal scanner env value
// to scanner.RedactedEnvValue. Nothing stopped that sentinel coming back in on
// the WRITE door, so a client that read GET /security/scanners and posted the
// document back stored "***" over the operator's real vendor API key. The
// frontend was taught to skip the sentinel; the server must not depend on a
// cooperating client, because any CLI or script can round-trip the document.

const scannerRealSecret = "sk-live-REAL-VENDOR-KEY"

func putScannerConfig(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/security/scanners/mcp-scan/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// TestConfigureScanner_RejectsRedactionSentinel is the end-to-end round-trip:
// read the redacted document off the production GET door, post it back
// verbatim, and assert the real secret is never overwritten.
//
// The GET half is the positive control — it proves the sentinel is genuinely
// what a client reads back, so the write guard is defending against a shape the
// API really produces rather than one invented by the test.
func TestConfigureScanner_RejectsRedactionSentinel(t *testing.T) {
	secCtrl := &mockSecurityController{
		scanners: []*scanner.ScannerPlugin{{
			ID:     "mcp-scan",
			Name:   "MCP Scan",
			Status: scanner.ScannerStatusInstalled,
			ConfiguredEnv: map[string]string{
				"VENDOR_API_KEY": scannerRealSecret,
				"KEYRING_REF":    "${keyring:vendor-key}",
			},
		}},
	}
	srv := newTestServerWithSecurity(t, secCtrl)

	// 1. Read the document back the way any API client would.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/security/scanners/mcp-scan/status", nil)
	getRec := httptest.NewRecorder()
	srv.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code, "body: %s", getRec.Body.String())

	var got scanner.ScannerPlugin
	secParseData(t, getRec.Body, &got)
	require.Equal(t, scanner.RedactedEnvValue, got.ConfiguredEnv["VENDOR_API_KEY"],
		"precondition: the read door really hands back the sentinel")
	require.Equal(t, "${keyring:vendor-key}", got.ConfiguredEnv["KEYRING_REF"],
		"precondition: a keyring REFERENCE is preserved verbatim and must keep round-tripping")

	// 2. Post it straight back, plus one genuinely new value — exactly what a
	//    naive "edit one field and PUT the whole document" client does.
	got.ConfiguredEnv["NEW_KEY"] = "brand-new-value"
	payload, err := json.Marshal(map[string]interface{}{"env": got.ConfiguredEnv})
	require.NoError(t, err)

	rec := putScannerConfig(t, srv, string(payload))
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	// 3. The sentinel must never reach the service. The service MERGES, so an
	//    absent key keeps its stored value — which is what the sentinel means.
	require.Equal(t, 1, secCtrl.configureCalls)
	assert.NotContains(t, secCtrl.configuredEnv, "VENDOR_API_KEY",
		"the redaction sentinel would have been stored over the real key")
	for k, v := range secCtrl.configuredEnv {
		assert.NotEqual(t, scanner.RedactedEnvValue, v, "env %q carried the redaction sentinel through to storage", k)
	}

	// The entries that are NOT the sentinel must still go through, or the guard
	// would have broken configuration instead of protecting it.
	assert.Equal(t, "brand-new-value", secCtrl.configuredEnv["NEW_KEY"])
	assert.Equal(t, "${keyring:vendor-key}", secCtrl.configuredEnv["KEYRING_REF"],
		"a keyring reference is a pointer, not a secret, and must remain writable")
}

// TestConfigureScanner_SentinelOnlyBodyIsRejected. A body whose every value is
// the sentinel carries no instruction at all. Silently answering 200 would tell
// the client its edit was saved when nothing was written, so it is a 400 that
// names the cause.
func TestConfigureScanner_SentinelOnlyBodyIsRejected(t *testing.T) {
	secCtrl := &mockSecurityController{}
	srv := newTestServerWithSecurity(t, secCtrl)

	rec := putScannerConfig(t, srv, `{"env":{"VENDOR_API_KEY":"`+scanner.RedactedEnvValue+`"}}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "redaction placeholder")
	assert.Zero(t, secCtrl.configureCalls, "nothing may reach the service")
}

// TestConfigureScanner_SentinelWithDockerImageStillConfigures. Dropping the
// sentinel must not swallow the rest of the request: a body that also carries a
// docker_image is a real instruction and has to be applied.
func TestConfigureScanner_SentinelWithDockerImageStillConfigures(t *testing.T) {
	secCtrl := &mockSecurityController{}
	srv := newTestServerWithSecurity(t, secCtrl)

	rec := putScannerConfig(t, srv,
		`{"env":{"VENDOR_API_KEY":"`+scanner.RedactedEnvValue+`"},"docker_image":"ghcr.io/example/scan:v2"}`)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "ghcr.io/example/scan:v2", secCtrl.configuredImage)
	assert.Empty(t, secCtrl.configuredEnv, "the sentinel-only env must not reach the service")
}

// TestStripRedactedScannerEnv covers the predicate directly, including that it
// never mutates the caller's map.
func TestStripRedactedScannerEnv(t *testing.T) {
	in := map[string]string{
		"REAL":    "value",
		"MASKED":  scanner.RedactedEnvValue,
		"KEYRING": "${keyring:name}",
		"EMPTY":   "",
	}
	out, dropped := stripRedactedScannerEnv(in)

	assert.True(t, dropped)
	assert.Equal(t, map[string]string{"REAL": "value", "KEYRING": "${keyring:name}", "EMPTY": ""}, out)
	assert.Equal(t, scanner.RedactedEnvValue, in["MASKED"], "the caller's map must not be mutated")

	out, dropped = stripRedactedScannerEnv(map[string]string{"REAL": "value"})
	assert.False(t, dropped)
	assert.Equal(t, map[string]string{"REAL": "value"}, out)

	out, dropped = stripRedactedScannerEnv(nil)
	assert.False(t, dropped)
	assert.Nil(t, out)

	out, dropped = stripRedactedScannerEnv(map[string]string{"MASKED": scanner.RedactedEnvValue})
	assert.True(t, dropped)
	assert.Empty(t, out, "an all-sentinel map yields nothing to write")
}

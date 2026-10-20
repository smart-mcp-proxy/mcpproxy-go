package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const bundleCLIDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func bundleCLIStatusBody() string {
	return `{
  "source": "file",
  "path": "/etc/mcpproxy/scanner-bundle.json",
  "bundle_version": "2026.10.1",
  "schema_version": "1",
  "signature_count": 12,
  "runnable_rules": 10,
  "skipped_rules": 1,
  "declared_skipped": 1,
  "generated_at": "2026-09-01T00:00:00Z",
  "fingerprint": "0123456789ab",
  "full_digest": "` + bundleCLIDigest + `",
  "sequence": "42",
  "authority": "publisher",
  "trust_epoch": 3,
  "signature_verified": true,
  "staleness_days": 49,
  "coverage_degraded": true,
  "degraded_reason": "operator_bundle_missing",
  "pinned": true,
  "rollback": true,
  "loaded_at": "2026-10-20T00:00:00Z",
  "last_rejection": {"reason": "downgrade", "detail": "sequence 7 below watermark 42", "at": "2026-10-19T12:00:00Z"}
}`
}

func TestSecurityBundleStatus_HumanTableShowsEveryOperatorField(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/security/bundle": okResp(bundleCLIStatusBody())})

	out, _, err := runCLI(t, GetSecurityCommand, "table", "bundle")
	require.NoError(t, err)

	assert.Contains(t, out, bundleCLIDigest, "the full digest is the rollback input and must be shown in full")
	for _, want := range []string{
		"42",        // sequence
		"publisher", // authority
		"3",         // epoch
		"verified",  // signature
		"49",        // staleness days
		"DEGRADED: operator_bundle_missing",
		"pinned",
		"rollback",
		"downgrade",                     // last rejection reason
		"sequence 7 below watermark 42", // detail
		"2026-10-19",                    // when
	} {
		assert.Contains(t, out, want)
	}
}

func TestSecurityBundleStatus_HealthyHasNoDegradedLine(t *testing.T) {
	body := `{"source":"embedded","runnable_rules":6,"full_digest":"` + bundleCLIDigest + `","fingerprint":"0123456789ab","sequence":"5","authority":"publisher","trust_epoch":1,"signature_verified":false,"coverage_degraded":false}`
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/security/bundle": okResp(body)})

	out, _, err := runCLI(t, GetSecurityCommand, "table", "bundle")
	require.NoError(t, err)
	assert.NotContains(t, out, "DEGRADED")
	assert.NotContains(t, out, "pinned")
	assert.NotContains(t, out, "Last rejection")
}

func TestSecurityBundleStatus_JSONAndYAMLPassThrough(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/security/bundle": okResp(bundleCLIStatusBody())})

	out, _, err := runCLI(t, GetSecurityCommand, "json", "bundle")
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	assert.Equal(t, bundleCLIDigest, got["full_digest"])
	assert.Equal(t, true, got["coverage_degraded"])

	out, _, err = runCLI(t, GetSecurityCommand, "yaml", "bundle")
	require.NoError(t, err)
	assert.Contains(t, out, "full_digest: "+bundleCLIDigest)
}

func TestSecurityBundleHistory(t *testing.T) {
	body := `[
 {"authority":"publisher","trust_epoch":3,"sequence":"41","full_digest":"` + strings.Repeat("a", 64) + `","generated_at":"2026-08-01T00:00:00Z","activated_at":"2026-08-02T00:00:00Z","source":"file","signature_verified":true},
 {"authority":"publisher","trust_epoch":3,"sequence":"42","full_digest":"` + bundleCLIDigest + `","generated_at":"2026-09-01T00:00:00Z","activated_at":"2026-09-02T00:00:00Z","source":"file","signature_verified":true,"rollback":true}
]`
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/security/bundle/history": okResp(body)})

	out, _, err := runCLI(t, GetSecurityCommand, "table", "bundle", "history")
	require.NoError(t, err)
	assert.Contains(t, out, strings.Repeat("a", 64), "history digests are the rollback input and are shown in full")
	assert.Contains(t, out, bundleCLIDigest)
	assert.Less(t, strings.Index(out, strings.Repeat("a", 64)), strings.Index(out, bundleCLIDigest), "oldest first")
	assert.Contains(t, out, "rollback")

	jsonOut, _, err := runCLI(t, GetSecurityCommand, "json", "bundle", "history")
	require.NoError(t, err)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &rows), jsonOut)
	assert.Len(t, rows, 2)
}

func TestSecurityBundleHistory_Empty(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/security/bundle/history": okResp(`[]`)})
	out, _, err := runCLI(t, GetSecurityCommand, "table", "bundle", "history")
	require.NoError(t, err)
	assert.Contains(t, out, "No bundle activations")
}

func TestSecurityBundleRollback_SendsRequestAndReportsResult(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/security/bundle/rollback": okResp(bundleCLIStatusBody())})

	out, _, err := runCLI(t, GetSecurityCommand, "table", "bundle", "rollback", "--path", "/tmp/good.json", "--digest", bundleCLIDigest)
	require.NoError(t, err)
	req := rec.only(t)
	assert.Equal(t, http.MethodPost, req.Method)
	body := req.jsonBody(t)
	wantPath, absErr := filepath.Abs("/tmp/good.json")
	require.NoError(t, absErr)
	assert.Equal(t, wantPath, body["path"])
	assert.Equal(t, bundleCLIDigest, body["digest"])
	assert.NotEqual(t, true, body["embedded"])
	assert.Contains(t, out, bundleCLIDigest)
}

// The daemon is another process with its own working directory (a tray-started
// core, a service), so a relative --path must reach it already resolved
// against the CLI's cwd.
func TestSecurityBundleRollback_RelativePathIsSentAbsolute(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/security/bundle/rollback": okResp(bundleCLIStatusBody())})
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	require.NoError(t, err)

	_, _, err = runCLI(t, GetSecurityCommand, "table", "bundle", "rollback", "--path", "./known-good.json", "--digest", bundleCLIDigest)
	require.NoError(t, err)

	got, _ := rec.only(t).jsonBody(t)["path"].(string)
	assert.True(t, filepath.IsAbs(got), "the daemon must not resolve the path against its own cwd: %q", got)
	assert.Equal(t, filepath.Join(cwd, "known-good.json"), got)
}

func TestSecurityBundleRollback_EmbeddedSendsEmbedded(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/security/bundle/rollback": okResp(bundleCLIStatusBody())})
	_, _, err := runCLI(t, GetSecurityCommand, "table", "bundle", "rollback", "--embedded", "--digest", bundleCLIDigest)
	require.NoError(t, err)
	body := rec.only(t).jsonBody(t)
	assert.Equal(t, true, body["embedded"])
	assert.Equal(t, bundleCLIDigest, body["digest"])
}

func TestSecurityBundleRollback_LocalValidationNeverCallsTheDaemon(t *testing.T) {
	cases := map[string][]string{
		"neither":    {"--digest", bundleCLIDigest},
		"both":       {"--embedded", "--path", "/x", "--digest", bundleCLIDigest},
		"short":      {"--embedded", "--digest", bundleCLIDigest[:12]},
		"uppercase":  {"--embedded", "--digest", strings.ToUpper(bundleCLIDigest)},
		"nodigest":   {"--embedded"},
		"nonhex":     {"--embedded", "--digest", strings.Repeat("z", 64)},
		"toolong":    {"--embedded", "--digest", bundleCLIDigest + "0"},
		"emptypath":  {"--path", "", "--digest", bundleCLIDigest},
		"whitespace": {"--embedded", "--digest", " " + bundleCLIDigest[:63]},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			rec := newRESTRecorder(t, map[string]cannedResponse{})
			_, _, err := runCLI(t, GetSecurityCommand, "table", append([]string{"bundle", "rollback"}, args...)...)
			require.Error(t, err)
			assert.Empty(t, rec.all(), "a malformed rollback must be refused before any request")
		})
	}
}

func TestSecurityBundleRollback_ShortFingerprintMessageSaysFullDigestRequired(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{})
	_, _, err := runCLI(t, GetSecurityCommand, "table", "bundle", "rollback", "--embedded", "--digest", bundleCLIDigest[:12])
	require.Error(t, err)
	assert.Contains(t, err.Error(), "full digest")
	assert.Contains(t, err.Error(), "64")
}

func TestSecurityBundleRollback_409PrintsTheRejectionReason(t *testing.T) {
	refused := `{"success":false,"error":"bundle rollback refused: rollback_target_mismatch: named 0123456789ab, the file holds ffffffffffff","error_code":"rollback_target_mismatch","rejection":{"reason":"rollback_target_mismatch","detail":"named 0123456789ab, the file holds ffffffffffff","at":"2026-10-20T00:00:00Z"}}`
	newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/security/bundle/rollback": refuse(http.StatusConflict, refused)})

	_, _, err := runCLI(t, GetSecurityCommand, "table", "bundle", "rollback", "--path", "/tmp/x.json", "--digest", bundleCLIDigest)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rollback_target_mismatch")
	assert.Contains(t, err.Error(), "named 0123456789ab")
	assert.Contains(t, err.Error(), "last-known-good", "the operator is told the serving corpus is unchanged")
}

func TestSecurityBundleRollback_403SurfacesTheDaemonRefusal(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/security/bundle/rollback": refuse(http.StatusForbidden, `{"success":false,"error":"operation requires admin access"}`)})
	_, _, err := runCLI(t, GetSecurityCommand, "table", "bundle", "rollback", "--embedded", "--digest", bundleCLIDigest)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires admin access")
}

func TestSecurityBundleUnpinAndReload(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{
		"POST /api/v1/security/bundle/unpin":  okResp(bundleCLIStatusBody()),
		"POST /api/v1/security/bundle/reload": okResp(bundleCLIStatusBody()),
	})

	out, _, err := runCLI(t, GetSecurityCommand, "table", "bundle", "unpin")
	require.NoError(t, err)
	assert.Contains(t, out, bundleCLIDigest)

	out, _, err = runCLI(t, GetSecurityCommand, "table", "bundle", "reload")
	require.NoError(t, err)
	assert.Contains(t, out, bundleCLIDigest)

	reqs := rec.all()
	require.Len(t, reqs, 2)
	assert.Equal(t, "/api/v1/security/bundle/unpin", reqs[0].Path)
	assert.Equal(t, http.MethodPost, reqs[0].Method)
	assert.Equal(t, "/api/v1/security/bundle/reload", reqs[1].Path)
}

func TestSecurityBundleUnpin_JSON(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"POST /api/v1/security/bundle/unpin": okResp(bundleCLIStatusBody())})
	out, _, err := runCLI(t, GetSecurityCommand, "json", "bundle", "unpin")
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	assert.Equal(t, bundleCLIDigest, got["full_digest"])
}

func TestSecurityBundleStatus_DaemonError(t *testing.T) {
	newRESTRecorder(t, map[string]cannedResponse{"GET /api/v1/security/bundle": refuse(http.StatusForbidden, `{"success":false,"error":"Agent tokens cannot read the TPA signature bundle status (administrators only)"}`)})
	_, _, err := runCLI(t, GetSecurityCommand, "table", "bundle")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Agent tokens cannot")
}

// signatureBundleLines is shared by `security overview` and `security bundle`.
func TestSignatureBundleLines_NewFields(t *testing.T) {
	var bundle map[string]any
	require.NoError(t, json.Unmarshal([]byte(bundleCLIStatusBody()), &bundle))
	lines := signatureBundleLines(map[string]any{"signature_bundle": bundle})
	joined := strings.Join(lines, "\n")

	assert.Contains(t, joined, bundleCLIDigest, "full digest, in full")
	assert.Contains(t, joined, "0123456789ab", "short fingerprint stays")
	assert.Contains(t, joined, "DEGRADED: operator_bundle_missing")
	assert.Contains(t, joined, "publisher")
	assert.Contains(t, joined, "downgrade")
	assert.Contains(t, joined, "sequence 7 below watermark 42")
}

func TestSignatureBundleLines_NoDegradedLineWhenHealthy(t *testing.T) {
	lines := signatureBundleLines(map[string]any{"signature_bundle": map[string]any{
		"source": "embedded", "runnable_rules": float64(6), "coverage_degraded": false,
		"sequence": "5", "authority": "publisher",
	}})
	joined := strings.Join(lines, "\n")
	assert.NotContains(t, joined, "DEGRADED")
	assert.Contains(t, joined, "5")
}

func TestSignatureBundleLines_UnsignedExternalShowsNoAuthorityNoise(t *testing.T) {
	lines := signatureBundleLines(map[string]any{"signature_bundle": map[string]any{
		"source": "file", "path": "/p", "runnable_rules": float64(6),
		"coverage_degraded": true, "degraded_reason": "unsigned_external_active",
	}})
	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, "DEGRADED: unsigned_external_active")
	assert.Contains(t, joined, "unsigned", "an unsigned external bundle says so")
}

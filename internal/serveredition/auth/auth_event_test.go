//go:build server

package auth

// Spec 107 T106 (PR-D) [compile-red until T107]: the auth_event emitter
// (NewAuditEmitter, T107) installed on OAuthHandler.LoginResultObserver
// writes exactly one `auth_event` line per terminal login attempt (every
// FR-013 reason, including authorization_denied via the PR-B refusal
// fixtures and fault injection -> internal_error), one per logout, and none
// for an abandoned redirect. Identity is stage-dependent, never
// reason-dependent (contracts/audit-line-events.md "auth_event"):
//   - ok | logout | subject_mismatch | user_disabled | internal_error
//     (once the store has been reached and a record exists) carry
//     caller.user_id + caller.kind session_user|session_admin.
//   - domain_not_allowed | userinfo_subject_mismatch | provider_error raised
//     by the userinfo fetch after a verified ID token carry caller.email_hash
//     + caller.kind anonymous, never user_id.
//   - every other reason (state_invalid, authorization_denied,
//     discovery_failed, provider_error from discovery/token exchange,
//     id_token_invalid, nonce_mismatch, audience_mismatch, issuer_mismatch,
//     token_expired, email_missing, email_unverified, and internal_error
//     before the store is consulted) carries neither.
//
// This file re-drives the PR-B fixtures of oauth_handler_refusal_test.go
// (same package: refusalRig, newOIDCRefusalRig, newRefusalRig,
// refusalServerEditionConfig, refusalUser/refusalSub/refusalCallbackURI,
// failingLoginStore/failingSessionCreator) against a captured audit.Sink and
// validates every captured line against the binding wire schema
// (contracts/audit-line.schema.json).
//
// Contract exercised here (tasks.md T106/T107):
//
//	func NewAuditEmitter(audit.Sink, *zap.SugaredLogger) func(LoginResult)

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// ---------------------------------------------------------------------------
// capture sink
// ---------------------------------------------------------------------------

// captureSink is an audit.Sink that records every written line verbatim, for
// assertion. It never fails a write.
type captureSink struct {
	mu    sync.Mutex
	lines [][]byte
}

func (s *captureSink) Write(line []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := append([]byte(nil), line...)
	s.lines = append(s.lines, cp)
	return nil
}

func (s *captureSink) WriteFailures() uint64 { return 0 }
func (s *captureSink) SanitizerHits() uint64 { return 0 }
func (s *captureSink) Close() error          { return nil }

func (s *captureSink) all() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.lines))
	copy(out, s.lines)
	return out
}

func (s *captureSink) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = nil
}

// exactlyOne asserts the count invariant (SC-003: #auth_event(surface=login)
// == #terminal login attempts) and returns the decoded line.
func (s *captureSink) exactlyOne(t *testing.T) map[string]interface{} {
	t.Helper()
	lines := s.all()
	require.Len(t, lines, 1, "exactly one auth_event line per terminal attempt, got %d: %s", len(lines), lines)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(lines[0], &m))
	return m
}

var _ audit.Sink = (*captureSink)(nil)

// ---------------------------------------------------------------------------
// schema validation
// ---------------------------------------------------------------------------

var authEventSchema *jsonschema.Schema

// loadAuthEventSchema compiles the binding wire schema once per process.
func loadAuthEventSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	if authEventSchema != nil {
		return authEventSchema
	}
	path := filepath.Join("..", "..", "..", "specs", "107-server-edition-sso-hardening", "contracts", "audit-line.schema.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "reading %s", path)
	var doc map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &doc))
	c := jsonschema.NewCompiler()
	require.NoError(t, c.AddResource("mem://auth-event-t106.json", doc))
	sch, err := c.Compile("mem://auth-event-t106.json")
	require.NoError(t, err)
	authEventSchema = sch
	return sch
}

func assertValidatesAgainstSchema(t *testing.T, line map[string]interface{}) {
	t.Helper()
	assert.NoError(t, loadAuthEventSchema(t).Validate(line), "auth_event line must validate against the binding wire schema: %+v", line)
}

// ---------------------------------------------------------------------------
// rig wiring: chain the PR-B result recorder with the T107 audit emitter so
// existing refusalRig helpers (exactlyOne, approveFlow, …) stay usable while
// every line the emitter writes is also captured.
// ---------------------------------------------------------------------------

func attachAuditCapture(rig *refusalRig) *captureSink {
	sink := &captureSink{}
	emit := NewAuditEmitter(sink, zap.NewNop().Sugar())
	rec := rig.results
	rig.handler.LoginResultObserver = func(res LoginResult) {
		rec.observe(res)
		emit(res)
	}
	return sink
}

// ---------------------------------------------------------------------------
// common fields every auth_event line carries (contracts/audit-line-
// events.md "Common keys").
// ---------------------------------------------------------------------------

func assertCommonAuthEventFields(t *testing.T, line map[string]interface{}, wantRequestID, wantSurface, wantReason string) {
	t.Helper()
	assert.EqualValues(t, float64(1), line["schema_version"])
	assert.Equal(t, "auth_event", line["event"])
	assert.Equal(t, "local", line["origin"], "login/logout is REST-only, never the tray socket or stdio")
	assert.Equal(t, "api", line["source"])
	assert.Equal(t, wantRequestID, line["request_id"])
	assert.Equal(t, wantSurface, line["surface"])
	assert.Equal(t, wantReason, line["reason"])
	require.Contains(t, line, "caller")
}

func callerOf(t *testing.T, line map[string]interface{}) map[string]interface{} {
	t.Helper()
	c, ok := line["caller"].(map[string]interface{})
	require.True(t, ok, "caller must be an object: %+v", line)
	return c
}

// ---------------------------------------------------------------------------
// stage-dependent identity across every FR-013 reason (T106 table)
// ---------------------------------------------------------------------------

func TestAuthEvent_StageDependentIdentityAcrossEveryReason(t *testing.T) {
	t.Run("state_invalid: neither user_id nor email_hash", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		sink := attachAuditCapture(rig)
		const rid = "req-ae-state-invalid"
		rig.callback(rid, refusalCallbackURI+"?code=x&state=bogus")

		line := sink.exactlyOne(t)
		assertCommonAuthEventFields(t, line, rid, "login", "state_invalid")
		c := callerOf(t, line)
		assert.Equal(t, "anonymous", c["kind"])
		assert.NotContains(t, c, "user_id")
		assert.NotContains(t, c, "email_hash")
		assertValidatesAgainstSchema(t, line)
	})

	t.Run("authorization_denied: neither identity field", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		sink := attachAuditCapture(rig)
		const rid = "req-ae-authz-denied"
		authURL := rig.mustLogin(rid)
		sink.reset()
		rig.results.reset()
		loc := rig.authorizeForm(authURL, "deny")
		rig.callback(rid, loc.String())

		line := sink.exactlyOne(t)
		assertCommonAuthEventFields(t, line, rid, "login", "authorization_denied")
		c := callerOf(t, line)
		assert.Equal(t, "anonymous", c["kind"])
		assert.NotContains(t, c, "user_id")
		assert.NotContains(t, c, "email_hash")
		assertValidatesAgainstSchema(t, line)
	})

	t.Run("id_token_invalid: neither identity field", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{IDTokenBadSignature: true}, nil)
		sink := attachAuditCapture(rig)
		const rid = "req-ae-id-token-invalid"
		rig.approveFlow(rid)

		line := sink.exactlyOne(t)
		assertCommonAuthEventFields(t, line, rid, "login", "id_token_invalid")
		c := callerOf(t, line)
		assert.Equal(t, "anonymous", c["kind"])
		assert.NotContains(t, c, "user_id")
		assert.NotContains(t, c, "email_hash")
		assertValidatesAgainstSchema(t, line)
	})

	t.Run("domain_not_allowed: email_hash, never user_id", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, func(c *config.ServerEditionOAuthConfig) {
			c.AllowedDomains = []string{"other.example"}
		})
		sink := attachAuditCapture(rig)
		const rid = "req-ae-domain-not-allowed"
		rig.approveFlow(rid)

		line := sink.exactlyOne(t)
		assertCommonAuthEventFields(t, line, rid, "login", "domain_not_allowed")
		c := callerOf(t, line)
		assert.Equal(t, "anonymous", c["kind"])
		assert.NotContains(t, c, "user_id")
		require.Contains(t, c, "email_hash")
		assert.Equal(t, emailHashOf(refusalUser), c["email_hash"])
		assertValidatesAgainstSchema(t, line)
	})

	t.Run("userinfo_subject_mismatch: email_hash, never user_id", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{UserinfoSubMismatch: true}, nil)
		sink := attachAuditCapture(rig)
		const rid = "req-ae-userinfo-sub-mismatch"
		rig.approveFlow(rid)

		line := sink.exactlyOne(t)
		assertCommonAuthEventFields(t, line, rid, "login", "userinfo_subject_mismatch")
		c := callerOf(t, line)
		assert.Equal(t, "anonymous", c["kind"])
		assert.NotContains(t, c, "user_id")
		require.Contains(t, c, "email_hash")
		assert.Equal(t, emailHashOf(refusalUser), c["email_hash"])
		assertValidatesAgainstSchema(t, line)
	})

	t.Run("provider_error from the userinfo fetch after a verified ID token: email_hash, never user_id", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{UserinfoUnavailable: true}, nil)
		sink := attachAuditCapture(rig)
		const rid = "req-ae-provider-error-userinfo"
		rig.approveFlow(rid)

		line := sink.exactlyOne(t)
		assertCommonAuthEventFields(t, line, rid, "login", "provider_error")
		c := callerOf(t, line)
		assert.Equal(t, "anonymous", c["kind"])
		assert.NotContains(t, c, "user_id")
		require.Contains(t, c, "email_hash")
		assert.Equal(t, emailHashOf(refusalUser), c["email_hash"])
		assertValidatesAgainstSchema(t, line)
	})

	t.Run("provider_error from the token exchange: neither identity field", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		sink := attachAuditCapture(rig)
		const rid = "req-ae-provider-error-token"
		authURL := rig.mustLogin(rid)
		cb := rig.authorizeForm(authURL, "approve")
		sink.reset()
		rig.results.reset()
		rig.fake.Server.SetErrorMode(oauthserver.ErrorMode{TokenServerError: true})
		rig.callback(rid, cb.String())

		line := sink.exactlyOne(t)
		assertCommonAuthEventFields(t, line, rid, "login", "provider_error")
		c := callerOf(t, line)
		assert.Equal(t, "anonymous", c["kind"])
		assert.NotContains(t, c, "user_id")
		assert.NotContains(t, c, "email_hash")
		assertValidatesAgainstSchema(t, line)
	})

	t.Run("discovery_failed: neither identity field", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{DiscoveryHTTPTokenEndpoint: true}, nil)
		sink := attachAuditCapture(rig)
		const rid = "req-ae-discovery-failed"
		rig.login(rid)

		line := sink.exactlyOne(t)
		assertCommonAuthEventFields(t, line, rid, "login", "discovery_failed")
		c := callerOf(t, line)
		assert.Equal(t, "anonymous", c["kind"])
		assert.NotContains(t, c, "user_id")
		assert.NotContains(t, c, "email_hash")
		assertValidatesAgainstSchema(t, line)
	})

	// "discovery_failed carries redirect_rejected" is a round-2 cross-review
	// regression (PR-D): the redirect_uri is sanitised in HandleLogin BEFORE
	// the pending state is stored, so a failure before that point (discovery,
	// provider errors) used to report its terminal result with NO flags at
	// all — the callback's own `attempt.flag(FlagRedirectRejected)` (read
	// from the stored pending state) never runs on this pre-redirect
	// failure path, because no pending state was ever stored for it.
	t.Run("discovery_failed carries redirect_rejected when the caller's redirect_uri was rejected", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{DiscoveryHTTPTokenEndpoint: true}, nil)
		sink := attachAuditCapture(rig)
		const rid = "req-ae-discovery-failed-redirect-rejected"

		req := withRequestID(httptest.NewRequest(http.MethodGet,
			"http://"+refusalHost+"/api/v1/auth/login?redirect_uri=https://evil.example.com/", nil), rid)
		w := httptest.NewRecorder()
		rig.handler.HandleLogin(w, req)
		require.NotEqual(t, http.StatusFound, w.Code, "control: this redirect_uri must not itself cause a redirect")

		line := sink.exactlyOne(t)
		assertCommonAuthEventFields(t, line, rid, "login", "discovery_failed")
		flags, _ := line["flags"].([]interface{})
		assert.Contains(t, flags, "redirect_rejected", "flags = %v", line["flags"])
		assertValidatesAgainstSchema(t, line)
	})

	t.Run("subject_mismatch: session caller + user_id, never email_hash", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		u := users.NewUser(refusalUser, "Alice Example", "oidc", "someone-else-sub")
		require.NoError(t, rig.store.CreateUser(u))
		sink := attachAuditCapture(rig)
		const rid = "req-ae-subject-mismatch"
		rig.approveFlow(rid)

		line := sink.exactlyOne(t)
		assertCommonAuthEventFields(t, line, rid, "login", "subject_mismatch")
		c := callerOf(t, line)
		assert.Equal(t, "session_user", c["kind"])
		assert.Equal(t, u.ID, c["user_id"])
		assert.Equal(t, "user", c["role"])
		assert.NotContains(t, c, "email_hash")
		assertValidatesAgainstSchema(t, line)
	})

	t.Run("user_disabled: session caller + user_id, never email_hash", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		u := users.NewUser(refusalUser, "Alice Example", "oidc", refusalSub)
		u.Disabled = true
		require.NoError(t, rig.store.CreateUser(u))
		sink := attachAuditCapture(rig)
		const rid = "req-ae-user-disabled"
		rig.approveFlow(rid)

		line := sink.exactlyOne(t)
		assertCommonAuthEventFields(t, line, rid, "login", "user_disabled")
		c := callerOf(t, line)
		assert.Equal(t, "session_user", c["kind"])
		assert.Equal(t, u.ID, c["user_id"])
		assertValidatesAgainstSchema(t, line)
	})

	t.Run("internal_error before the store is consulted (failing loginStore): neither identity field", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		rig.handler.loginStore = failingLoginStore{}
		sink := attachAuditCapture(rig)
		const rid = "req-ae-internal-store"
		rig.approveFlow(rid)

		line := sink.exactlyOne(t)
		assertCommonAuthEventFields(t, line, rid, "login", "internal_error")
		c := callerOf(t, line)
		assert.Equal(t, "anonymous", c["kind"])
		assert.NotContains(t, c, "user_id")
		assert.NotContains(t, c, "email_hash")
		assertValidatesAgainstSchema(t, line)
	})

	t.Run("internal_error after the record exists (failing sessionCreator): session caller + user_id", func(t *testing.T) {
		rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
		rig.handler.sessionCreator = failingSessionCreator{}
		sink := attachAuditCapture(rig)
		const rid = "req-ae-internal-session"
		rig.approveFlow(rid)

		line := sink.exactlyOne(t)
		assertCommonAuthEventFields(t, line, rid, "login", "internal_error")
		c := callerOf(t, line)
		assert.Equal(t, "session_user", c["kind"])
		require.Contains(t, c, "user_id")
		assert.NotContains(t, c, "email_hash")
		assertValidatesAgainstSchema(t, line)
	})
}

// ---------------------------------------------------------------------------
// ok / admin role / logout / abandoned redirect
// ---------------------------------------------------------------------------

func TestAuthEvent_LoginOK_SessionUserCaller(t *testing.T) {
	rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
	sink := attachAuditCapture(rig)
	const rid = "req-ae-ok"
	w := rig.approveFlow(rid)
	require.Equal(t, http.StatusFound, w.Code, "login must succeed; body=%s", w.Body.String())

	line := sink.exactlyOne(t)
	assertCommonAuthEventFields(t, line, rid, "login", "ok")
	c := callerOf(t, line)
	assert.Equal(t, "session_user", c["kind"])
	assert.Equal(t, "user", c["role"])
	assert.Equal(t, "oidc", c["provider"])
	require.Contains(t, c, "user_id")
	assert.NotEmpty(t, c["user_id"])
	assert.NotContains(t, c, "email_hash", "email_hash and user_id are mutually exclusive")
	assertValidatesAgainstSchema(t, line)
}

func TestAuthEvent_LoginOK_AdminEmail_SessionAdminCaller(t *testing.T) {
	rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
	promoted := refusalServerEditionConfig(rig.live.get().OAuth)
	promoted.AdminEmails = []string{refusalUser}
	rig.live.swap(promoted)
	sink := attachAuditCapture(rig)
	const rid = "req-ae-ok-admin"
	w := rig.approveFlow(rid)
	require.Equal(t, http.StatusFound, w.Code, "login must succeed; body=%s", w.Body.String())

	line := sink.exactlyOne(t)
	c := callerOf(t, line)
	assert.Equal(t, "session_admin", c["kind"])
	assert.Equal(t, "admin", c["role"])
	assertValidatesAgainstSchema(t, line)
}

func TestAuthEvent_Logout_SurfaceLogoutReasonLogout(t *testing.T) {
	rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
	sink := attachAuditCapture(rig)

	w := rig.approveFlow("req-ae-logout-login")
	require.Equal(t, http.StatusFound, w.Code)
	loginLine := sink.exactlyOne(t)
	loginUserID := callerOf(t, loginLine)["user_id"]
	require.NotEmpty(t, loginUserID)
	sink.reset()
	rig.results.reset()

	cookies := w.Result().Cookies()
	require.NotEmpty(t, cookies, "login must set the session cookie")

	const logoutRID = "req-ae-logout"
	logoutReq := withRequestID(httptest.NewRequest(http.MethodPost, "http://"+refusalHost+"/api/v1/auth/logout", nil), logoutRID)
	for _, ck := range cookies {
		logoutReq.AddCookie(ck)
	}
	logoutW := httptest.NewRecorder()
	rig.handler.HandleLogout(logoutW, logoutReq)
	require.Equal(t, http.StatusOK, logoutW.Code, "logout must succeed; body=%s", logoutW.Body.String())

	line := sink.exactlyOne(t)
	assertCommonAuthEventFields(t, line, logoutRID, "logout", "logout")
	c := callerOf(t, line)
	assert.Equal(t, "session_user", c["kind"])
	assert.Equal(t, loginUserID, c["user_id"])
	assertValidatesAgainstSchema(t, line)
}

// failingAuditSink is an audit.Sink whose Write always fails, for proving
// NewAuditEmitter's own logging behaviour on a persistent sink failure.
type failingAuditSink struct{ writes int }

func (s *failingAuditSink) Write(_ []byte) error  { s.writes++; return errTestSinkWrite }
func (s *failingAuditSink) WriteFailures() uint64 { return uint64(s.writes) }
func (s *failingAuditSink) SanitizerHits() uint64 { return 0 }
func (s *failingAuditSink) Close() error          { return nil }

var errTestSinkWrite = fmt.Errorf("test: sink write always fails")

// TestAuthEvent_WriteFailureNotLoggedPerCall is a round-3 cross-review
// regression (Spec 107 PR-D): FR-018 caps runtime audit-sink-failure
// logging at once per minute. That cap lives on the sink's own
// WithFailureLogger (internal/audit, T109); NewAuditEmitter must not ALSO
// log a warning on every failed write, or a persistent disk/stdout failure
// produces one unbounded warning per login/logout, bypassing the sink's
// rate limit entirely. Before this fix, every failed sink.Write logged
// unconditionally here.
func TestAuthEvent_WriteFailureNotLoggedPerCall(t *testing.T) {
	sink := &failingAuditSink{}
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core).Sugar()

	emit := NewAuditEmitter(sink, logger)
	for i := 0; i < 5; i++ {
		emit(LoginResult{RequestID: fmt.Sprintf("req-fail-%d", i), Surface: "login", Reason: LoginRefusal("ok"), UserID: "u1", Role: "user", Provider: "oidc"})
	}

	assert.Equal(t, 5, sink.writes, "control: every call must have reached the sink")
	assert.Empty(t, logs.All(), "a per-request write-failure warning bypasses the sink's own once-per-minute rate limit (FR-018)")
}

func TestAuthEvent_AbandonedRedirect_NoLine(t *testing.T) {
	rig := newOIDCRefusalRig(t, oauthserver.ErrorMode{}, nil)
	sink := attachAuditCapture(rig)

	// A login that redirects to the IdP but never comes back writes no
	// terminal LoginResult and therefore no auth_event line.
	rig.mustLogin("req-ae-abandoned")
	assert.Empty(t, sink.all(), "an abandoned redirect (pending state never returns) writes no auth_event line")
	assert.Empty(t, rig.results.all())
}

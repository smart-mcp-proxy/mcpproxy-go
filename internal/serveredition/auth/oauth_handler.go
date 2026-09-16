//go:build server

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// LoginRefusal is the closed terminal-result vocabulary of one login attempt
// (Spec 107 FR-013 `auth_event.reason`). Every callback branch maps to exactly
// one value; the value reaches the server log and the audit line, never the
// page rendered to the user (FR-024).
type LoginRefusal string

// The FR-013 vocabulary. "ok" and "logout" are the two non-refusals.
const (
	LoginOK                      LoginRefusal = "ok"
	LoginLogout                  LoginRefusal = "logout"
	LoginAuthorizationDenied     LoginRefusal = "authorization_denied"
	LoginIDTokenInvalid          LoginRefusal = "id_token_invalid"
	LoginNonceMismatch           LoginRefusal = "nonce_mismatch"
	LoginAudienceMismatch        LoginRefusal = "audience_mismatch"
	LoginIssuerMismatch          LoginRefusal = "issuer_mismatch"
	LoginTokenExpired            LoginRefusal = "token_expired"
	LoginEmailMissing            LoginRefusal = "email_missing"
	LoginEmailUnverified         LoginRefusal = "email_unverified"
	LoginDomainNotAllowed        LoginRefusal = "domain_not_allowed"
	LoginSubjectMismatch         LoginRefusal = "subject_mismatch"
	LoginUserinfoSubjectMismatch LoginRefusal = "userinfo_subject_mismatch"
	LoginUserDisabled            LoginRefusal = "user_disabled"
	LoginStateInvalid            LoginRefusal = "state_invalid"
	LoginProviderError           LoginRefusal = "provider_error"
	LoginDiscoveryFailed         LoginRefusal = "discovery_failed"
	LoginInternalError           LoginRefusal = "internal_error"
)

// IsUnavailable reports the 503 class (FR-024): an IdP-side failure or a
// proxy-side failure after verification, never the user's fault.
func (r LoginRefusal) IsUnavailable() bool {
	switch r {
	case LoginProviderError, LoginDiscoveryFailed, LoginInternalError:
		return true
	}
	return false
}

// LoginFlag is a non-terminal fact of one attempt (FR-013 `flags`).
type LoginFlag string

// The closed flags vocabulary.
const (
	FlagProviderRebound    LoginFlag = "provider_rebound"
	FlagRedirectRejected   LoginFlag = "redirect_rejected"
	FlagGroupsClaimMissing LoginFlag = "groups_claim_missing"
)

// Surfaces a LoginResult is reported on.
const (
	LoginSurfaceLogin  = "login"
	LoginSurfaceLogout = "logout"
)

// LoginResult is the typed terminal result of one login (or logout) attempt,
// reported exactly once through OAuthHandler.LoginResultObserver.
//
// Identity is stage-dependent (FR-013): UserID when the attempt reached the
// user store and a record exists; otherwise EmailHash only when a verified
// email is known and the store was not yet consulted; neither before an
// identity is established. The two never appear together.
type LoginResult struct {
	RequestID string
	Surface   string // LoginSurfaceLogin | LoginSurfaceLogout
	Reason    LoginRefusal
	UserID    string
	EmailHash string // hex SHA-256 of the normalised email
	Flags     []LoginFlag
}

// loginStore is the narrow user-store seam the callback writes through.
type loginStore interface {
	UpdateUserLogin(ctx context.Context, claims users.LoginClaims) (users.LoginOutcome, error)
	GetUserByEmail(email string) (*users.User, error)
}

// sessionCreator is the narrow session seam the callback creates through.
type sessionCreator interface {
	CreateSession(userID string, r *http.Request) (*users.Session, error)
}

// bearerSignerFunc mints the user JWT; defaulted to GenerateBearerToken.
type bearerSignerFunc func(hmacKey []byte, userID, email, displayName, role, provider string, ttl time.Duration) (string, error)

// OAuthHandler handles the OAuth login/callback/logout HTTP endpoints.
type OAuthHandler struct {
	userStore      *users.UserStore
	sessionManager *SessionManager
	// config yields the LIVE server-edition block: the login role is derived
	// from the current admin_emails, never the boot pointer (#1169).
	config ServerEditionConfigProvider
	// oauthCfg is the boot OAuth block the provider was built from; oauth.*
	// is restart-pinned (contracts/config-keys.md).
	oauthCfg *config.ServerEditionOAuthConfig
	// provider is resolved ONCE at construction (T042) so the oidc discovery
	// and JWKS caches live as long as the handler; providerErr records a
	// registry miss, reported as internal_error on every attempt.
	provider    *OAuthProvider
	providerErr error
	hmacKey     []byte
	logger      *zap.SugaredLogger

	// Fault-injection seams (T044), defaulted to the real implementations.
	loginStore     loginStore
	sessionCreator sessionCreator
	bearerSigner   bearerSignerFunc

	// LoginResultObserver receives the typed terminal result of every attempt
	// exactly once (nil = no-op). PR-D installs the auth_event emitter here.
	LoginResultObserver func(LoginResult)

	// CSRF state storage (in-memory, keyed by state string)
	pendingStates map[string]*oauthState
	statesMu      sync.Mutex
	lastSweep     time.Time // guarded by statesMu; throttles the age sweep
}

type oauthState struct {
	CodeVerifier     string    // PKCE code verifier
	Nonce            string    // OIDC nonce bound to this state (`oidc` only)
	RedirectURI      string    // Where to redirect after login (same-origin path)
	RedirectRejected bool      // The requested redirect_uri was replaced by /ui/
	CreatedAt        time.Time // For cleanup and cap eviction
}

const (
	// stateMaxAge is the maximum age for pending OAuth states before cleanup.
	stateMaxAge = 10 * time.Minute
	// stateSweepInterval throttles the age sweep HandleLogin runs.
	stateSweepInterval = time.Minute
	// maxPendingStates bounds the map (FR-021): the oldest entry is evicted on
	// insert when full, in addition to the age sweep.
	maxPendingStates = 10000
	// defaultLoginRedirect is where a login lands without a valid redirect_uri.
	defaultLoginRedirect = "/ui/"
)

// NewOAuthHandler creates a new OAuthHandler. The provider is resolved from
// the block cfg yields at construction; discovery stays lazy, so no network
// I/O happens here.
func NewOAuthHandler(
	userStore *users.UserStore,
	sessionManager *SessionManager,
	cfg ServerEditionConfigProvider,
	hmacKey []byte,
	logger *zap.SugaredLogger,
) *OAuthHandler {
	h := &OAuthHandler{
		userStore:      userStore,
		sessionManager: sessionManager,
		config:         cfg,
		hmacKey:        hmacKey,
		logger:         logger,
		loginStore:     userStore,
		sessionCreator: sessionManager,
		bearerSigner:   GenerateBearerToken,
		pendingStates:  make(map[string]*oauthState),
	}
	if cfg != nil {
		if boot := cfg(); boot != nil && boot.OAuth != nil {
			h.oauthCfg = boot.Clone().OAuth
			h.provider, h.providerErr = GetProviderFromConfig(h.oauthCfg)
		}
	}
	if h.oauthCfg == nil {
		h.providerErr = errors.New("OAuth not configured")
	}
	return h
}

// HandleLogin initiates the OAuth login flow by redirecting the user to the
// OAuth provider's authorization URL.
// GET /api/v1/auth/login?redirect_uri=/dashboard
func (h *OAuthHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	attempt := h.newAttempt(r)

	if h.providerErr != nil {
		attempt.unavailable(w, LoginInternalError, "provider", h.providerErr)
		return
	}

	// Clean up stale states to prevent memory leaks
	h.maybeCleanupStaleStates()

	state, err := randomToken(32, hex.EncodeToString)
	if err != nil {
		attempt.unavailable(w, LoginInternalError, "state generation", err)
		return
	}
	codeVerifier, err := randomToken(32, base64.RawURLEncoding.EncodeToString)
	if err != nil {
		attempt.unavailable(w, LoginInternalError, "code verifier generation", err)
		return
	}
	challengeHash := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(challengeHash[:])

	redirectURI, rejected := sanitizeLoginRedirect(r.URL.Query().Get("redirect_uri"))
	callbackURL := buildCallbackURL(r)

	// Build the authorization URL before allocating the pending state, so a
	// refused login (discovery rejected, IdP unreachable) leaves none behind.
	// Login never asks the IdP for offline access: the session and the bearer
	// JWT carry the user, and the IdP refresh token that `store_idp_tokens`
	// once persisted had no reader (Spec 107 FR-033).
	var (
		authURL string
		nonce   string
	)
	if h.provider.IsGenericOIDC() {
		nonce, err = randomToken(32, base64.RawURLEncoding.EncodeToString)
		if err != nil {
			attempt.unavailable(w, LoginInternalError, "nonce generation", err)
			return
		}
		authURL, err = h.provider.oidc.authorizationURL(r.Context(), callbackURL, state, nonce, codeChallenge)
		if err != nil {
			attempt.fail(w, err)
			return
		}
	} else {
		authURL = h.provider.BuildAuthURL(h.oauthCfg.ClientID, callbackURL, state, codeChallenge)
	}

	h.storePendingState(state, &oauthState{
		CodeVerifier:     codeVerifier,
		Nonce:            nonce,
		RedirectURI:      redirectURI,
		RedirectRejected: rejected,
		CreatedAt:        time.Now(),
	})

	h.logger.Infow("initiating OAuth login", "provider", h.provider.Name, "request_id", attempt.requestID, "redirect_rejected", rejected)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// HandleCallback handles the OAuth provider callback after user authentication.
// GET /api/v1/auth/callback?code=xxx&state=yyy
//
// The pending state is consumed FIRST; only then is the response classified:
// an IdP authorization error (`error=…`, RFC 6749 §4.1.2.1) on a live state
// is authorization_denied, a missing or unknown state is state_invalid.
func (h *OAuthHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	attempt := h.newAttempt(r)

	if h.providerErr != nil {
		attempt.unavailable(w, LoginInternalError, "provider", h.providerErr)
		return
	}

	q := r.URL.Query()
	pending, ok := h.consumePendingState(q.Get("state"))
	if !ok {
		attempt.refuse(w, LoginStateInvalid, "state missing, unknown or expired")
		return
	}
	if pending.RedirectRejected {
		attempt.flag(FlagRedirectRejected)
	}

	if idpErr := q.Get("error"); idpErr != "" {
		// The IdP's error and description reach the server log only.
		h.logger.Warnw("IdP answered the authorization request with an error",
			"request_id", attempt.requestID, "idp_error", idpErr, "idp_error_description", q.Get("error_description"))
		attempt.refuse(w, LoginAuthorizationDenied, "authorization response error")
		return
	}
	code := q.Get("code")
	if code == "" {
		attempt.refuse(w, LoginAuthorizationDenied, "authorization response carries neither code nor error")
		return
	}

	callbackURL := buildCallbackURL(r)
	var (
		userInfo *OAuthUserInfo
		err      error
	)
	if h.provider.IsGenericOIDC() {
		userInfo, err = h.oidcIdentity(r.Context(), attempt, code, callbackURL, pending)
	} else {
		userInfo, err = h.legacyIdentity(r.Context(), code, callbackURL, pending)
	}
	if err != nil {
		attempt.fail(w, err)
		return
	}
	if userInfo.Email == "" {
		attempt.refuse(w, LoginEmailMissing, "email claim missing")
		return
	}
	attempt.emailHash = emailHash(userInfo.Email)

	if !h.isDomainAllowed(userInfo.Email) {
		attempt.refuse(w, LoginDomainNotAllowed, "email domain not in allowed_domains")
		return
	}

	// Upsert through the transaction-owned store contract (FR-023).
	groups := userInfo.Groups
	if groups == nil {
		groups = []string{} // legacy providers store []
	}
	outcome, err := h.loginStore.UpdateUserLogin(r.Context(), users.LoginClaims{
		Email:       userInfo.Email,
		Provider:    h.provider.Name,
		Subject:     userInfo.SubjectID,
		Name:        userInfo.DisplayName,
		AvatarURL:   userInfo.AvatarURL,
		Groups:      groups,
		GroupsKnown: true,
	})
	if err != nil {
		switch {
		case errors.Is(err, users.ErrSubjectMismatch):
			attempt.setUserID(h.lookupUserID(userInfo.Email))
			attempt.refuse(w, LoginSubjectMismatch, "provider subject differs from the stored binding")
		case errors.Is(err, users.ErrUserDisabled):
			attempt.setUserID(h.lookupUserID(userInfo.Email))
			attempt.refuse(w, LoginUserDisabled, "user record disabled")
		default:
			attempt.unavailable(w, LoginInternalError, "user store", err)
		}
		return
	}
	user := outcome.User
	attempt.setUserID(user.ID)
	if outcome.Rebound {
		attempt.flag(FlagProviderRebound)
	}

	// Role from the CURRENT admin_emails.
	role := "user"
	if live := h.liveConfig(); live != nil && live.IsAdminEmail(user.Email) {
		role = "admin"
	}

	bearerToken, err := h.bearerSigner(h.hmacKey, user.ID, user.Email, user.DisplayName, role, user.Provider, h.bearerTokenTTL())
	if err != nil {
		attempt.unavailable(w, LoginInternalError, "bearer token signer", err)
		return
	}

	session, err := h.sessionCreator.CreateSession(user.ID, r)
	if err != nil {
		attempt.unavailable(w, LoginInternalError, "session creation", err)
		return
	}
	session.BearerToken = bearerToken
	if err := h.sessionManager.store.CreateSession(session); err != nil {
		attempt.unavailable(w, LoginInternalError, "session persistence", err)
		return
	}
	h.sessionManager.SetSessionCookie(w, session)

	h.logger.Infow("OAuth login successful",
		"request_id", attempt.requestID,
		"user_id", user.ID,
		"provider", user.Provider,
		"role", role,
		"created", outcome.Created,
		"rebound", outcome.Rebound,
	)
	attempt.succeed()

	redirectTarget := pending.RedirectURI
	if redirectTarget == "" {
		redirectTarget = defaultLoginRedirect
	}
	http.Redirect(w, r, redirectTarget, http.StatusFound)
}

// oidcIdentity runs the `oidc` callback half: token exchange, verified ID
// token, email/email_verified policy, groups (FR-021/FR-022/FR-008).
func (h *OAuthHandler) oidcIdentity(ctx context.Context, attempt *loginAttempt, code, callbackURL string, pending *oauthState) (*OAuthUserInfo, error) {
	prov := h.provider.oidc
	tokenResp, err := prov.exchangeCode(ctx, code, callbackURL, pending.CodeVerifier)
	if err != nil {
		return nil, err
	}
	claims, err := prov.verifyIDToken(ctx, tokenResp.IDToken, pending.Nonce)
	if err != nil {
		return nil, err
	}
	if claims.Email == "" {
		return nil, newOIDCError(LoginEmailMissing, "id_token has no email claim", nil)
	}
	switch policy := h.oauthCfg.EmailVerifiedPolicy; policy {
	case config.EmailVerifiedPolicyIgnore:
	case config.EmailVerifiedPolicyRequireTrue:
		if claims.EmailVerified == nil || !*claims.EmailVerified {
			return nil, newOIDCError(LoginEmailUnverified, "email_verified is not true (policy require_true)", nil)
		}
	default: // refuse_false
		if claims.EmailVerified != nil && !*claims.EmailVerified {
			return nil, newOIDCError(LoginEmailUnverified, "email_verified is false (policy refuse_false)", nil)
		}
	}
	// The email is now verified: the attempt carries its hash from here on.
	attempt.emailHash = emailHash(claims.Email)

	groups, missing, err := prov.resolveGroups(ctx, claims, tokenResp.AccessToken)
	if err != nil {
		return nil, err
	}
	if missing {
		attempt.flag(FlagGroupsClaimMissing)
		attempt.groupsClaimMissing = true
	}
	return &OAuthUserInfo{
		Email:         claims.Email,
		DisplayName:   claims.Name,
		SubjectID:     claims.Subject,
		AvatarURL:     claims.Picture,
		Groups:        groups,
		EmailVerified: claims.EmailVerified,
	}, nil
}

// legacyIdentity runs the unchanged google/github/microsoft callback half
// (US2.10): client_secret_post exchange, then the ID token payload or the
// provider's userinfo/emails endpoints.
func (h *OAuthHandler) legacyIdentity(ctx context.Context, code, callbackURL string, pending *oauthState) (*OAuthUserInfo, error) {
	tokenResp, err := h.provider.ExchangeCode(ctx, code, callbackURL, h.oauthCfg.ClientID, h.oauthCfg.ClientSecret, pending.CodeVerifier)
	if err != nil {
		return nil, newOIDCError(LoginProviderError, "token exchange", err)
	}
	userInfo, err := h.provider.FetchUserInfoFromToken(ctx, tokenResp)
	if err != nil {
		return nil, newOIDCError(LoginProviderError, "userinfo", err)
	}
	return userInfo, nil
}

// HandleLogout handles user logout by revoking the session and clearing the cookie.
// POST /api/v1/auth/logout
func (h *OAuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get session from request
	session, err := h.sessionManager.GetSessionFromRequest(r)
	if err != nil {
		h.logger.Errorw("failed to get session", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if session == nil {
		writeJSONError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// Revoke session
	if err := h.sessionManager.RevokeSession(session.ID); err != nil {
		h.logger.Errorw("failed to revoke session", "error", err, "session_id", session.ID)
		// Continue with cookie clearing even if revoke fails
	}

	// Clear session cookie
	h.sessionManager.ClearSessionCookie(w)

	h.logger.Infow("user logged out", "user_id", session.UserID, "session_id", session.ID)
	h.observe(LoginResult{
		RequestID: reqcontext.GetRequestID(r.Context()),
		Surface:   LoginSurfaceLogout,
		Reason:    LoginLogout,
		UserID:    session.UserID,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "logged_out"}) //nolint:errcheck
}

// --- attempt bookkeeping -------------------------------------------------------

// loginAttempt accumulates the stage-dependent identity and flags of one
// attempt and renders exactly one terminal outcome.
type loginAttempt struct {
	h                  *OAuthHandler
	requestID          string
	userID             string
	emailHash          string
	flags              []LoginFlag
	groupsClaimMissing bool
	reported           bool
}

func (h *OAuthHandler) newAttempt(r *http.Request) *loginAttempt {
	return &loginAttempt{h: h, requestID: reqcontext.GetRequestID(r.Context())}
}

func (a *loginAttempt) flag(f LoginFlag) { a.flags = append(a.flags, f) }

// setUserID records that the store was reached and a record exists; the
// email hash is dropped, the two never appear together (FR-013).
func (a *loginAttempt) setUserID(id string) {
	if id == "" {
		return
	}
	a.userID, a.emailHash = id, ""
}

func (a *loginAttempt) result(reason LoginRefusal) LoginResult {
	return LoginResult{
		RequestID: a.requestID,
		Surface:   LoginSurfaceLogin,
		Reason:    reason,
		UserID:    a.userID,
		EmailHash: a.emailHash,
		Flags:     append([]LoginFlag(nil), a.flags...),
	}
}

// report delivers the terminal result once.
func (a *loginAttempt) report(reason LoginRefusal) {
	if a.reported {
		return
	}
	a.reported = true
	a.h.observe(a.result(reason))
}

// refuse renders the generic 403 page; the reason and the failed check reach
// the log only.
func (a *loginAttempt) refuse(w http.ResponseWriter, reason LoginRefusal, check string) {
	a.h.logger.Warnw("login refused", "request_id", a.requestID, "reason", string(reason), "check", check, "user_id", a.userID)
	a.report(reason)
	writeLoginRefusedPage(w, a.requestID)
}

// unavailable renders the 503 page for the IdP-side and proxy-side failure
// class.
func (a *loginAttempt) unavailable(w http.ResponseWriter, reason LoginRefusal, check string, err error) {
	a.h.logger.Errorw("login unavailable", "request_id", a.requestID, "reason", string(reason), "check", check, "error", err, "user_id", a.userID)
	a.report(reason)
	writeLoginUnavailablePage(w, a.requestID)
}

// fail classifies a provider/verification error into its closed reason.
func (a *loginAttempt) fail(w http.ResponseWriter, err error) {
	var oerr *oidcError
	if !errors.As(err, &oerr) {
		a.unavailable(w, LoginInternalError, "unclassified", err)
		return
	}
	if oerr.reason.IsUnavailable() {
		a.unavailable(w, oerr.reason, oerr.check, oerr.err)
		return
	}
	check := oerr.check
	if oerr.err != nil {
		check += ": " + oerr.err.Error()
	}
	a.refuse(w, oerr.reason, check)
}

// succeed reports the ok result (and the groups_claim_missing warning).
func (a *loginAttempt) succeed() {
	if a.groupsClaimMissing {
		a.h.logger.Warnw("groups_claim_missing: the verified identity carried no usable groups claim; stored []",
			"request_id", a.requestID, "user_id", a.userID, "groups_claim", a.h.oauthCfg.GroupsClaim)
	}
	a.report(LoginOK)
}

func (h *OAuthHandler) observe(res LoginResult) {
	if h.LoginResultObserver != nil {
		h.LoginResultObserver(res)
	}
}

// --- helpers ------------------------------------------------------------------

// liveConfig returns the current server-edition block (nil-safe).
func (h *OAuthHandler) liveConfig() *config.ServerEditionConfig {
	if h.config == nil {
		return nil
	}
	return h.config()
}

func (h *OAuthHandler) bearerTokenTTL() time.Duration {
	if live := h.liveConfig(); live != nil && live.BearerTokenTTL.Duration() > 0 {
		return live.BearerTokenTTL.Duration()
	}
	return 24 * time.Hour
}

// lookupUserID resolves the record id for a refused login that reached the
// store (subject_mismatch, user_disabled); best effort.
func (h *OAuthHandler) lookupUserID(email string) string {
	u, err := h.loginStore.GetUserByEmail(email)
	if err != nil || u == nil {
		return ""
	}
	return u.ID
}

// emailHash is the FR-013 email_hash: hex SHA-256 of the normalised email.
func emailHash(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

// randomToken draws n random bytes and encodes them.
func randomToken(n int, encode func([]byte) string) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random token: %w", err)
	}
	return encode(b), nil
}

// storePendingState inserts a state, evicting the oldest entry when the cap
// is reached (FR-021).
func (h *OAuthHandler) storePendingState(state string, s *oauthState) {
	h.statesMu.Lock()
	defer h.statesMu.Unlock()
	for len(h.pendingStates) >= maxPendingStates {
		var (
			oldestKey string
			oldest    time.Time
			first     = true
		)
		for k, v := range h.pendingStates {
			if first || v.CreatedAt.Before(oldest) {
				oldestKey, oldest, first = k, v.CreatedAt, false
			}
		}
		delete(h.pendingStates, oldestKey)
	}
	h.pendingStates[state] = s
}

// consumePendingState removes and returns the pending state (CSRF check).
func (h *OAuthHandler) consumePendingState(state string) (*oauthState, bool) {
	if state == "" {
		return nil, false
	}
	h.statesMu.Lock()
	defer h.statesMu.Unlock()
	pending, ok := h.pendingStates[state]
	if !ok {
		return nil, false
	}
	delete(h.pendingStates, state)
	if time.Since(pending.CreatedAt) > stateMaxAge {
		return nil, false
	}
	return pending, true
}

// maybeCleanupStaleStates runs the age sweep at most once per sweep interval,
// so a burst of logins is not O(n) per request; the cap eviction on insert
// bounds the map regardless.
func (h *OAuthHandler) maybeCleanupStaleStates() {
	h.statesMu.Lock()
	due := time.Since(h.lastSweep) >= stateSweepInterval
	if due {
		h.lastSweep = time.Now()
	}
	h.statesMu.Unlock()
	if due {
		h.cleanupStaleStates()
	}
}

// cleanupStaleStates removes pending states older than stateMaxAge.
func (h *OAuthHandler) cleanupStaleStates() {
	h.statesMu.Lock()
	defer h.statesMu.Unlock()

	cutoff := time.Now().Add(-stateMaxAge)
	for state, info := range h.pendingStates {
		if info.CreatedAt.Before(cutoff) {
			delete(h.pendingStates, state)
		}
	}
}

// sanitizeLoginRedirect admits redirect_uri only as a same-origin path
// (contracts/rest-endpoints.md §10, research D10): a single leading "/", not
// "//" or "/\", no scheme, host, backslash, CR, LF or control character —
// checked on the percent-decoded value. Anything else is replaced by /ui/
// and reported as rejected.
func sanitizeLoginRedirect(raw string) (target string, rejected bool) {
	if raw == "" {
		return defaultLoginRedirect, false
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "/\\") {
		return defaultLoginRedirect, true
	}
	for _, c := range raw {
		if c < 0x20 || c == 0x7f || c == '\\' {
			return defaultLoginRedirect, true
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return defaultLoginRedirect, true
	}
	return raw, false
}

// isDomainAllowed checks if the user's email domain is in the allowed domains list.
// Returns true if no allowed domains are configured (allow all).
func (h *OAuthHandler) isDomainAllowed(email string) bool {
	if h.oauthCfg == nil || len(h.oauthCfg.AllowedDomains) == 0 {
		return true
	}

	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return false
	}
	domain := strings.ToLower(parts[1])

	for _, allowed := range h.oauthCfg.AllowedDomains {
		if strings.EqualFold(allowed, domain) {
			return true
		}
	}
	return false
}

// buildCallbackURL constructs the OAuth callback URL from the HTTP request.
func buildCallbackURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// Check X-Forwarded-Proto for reverse proxy setups
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return fmt.Sprintf("%s://%s/api/v1/auth/callback", scheme, r.Host)
}

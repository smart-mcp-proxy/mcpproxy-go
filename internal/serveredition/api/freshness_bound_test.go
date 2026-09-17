//go:build server

// T073 (Spec 107 PR-C, contracts/entitlement-predicate.md §4-§6): the
// session-cookie-only minting doors and the freshness bound (FR-011).
//
// COMPILE-RED until T076 (auth.CredentialKind, IsSessionPrincipal,
// AgentToken.OwnerEmail/OwnerProvider/OwnerRole) and T078
// (auth.ParseTokenExpiry, the 401 gate on the minting doors) land: this file
// references those not-yet-declared symbols on purpose. No production code
// is added here.
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// --- helpers -----------------------------------------------------------

// withCredentialKind clones ac and stamps CredentialKind, the way T078's
// middleware/resolver does on a real request. It never mutates the shared
// fixture contexts (userACtx, adminUserCtx, ...).
func withCredentialKind(ac *auth.AuthContext, kind auth.CredentialKind) *auth.AuthContext {
	clone := *ac
	clone.CredentialKind = kind
	return &clone
}

// agentTokenCtxFor builds the AuthContext an agent-token-authenticated
// request carries: Type stays AuthTypeAgent (never IsUser()), but the token
// records CredentialKind so a door that checks kind alone (rather than
// falling through to the Type check) still refuses it explicitly.
func agentTokenCtxFor(ownerUserID string) *auth.AuthContext {
	tok := &auth.AgentToken{
		Name:           "agent-tok",
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
		UserID:         ownerUserID,
	}
	ac := tok.AuthContext()
	ac.CredentialKind = auth.CredentialKindAgentToken
	return ac
}

// mintingDoorCase is one (door, credential) combination under test.
type mintingDoorCase struct {
	name string
	kind auth.CredentialKind
	// wantMintStatus is what a MINTING door (generate/create/regenerate) must
	// answer for this credential kind.
	wantMintStatus int
}

var mintingDoorCases = []mintingDoorCase{
	{name: "cookie", kind: auth.CredentialKindCookie, wantMintStatus: http.StatusOK},
	{name: "bearer_jwt", kind: auth.CredentialKindBearerJWT, wantMintStatus: http.StatusUnauthorized},
}

// --- POST /auth/token ---------------------------------------------------

func TestGenerateToken_SessionCookieOnly(t *testing.T) {
	endpoints, store := authTestSetup(t)
	user := &users.User{
		ID:                testUserID,
		Email:             "freshness@example.com",
		DisplayName:       "Freshness User",
		Provider:          "google",
		ProviderSubjectID: "sub-freshness",
		CreatedAt:         time.Now().UTC(),
		LastLoginAt:       time.Now().UTC(),
	}
	require.NoError(t, store.CreateUser(user))

	for _, tc := range mintingDoorCases {
		t.Run(tc.name, func(t *testing.T) {
			ac := withCredentialKind(auth.UserContext(testUserID, user.Email, user.DisplayName, "google"), tc.kind)
			router := authTestRouter(endpoints, ac)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, tc.wantMintStatus, w.Code, "POST /auth/token with credential kind %q", tc.kind)
		})
	}

	t.Run("agent_token", func(t *testing.T) {
		router := authTestRouter(endpoints, agentTokenCtxFor(testUserID))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code, "POST /auth/token must refuse an agent-token bearer")
	})
}

// --- POST /user/tokens ---------------------------------------------------

func TestCreateUserToken_SessionCookieOnly(t *testing.T) {
	for _, tc := range mintingDoorCases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newTokenTestRig(t)
			rig.actAs(withCredentialKind(userACtx(), tc.kind))

			want := tc.wantMintStatus
			if want == http.StatusOK {
				want = http.StatusCreated // the create door's success status
			}
			w := rig.createToken(t, "tok-"+tc.name, nil)
			assert.Equal(t, want, w.Code, "POST /user/tokens with credential kind %q", tc.kind)
		})
	}

	t.Run("agent_token", func(t *testing.T) {
		rig := newTokenTestRig(t)
		rig.actAs(agentTokenCtxFor(tokenUserA))
		w := rig.createToken(t, "tok-agent", nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code, "POST /user/tokens must refuse an agent-token bearer")
	})
}

// --- POST /user/tokens/{name}/regenerate ---------------------------------

func TestRegenerateUserToken_SessionCookieOnly(t *testing.T) {
	for _, tc := range mintingDoorCases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newTokenTestRig(t)
			// Seed the token under a genuine cookie identity first so a 401
			// below can only be the door's own gate, never "token absent".
			rig.actAs(withCredentialKind(userACtx(), auth.CredentialKindCookie))
			created := rig.createToken(t, "regen-"+tc.name, nil)
			require.Equal(t, http.StatusCreated, created.Code, "setup: seeding the token must succeed")

			rig.actAs(withCredentialKind(userACtx(), tc.kind))
			w := rig.call(t, http.MethodPost, "/api/v1/user/tokens/regen-"+tc.name+"/regenerate")
			assert.Equal(t, tc.wantMintStatus, w.Code, "POST .../regenerate with credential kind %q", tc.kind)
		})
	}
}

// --- list / revoke still accept a JWT ------------------------------------

func TestListAndRevokeUserToken_AcceptBearerJWT(t *testing.T) {
	rig := newTokenTestRig(t)
	rig.actAs(withCredentialKind(userACtx(), auth.CredentialKindCookie))
	created := rig.createToken(t, "list-me", nil)
	require.Equal(t, http.StatusCreated, created.Code)

	jwtCtx := withCredentialKind(userACtx(), auth.CredentialKindBearerJWT)
	rig.actAs(jwtCtx)

	list := rig.call(t, http.MethodGet, "/api/v1/user/tokens")
	require.Equal(t, http.StatusOK, list.Code, "listing tokens must not be a minting door")

	revoke := rig.call(t, http.MethodDelete, "/api/v1/user/tokens/list-me")
	assert.Equal(t, http.StatusOK, revoke.Code, "revoking a token must not be a minting door")
}

// --- expires_in cap --------------------------------------------------------

func TestCreateUserToken_ExpiresInCapRejected(t *testing.T) {
	rig := newTokenTestRig(t)
	rig.actAs(withCredentialKind(userACtx(), auth.CredentialKindCookie))

	body := map[string]interface{}{
		"name":        "too-long-lived",
		"permissions": []string{auth.PermRead},
		"expires_in":  "9000h", // 375 days > the 365-day cap (FR-011)
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/tokens", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rig.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, "expires_in beyond the 365-day cap must be rejected")
}

// auth.ParseTokenExpiry is the shared rule httpapi.parseExpiry becomes a
// wrapper around (contracts/entitlement-predicate.md §5): positive, <= 365d,
// fixed error text. Pinned directly so the REST-layer 400 above cannot be
// coincidental (e.g. a generic time.ParseDuration failure on a differently
// malformed string).
func TestParseTokenExpiry_PositiveAndCapped(t *testing.T) {
	now := time.Now().UTC()

	_, err := auth.ParseTokenExpiry("9000h", now)
	require.Error(t, err, "375 days must be rejected")

	_, err = auth.ParseTokenExpiry("-1h", now)
	require.Error(t, err, "a non-positive duration must be rejected")

	got, err := auth.ParseTokenExpiry("24h", now)
	require.NoError(t, err)
	assert.WithinDuration(t, now.Add(24*time.Hour), got, time.Second)

	got, err = auth.ParseTokenExpiry("8760h", now) // exactly 365 days
	require.NoError(t, err, "the boundary itself (365d) must be accepted")
	assert.WithinDuration(t, now.Add(365*24*time.Hour), got, time.Second)
}

// --- owned token never has a zero ExpiresAt -------------------------------

func TestCreateUserToken_OwnedTokenNeverHasZeroExpiry(t *testing.T) {
	rig := newTokenTestRig(t)
	rig.actAs(withCredentialKind(userACtx(), auth.CredentialKindCookie))

	w := rig.createToken(t, "owned-nonzero", nil)
	require.Equal(t, http.StatusCreated, w.Code)

	var resp AgentTokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.ExpiresAt.IsZero(), "a server-edition (owned) token must never carry a zero ExpiresAt")

	// Contrast: an ownerless record — unreachable through this door, which
	// always stamps UserID — MAY be created with a zero ExpiresAt directly at
	// the storage layer (the personal edition's "never expires" token).
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, rig.store.CreateAgentToken(auth.AgentToken{
		Name:        "ownerless-may-be-zero",
		Permissions: []string{auth.PermRead},
		// UserID and ExpiresAt both left zero-valued on purpose.
	}, raw, tokenTestHMACKey))
}

// --- freshness bound: session TTL + max(JWT TTL, token TTL) --------------

// TestFreshnessBound_AccessOutlivesSessionUntilCredentialExpiry is the
// structural proof behind FR-011's formula. It uses a FAKE CLOCK — direct
// manipulation of stored ExpiresAt / negative TTLs — rather than sleeping, so
// the outcome is deterministic:
//
//  1. mint a JWT and an agent token while the session is still alive;
//  2. jump the clock forward past the session's own TTL by rewriting the
//     stored session's ExpiresAt into the past;
//  3. the session is now dead (GetSessionFromRequest returns nil) — but the
//     JWT and the agent token minted in step 1, which carry their OWN expiry
//     independent of the session record, are still valid: access survives
//     session death for up to max(jwt_ttl, token_ttl), never less;
//  4. jump the clock forward again past both credential TTLs (a JWT minted
//     with a negative TTL and a token ExpiresAt placed in the past model
//     "already past its own bound") — both are now refused, proving the
//     window is session_ttl + max(jwt_ttl, token_ttl) and not indefinite.
func TestFreshnessBound_AccessOutlivesSessionUntilCredentialExpiry(t *testing.T) {
	rig := newTokenTestRig(t)

	const (
		jwtTTL   = 30 * time.Minute
		tokenTTL = 45 * time.Minute // max(jwtTTL, tokenTTL) == tokenTTL here
	)

	// Step 1: mint while the session is alive (a real, not-yet-expired
	// session — the ONLY principal these minting doors accept, T078).
	session := users.NewSession(tokenUserA, time.Hour)
	require.NoError(t, rig.users.CreateSession(session))

	jwt, err := teamsauth.GenerateBearerToken(tokenTestHMACKey, tokenUserA, "a@example.com", "User A", "user", "google", jwtTTL)
	require.NoError(t, err)

	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, rig.store.CreateAgentToken(auth.AgentToken{
		Name:        "chained-token",
		UserID:      tokenUserA,
		Permissions: []string{auth.PermRead},
		ExpiresAt:   time.Now().UTC().Add(tokenTTL),
	}, rawToken, tokenTestHMACKey))

	// Step 2: fake-advance the clock past the session TTL by rewriting the
	// stored session directly (no sleep — deterministic).
	session.ExpiresAt = time.Now().UTC().Add(-time.Second)
	require.NoError(t, rig.users.CreateSession(session)) // overwrite in place

	cookieReq := httptest.NewRequest(http.MethodGet, "/", nil)
	cookieReq.AddCookie(&http.Cookie{Name: teamsauth.SessionCookieName, Value: session.ID})
	sessionManager := teamsauth.NewSessionManager(rig.users, time.Hour, false)
	gone, err := sessionManager.GetSessionFromRequest(cookieReq)
	require.NoError(t, err)
	assert.Nil(t, gone, "the session must be dead once its own TTL has elapsed")

	// Step 3: the JWT and the token minted BEFORE the session died still
	// carry their own, independent expiry and are still valid.
	claims, err := teamsauth.ValidateBearerToken(jwt, tokenTestHMACKey)
	require.NoError(t, err, "a JWT minted before session death must survive it, up to its own TTL")
	assert.Equal(t, tokenUserA, claims.Subject)

	validated, err := rig.store.ValidateAgentToken(rawToken, tokenTestHMACKey)
	require.NoError(t, err, "a token minted before session death must survive it, up to its own expiry")
	assert.Equal(t, "chained-token", validated.Name)

	// Step 4: fake-advance the clock again, past BOTH credential TTLs.
	expiredJWT, err := teamsauth.GenerateBearerToken(tokenTestHMACKey, tokenUserA, "a@example.com", "User A", "user", "google", -time.Minute)
	require.NoError(t, err)
	_, err = teamsauth.ValidateBearerToken(expiredJWT, tokenTestHMACKey)
	assert.Error(t, err, "a JWT past its own TTL must be refused however alive the session once was")

	rawExpiredToken, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, rig.store.CreateAgentToken(auth.AgentToken{
		Name:        "past-bound-token",
		UserID:      tokenUserA,
		Permissions: []string{auth.PermRead},
		ExpiresAt:   time.Now().UTC().Add(-time.Minute),
	}, rawExpiredToken, tokenTestHMACKey))
	_, err = rig.store.ValidateAgentToken(rawExpiredToken, tokenTestHMACKey)
	assert.Error(t, err, "a token past its own expiry must be refused however alive the session once was")

	// The bound is exactly session_ttl + max(jwt_ttl, token_ttl): neither
	// credential's own window was extended by the session's, and neither was
	// cut short by the session's death.
	_ = jwtTTL
}

// IsSessionPrincipal is the predicate T083 (PR-C phase C.3) consumes to
// restrict the tenant session allowlist; pinned here because T076 declares
// it in this same phase and this file is its first consumer.
func TestIsSessionPrincipal(t *testing.T) {
	cookie := withCredentialKind(auth.UserContext(testUserID, "e@example.com", "E", "google"), auth.CredentialKindCookie)
	assert.True(t, cookie.IsSessionPrincipal())

	bearer := withCredentialKind(auth.UserContext(testUserID, "e@example.com", "E", "google"), auth.CredentialKindBearerJWT)
	assert.True(t, bearer.IsSessionPrincipal())

	apiKey := withCredentialKind(auth.AdminContext(), auth.CredentialKindAPIKey)
	assert.False(t, apiKey.IsSessionPrincipal())

	agentTok := agentTokenCtxFor(testUserID)
	assert.False(t, agentTok.IsSessionPrincipal())
}

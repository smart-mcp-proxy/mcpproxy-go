//go:build server

package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/broker"
)

// idpSubjectTokenType is the credential Type recorded for persisted IdP subject
// tokens. It mirrors the value documented on broker.UpstreamCredential.
const idpSubjectTokenType = "idp_subject_token"

// idpRefreshSkew is how far ahead of expiry a stored IdP subject token is
// considered "near-expiry" and proactively refreshed, so a token never goes
// stale mid-use (FR-005).
const idpRefreshSkew = 60 * time.Second

// ErrReauthRequired signals that no usable IdP subject token can be produced for
// the user: none is stored, the store is disabled, or the token expired and
// cannot be refreshed. Callers MUST re-authenticate the user rather than fall
// back to a stale token (FR-005).
var ErrReauthRequired = errors.New("idp subject token requires re-authentication")

// persistIDPSubjectToken stores the freshly-obtained provider token for userID
// when capture is enabled. It is best-effort and never returns an error: a
// disabled flag, disabled/absent store, or write failure leaves login behaving
// exactly as before (FR-004/FR-006).
func (h *OAuthHandler) persistIDPSubjectToken(userID string, tokenResp *TokenResponse) {
	if h.config == nil || !h.config.StoreIDPTokens {
		return // default-off: behave exactly as today
	}
	if tokenResp == nil {
		return
	}
	if h.credStore == nil || !h.credStore.Enabled() {
		h.logger.Warnw("teams.store_idp_tokens is enabled but the credential store is disabled; "+
			"IdP subject token not persisted (set MCPPROXY_CRED_KEY or teams.credential_encryption_key)",
			"user_id", userID)
		return
	}

	cred := &broker.UpstreamCredential{
		Type:         idpSubjectTokenType,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresAt:    expiryFromExpiresIn(tokenResp.ExpiresIn),
		Scopes:       splitScopes(tokenResp.Scope),
		ObtainedVia:  "login",
		UpdatedAt:    time.Now(),
	}
	if err := h.credStore.Put(userID, "", cred); err != nil {
		h.logger.Errorw("failed to persist IdP subject token", "user_id", userID, "error", err)
		return
	}
	h.logger.Debugw("persisted IdP subject token",
		"user_id", userID, "has_refresh", tokenResp.RefreshToken != "")
}

// GetValidIDPSubjectToken returns a non-expired IdP subject token for the user,
// refreshing it via the provider's refresh_token grant when it is expired or
// near-expiry. It never returns a stale token: when no valid token can be
// produced (none stored, store disabled, expired-and-not-refreshable, or refresh
// failed) it returns ErrReauthRequired (FR-005). This is the prerequisite seam
// consumed by the credential resolver (Path A token exchange).
func (h *OAuthHandler) GetValidIDPSubjectToken(ctx context.Context, userID string) (*broker.UpstreamCredential, error) {
	if h.credStore == nil || !h.credStore.Enabled() {
		return nil, ErrReauthRequired
	}

	cred, err := h.credStore.Get(userID, "")
	if err != nil {
		// Absent or undecryptable -> require re-auth, never a stale token.
		return nil, ErrReauthRequired
	}

	// Fast path: valid and not within the refresh skew window.
	if cred.IsValid() && !cred.ExpiresWithin(idpRefreshSkew) {
		return cred, nil
	}

	// Needs refresh. Without a refresh token, re-auth is the only safe option.
	if cred.RefreshToken == "" {
		return nil, ErrReauthRequired
	}

	if h.config == nil || h.config.OAuth == nil {
		return nil, ErrReauthRequired
	}
	provider, err := GetProvider(h.config.OAuth.Provider, h.config.OAuth.TenantID)
	if err != nil {
		h.logger.Warnw("cannot refresh IdP subject token: provider lookup failed",
			"user_id", userID, "error", err)
		return nil, ErrReauthRequired
	}

	tokenResp, err := provider.RefreshAccessToken(ctx, cred.RefreshToken,
		h.config.OAuth.ClientID, h.config.OAuth.ClientSecret)
	if err != nil {
		h.logger.Warnw("IdP subject token refresh failed; re-auth required",
			"user_id", userID, "error", err)
		return nil, ErrReauthRequired
	}

	refreshed := &broker.UpstreamCredential{
		Type:         idpSubjectTokenType,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: firstNonEmpty(tokenResp.RefreshToken, cred.RefreshToken),
		TokenType:    firstNonEmpty(tokenResp.TokenType, cred.TokenType),
		ExpiresAt:    expiryFromExpiresIn(tokenResp.ExpiresIn),
		Scopes:       chooseScopes(tokenResp.Scope, cred.Scopes),
		ObtainedVia:  "token_refresh",
		UpdatedAt:    time.Now(),
	}

	// Re-persist the refreshed credential. A write failure is non-fatal: the
	// in-hand token is still valid for this call.
	if err := h.credStore.Put(userID, "", refreshed); err != nil {
		h.logger.Warnw("failed to persist refreshed IdP subject token",
			"user_id", userID, "error", err)
	}
	return refreshed, nil
}

// expiryFromExpiresIn converts an OAuth expires_in (seconds) into an absolute
// expiry. A non-positive value yields the zero time, matching the
// never-expiring convention used by UpstreamCredential.
func expiryFromExpiresIn(expiresIn int) time.Time {
	if expiresIn <= 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(expiresIn) * time.Second)
}

// splitScopes splits a space-delimited OAuth scope string into a slice. It
// returns nil for an empty input so the field is omitted when serialized.
func splitScopes(scope string) []string {
	fields := strings.Fields(scope)
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// chooseScopes prefers freshly-returned scopes, falling back to the previously
// stored scopes when the refresh response omits them.
func chooseScopes(scope string, prev []string) []string {
	if s := splitScopes(scope); len(s) > 0 {
		return s
	}
	return prev
}

// firstNonEmpty returns a if non-empty, otherwise b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

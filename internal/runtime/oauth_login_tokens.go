package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/clientidentity"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// --- refresh ----------------------------------------------------------------------

// Refresh is the refresh-token grant. Every refusal is the one indistinguishable
// invalid_grant, and none of them writes anything or extends a lifetime, except
// an authentic refresh token that is not the current one: that revokes the
// login (reason replay) and writes a replay_revoke row.
//
// Check order (FR-009, FR-032): origin, then parse and MAC, then the grant,
// then client and resource, then storage.RotateOAuthRefresh.
func (s *OAuthLoginService) Refresh(in TokenInput) (TokenResult, *TokenError) {
	if !s.Enabled() {
		return TokenResult{}, errInvalidGrant
	}
	key, err := s.hmacKey()
	if err != nil {
		s.logger.Error("browser login: cannot read the token key", zap.Error(err))
		return TokenResult{}, errTokenServer
	}
	grantID, secret, ok := mcplogin.ParseRefreshToken(key, in.RefreshToken)
	if !ok {
		return TokenResult{}, errInvalidGrant
	}
	grant, err := s.store.GetOAuthGrant(grantID)
	if err != nil || grant == nil {
		if err != nil && !errors.Is(err, storage.ErrOAuthGrantNotFound) {
			s.logger.Warn("browser login: cannot read a grant", zap.Error(err))
		}
		return TokenResult{}, errInvalidGrant
	}
	if grant.Issuer != in.Origin || grant.RegistrationID != in.ClientID {
		return TokenResult{}, errInvalidGrant
	}
	// A dead grant answers the uniform invalid_grant BEFORE the resource check,
	// so it never reveals its resource. (The rotation re-checks liveness inside
	// its own transaction.)
	if grant.Revoked || !grant.RefreshExpiresAt.After(s.now()) {
		return TokenResult{}, errInvalidGrant
	}
	if in.Resource != "" && canonicalResource(in.Origin, in.Resource) != grant.Resource {
		return TokenResult{}, errInvalidTarget
	}
	ts, err := newTokenSet(key, grantID)
	if err != nil {
		s.logger.Error("browser login: cannot mint tokens", zap.Error(err))
		return TokenResult{}, errTokenServer
	}
	outcome, after, rerr := s.rotateFenced(grantID, mcplogin.HashSecret(key, mcplogin.KindRefresh, secret), ts)
	if rerr != nil {
		return TokenResult{}, rerr
	}
	switch outcome {
	case storage.RefreshRotated:
		return ts.result(), nil
	case storage.RefreshReplay:
		s.writeGrantRow(profile.ChangeReplayRevoke, after, mcplogin.Decision{}, "refresh_replay")
		s.publishGrantChanged(after, profile.ChangeReplayRevoke, "")
		s.notifyGrantRevoked(after)
	}
	return TokenResult{}, errInvalidGrant
}

// revokeReplayedCode revokes the login an authorization code produced when the
// code is presented again inside the tombstone window. The revoke and its
// activity row happen now (under the caller's locks); the event and the
// notifier callback are returned to run after they are released.
func (s *OAuthLoginService) revokeReplayedCode(grantID string) []func() {
	if grantID == "" {
		return nil
	}
	now := s.now().UTC()
	changed, err := s.store.RevokeOAuthGrant(grantID, storage.OAuthRevokeReplay, now)
	if err != nil {
		if !errors.Is(err, storage.ErrOAuthGrantNotFound) {
			s.logger.Error("browser login: cannot revoke a replayed login", zap.Error(err))
		}
		return nil
	}
	if !changed {
		return nil
	}
	g, err := s.store.GetOAuthGrant(grantID)
	if err != nil || g == nil {
		return nil
	}
	s.writeGrantRow(profile.ChangeReplayRevoke, *g, mcplogin.Decision{}, "code_replay")
	return []func(){func() {
		s.publishGrantChanged(*g, profile.ChangeReplayRevoke, "")
		s.notifyGrantRevoked(*g)
	}}
}

// --- authentication ------------------------------------------------------------------

// ErrOAuthUnauthorized is the one answer for every failed access-token check.
var ErrOAuthUnauthorized = errors.New("oauth access token not accepted")

// Authenticate resolves an mcp_oat_ access token presented at origin+path. The
// grant must be live, its resource must be exactly this origin and path, and
// (on a per-client address) its client must be the path's client. The binding
// is read through storage.CurrentClientBinding, so the API-key record wins. The
// context comes from auth.ClientAuthContext, the builder the mcp_cli_ path
// uses. Every failure is the same ErrOAuthUnauthorized.
func (s *OAuthLoginService) Authenticate(accessToken, origin, path string, now time.Time) (*auth.AuthContext, *storage.OAuthGrant, error) {
	if !s.Enabled() || !mcplogin.IsAccessToken(accessToken) {
		return nil, nil, ErrOAuthUnauthorized
	}
	clientID, _, ok := mcplogin.ParseResourcePath(path)
	if !ok {
		return nil, nil, ErrOAuthUnauthorized
	}
	key, err := s.hmacKey()
	if err != nil {
		return nil, nil, ErrOAuthUnauthorized
	}
	grant, err := s.store.AuthenticateOAuthAccess(mcplogin.HashSecret(key, mcplogin.KindAccess, accessToken), now)
	if err != nil {
		if !errors.Is(err, storage.ErrOAuthGrantNotFound) {
			s.logger.Warn("browser login: access-token lookup failed", zap.Error(err))
		}
		return nil, nil, ErrOAuthUnauthorized
	}
	if grant.Issuer != origin || grant.Resource != origin+mcplogin.ResourcePath(clientID) ||
		(clientID != "" && grant.Client != clientID) {
		return nil, nil, ErrOAuthUnauthorized
	}
	pin, mode, found, err := s.store.CurrentClientBinding(grant.Client)
	if err != nil || !found {
		return nil, nil, ErrOAuthUnauthorized
	}
	ac := auth.ClientAuthContext(auth.ClientIdentity{
		ClientID: grant.Client, ProfilePin: pin, ProfileMode: mode,
		CredentialKind: auth.CredentialKindOAuth, GrantID: grant.GrantID,
		TokenPrefix: grant.DisplayPrefix, TokenGeneration: grant.GrantID,
	})
	return ac, grant, nil
}

// GrantLive reports whether the login grantID still authenticates the access
// token that expires at accessExpiresAt: the feature is on, the grant exists
// (a forgotten client's grants are gone), is not revoked, its refresh lifetime
// has not run out and its access token is the one that was authenticated (a
// refresh rotates it). A plain storage read that takes no runtime lock; any
// read error answers false (fail closed).
func (s *OAuthLoginService) GrantLive(grantID string, accessExpiresAt time.Time) bool {
	if !s.Enabled() {
		return false
	}
	g, err := s.store.GetOAuthGrant(grantID)
	if err != nil || g == nil || g.Revoked {
		return false
	}
	now := s.now()
	return g.AccessExpiresAt.Equal(accessExpiresAt) && g.AccessExpiresAt.After(now) && g.RefreshExpiresAt.After(now)
}

// touchInterval bounds how often a grant's last-used stamp is rewritten.
const touchInterval = time.Minute

// Touch stamps the grant's last use and, when non-empty, the name the client
// sent at initialize. It is best effort and off the request path, like the
// agent-token stamp: one write per grant per minute, a new observed name
// always.
func (s *OAuthLoginService) Touch(grantID, observedName string) {
	if grantID == "" {
		return
	}
	observed := clientidentity.SanitizeDisplay(mcplogin.StripFormat(observedName))
	now := s.now()
	s.touchMu.Lock()
	last, seen := s.lastTouch[grantID]
	if seen && observed == "" && now.Sub(last) < touchInterval {
		s.touchMu.Unlock()
		return
	}
	if len(s.lastTouch) > 4096 {
		s.lastTouch = map[string]time.Time{}
	}
	s.lastTouch[grantID] = now
	s.touchMu.Unlock()
	go func() {
		if err := s.store.TouchOAuthGrant(grantID, now, observed); err != nil && !errors.Is(err, storage.ErrOAuthGrantNotFound) {
			s.logger.Debug("browser login: cannot stamp last use", zap.Error(err))
		}
	}()
}

// --- admin: list and revoke ------------------------------------------------------------

// ListGrants returns the client's browser logins, newest approval first.
func (s *OAuthLoginService) ListGrants(clientID string) ([]storage.OAuthGrant, error) {
	return s.store.ListOAuthGrantsForClient(clientID)
}

// ErrOAuthGrantNotFound is RevokeGrant's answer for a grant that is not the
// client's.
var ErrOAuthGrantNotFound = errors.New("browser login not found")

// RevokeGrant revokes one login of a client (reason admin) under
// bindingWriteMu, writes a `revoke` row naming the request's actor, emits
// credentials.changed and tells live sessions so they drop on their next
// request. It reports whether the login changed (an already revoked login is
// not an error).
func (s *OAuthLoginService) RevokeGrant(ctx context.Context, clientID, grantID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revokeGrantLocked(ctx, clientID, grantID)
}

func (s *OAuthLoginService) revokeGrantLocked(ctx context.Context, clientID, grantID string) (bool, error) {
	g, err := s.store.GetOAuthGrant(grantID)
	if err != nil || g == nil || g.Client != clientID {
		if err != nil && !errors.Is(err, storage.ErrOAuthGrantNotFound) {
			return false, err
		}
		return false, ErrOAuthGrantNotFound
	}
	changed, err := s.store.RevokeOAuthGrant(grantID, storage.OAuthRevokeAdmin, s.now().UTC())
	if err != nil {
		if errors.Is(err, storage.ErrOAuthGrantNotFound) {
			return false, ErrOAuthGrantNotFound
		}
		return false, err
	}
	if !changed {
		return false, nil
	}
	actor, _ := RequestActor(ctx)
	if actor.Surface == "" {
		actor.Surface = profile.SurfaceAPI
	}
	pin, _, _, _ := s.store.CurrentClientBinding(clientID)
	writeChangeRecord(ctx, s.activity, s.logger, s.now(), actor, changeRecord{
		change: profile.ChangeRevoke, profile: pin, previousProfile: pin,
		clientID: clientID, tokenName: auth.ClientTokenName(clientID),
		meta: map[string]interface{}{"grant_id": grantID, "credential_kind": CredentialKindBrowserLogin},
	})
	// Terminating live MCP sessions or streams is NOT done here: a revoked login is
	// refused on its NEXT request (storage no longer authenticates it), which the
	// HTTP layer enforces per request.
	s.publishGrantChanged(*g, profile.ChangeRevoke, pin)
	s.notifyGrantRevoked(*g)
	return true, nil
}

// notifyGrantRevoked tells the sessions of the client's name that their
// binding state changed so they re-list. A revoked access token already stops
// authenticating on the next request (storage refuses it).
func (s *OAuthLoginService) notifyGrantRevoked(g storage.OAuthGrant) {
	if s.notifier != nil {
		s.notifier.NotifyBindingChanged(auth.ClientTokenName(g.Client))
	}
}

// --- rows and events ---------------------------------------------------------------------

// CredentialKindBrowserLogin is the credentials vocabulary's name for a grant.
const CredentialKindBrowserLogin = "browser_login"

func (s *OAuthLoginService) publishGrantChanged(g storage.OAuthGrant, change profile.ChangeKind, pin string) {
	if s.publish == nil {
		return
	}
	s.publish(newEvent(EventTypeCredentialsChanged, map[string]any{
		"kind": CredentialKindBrowserLogin, "id": g.GrantID, "client": g.Client,
		"token_name": auth.ClientTokenName(g.Client), "change": string(change), "profile": pin,
	}))
}

// writeGrantRow writes a row about a STORED login on the public token endpoint
// (issue, replay_revoke): it names the administrator who approved the login and
// says the trigger was the client.
func (s *OAuthLoginService) writeGrantRow(change profile.ChangeKind, g storage.OAuthGrant, d mcplogin.Decision, reason string) {
	actor := Actor{Kind: g.ApprovedBy.Kind, Name: g.ApprovedBy.Name, Surface: profile.SurfaceAPI}
	pin, _, _, _ := s.store.CurrentClientBinding(g.Client)
	meta := map[string]interface{}{
		"grant_id": g.GrantID, "trigger": "client", "credential_kind": CredentialKindBrowserLogin,
		"approved_by": map[string]interface{}{"kind": g.ApprovedBy.Kind, "name": g.ApprovedBy.Name},
	}
	if reason != "" {
		meta["reason"] = reason
	}
	if d.ReplaceGrantID != "" {
		meta["replaced_grant"] = d.ReplaceGrantID
	}
	writeChangeRecord(context.Background(), s.activity, s.logger, s.now(), actor, changeRecord{
		change: change, profile: pin, previousProfile: pin,
		clientID: g.Client, tokenName: auth.ClientTokenName(g.Client), meta: meta,
	})
}

// writeDecisionRow writes the `deny` row of an exchange that was refused after
// approval: it names the approving administrator with trigger client.
func (s *OAuthLoginService) writeDecisionRow(change profile.ChangeKind, req mcplogin.Request, reason string, now time.Time) {
	actor := Actor{Kind: req.Decision.ActorKind, Name: req.Decision.ActorName, Surface: profile.SurfaceAPI}
	writeChangeRecord(context.Background(), s.activity, s.logger, now, actor, changeRecord{
		change: change, profile: req.Decision.Profile,
		clientID: req.Decision.ClientID, tokenName: auth.ClientTokenName(req.Decision.ClientID),
		meta: map[string]interface{}{"request_id": req.ID, "trigger": "client", "reason": reason},
	})
}

// --- guard -------------------------------------------------------------------------------

// guardStatesFor builds the current state and the candidate state a login of
// clientID with (pin, mode) would create: the candidate adds or replaces the
// client's own browser-login entry with Live true, before anything is stored.
// A storage read error is returned and the caller must refuse (fail closed).
func (s *OAuthLoginService) guardStatesFor(cfg *config.Config, clientID, pin, mode string) (current, candidate GuardState, err error) {
	all, err := s.store.ListAgentTokens()
	if err != nil {
		return current, candidate, fmt.Errorf("cannot inspect client bindings: %w", err)
	}
	oauth, err := LoadOAuthBindings(s.store, s.now())
	if err != nil {
		return current, candidate, err
	}
	tokens := guardedOnly(all)
	current = GuardState{Config: cfg, Tokens: tokens, OAuthBindings: oauth}
	candidate = GuardState{Config: cfg, Tokens: tokens,
		OAuthBindings: withOAuthClient(oauth, OAuthBinding{ClientID: clientID, Pin: pin, Mode: mode, Live: true})}
	return current, candidate, nil
}

func (s *OAuthLoginService) recheckGuard(cfg *config.Config, d mcplogin.Decision) error {
	cur, cand, err := s.guardStatesFor(cfg, d.ClientID, d.Profile, d.Mode)
	if err != nil {
		return err
	}
	return CheckBindingGuard(s.guard(), cur, cand)
}

// --- name mismatch (research R14) -------------------------------------------------------

// OAuthNameMismatch derives the advisory warning of a login: the name the
// client sent at initialize is neither the name it registered with nor, for a
// registry client, one of its id, display name or ClientInfoNames. An empty
// observed name never mismatches. It takes no part in selection or
// authorization.
func OAuthNameMismatch(observed, reportedName, clientID string) bool {
	observed = mcplogin.StripFormat(observed) // same normalisation as the registered name
	norm := clientidentity.NormalizeRaw(observed)
	if norm == "" {
		return false
	}
	known := []string{reportedName}
	if def := connect.FindClient(clientID); def != nil {
		known = append(known, def.ID, def.Name)
		known = append(known, def.ClientInfoNames...)
	}
	for _, k := range known {
		if k != "" && (norm == clientidentity.NormalizeRaw(k) || clientidentity.AliasCandidate(observed) == clientidentity.NormalizeRaw(k)) {
			return false
		}
	}
	return true
}

// redirectHost is the host part of a stored redirect URI, for listings.
func redirectHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

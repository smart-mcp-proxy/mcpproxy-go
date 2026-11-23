package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 132: the credentials service lists and revokes browser logins next to
// the API keys and tokens it already owns. It never issues one: a login exists
// only through the consent page.

// CredentialStateLapsed is the state of a browser login whose refresh token
// expired.
const CredentialStateLapsed = "lapsed"

// SetOAuthLogin installs the browser-login service used to revoke one login.
func (s *CredentialsService) SetOAuthLogin(o *OAuthLoginService) { s.oauth = o }

// grantView projects a grant to its safe view: no token, no hash.
func (s *CredentialsService) grantView(g *storage.OAuthGrant, st oauthClientsState, binding *storage.OAuthClientBinding) CredentialView {
	now := s.now()
	exp := g.RefreshExpiresAt
	mismatch := OAuthNameMismatch(g.ObservedClientName, st.reported[g.RegistrationID], g.Client)
	v := CredentialView{
		Kind: CredentialKindBrowserLogin, ID: g.GrantID, Client: g.Client, TokenName: auth.ClientTokenName(g.Client),
		State: GrantStateOf(g, now), CreatedAt: g.ApprovedAt, ExpiresAt: &exp, RevokedAt: g.RevokedAt,
		LastUsedAt: g.LastUsedAt, TokenPrefix: g.DisplayPrefix, ReportedName: st.reported[g.RegistrationID],
		RedirectHost: redirectHost(g.RedirectURI), ObservedName: g.ObservedClientName, NameMismatch: &mismatch,
	}
	if binding != nil {
		v.Profile, v.Binding, v.ProfileState = binding.ProfilePin, binding.ProfileMode, ProfileStateOf(binding.ProfilePin, s.cfg())
	} else {
		v.ProfileState = ProfileStateNone
	}
	return v
}

// oauthOnlyCredentialView is the view of a client that has browser logins and
// no API-key record: its state is the best state of its logins.
func (s *CredentialsService) oauthOnlyCredentialView(id string, b storage.OAuthClientBinding, grants []storage.OAuthGrant) CredentialView {
	now := s.now()
	v := CredentialView{
		Kind: CredentialKindClient, ID: id, TokenName: auth.ClientTokenName(id), DisplayName: b.DisplayName,
		Profile: b.ProfilePin, Binding: b.ProfileMode, ProfileState: ProfileStateOf(b.ProfilePin, s.cfg()),
		State: CredentialStateRevoked, CreatedAt: b.UpdatedAt,
	}
	sawExpired := false
	for i := range grants {
		g := &grants[i]
		if v.CreatedAt.IsZero() || g.ApprovedAt.Before(v.CreatedAt) {
			v.CreatedAt = g.ApprovedAt
		}
		if g.LastUsedAt != nil && (v.LastUsedAt == nil || g.LastUsedAt.After(*v.LastUsedAt)) {
			t := *g.LastUsedAt
			v.LastUsedAt = &t
		}
		switch GrantStateOf(g, now) {
		case GrantStateActive:
			v.State = CredentialStateActive
		case GrantStateLapsed:
			sawExpired = true
		}
	}
	if v.State != CredentialStateActive && sawExpired {
		v.State = CredentialStateExpired
	}
	return v
}

// listOAuth adds the browser logins (and the clients that have only logins) to
// a List result. It adds nothing while the feature was never used.
func (s *CredentialsService) listOAuth(f CredentialFilter, out []CredentialView, have map[string]bool) ([]CredentialView, error) {
	if s.clients == nil {
		return out, nil
	}
	st, err := s.clients.loadOAuthState()
	if err != nil {
		return nil, err
	}
	if !st.active {
		return out, nil
	}
	add := func(v CredentialView) {
		if f.Kind != "" && f.Kind != "all" && f.Kind != v.Kind {
			return
		}
		if f.Profile != "" && f.Profile != v.Profile {
			return
		}
		if f.State != "" && f.State != "all" && f.State != v.State {
			return
		}
		out = append(out, v)
	}
	if !f.ExcludeClients {
		for id, b := range st.bindings {
			if !have[id] {
				add(s.oauthOnlyCredentialView(id, b, st.grants[id]))
			}
		}
		for id, gs := range st.grants {
			b := st.bindings[id]
			for i := range gs {
				add(s.grantView(&gs[i], st, &b))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// getOAuthOnlyClient answers Get(client) for a client with browser logins and
// no API-key record; nil when it has none.
func (s *CredentialsService) getOAuthOnlyClient(clientID string) (*CredentialView, error) {
	if s.clients == nil {
		return nil, nil
	}
	st, err := s.clients.loadOAuthState()
	if err != nil {
		return nil, err
	}
	b, ok := st.bindings[clientID]
	if !ok {
		return nil, nil
	}
	v := s.oauthOnlyCredentialView(clientID, b, st.grants[clientID])
	return &v, nil
}

// revokeGrantLocked revokes one browser login (s.mu held): the login service
// writes the `revoke` record and the credentials.changed event itself.
func (s *CredentialsService) revokeGrantLocked(ctx context.Context, clientID, grantID string) (*RevokeResult, error) {
	if s.oauth == nil || s.clients == nil {
		return nil, ErrCredentialsUnavailable
	}
	if !auth.ValidClientID(clientID) {
		return nil, credErr(profile.CredentialErrorCodeInvalidArgument, "client",
			`invalid argument "client": must be lower-case letters, digits, '-' or '_', at most 56 characters`, 400)
	}
	changed, err := s.oauth.revokeGrantLocked(ctx, clientID, grantID)
	if err != nil {
		if errors.Is(err, ErrOAuthGrantNotFound) {
			return nil, credErr(profile.CredentialErrorCodeIdentityNotFound, "grant", "browser login not found", 404)
		}
		return nil, err
	}
	st, err := s.clients.loadOAuthState()
	if err != nil {
		return nil, err
	}
	b := st.bindings[clientID]
	for i := range st.grants[clientID] {
		if g := &st.grants[clientID][i]; g.GrantID == grantID {
			return &RevokeResult{View: s.grantView(g, st, &b), Changed: changed, ClientConfigUntouched: true}, nil
		}
	}
	return nil, credErr(profile.CredentialErrorCodeIdentityNotFound, "grant", "browser login not found", 404)
}

// hasLiveGrant reports whether the client has a browser login that is neither
// revoked nor lapsed.
func (s *CredentialsService) hasLiveGrant(clientID string, now time.Time) (bool, error) {
	o := s.clients.oauthStore()
	if o == nil {
		return false, nil
	}
	gs, err := o.ListOAuthGrants()
	if err != nil {
		return false, fmt.Errorf("cannot read browser logins: %w", err) // fail closed
	}
	for i := range gs {
		if gs[i].Client == clientID && GrantStateOf(&gs[i], now) == GrantStateActive {
			return true, nil
		}
	}
	return false, nil
}

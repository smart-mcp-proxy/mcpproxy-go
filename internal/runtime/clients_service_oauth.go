package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 132: a client holds an API key, browser logins, or both. The binding is
// one per client; this file is where ClientsService learns about the OAuth side
// without changing a byte of what it does for a client that never used it: every
// entry point first asks whether the store has browser-login storage AND whether
// the client (or the install) has any.

// oauthClientStore is the browser-login storage surface ClientsService uses
// beyond ClientCredentialStore; *storage.Manager implements it. A store that
// does not (a test fake) keeps every behaviour exactly as it was.
type oauthClientStore interface {
	OAuthBindingSource
	GetOAuthClientBinding(clientID string) (*storage.OAuthClientBinding, error)
	ListOAuthRegistrations() ([]storage.OAuthRegistration, error)
	OAuthLoginEverUsed() (bool, error)
	ForgetClient(clientID string) (*auth.AgentToken, error)
	RevokeClient(clientID string, now time.Time) (*auth.AgentToken, error)
	RevokeClientAPIKey(clientID string) (*auth.AgentToken, error)
	UpdateOAuthClientBinding(clientID, pin, mode string) (beforePin, beforeMode string, err error)
	RepairOAuthClientBindings() (repaired, skipped []string, err error)
}

func (s *ClientsService) oauthStore() oauthClientStore {
	o, _ := s.store.(oauthClientStore)
	return o
}

// SetLoginCanceler installs the hook that cancels a client's pending and
// approved sign-in requests; it runs under the service's mutex (the same one
// the token exchange commits under) after a revoke or forget.
func (s *ClientsService) SetLoginCanceler(fn func(clientID string)) {
	s.cancelLogins = func(id string) {
		if fn != nil {
			fn(id)
		}
	}
}

func (s *ClientsService) cancelLoginRequests(clientID string) {
	if s.cancelLogins != nil {
		s.cancelLogins(clientID)
	}
}

// hasLoginRecords reports whether the client has browser-login storage.
func (s *ClientsService) hasLoginRecords(o oauthClientStore, clientID string) bool {
	if o == nil {
		return false
	}
	b, err := o.GetOAuthClientBinding(clientID)
	return err == nil && b != nil
}

// loadOAuthBindings reads the OAuth side of a guard state (fail closed: an error
// refuses the change).
func (s *ClientsService) loadOAuthBindings() ([]OAuthBinding, error) {
	return LoadOAuthBindings(s.store, s.now())
}

// oauthBindingsBestEffort is loadOAuthBindings for an advisory view (the
// warnings list): an unreadable store yields no bindings instead of an error,
// because nothing is being refused.
func (s *ClientsService) oauthBindingsBestEffort() []OAuthBinding {
	b, err := s.loadOAuthBindings()
	if err != nil {
		s.logger.Warn("cannot read browser-login bindings for the warnings", zap.Error(err))
		return nil
	}
	return b
}

// repinOAuthClient returns bindings with the client's entry set to (pin, mode).
func repinOAuthClient(bindings []OAuthBinding, clientID, pin, mode string) []OAuthBinding {
	if len(bindings) == 0 {
		return bindings
	}
	out := make([]OAuthBinding, len(bindings))
	copy(out, bindings)
	for i := range out {
		if out[i].ClientID == clientID {
			out[i].Pin, out[i].Mode = pin, mode
		}
	}
	return out
}

// --- views ----------------------------------------------------------------------

// ClientCredentialEntry is one credential of a client in the Clients views
// (contracts/rest-admin.md): the API key or a browser login. A secret, a hash or
// a token never appears here.
type ClientCredentialEntry struct {
	// Kind is api_key or browser_login.
	Kind   string `json:"kind"`
	Prefix string `json:"prefix,omitempty"`
	// GrantID names a browser login.
	GrantID string `json:"grant_id,omitempty"`
	// State is active|expired|revoked for an API key and active|lapsed|revoked
	// for a login.
	State        string     `json:"state"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	ApprovedAt   *time.Time `json:"approved_at,omitempty"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	ReportedName string     `json:"reported_name,omitempty"`
	RedirectHost string     `json:"redirect_host,omitempty"`
	ObservedName string     `json:"observed_name,omitempty"`
	NameMismatch *bool      `json:"name_mismatch,omitempty"`
}

// Browser-login states of a grant.
const (
	GrantStateActive  = "active"
	GrantStateLapsed  = "lapsed"
	GrantStateRevoked = "revoked"
)

// GrantStateOf classifies a grant: revoked over lapsed over active.
func GrantStateOf(g *storage.OAuthGrant, now time.Time) string {
	switch {
	case g.Revoked:
		return GrantStateRevoked
	case !g.RefreshExpiresAt.After(now):
		return GrantStateLapsed
	default:
		return GrantStateActive
	}
}

// oauthClientsState is what the views need from the browser-login storage. It
// is inactive (the zero value) for an install that never used the feature, and
// then every view is exactly what it was before Spec 132.
type oauthClientsState struct {
	active   bool
	disabled bool
	bindings map[string]storage.OAuthClientBinding
	grants   map[string][]storage.OAuthGrant
	reported map[string]string // registration id -> reported name
}

func (s *ClientsService) loadOAuthState() (oauthClientsState, error) {
	o := s.oauthStore()
	if o == nil {
		return oauthClientsState{}, nil
	}
	bindings, err := o.ListOAuthClientBindings()
	if err != nil {
		return oauthClientsState{}, fmt.Errorf("cannot read browser-login bindings: %w", err)
	}
	used := len(bindings) > 0
	if !used {
		if used, err = o.OAuthLoginEverUsed(); err != nil {
			return oauthClientsState{}, fmt.Errorf("cannot read the browser-login marker: %w", err)
		}
	}
	if !used {
		return oauthClientsState{}, nil
	}
	grants, err := o.ListOAuthGrants()
	if err != nil {
		return oauthClientsState{}, fmt.Errorf("cannot read browser logins: %w", err)
	}
	regs, err := o.ListOAuthRegistrations()
	if err != nil {
		return oauthClientsState{}, fmt.Errorf("cannot read browser-login registrations: %w", err)
	}
	st := oauthClientsState{active: true, bindings: bindings, grants: map[string][]storage.OAuthGrant{}, reported: map[string]string{}}
	if c := s.cfg(); c == nil || !c.MCPOAuthEnabled {
		st.disabled = true
	}
	for i := range regs {
		st.reported[regs[i].ClientID] = regs[i].ReportedName
	}
	sort.SliceStable(grants, func(i, j int) bool { return grants[i].ApprovedAt.After(grants[j].ApprovedAt) })
	for i := range grants {
		st.grants[grants[i].Client] = append(st.grants[grants[i].Client], grants[i])
	}
	return st, nil
}

// decorate adds the credentials list and browser_login_disabled to a view. It
// is a no-op while the feature was never used.
func (st oauthClientsState) decorate(v *ClientCredentialView, rec *auth.AgentToken, now time.Time) {
	if !st.active {
		return
	}
	creds := []ClientCredentialEntry{}
	if rec != nil && rec.Kind == auth.KindClient {
		e := ClientCredentialEntry{Kind: "api_key", Prefix: rec.TokenPrefix, State: CredentialStateOf(rec, now), LastUsedAt: rec.LastUsedAt}
		if !rec.ExpiresAt.IsZero() {
			exp := rec.ExpiresAt
			e.ExpiresAt = &exp
		}
		creds = append(creds, e)
	}
	grants := st.grants[v.ID]
	for i := range grants {
		g := &grants[i]
		mismatch := OAuthNameMismatch(g.ObservedClientName, st.reported[g.RegistrationID], g.Client)
		approved := g.ApprovedAt
		exp := g.RefreshExpiresAt
		creds = append(creds, ClientCredentialEntry{
			Kind: CredentialKindBrowserLogin, GrantID: g.GrantID, State: GrantStateOf(g, now),
			Prefix: g.DisplayPrefix, ExpiresAt: &exp, ApprovedAt: &approved, LastUsedAt: g.LastUsedAt,
			ReportedName: st.reported[g.RegistrationID], RedirectHost: redirectHost(g.RedirectURI),
			ObservedName: g.ObservedClientName, NameMismatch: &mismatch,
		})
	}
	v.Credentials = creds
	disabled := st.disabled && len(grants) > 0
	v.BrowserLoginDisabled = &disabled
}

// oauthOnlyView is the view of a client that has browser logins and no API-key
// record.
func (s *ClientsService) oauthOnlyView(id string, b storage.OAuthClientBinding) *ClientCredentialView {
	return &ClientCredentialView{
		ID: id, DisplayName: b.DisplayName, TokenName: auth.ClientTokenName(id),
		Profile: b.ProfilePin, Mode: b.ProfileMode, CredentialState: profile.CredentialStateNone,
	}
}

// --- binding change ---------------------------------------------------------------

// setOAuthBindingLocked is setBindingLocked for a client without an ACTIVE API
// key that has browser-login storage (s.mu held). handled is false when the
// client has none, and the caller answers no_client_credential as before. The
// one binding writer keeps an existing API-key record equal.
func (s *ClientsService) setOAuthBindingLocked(ctx context.Context, a Actor, all []auth.AgentToken, rec *auth.AgentToken, clientID, profileName string, modeArg *string) (view *ClientCredentialView, handled bool, err error) {
	o := s.oauthStore()
	if o == nil {
		return nil, false, nil
	}
	entry, err := o.GetOAuthClientBinding(clientID)
	if err != nil {
		return nil, true, err
	}
	if entry == nil {
		return nil, false, nil
	}
	curPin, curMode := entry.ProfilePin, entry.ProfileMode
	if rec != nil && rec.Kind == auth.KindClient {
		curPin, curMode = rec.ProfilePin, rec.ProfileMode // the record wins
	}
	if profileName != "" && !s.profileExists(s.cfg(), profileName) {
		return nil, true, &ValidationError{Field: "profile", Message: fmt.Sprintf("unknown profile %q", profileName)}
	}
	mode, err := resolveMode(profileName, modeArg, curMode)
	if err != nil {
		return nil, true, err
	}
	state, err := s.loadOAuthState()
	if err != nil {
		return nil, true, err
	}
	if curPin == profileName && curMode == mode {
		v := s.oauthOnlyView(clientID, *entry)
		v.Profile, v.Mode = curPin, curMode
		state.decorate(v, rec, s.now())
		return v, true, nil
	}
	// The guard judges the whole candidate: the API-key record (when there is
	// one, whatever its state) and the browser-login entry both move.
	oauth, err := s.loadOAuthBindings()
	if err != nil {
		return nil, true, err
	}
	cfg := s.cfg()
	tokens := guardedOnly(all)
	candTokens := tokens
	if rec != nil && rec.Kind == auth.KindClient {
		next := *rec
		next.ProfilePin, next.ProfileMode = profileName, mode
		candTokens = candidateTokens(all, clientID, &next)
	}
	if err := CheckBindingGuard(s.guard(),
		GuardState{Config: cfg, Tokens: tokens, OAuthBindings: oauth},
		GuardState{Config: cfg, Tokens: candTokens, OAuthBindings: repinOAuthClient(oauth, clientID, profileName, mode)}); err != nil {
		return nil, true, err
	}
	if _, _, err := o.UpdateOAuthClientBinding(clientID, profileName, mode); err != nil {
		if errors.Is(err, storage.ErrClientCredentialNotFound) {
			return nil, false, nil
		}
		return nil, true, err
	}
	before := &auth.AgentToken{Name: auth.ClientTokenName(clientID), Kind: auth.KindClient, ClientID: clientID, ProfilePin: curPin, ProfileMode: curMode}
	after := &auth.AgentToken{Name: auth.ClientTokenName(clientID), Kind: auth.KindClient, ClientID: clientID, ProfilePin: profileName, ProfileMode: mode}
	change, diff := bindingChange(before, after)
	s.writeChange(ctx, a, changeRecord{
		change: change, profile: profileName, previousProfile: curPin,
		clientID: clientID, tokenName: after.Name, diff: diff,
	})
	s.announce(before, after)
	v := s.oauthOnlyView(clientID, *entry)
	v.Profile, v.Mode = profileName, mode
	state, _ = s.loadOAuthState()
	state.decorate(v, rec, s.now())
	return v, true, nil
}

// oauthOnlyBindingClients returns the ids of clients with a browser-login entry
// but no kind=client API-key record whose binding pin is from, sorted.
func (s *ClientsService) oauthOnlyBindingClients(all []auth.AgentToken, from string) []string {
	o := s.oauthStore()
	if o == nil {
		return nil
	}
	bindings, err := o.ListOAuthClientBindings()
	if err != nil || len(bindings) == 0 {
		return nil
	}
	var ids []string
	for id, b := range bindings {
		if rec := clientRecord(all, id); rec != nil && rec.Kind == auth.KindClient {
			continue // listed through its record
		}
		if b.ProfilePin == from {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// --- forget and revoke --------------------------------------------------------------

// oauthForgetFunc is the store call a forget or revoke makes for a client that
// has browser-login storage: ForgetClient deletes grants and the entry,
// RevokeClient marks the key and every grant revoked and keeps them for
// display. It returns the API-key record, or a revoked stand-in carrying the
// binding for a client that has none (so the surfaces and the audit record read
// the same).
func (s *ClientsService) oauthForgetFunc(o oauthClientStore, change profile.ChangeKind) func(string) (*auth.AgentToken, error) {
	return func(clientID string) (*auth.AgentToken, error) {
		entry, _ := o.GetOAuthClientBinding(clientID)
		var rec *auth.AgentToken
		var err error
		if change == profile.ChangeRevoke {
			rec, err = o.RevokeClient(clientID, s.now().UTC())
		} else {
			rec, err = o.ForgetClient(clientID)
		}
		if err != nil {
			return nil, err
		}
		if rec == nil {
			standIn := &auth.AgentToken{
				Name: auth.ClientTokenName(clientID), Kind: auth.KindClient, ClientID: clientID,
				Revoked: true, ExpiresAt: time.Time{},
			}
			if entry != nil {
				standIn.ProfilePin, standIn.ProfileMode, standIn.DisplayName = entry.ProfilePin, entry.ProfileMode, entry.DisplayName
			}
			rec = standIn
		}
		return rec, nil
	}
}

// RevokeAPIKey revokes the client's API key alone: its browser logins and its
// binding stay (Spec 132). It writes a `revoke` record and emits
// credentials.changed. A client with no active API key is
// *NoClientCredentialError.
func (s *ClientsService) RevokeAPIKey(ctx context.Context, a Actor, clientID string) (*ClientCredentialView, error) {
	if !auth.ValidClientID(clientID) {
		return nil, &ValidationError{Field: "id", Message: fmt.Sprintf("invalid client id %q", clientID)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.records()
	if err != nil {
		return nil, err
	}
	rec := clientRecord(all, clientID)
	if state := s.stateOf(rec); rec == nil || rec.Kind != auth.KindClient || rec.Revoked {
		return nil, &NoClientCredentialError{ClientID: clientID, State: state}
	}
	var revoked *auth.AgentToken
	if o := s.oauthStore(); o != nil {
		revoked, err = o.RevokeClientAPIKey(clientID)
	} else {
		revoked, err = s.store.ForgetClientCredential(clientID)
	}
	if err != nil {
		if errors.Is(err, storage.ErrClientCredentialNotFound) {
			return nil, &NoClientCredentialError{ClientID: clientID, State: profile.CredentialStateNone}
		}
		return nil, err
	}
	s.writeChange(ctx, a, changeRecord{
		change: profile.ChangeRevoke, profile: revoked.ProfilePin, previousProfile: revoked.ProfilePin,
		clientID: clientID, tokenName: revoked.Name,
		diff: map[string]interface{}{"credential_kind": "api_key", "via": viaOf(a), "changed": true},
	})
	if s.publish != nil {
		s.publish(newEvent(EventTypeCredentialsChanged, map[string]any{
			"kind": CredentialKindClient, "id": clientID, "token_name": revoked.Name,
			"change": string(profile.ChangeRevoke), "profile": revoked.ProfilePin,
		}))
	}
	if s.notifier != nil {
		s.notifier.NotifyBindingChanged(revoked.Name)
	}
	v := s.view(revoked)
	if st, lerr := s.loadOAuthState(); lerr == nil {
		st.decorate(v, revoked, s.now())
	}
	return v, nil
}

// --- startup ---------------------------------------------------------------------------

// repairOAuthBindings makes every client that has both an API-key record and a
// browser-login entry agree (the record is the truth) and logs what it did at
// WARN. It runs once at startup, before serving. On a database that never used
// browser login it opens no write transaction.
func (r *Runtime) repairOAuthBindings() {
	sm := r.storageManager
	if sm == nil {
		return
	}
	repaired, skipped, err := sm.RepairOAuthClientBindings()
	if err != nil {
		r.logger.Warn("browser login: cannot check client bindings at startup", zap.Error(err))
		return
	}
	if len(repaired) > 0 {
		r.logger.Warn("browser login: repaired client bindings that differed from the API-key record",
			zap.Strings("clients", repaired))
	}
	if len(skipped) > 0 {
		r.logger.Warn("browser login: left client bindings alone because the API-key binding is invalid",
			zap.Strings("clients", skipped))
	}
}

package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// --- GET /oauth/authorize ----------------------------------------------------------

// AuthorizeInput is the query of GET /oauth/authorize. Origin is the origin of
// this request (mcplogin.Origin).
type AuthorizeInput struct {
	Origin, ClientID, RedirectURI, State, CodeChallenge, Method, Resource, ResponseType string
}

// AuthorizeErrorKind says what the HTTP layer does with a refusal.
type AuthorizeErrorKind int

const (
	// AuthorizeNoRedirect: an HTML error page (400), never a redirect: the
	// client id or redirect URI cannot be trusted.
	AuthorizeNoRedirect AuthorizeErrorKind = iota
	// AuthorizeNotFound: an HTML 404 page, no redirect (the per-client resource
	// names a client that does not exist).
	AuthorizeNotFound
	// AuthorizeRedirect: redirect to the registered redirect URI with error.
	AuthorizeRedirect
)

// AuthorizeError is a refusal of BeginAuthorization.
type AuthorizeError struct {
	Kind        AuthorizeErrorKind
	Code        string // OAuth error code (redirect kind)
	Description string
	// RedirectURI and State are set for AuthorizeRedirect.
	RedirectURI string
	State       string
}

func (e *AuthorizeError) Error() string { return e.Code + ": " + e.Description }

// RedirectURL builds the error redirect from the registered redirect URI,
// keeping its query and appending percent-encoded parameters; state only when
// the client sent one.
func (e *AuthorizeError) RedirectURL() (string, error) {
	params := map[string]string{"error": e.Code, "error_description": e.Description}
	if e.State != "" {
		params["state"] = e.State
	}
	return mcplogin.BuildRedirect(e.RedirectURI, params)
}

func authzNoRedirect(desc string) *AuthorizeError {
	return &AuthorizeError{Kind: AuthorizeNoRedirect, Code: "invalid_request", Description: desc}
}

// BeginAuthorization validates an authorization request in the order of
// contracts/oauth-endpoints.md, stores it (10 minutes), marks the registration
// authorized and publishes oauth.authorization_pending. It returns the request
// id the consent URL carries.
func (s *OAuthLoginService) BeginAuthorization(in AuthorizeInput) (string, *AuthorizeError) {
	if !s.Enabled() {
		return "", &AuthorizeError{Kind: AuthorizeNotFound, Code: "not_found", Description: "not found"}
	}
	// 1. Unknown client_id, or a redirect URI the registration does not hold:
	// no redirect, because neither can be trusted.
	reg, err := s.store.GetOAuthRegistration(in.ClientID)
	if err != nil || reg == nil {
		return "", authzNoRedirect("unknown client_id")
	}
	redirect, ok := mcplogin.MatchRedirect(reg.RedirectURIs, in.RedirectURI)
	if !ok {
		return "", authzNoRedirect("redirect_uri is not registered")
	}
	fail := func(code, desc string) *AuthorizeError {
		return &AuthorizeError{Kind: AuthorizeRedirect, Code: code, Description: desc, RedirectURI: redirect, State: in.State}
	}
	// 2. A per-client resource naming a client that does not exist: 404, no
	// redirect, checked before the general resource rule (FR-015).
	pathClient, generic, resourceOK := mcplogin.ParseResource(in.Origin, in.Resource)
	if resourceOK && !generic && !s.clientExists(pathClient) {
		return "", &AuthorizeError{Kind: AuthorizeNotFound, Code: "not_found", Description: "unknown client"}
	}
	// 3. The rest are redirectable.
	if in.ResponseType != "code" {
		return "", fail("unsupported_response_type", "only response_type=code is supported")
	}
	if in.Method != "S256" || !mcplogin.ValidChallenge(in.CodeChallenge) {
		return "", fail("invalid_request", "a PKCE S256 code_challenge is required")
	}
	if !resourceOK {
		return "", fail("invalid_target", "resource must be an MCPProxy browser-login address on this origin")
	}
	reqs, err := s.requests()
	if err != nil {
		s.logger.Error("browser login: cannot build the request store", zap.Error(err))
		return "", fail("temporarily_unavailable", "try again")
	}
	req, err := reqs.Create(mcplogin.Request{
		RegistrationID: reg.ClientID, RedirectURI: redirect, State: in.State, CodeChallenge: in.CodeChallenge,
		Resource: in.Resource, Issuer: in.Origin, PathClient: pathClient,
	})
	if err != nil {
		if errors.Is(err, mcplogin.ErrTooManyPending) {
			return "", fail("temporarily_unavailable", "too many sign-in requests are waiting")
		}
		s.logger.Error("browser login: cannot store the request", zap.Error(err))
		return "", fail("temporarily_unavailable", "try again")
	}
	if err := s.store.MarkOAuthRegistrationAuthorized(reg.ClientID, s.now()); err != nil {
		s.logger.Warn("browser login: cannot mark the registration authorized", zap.Error(err))
	}
	if s.publish != nil {
		s.publish(newEvent(EventTypeOAuthAuthorizationPending, map[string]any{
			"request_id": req.ID, "client_id": pathClient, "client_label": clientLabel(reg.ReportedName),
		}))
	}
	return req.ID, nil
}

func clientLabel(reported string) string {
	if strings.TrimSpace(reported) == "" {
		return "an application"
	}
	return reported
}

// --- client lookups ------------------------------------------------------------------

// clientExists: a connect-registry id (supported or not), or an existing client
// record (API key in any state) or browser-login binding.
func (s *OAuthLoginService) clientExists(clientID string) bool {
	if connect.FindClient(clientID) != nil {
		return true
	}
	return s.hasClientRecords(clientID)
}

// hasClientRecords: the client has an API-key record or a browser-login entry
// (a registry id alone does not count).
func (s *OAuthLoginService) hasClientRecords(clientID string) bool {
	rec, err := s.store.GetAgentTokenByName(auth.ClientTokenName(clientID))
	if err == nil && rec != nil {
		return true
	}
	b, err := s.store.GetOAuthClientBinding(clientID)
	return err == nil && b != nil
}

// --- views ---------------------------------------------------------------------------

// BindingView is a client's binding and its revision.
type BindingView struct {
	Profile  string `json:"profile"`
	Mode     string `json:"mode"`
	Revision string `json:"revision"`
}

// AuthorizationClientView is the fixed client of a per-client address.
type AuthorizationClientView struct {
	ID          string       `json:"id"`
	DisplayName string       `json:"display_name"`
	Exists      bool         `json:"exists"`
	HasAPIKey   bool         `json:"has_api_key"`
	Logins      int          `json:"logins"`
	Binding     *BindingView `json:"binding,omitempty"`
}

// ExistingClientView is one choice of the generic address.
type ExistingClientView struct {
	ID          string       `json:"id"`
	DisplayName string       `json:"display_name"`
	Binding     *BindingView `json:"binding,omitempty"`
}

// SuggestedClient is the new-client suggestion derived from the reported name.
type SuggestedClient struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// ProfileChoice names a profile the page may offer.
type ProfileChoice struct {
	Name string `json:"name"`
}

// PreviousLogin is a live login of THIS registration.
type PreviousLogin struct {
	GrantID    string    `json:"grant_id"`
	ApprovedAt time.Time `json:"approved_at"`
}

// GuardVerdict is the binding guard's refusal as the page shows it.
type GuardVerdict struct {
	Code     string       `json:"code"`
	Bindings []BindingRef `json:"bindings"`
	Fixes    []GuardFix   `json:"fixes"`
}

// AuthorizationSummary is one row of the pending list.
type AuthorizationSummary struct {
	ID          string    `json:"id"`
	ClientLabel string    `json:"client_label"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// AuthorizationView is GET /oauth/authorizations/{id} (contracts/rest-admin.md).
type AuthorizationView struct {
	ID                 string                   `json:"id"`
	Status             string                   `json:"status"`
	ExpiresAt          time.Time                `json:"expires_at"`
	ReportedName       string                   `json:"reported_name"`
	RedirectURI        string                   `json:"redirect_uri"`
	Resource           string                   `json:"resource"`
	AddressKind        string                   `json:"address_kind"`
	Client             *AuthorizationClientView `json:"client,omitempty"`
	ExistingClients    []ExistingClientView     `json:"existing_clients,omitempty"`
	SuggestedNewClient *SuggestedClient         `json:"suggested_new_client,omitempty"`
	Profiles           []ProfileChoice          `json:"profiles"`
	ProfileRequired    bool                     `json:"profile_required"`
	PreviousLogin      *PreviousLogin           `json:"previous_login,omitempty"`
	Guard              *GuardVerdict            `json:"guard"`
}

// PreviewAccess is the access sentence of a proposed choice.
type PreviewAccess struct {
	Client                     string `json:"client"`
	Profile                    string `json:"profile"`
	Mode                       string `json:"mode"`
	SameAccessAsExistingClient bool   `json:"same_access_as_existing_client"`
}

// PreviewView is POST /oauth/authorizations/{id}/preview.
type PreviewView struct {
	Complete bool           `json:"complete"`
	Access   *PreviewAccess `json:"access,omitempty"`
	Guard    *GuardVerdict  `json:"guard"`
}

// ListPending returns the pending requests, newest first.
func (s *OAuthLoginService) ListPending() []AuthorizationSummary {
	reqs := s.existingRequests()
	out := []AuthorizationSummary{}
	if reqs == nil || !s.Enabled() {
		return out
	}
	for _, r := range reqs.ListPending() {
		label := "an application"
		if reg, err := s.store.GetOAuthRegistration(r.RegistrationID); err == nil && reg != nil {
			label = clientLabel(reg.ReportedName)
		}
		out = append(out, AuthorizationSummary{ID: r.ID, ClientLabel: label, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// pendingRequest returns the request when it is still pending.
func (s *OAuthLoginService) pendingRequest(id string) (mcplogin.Request, *mcplogin.Store, error) {
	reqs := s.existingRequests()
	if reqs == nil || !s.Enabled() {
		return mcplogin.Request{}, nil, ErrOAuthRequestNotFound
	}
	req, ok := reqs.Get(id)
	if !ok || req.Status != mcplogin.StatusPending {
		return mcplogin.Request{}, nil, ErrOAuthRequestNotFound
	}
	return req, reqs, nil
}

// Describe returns the consent page's view of a pending request. The reported
// name is shown and NEVER selects anything.
func (s *OAuthLoginService) Describe(ctx context.Context, requestID string) (*AuthorizationView, error) {
	req, _, err := s.pendingRequest(requestID)
	if err != nil {
		return nil, err
	}
	reg, err := s.store.GetOAuthRegistration(req.RegistrationID)
	if err != nil || reg == nil {
		return nil, ErrOAuthRequestNotFound
	}
	cfg := s.cfg()
	all, err := s.store.ListAgentTokens()
	if err != nil {
		return nil, fmt.Errorf("cannot read client credentials: %w", err)
	}
	grants, err := s.store.ListOAuthGrants()
	if err != nil {
		return nil, fmt.Errorf("cannot read browser logins: %w", err)
	}
	bindings, err := s.store.ListOAuthClientBindings()
	if err != nil {
		return nil, fmt.Errorf("cannot read browser-login bindings: %w", err)
	}
	now := s.now()
	v := &AuthorizationView{
		ID: req.ID, Status: string(req.Status), ExpiresAt: req.ExpiresAt, ReportedName: reg.ReportedName,
		RedirectURI: req.RedirectURI, Resource: req.Resource, Profiles: profileChoices(cfg),
	}
	liveByClient := map[string]int{}
	for i := range grants {
		g := &grants[i]
		if !g.Revoked && g.RefreshExpiresAt.After(now) {
			liveByClient[g.Client]++
			if g.RegistrationID == reg.ClientID && (req.PathClient == "" || g.Client == req.PathClient) &&
				(v.PreviousLogin == nil || g.ApprovedAt.After(v.PreviousLogin.ApprovedAt)) {
				v.PreviousLogin = &PreviousLogin{GrantID: g.GrantID, ApprovedAt: g.ApprovedAt}
			}
		}
	}
	bindingOf := func(id string) (*BindingView, bool) {
		pin, mode, found, err := s.store.CurrentClientBinding(id)
		if err != nil || !found {
			return nil, false
		}
		rev, err := s.store.ClientBindingRevision(id)
		if err != nil {
			return nil, false
		}
		return &BindingView{Profile: pin, Mode: mode, Revision: rev}, true
	}

	if req.PathClient != "" {
		v.AddressKind = "client"
		cv := &AuthorizationClientView{ID: req.PathClient, DisplayName: s.displayNameOf(req.PathClient, all, bindings),
			Logins: liveByClient[req.PathClient]}
		rec := clientRecord(all, req.PathClient)
		cv.HasAPIKey = rec != nil && rec.Kind == auth.KindClient
		_, hasEntry := bindings[req.PathClient]
		cv.Exists = cv.HasAPIKey || hasEntry
		if b, ok := bindingOf(req.PathClient); ok && cv.Exists {
			cv.Binding = b
		}
		v.Client = cv
		v.ProfileRequired = len(v.Profiles) > 0 && cv.Binding == nil
		if cv.Binding != nil {
			if perr := s.checkCandidateGuard(cv.ID, cv.Binding.Profile, cv.Binding.Mode); perr != nil {
				var ge *BindingGuardError
				if errors.As(perr, &ge) {
					v.Guard = verdictOf(ge)
				}
			}
		}
	} else {
		v.AddressKind = "generic"
		ids := map[string]bool{}
		for i := range all {
			if all[i].Kind == auth.KindClient && all[i].UserID == "" {
				ids[all[i].ClientID] = true
			}
		}
		for id := range bindings {
			ids[id] = true
		}
		sorted := make([]string, 0, len(ids))
		for id := range ids {
			sorted = append(sorted, id)
		}
		sort.Strings(sorted)
		v.ExistingClients = []ExistingClientView{}
		for _, id := range sorted {
			ev := ExistingClientView{ID: id, DisplayName: s.displayNameOf(id, all, bindings)}
			if b, ok := bindingOf(id); ok {
				ev.Binding = b
			}
			v.ExistingClients = append(v.ExistingClients, ev)
		}
		v.ProfileRequired = len(v.Profiles) > 0
		v.SuggestedNewClient = s.suggestNewClient(reg.ReportedName, ids)
	}
	return v, nil
}

func profileChoices(cfg *config.Config) []ProfileChoice {
	out := []ProfileChoice{}
	if cfg != nil {
		for i := range cfg.Profiles {
			out = append(out, ProfileChoice{Name: cfg.Profiles[i].Name})
		}
	}
	return out
}

func verdictOf(ge *BindingGuardError) *GuardVerdict {
	return &GuardVerdict{Code: ge.Code(), Bindings: ge.Bindings, Fixes: ge.Fixes}
}

// displayNameOf is the registry name, else the record's display name, else the id.
func (s *OAuthLoginService) displayNameOf(id string, all []auth.AgentToken, bindings map[string]storage.OAuthClientBinding) string {
	if def := connect.FindClient(id); def != nil && def.Name != "" {
		return def.Name
	}
	if rec := clientRecord(all, id); rec != nil && rec.DisplayName != "" {
		return rec.DisplayName
	}
	if b, ok := bindings[id]; ok && b.DisplayName != "" {
		return b.DisplayName
	}
	return id
}

// suggestNewClient derives an id from the reported name: lower-case slug of
// letters, digits, '-' and '_', at most 56 characters, made unique against the
// registry and the existing clients ("-2", "-3", ...). It only SUGGESTS: the
// page shows it in an editable field and the administrator submits what they
// want. The suggested display name is the reported name.
func (s *OAuthLoginService) suggestNewClient(reported string, existing map[string]bool) *SuggestedClient {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(reported) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r == '_' || r == '-':
			if b.Len() > 0 && !dash {
				b.WriteRune('-')
				dash = true
			}
		default:
			if b.Len() > 0 && !dash {
				b.WriteRune('-')
				dash = true
			}
		}
	}
	base := strings.Trim(b.String(), "-_")
	if base == "" {
		base = "client"
	}
	if len(base) > 50 {
		base = strings.Trim(base[:50], "-_")
	}
	taken := func(id string) bool {
		return existing[id] || connect.FindClient(id) != nil || s.hasClientRecords(id)
	}
	id := base
	for n := 2; taken(id); n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	name := strings.TrimSpace(reported)
	if name == "" {
		name = id
	}
	if utf8.RuneCountInString(name) > auth.MaxClientDisplayName {
		name = string([]rune(name)[:auth.MaxClientDisplayName])
	}
	return &SuggestedClient{ID: id, DisplayName: name}
}

// --- choice ------------------------------------------------------------------------------

// NewClientChoice is a client the approval creates.
type NewClientChoice struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
}

// ClientChoice is the client of an approval: an existing id, or a new one.
type ClientChoice struct {
	ID  string           `json:"id,omitempty"`
	New *NewClientChoice `json:"new,omitempty"`
}

// ApprovalChoice is the body of preview and approve. Profile nil means "not
// chosen" (an empty string is the explicit choice All servers).
type ApprovalChoice struct {
	Client          ClientChoice `json:"client"`
	Profile         *string      `json:"profile,omitempty"`
	Mode            string       `json:"mode,omitempty"`
	BindingRevision string       `json:"binding_revision,omitempty"`
	ReplaceGrant    string       `json:"replace_grant,omitempty"`
}

// Typed refusals of Preview, Approve and Deny. The REST layer maps them with
// OAuthErrorStatus.
var (
	ErrOAuthRequestNotFound  = errors.New("authorization request not found")
	ErrOAuthAlreadyDecided   = errors.New("authorization request already decided")
	ErrOAuthBindingChanged   = errors.New("the client's binding changed since it was displayed")
	ErrOAuthClientFixed      = errors.New("this address fixes the client")
	ErrOAuthClientRequired   = errors.New("choose a client")
	ErrOAuthClientUnknown    = errors.New("unknown client")
	ErrOAuthProfileRequired  = errors.New("choose a profile")
	ErrOAuthProfileNotFound  = errors.New("unknown profile")
	ErrOAuthIdentityExists   = errors.New("a client with this id already exists")
	ErrOAuthReservedIdentity = errors.New("this id belongs to a supported client")
)

// OAuthErrorStatus maps a refusal to its wire code and HTTP status
// (contracts/rest-admin.md). ok is false for an error that is not one of them.
func OAuthErrorStatus(err error) (code string, status int, ok bool) {
	var ge *BindingGuardError
	var ve *ValidationError
	switch {
	case errors.Is(err, ErrOAuthRequestNotFound):
		return "request_not_found", http.StatusNotFound, true
	case errors.Is(err, ErrOAuthAlreadyDecided):
		return "already_decided", http.StatusConflict, true
	case errors.Is(err, ErrOAuthBindingChanged):
		return "binding_changed", http.StatusConflict, true
	case errors.As(err, &ge):
		return ge.Code(), http.StatusConflict, true
	case errors.Is(err, ErrOAuthClientFixed):
		return "client_fixed", http.StatusUnprocessableEntity, true
	case errors.Is(err, ErrOAuthClientRequired):
		return "client_required", http.StatusUnprocessableEntity, true
	case errors.Is(err, ErrOAuthClientUnknown):
		return "client_not_found", http.StatusNotFound, true
	case errors.Is(err, ErrOAuthProfileRequired):
		return "profile_required", http.StatusUnprocessableEntity, true
	case errors.Is(err, ErrOAuthProfileNotFound):
		return "profile_not_found", http.StatusNotFound, true
	case errors.Is(err, ErrOAuthIdentityExists):
		return "identity_exists", http.StatusUnprocessableEntity, true
	case errors.Is(err, ErrOAuthReservedIdentity):
		return "reserved_identity", http.StatusUnprocessableEntity, true
	case errors.As(err, &ve):
		return "invalid_" + ve.Field, http.StatusUnprocessableEntity, true
	}
	return "", 0, false
}

// resolvedChoice is a validated choice.
type resolvedChoice struct {
	clientID   string
	newClient  bool
	display    string
	pin, mode  string
	revision   string // expected binding revision at commit
	hasBinding bool
	replace    string
	complete   bool
}

// resolveChoice validates a choice against the request and the stored state.
// An incomplete choice (a required selection is missing) is not an error: it is
// reported as complete=false so a preview can say so. Invalid values are
// errors in both cases.
func (s *OAuthLoginService) resolveChoice(req mcplogin.Request, c ApprovalChoice) (*resolvedChoice, error) {
	cfg := s.cfg()
	rc := &resolvedChoice{complete: true}

	// 1. Which client.
	switch {
	case req.PathClient != "":
		if c.Client.New != nil || (c.Client.ID != "" && c.Client.ID != req.PathClient) {
			return nil, ErrOAuthClientFixed
		}
		rc.clientID = req.PathClient
		if !s.hasClientRecords(rc.clientID) {
			def := connect.FindClient(rc.clientID)
			if def == nil {
				return nil, ErrOAuthClientUnknown // the client vanished since authorize
			}
			rc.newClient, rc.display = true, def.Name
		}
	case c.Client.New != nil:
		if c.Client.ID != "" {
			return nil, &ValidationError{Field: "client", Message: "choose an existing client or a new one, not both"}
		}
		n := c.Client.New
		if !auth.ValidClientID(n.ID) {
			return nil, &ValidationError{Field: "client_new_id", Message: fmt.Sprintf(
				"invalid client id %q: must be lower-case letters, digits, '-' or '_', start with a letter or digit, and be at most 56 characters", n.ID)}
		}
		if connect.FindClient(n.ID) != nil {
			return nil, ErrOAuthReservedIdentity
		}
		if s.hasClientRecords(n.ID) {
			return nil, ErrOAuthIdentityExists
		}
		if utf8.RuneCountInString(n.DisplayName) > auth.MaxClientDisplayName {
			return nil, &ValidationError{Field: "client_new_display_name", Message: "display name is too long"}
		}
		rc.clientID, rc.newClient, rc.display = n.ID, true, n.DisplayName
	case c.Client.ID != "":
		if !s.hasClientRecords(c.Client.ID) {
			return nil, ErrOAuthClientUnknown
		}
		rc.clientID = c.Client.ID
	default:
		rc.complete = false
	}
	if !rc.complete {
		return rc, nil
	}

	// 2. The binding: kept for a client that has one, chosen for a new one.
	if !rc.newClient {
		pin, mode, found, err := s.store.CurrentClientBinding(rc.clientID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrOAuthClientUnknown
		}
		rev, err := s.store.ClientBindingRevision(rc.clientID)
		if err != nil {
			return nil, err
		}
		if c.BindingRevision != rev {
			return nil, ErrOAuthBindingChanged
		}
		rc.pin, rc.mode, rc.revision, rc.hasBinding = pin, mode, rev, true
	} else {
		rc.revision = storage.OAuthBindingAbsent
		if c.Profile == nil {
			if cfg != nil && len(cfg.Profiles) > 0 {
				rc.complete = false // profiles exist: nothing is pre-selected
				return rc, nil
			}
			c.Profile = new(string)
		}
		pin := *c.Profile
		if pin != "" && !profileConfigured(cfg, pin) {
			return nil, ErrOAuthProfileNotFound
		}
		var modeArg *string
		if c.Mode != "" {
			m := c.Mode
			modeArg = &m
		}
		mode, err := resolveMode(pin, modeArg, "")
		if err != nil {
			return nil, err
		}
		rc.pin, rc.mode = pin, mode
	}

	// 3. Replace the previous login: only a live login of THIS registration
	// and of this client; anything else is ignored, never revoked.
	if c.ReplaceGrant != "" {
		if g, err := s.store.GetOAuthGrant(c.ReplaceGrant); err == nil && g != nil && g.Client == rc.clientID &&
			!g.Revoked && g.RefreshExpiresAt.After(s.now()) && g.RegistrationID == req.RegistrationID {
			rc.replace = g.GrantID
		}
	}
	return rc, nil
}

func profileConfigured(cfg *config.Config, name string) bool {
	if cfg == nil {
		return false
	}
	for i := range cfg.Profiles {
		if cfg.Profiles[i].Name == name {
			return true
		}
	}
	return false
}

// checkCandidateGuard runs the binding guard over the state this client's login
// would create (its entry added or replaced with Live true).
func (s *OAuthLoginService) checkCandidateGuard(clientID, pin, mode string) error {
	cur, cand, err := s.guardStatesFor(clientID, pin, mode)
	if err != nil {
		return err
	}
	return CheckBindingGuard(s.guard(), cur, cand)
}

// Preview is the read-only verdict of a proposed choice. It writes nothing.
// A guard refusal is part of the answer, not an error.
func (s *OAuthLoginService) Preview(ctx context.Context, requestID string, c ApprovalChoice) (*PreviewView, error) {
	req, _, err := s.pendingRequest(requestID)
	if err != nil {
		return nil, err
	}
	rc, err := s.resolveChoice(req, c)
	if err != nil {
		return nil, err
	}
	out := &PreviewView{Complete: rc.complete}
	if !rc.complete {
		return out, nil
	}
	out.Access = &PreviewAccess{Client: rc.clientID, Profile: rc.pin, Mode: rc.mode, SameAccessAsExistingClient: rc.hasBinding}
	if gerr := s.checkCandidateGuard(rc.clientID, rc.pin, rc.mode); gerr != nil {
		var ge *BindingGuardError
		if errors.As(gerr, &ge) {
			out.Guard = verdictOf(ge)
			return out, nil
		}
		return nil, gerr
	}
	return out, nil
}

// --- approve and deny ----------------------------------------------------------------------

// Approve records the administrator's decision on the in-memory request and
// returns the redirect carrying the single-use code. It stores NO registration,
// login, binding or marker: the login is stored by the token exchange. It
// writes the `approve` activity row (the one thing it persists), naming the
// request's actor with the request id, and publishes
// oauth.authorization_decided. It runs under bindingWriteMu, so no binding
// write interleaves between the revision check and the decision.
func (s *OAuthLoginService) Approve(ctx context.Context, requestID string, c ApprovalChoice) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, reqs, err := s.pendingRequestForDecision(requestID)
	if err != nil {
		return "", err
	}
	rc, err := s.resolveChoice(req, c)
	if err != nil {
		return "", err
	}
	if !rc.complete {
		if rc.clientID == "" {
			return "", ErrOAuthClientRequired
		}
		return "", ErrOAuthProfileRequired
	}
	if gerr := s.checkCandidateGuard(rc.clientID, rc.pin, rc.mode); gerr != nil {
		return "", gerr
	}
	actor, _ := RequestActor(ctx)
	if actor.Surface == "" {
		actor.Surface = profile.SurfaceAPI
	}
	code, err := reqs.Approve(requestID, mcplogin.Decision{
		ClientID: rc.clientID, NewClient: rc.newClient, DisplayName: rc.display, Profile: rc.pin, Mode: rc.mode,
		BindingRevision: rc.revision, ReplaceGrantID: rc.replace, ActorKind: actor.Kind, ActorName: actor.Name,
	})
	if err != nil {
		return "", mapRequestStoreError(err)
	}
	writeChangeRecord(ctx, s.activity, s.logger, s.now(), actor, changeRecord{
		change: profile.ChangeApprove, profile: rc.pin, clientID: rc.clientID, tokenName: auth.ClientTokenName(rc.clientID),
		meta: map[string]interface{}{"request_id": requestID},
	})
	s.publishDecided(requestID, string(mcplogin.StatusApproved))
	params := map[string]string{"code": code}
	if req.State != "" {
		params["state"] = req.State
	}
	return mcplogin.BuildRedirect(req.RedirectURI, params)
}

// Deny refuses the request: the redirect carries error=access_denied.
func (s *OAuthLoginService) Deny(ctx context.Context, requestID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, reqs, err := s.pendingRequestForDecision(requestID)
	if err != nil {
		return "", err
	}
	if err := reqs.Deny(requestID); err != nil {
		return "", mapRequestStoreError(err)
	}
	actor, _ := RequestActor(ctx)
	if actor.Surface == "" {
		actor.Surface = profile.SurfaceAPI
	}
	writeChangeRecord(ctx, s.activity, s.logger, s.now(), actor, changeRecord{
		change: profile.ChangeDeny, clientID: req.PathClient, tokenName: authTokenNameOrEmpty(req.PathClient),
		meta: map[string]interface{}{"request_id": requestID},
	})
	s.publishDecided(requestID, string(mcplogin.StatusDenied))
	params := map[string]string{"error": "access_denied", "error_description": "the administrator denied the request"}
	if req.State != "" {
		params["state"] = req.State
	}
	return mcplogin.BuildRedirect(req.RedirectURI, params)
}

func authTokenNameOrEmpty(clientID string) string {
	if clientID == "" {
		return ""
	}
	return auth.ClientTokenName(clientID)
}

// pendingRequestForDecision is pendingRequest, but a request that was decided
// already answers ErrOAuthAlreadyDecided instead of not-found.
func (s *OAuthLoginService) pendingRequestForDecision(id string) (mcplogin.Request, *mcplogin.Store, error) {
	reqs := s.existingRequests()
	if reqs == nil || !s.Enabled() {
		return mcplogin.Request{}, nil, ErrOAuthRequestNotFound
	}
	req, ok := reqs.Get(id)
	if !ok {
		return mcplogin.Request{}, nil, ErrOAuthRequestNotFound
	}
	switch req.Status {
	case mcplogin.StatusPending:
		return req, reqs, nil
	case mcplogin.StatusCancelled:
		return mcplogin.Request{}, nil, ErrOAuthRequestNotFound
	default:
		return mcplogin.Request{}, nil, ErrOAuthAlreadyDecided
	}
}

func mapRequestStoreError(err error) error {
	switch {
	case errors.Is(err, mcplogin.ErrAlreadyDecided):
		return ErrOAuthAlreadyDecided
	case errors.Is(err, mcplogin.ErrRequestNotFound):
		return ErrOAuthRequestNotFound
	}
	return err
}

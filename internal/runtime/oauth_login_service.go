package runtime

import (
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/mcplogin"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// OAuthLoginService is the runtime half of the browser login for MCP clients
// (Spec 132): registration, the authorization request and its consent
// decision, the token endpoint's exchange and refresh, access-token
// authentication, and the admin revoke of one login.
//
// LOCK ORDER (one rule, no cycles). Every path that pairs a storage write with
// the in-memory request store takes the runtime's bindingWriteMu FIRST and the
// request store's own mutex second:
//
//		bindingWriteMu  ->  mcplogin.Store.mu  ->  storage.Manager.mu
//
//	  - Exchange takes bindingWriteMu BEFORE calling Store.Consume, and its commit
//	    callback (which Consume runs under Store.mu) does NOT take bindingWriteMu
//	    again. That is why the callback may do the guard check and the storage
//	    commit: the lock above it is already held.
//	  - Revoke-client, forget-client and RevokeGrant hold bindingWriteMu (their
//	    own services take it) and then call Store.CancelForClient, the same order.
//	  - Approve holds bindingWriteMu while it validates the choice and calls
//	    Store.Approve, the same order.
//	  - The turn-off cancellation (CancelAll from the config commit) takes Store.mu
//	    alone. It needs no bindingWriteMu: it is one in-memory step, atomic with
//	    Consume through Store.mu, and may run while the caller already holds
//	    bindingWriteMu (MutateConfig) or does not (file reload).
//	  - Nothing holding Store.mu ever acquires bindingWriteMu (the commit callback
//	    never does), so the two cannot be taken in opposite orders.
type OAuthLoginService struct {
	store   OAuthLoginStore
	hmacKey func() ([]byte, error)
	cfg     func() *config.Config
	guard   func() BindingGuard
	// activity writes a profile_change row; publish emits an SSE event.
	activity func(*storage.ActivityRecord) error
	publish  func(Event)
	now      func() time.Time
	mu       *sync.Mutex
	logger   *zap.Logger

	notifier BindingNotifier

	// reqs is built on first use, because building it needs the token key and
	// creating that key writes a file: a default-off install must not.
	reqMu sync.Mutex
	reqs  *mcplogin.Store

	everUsed  bool // positive results only
	everMu    sync.Mutex
	touchMu   sync.Mutex
	lastTouch map[string]time.Time
}

// OAuthLoginStore is the storage surface the service needs; *storage.Manager
// implements it.
type OAuthLoginStore interface {
	ListAgentTokens() ([]auth.AgentToken, error)
	GetAgentTokenByName(name string) (*auth.AgentToken, error)
	CreateOAuthRegistration(reg storage.OAuthRegistration) error
	GetOAuthRegistration(id string) (*storage.OAuthRegistration, error)
	MarkOAuthRegistrationAuthorized(id string, at time.Time) error
	CommitOAuthLogin(in storage.CommitLoginInput) (storage.OAuthGrant, error)
	GetOAuthGrant(id string) (*storage.OAuthGrant, error)
	ListOAuthGrants() ([]storage.OAuthGrant, error)
	ListOAuthGrantsForClient(clientID string) ([]storage.OAuthGrant, error)
	AuthenticateOAuthAccess(accessHash string, now time.Time) (*storage.OAuthGrant, error)
	RotateOAuthRefresh(grantID, presentedSecretHash, newRefreshHash, newRefreshPrefix, newAccessHash string, now time.Time, accessTTL, refreshTTL time.Duration) (storage.RefreshOutcome, storage.OAuthGrant, error)
	RevokeOAuthGrant(id, reason string, now time.Time) (bool, error)
	TouchOAuthGrant(id string, lastUsedAt time.Time, observedName string) error
	GetOAuthClientBinding(clientID string) (*storage.OAuthClientBinding, error)
	ListOAuthClientBindings() (map[string]storage.OAuthClientBinding, error)
	CurrentClientBinding(clientID string) (pin, mode string, found bool, err error)
	ClientBindingRevision(clientID string) (string, error)
	OAuthLoginEverUsed() (bool, error)
}

// Token lifetimes of a browser login (research R6).
const (
	OAuthAccessTokenTTL  = time.Hour
	OAuthRefreshTokenTTL = 30 * 24 * time.Hour
)

// OAuthLoginDeps wires an OAuthLoginService. Zero values are safe in tests:
// Now defaults to time.Now, Mu to a private mutex, Guard to the conservative
// evaluator, Logger to a no-op, Requests to a store built on first use.
type OAuthLoginDeps struct {
	Store    OAuthLoginStore
	HMACKey  func() ([]byte, error)
	Config   func() *config.Config
	Guard    func() BindingGuard
	Activity func(*storage.ActivityRecord) error
	Publish  func(Event)
	Now      func() time.Time
	Mu       *sync.Mutex
	Logger   *zap.Logger
	// Requests is the in-memory request and code store (tests inject one with
	// a fake clock).
	Requests *mcplogin.Store
}

// NewOAuthLoginService builds the service.
func NewOAuthLoginService(d OAuthLoginDeps) *OAuthLoginService {
	s := &OAuthLoginService{
		store: d.Store, hmacKey: d.HMACKey, cfg: d.Config, guard: d.Guard, activity: d.Activity,
		publish: d.Publish, now: d.Now, mu: d.Mu, logger: d.Logger, reqs: d.Requests,
		lastTouch: map[string]time.Time{},
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.mu == nil {
		s.mu = &sync.Mutex{}
	}
	if s.guard == nil {
		s.guard = func() BindingGuard { return ConservativeBindingGuard{} }
	}
	if s.logger == nil {
		s.logger = zap.NewNop()
	}
	if s.cfg == nil {
		s.cfg = func() *config.Config { return nil }
	}
	return s
}

// OAuthLoginService returns the runtime's browser-login service (Spec 132).
func (r *Runtime) OAuthLoginService() *OAuthLoginService { return r.oauthLoginService }

// SetNotifier installs the live-session notifier (the server).
func (s *OAuthLoginService) SetNotifier(n BindingNotifier) { s.notifier = n }

// Enabled reports whether the feature is on in the live configuration.
func (s *OAuthLoginService) Enabled() bool {
	c := s.cfg()
	return c != nil && c.MCPOAuthEnabled
}

// EverUsed reports whether a browser login was ever stored. Only a positive
// answer is cached: the marker is never cleared, a negative one can change.
func (s *OAuthLoginService) EverUsed() bool {
	s.everMu.Lock()
	defer s.everMu.Unlock()
	if s.everUsed {
		return true
	}
	used, err := s.store.OAuthLoginEverUsed()
	if err != nil {
		s.logger.Warn("cannot read the browser-login marker", zap.Error(err))
		return false
	}
	s.everUsed = used
	return used
}

// requests returns the request store, building it on first use.
func (s *OAuthLoginService) requests() (*mcplogin.Store, error) {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	if s.reqs != nil {
		return s.reqs, nil
	}
	key, err := s.hmacKey()
	if err != nil {
		return nil, err
	}
	s.reqs = mcplogin.NewStore(key, s.now)
	return s.reqs, nil
}

// existingRequests returns the request store when one was ever built.
func (s *OAuthLoginService) existingRequests() *mcplogin.Store {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	return s.reqs
}

// --- cancellation ------------------------------------------------------------

// CancelAll cancels every pending and approved-but-unconsumed request (the
// feature is being turned off). It takes the request store's mutex only (see
// the lock-order note): it may run under bindingWriteMu or without it.
func (s *OAuthLoginService) CancelAll(reason string) int {
	reqs := s.existingRequests()
	if reqs == nil {
		return 0
	}
	pending := reqs.ListPending()
	n := reqs.CancelAll()
	for _, p := range pending {
		s.publishDecided(p.ID, string(mcplogin.StatusCancelled))
	}
	if n > 0 {
		s.logger.Info("cancelled browser sign-in requests", zap.Int("count", n), zap.String("reason", reason))
	}
	return n
}

// CancelForClient cancels the pending and approved requests that target the
// client, by address or by decision. Callers hold bindingWriteMu (revoke and
// forget run under it), so an exchange either completed before them or finds
// its request cancelled.
func (s *OAuthLoginService) CancelForClient(clientID string) int {
	reqs := s.existingRequests()
	if reqs == nil {
		return 0
	}
	var ids []string
	for _, p := range reqs.ListPending() {
		if p.PathClient == clientID {
			ids = append(ids, p.ID)
		}
	}
	n := reqs.CancelForClient(clientID)
	for _, id := range ids {
		s.publishDecided(id, string(mcplogin.StatusCancelled))
	}
	return n
}

func (s *OAuthLoginService) publishDecided(requestID, status string) {
	if s.publish != nil {
		s.publish(newEvent(EventTypeOAuthAuthorizationDecided, map[string]any{"request_id": requestID, "status": status}))
	}
}

// --- registration --------------------------------------------------------------

// Register validates an RFC 7591 request and stores a pending registration.
// The first return is the 201 body; the second a refusal shaped for the
// client; the third an internal failure.
func (s *OAuthLoginService) Register(req mcplogin.RegistrationRequest) (map[string]any, *mcplogin.RegError, error) {
	reg, regErr := mcplogin.ValidateRegistration(req)
	if regErr != nil {
		return nil, regErr, nil
	}
	id, err := mcplogin.NewClientID()
	if err != nil {
		return nil, nil, err
	}
	reg.ClientID = id
	now := s.now().UTC()
	if err := s.store.CreateOAuthRegistration(storage.OAuthRegistration{
		ClientID: id, ReportedName: reg.ClientName, RedirectURIs: reg.RedirectURIs,
		SoftwareID: reg.SoftwareID, SoftwareVersion: reg.SoftwareVersion,
		TokenEndpointAuthMethod: reg.TokenEndpointAuthMethod, GrantTypes: reg.GrantTypes, CreatedAt: now,
	}); err != nil {
		return nil, nil, err
	}
	return reg.Response(now), nil, nil
}

// --- tokens ----------------------------------------------------------------------

// TokenInput is a token request, after the HTTP layer parsed the form. Origin
// is the origin of THIS request (mcplogin.Origin).
type TokenInput struct {
	Origin       string
	Code         string
	RedirectURI  string
	ClientID     string // the registration's client_id
	CodeVerifier string
	RefreshToken string
	Resource     string
}

// TokenResult is a successful token answer.
type TokenResult struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// Token error codes (contracts/oauth-endpoints.md).
const (
	TokenErrInvalidGrant  = "invalid_grant"
	TokenErrInvalidTarget = "invalid_target"
	TokenErrServer        = "server_error"
)

// TokenError is a refusal of the token endpoint: always 400 except
// server_error. Descriptions are deliberately uniform: they never tell which
// check failed.
type TokenError struct {
	Code        string
	Description string
}

func (e *TokenError) Error() string { return e.Code + ": " + e.Description }

var (
	errInvalidGrant  = &TokenError{Code: TokenErrInvalidGrant, Description: "the grant is invalid, expired or revoked"}
	errInvalidTarget = &TokenError{Code: TokenErrInvalidTarget, Description: "the resource does not match the approved one"}
	errTokenServer   = &TokenError{Code: TokenErrServer, Description: "the server could not complete the request"}
)

// errExchangeRefused is how an exchange re-check says "invalid_grant".
var errExchangeRefused = errors.New("oauth exchange refused")

// newTokenSet mints the access and refresh tokens of a grant.
type tokenSet struct {
	access, refresh                        string
	accessHash, refreshHash, refreshPrefix string
	displayPrefix                          string
}

func newTokenSet(key []byte, grantID string) (*tokenSet, error) {
	access, err := mcplogin.NewAccessToken()
	if err != nil {
		return nil, err
	}
	refresh, secret, err := mcplogin.NewRefreshToken(key, grantID)
	if err != nil {
		return nil, err
	}
	return &tokenSet{
		access: access, refresh: refresh,
		accessHash:    mcplogin.HashSecret(key, mcplogin.KindAccess, access),
		refreshHash:   mcplogin.HashSecret(key, mcplogin.KindRefresh, secret),
		refreshPrefix: mcplogin.DisplayPrefix(refresh),
		displayPrefix: mcplogin.DisplayPrefix(access),
	}, nil
}

func (t *tokenSet) result() TokenResult {
	return TokenResult{
		AccessToken: t.access, TokenType: "Bearer", ExpiresIn: int(OAuthAccessTokenTTL / time.Second),
		RefreshToken: t.refresh,
	}
}

// Exchange is the authorization-code grant. It is where the login is STORED
// (research R15): the request's origin is compared with the stored issuer
// first (a mismatch consumes nothing), then, under bindingWriteMu and the
// request store's single-use boundary, the PKCE verifier, client, redirect URI
// and resource are checked, the guard is re-run over the decided candidate and
// storage.CommitOAuthLogin re-checks the registration, the new-client id and
// the binding revision in one transaction. Any failed check answers
// invalid_grant (invalid_target for a resource mismatch) with nothing stored.
func (s *OAuthLoginService) Exchange(in TokenInput) (TokenResult, *TokenError) {
	if !s.Enabled() {
		return TokenResult{}, errInvalidGrant
	}
	reqs, err := s.requests()
	if err != nil {
		s.logger.Error("browser login: cannot build the request store", zap.Error(err))
		return TokenResult{}, errTokenServer
	}
	key, err := s.hmacKey()
	if err != nil {
		s.logger.Error("browser login: cannot read the token key", zap.Error(err))
		return TokenResult{}, errTokenServer
	}
	// The origin check comes first and consumes nothing.
	peek, ok := reqs.PeekCode(in.Code)
	if ok && peek.Issuer != in.Origin {
		return TokenResult{}, errInvalidGrant
	}

	// Lock order: bindingWriteMu BEFORE the request store (see the type doc).
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		tokens  *tokenSet
		grant   storage.OAuthGrant
		decided mcplogin.Decision
		target  bool
	)
	commit := func(req mcplogin.Request) (string, error) {
		decided = req.Decision
		if req.Issuer != in.Origin {
			return "", errExchangeRefused // the code was re-issued between peek and consume: refuse, burn
		}
		if !mcplogin.VerifyS256(in.CodeVerifier, req.CodeChallenge) ||
			in.ClientID != req.RegistrationID || in.RedirectURI != req.RedirectURI {
			return "", errExchangeRefused
		}
		if in.Resource != "" && in.Resource != req.Resource {
			target = true
			return "", errExchangeRefused
		}
		now := s.now().UTC()
		if err := s.recheckGuard(req.Decision); err != nil {
			s.writeDecisionRow(profile.ChangeDeny, req, "guard", now)
			return "", errExchangeRefused
		}
		grantID, err := mcplogin.NewGrantID()
		if err != nil {
			return "", err
		}
		ts, err := newTokenSet(key, grantID)
		if err != nil {
			return "", err
		}
		stored, err := s.store.CommitOAuthLogin(storage.CommitLoginInput{
			Grant: storage.OAuthGrant{
				GrantID: grantID, RegistrationID: req.RegistrationID, Client: req.Decision.ClientID,
				RedirectURI: req.RedirectURI, Resource: req.Resource, Issuer: req.Issuer,
				RefreshHash: ts.refreshHash, RefreshPrefix: ts.refreshPrefix, AccessHash: ts.accessHash,
				AccessExpiresAt: now.Add(OAuthAccessTokenTTL), RefreshExpiresAt: now.Add(OAuthRefreshTokenTTL),
				ApprovedAt: now, ApprovedBy: storage.OAuthActor{Kind: req.Decision.ActorKind, Name: req.Decision.ActorName},
				DisplayPrefix: ts.displayPrefix,
			},
			Client: req.Decision.ClientID, NewClient: req.Decision.NewClient,
			Pin: req.Decision.Profile, Mode: req.Decision.Mode, DisplayName: req.Decision.DisplayName,
			ExpectedBindingRevision: req.Decision.BindingRevision, ReplaceGrantID: req.Decision.ReplaceGrantID, Now: now,
		})
		if err != nil {
			reason := ""
			switch {
			case errors.Is(err, storage.ErrOAuthBindingChanged):
				reason = "binding_changed"
			case errors.Is(err, storage.ErrOAuthClientExists):
				reason = "client_exists"
			case errors.Is(err, storage.ErrOAuthRegistrationGone):
				reason = "registration_gone"
			}
			if reason == "" {
				return "", err // a storage failure: not a refusal
			}
			s.writeDecisionRow(profile.ChangeDeny, req, reason, now)
			return "", errExchangeRefused
		}
		tokens, grant = ts, stored
		return grantID, nil
	}

	req, replayedGrant, err := reqs.Consume(in.Code, commit)
	switch {
	case err == nil:
	case errors.Is(err, mcplogin.ErrCodeReused):
		// A code presented again inside the tombstone window revokes the login
		// it produced. An empty grant id means the first presentation stored
		// nothing: there is nothing to revoke.
		s.revokeReplayedCode(replayedGrant)
		return TokenResult{}, errInvalidGrant
	default:
		if target {
			return TokenResult{}, errInvalidTarget
		}
		if errors.Is(err, errExchangeRefused) || errors.Is(err, mcplogin.ErrCodeUnknown) {
			return TokenResult{}, errInvalidGrant
		}
		s.logger.Error("browser login: token exchange failed", zap.Error(err))
		return TokenResult{}, errTokenServer
	}

	s.writeGrantRow(profile.ChangeIssue, grant, decided, "")
	s.publishGrantChanged(grant, profile.ChangeIssue, req.Decision.Profile)
	if s.notifier != nil && decided.ReplaceGrantID != "" {
		s.notifier.NotifyBindingChanged(auth.ClientTokenName(grant.Client))
	}
	return tokens.result(), nil
}

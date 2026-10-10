package runtime

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// CredentialsService is the one issuance and revocation path for both worker
// credential kinds (Spec 115, research D2): custom client credentials
// (delegated to ClientsService, which already owns them) and agent tokens
// (the logic that used to live inline in the REST token handler). The MCP
// `credentials` tool, REST POST/DELETE /api/v1/tokens, REST POST
// /api/v1/clients and the CLI (through REST) all call it, so validation, the
// secret-shaped input screen, the binding guard, the audit record and the live
// event cannot drift between surfaces.
//
// Every create and revoke holds the runtime's bindingWriteMu (the same mutex as
// ClientsService and the guarded config apply), so a profile delete or rename
// can never interleave with an issue (FR-008).
type CredentialsService struct {
	tokens   CredentialTokenStore
	clients  *ClientsService
	hmacKey  func() ([]byte, error)
	cfg      func() *config.Config
	guard    func() BindingGuard
	activity func(*storage.ActivityRecord) error
	publish  func(Event)
	now      func() time.Time
	mu       *sync.Mutex
	logger   *zap.Logger
	generate func() (string, error)
}

// CredentialTokenStore is the token-store surface the service needs. Every
// method addresses the OWNERLESS namespace (data-model §9):
// GetAgentTokenByName and RevokeAgentToken resolve ownerless tokens only, and
// List filters UserID == "". *storage.Manager implements it.
type CredentialTokenStore interface {
	CreateAgentToken(token auth.AgentToken, rawToken string, hmacKey []byte) error
	ListAgentTokens() ([]auth.AgentToken, error)
	GetAgentTokenByName(name string) (*auth.AgentToken, error)
	RevokeAgentToken(name string) error
}

// tokenRevokeReporter is the optional store method that reports the record
// before and after a revoke in one transaction (*storage.Manager).
type tokenRevokeReporter interface {
	RevokeAgentTokenReport(userID, name string) (before, after *auth.AgentToken, err error)
}

// CredentialsServiceDeps wires a CredentialsService. Zero values are safe in
// tests: Now defaults to time.Now, Mu to the clients service's mutex (or a
// private one), Guard to the conservative evaluator, Generate to
// auth.GenerateToken, Logger to a no-op.
type CredentialsServiceDeps struct {
	Tokens   CredentialTokenStore
	Clients  *ClientsService
	HMACKey  func() ([]byte, error)
	Config   func() *config.Config
	Guard    func() BindingGuard
	Activity func(*storage.ActivityRecord) error
	Publish  func(Event)
	Now      func() time.Time
	Mu       *sync.Mutex
	Logger   *zap.Logger
	// Generate mints a raw agent-token secret (tests inject a spy).
	Generate func() (string, error)
}

// NewCredentialsService builds the service from its dependencies.
func NewCredentialsService(d CredentialsServiceDeps) *CredentialsService {
	s := &CredentialsService{
		tokens: d.Tokens, clients: d.Clients, hmacKey: d.HMACKey, cfg: d.Config, guard: d.Guard,
		activity: d.Activity, publish: d.Publish, now: d.Now, mu: d.Mu, logger: d.Logger, generate: d.Generate,
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.mu == nil {
		if d.Clients != nil {
			s.mu = d.Clients.mu
		} else {
			s.mu = &sync.Mutex{}
		}
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
	if s.generate == nil {
		s.generate = auth.GenerateToken
	}
	return s
}

// CredentialsService returns the runtime's credential lifecycle service
// (Spec 115).
func (r *Runtime) CredentialsService() *CredentialsService { return r.credentialsService }

// --- views ------------------------------------------------------------------

// Credential kinds in the MCP vocabulary (data-model §2).
const (
	CredentialKindClient = "client"
	CredentialKindToken  = "token"
)

// Credential states (data-model §2): revoked takes precedence over expired.
const (
	CredentialStateActive  = "active"
	CredentialStateExpired = "expired"
	CredentialStateRevoked = "revoked"
)

// Profile states of a credential's binding (data-model §2).
const (
	ProfileStateOK       = "ok"
	ProfileStateDangling = "dangling"
	ProfileStateNone     = "none"
)

// BindingPinned is the binding of a profile-pinned agent token.
const BindingPinned = "pinned"

// CredentialView is the SAFE projection of a credential record (data-model
// §2): it never carries the secret, its HMAC hash, the pending-rotation hash
// or prefix, the prior binding or the owner.
type CredentialView struct {
	Kind         string                 `json:"kind"`
	ID           string                 `json:"id"`
	TokenName    string                 `json:"token_name"`
	DisplayName  string                 `json:"display_name,omitempty"`
	Profile      string                 `json:"profile"`
	Binding      string                 `json:"binding"`
	ProfileState string                 `json:"profile_state"`
	State        string                 `json:"state"`
	CreatedAt    time.Time              `json:"created_at"`
	ExpiresAt    *time.Time             `json:"expires_at,omitempty"`
	RevokedAt    *time.Time             `json:"revoked_at,omitempty"`
	LastUsedAt   *time.Time             `json:"last_used_at,omitempty"`
	Lease        bool                   `json:"lease"`
	Issuer       *auth.CredentialIssuer `json:"issuer,omitempty"`
	Purpose      string                 `json:"purpose,omitempty"`
	TokenPrefix  string                 `json:"token_prefix"`
	LegacyScope  *bool                  `json:"legacy_scope,omitempty"`
}

// CredentialStateOf classifies a record: revoked over expired over active.
func CredentialStateOf(t *auth.AgentToken, now time.Time) string {
	switch {
	case t.Revoked:
		return CredentialStateRevoked
	case !t.ExpiresAt.IsZero() && !t.ExpiresAt.After(now):
		return CredentialStateExpired
	default:
		return CredentialStateActive
	}
}

// ProfileStateOf reports whether the record's profile still exists.
func ProfileStateOf(pin string, cfg *config.Config) string {
	if pin == "" {
		return ProfileStateNone
	}
	if cfg != nil {
		for i := range cfg.Profiles {
			if cfg.Profiles[i].Name == pin {
				return ProfileStateOK
			}
		}
	}
	return ProfileStateDangling
}

// TokenLegacyScope reports whether a regular token's scope is its own
// allowed_servers/permissions rather than a profile's (data-model §3 of 108).
func TokenLegacyScope(t *auth.AgentToken) bool {
	if len(t.AllowedServers) != 1 || t.AllowedServers[0] != "*" {
		return true
	}
	seen := map[string]bool{}
	for _, p := range t.Permissions {
		seen[p] = true
	}
	return !seen[auth.PermRead] || !seen[auth.PermWrite] || !seen[auth.PermDestructive]
}

// ViewOf projects a record to its safe view.
func ViewOf(t *auth.AgentToken, cfg *config.Config, now time.Time) CredentialView {
	v := CredentialView{
		TokenName: t.Name, Profile: t.ProfilePin, ProfileState: ProfileStateOf(t.ProfilePin, cfg),
		State: CredentialStateOf(t, now), CreatedAt: t.CreatedAt, RevokedAt: t.RevokedAt,
		LastUsedAt: t.LastUsedAt, Lease: t.IsLease(), Issuer: t.Issuer, Purpose: t.Purpose,
		TokenPrefix: t.TokenPrefix,
	}
	if !t.ExpiresAt.IsZero() {
		exp := t.ExpiresAt
		v.ExpiresAt = &exp
	}
	if t.Kind == auth.KindClient {
		v.Kind, v.ID, v.DisplayName, v.Binding = CredentialKindClient, t.ClientID, t.DisplayName, t.ProfileMode
		return v
	}
	v.Kind, v.ID = CredentialKindToken, t.Name
	if t.ProfilePin != "" {
		v.Binding = BindingPinned
	}
	legacy := TokenLegacyScope(t)
	v.LegacyScope = &legacy
	return v
}

// --- errors -----------------------------------------------------------------

// CredentialError is a typed refusal of the credentials service. Code is a
// profile.CredentialErrorCode*; Field names the argument at fault; State is
// set for identity_exists. Status is the HTTP status a REST surface answers
// (REST keeps its existing codes and texts; the MCP tool uses Code).
type CredentialError struct {
	ErrCode string
	Field   string
	Msg     string
	State   string
	Status  int
}

func (e *CredentialError) Error() string { return e.Msg }

// Code is the wire `code`.
func (e *CredentialError) Code() string { return e.ErrCode }

func credErr(code, field, msg string, status int) *CredentialError {
	return &CredentialError{ErrCode: code, Field: field, Msg: msg, Status: status}
}

// ErrCredentialsUnavailable answers when the store is not wired (startup).
var ErrCredentialsUnavailable = &CredentialError{ErrCode: profile.CredentialErrorCodeCredentialsUnavailable, Msg: "credential service not available", Status: 503}

// --- requests ---------------------------------------------------------------

// ExpiryDefault says what an EMPTY raw ExpiresIn means (data-model §6). It never
// applies to a non-empty value, which is always parsed after the screen.
type ExpiryDefault int

const (
	// ExpiryRequired (MCP): empty is missing_argument.
	ExpiryRequired ExpiryDefault = iota
	// ExpiryTokenDefault (REST/CLI tokens): empty is 30 days.
	ExpiryTokenDefault
	// ExpiryClientCap (REST/CLI clients): empty is 365 days.
	ExpiryClientCap
)

// IssueClientRequest is a custom client issuance. ExpiresIn, Profile and Mode
// are the caller's RAW text; the service screens them before any parse.
type IssueClientRequest struct {
	ID, DisplayName, Profile, Purpose string
	// ProfilePresent reports whether the caller sent `profile` at all (MCP:
	// absent is missing_argument, "" is profile_required).
	ProfilePresent bool
	Mode           *string
	ExpiresIn      string
	Expiry         ExpiryDefault
	// Now is the caller's clock reading (zero = the service clock).
	Now time.Time
	// RequireProfile and RefuseExistingRecord are both true from MCP.
	RequireProfile, RefuseExistingRecord bool
	// expiresAt is a pre-parsed expiry (ClientsService.Add's legacy entry);
	// zero means "parse ExpiresIn".
	expiresAt time.Time
}

// IssueTokenRequest is an agent token issuance. Profile and ProfilePin are the
// two RAW aliases (MCP passes ProfilePin ""), ExpiresIn the raw text.
type IssueTokenRequest struct {
	Name           string
	Profile        string
	ProfilePresent bool
	ProfilePin     string
	Purpose        string
	// AllowedServers and Permissions are the REST legacy scope; MCP passes nil.
	AllowedServers, Permissions []string
	ExpiresIn                   string
	Expiry                      ExpiryDefault
	// RequireProfile is true from MCP; EnforceGuard (MCP) also stamps GuardBound.
	RequireProfile, EnforceGuard bool
	// ValidateServers checks REST allowed_servers against the known servers
	// (the REST handler supplies it so its error texts are unchanged).
	ValidateServers func([]string) error
}

// CredentialRef names exactly one credential: a custom client id or an agent
// token name (the OWNERLESS namespace only, data-model §9).
type CredentialRef struct{ Client, Token string }

// CredentialFilter narrows List.
type CredentialFilter struct {
	Kind, Profile, State string
	// IncludeClients is false in the server edition (no client credentials).
	ExcludeClients bool
}

// IssuedCredentialResult is a successful issuance: the safe view projected
// from the committed record and the raw secret, shown once.
type IssuedCredentialResult struct {
	View   CredentialView
	Secret string
	// record is the committed record the view was projected from.
	record *auth.AgentToken
}

// Scope returns the committed token's allowed servers and permissions (the
// REST create response echoes them without a post-commit store read).
func (r *IssuedCredentialResult) Scope() (allowedServers, permissions []string) {
	if r == nil || r.record == nil {
		return nil, nil
	}
	return append([]string(nil), r.record.AllowedServers...), append([]string(nil), r.record.Permissions...)
}

// RevokeResult is a revoke outcome.
type RevokeResult struct {
	View                  CredentialView
	Changed               bool
	ClientConfigUntouched bool
}

// --- shared helpers ---------------------------------------------------------

var tokenNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// profileNameEchoPattern is the syntax a profile name must have before an
// error text may quote it (contracts/errors.md echo rule).
var profileNameEchoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func issuerOf(a Actor) *auth.CredentialIssuer {
	return &auth.CredentialIssuer{ActorKind: a.Kind, ActorName: a.Name, Surface: string(a.Surface)}
}

func viaOf(a Actor) string {
	switch a.Surface {
	case profile.SurfaceMCP:
		return "credentials"
	case profile.SurfaceCLI:
		return "cli"
	default:
		return "rest"
	}
}

func profileExistsIn(cfg *config.Config, name string) bool {
	return ProfileStateOf(name, cfg) == ProfileStateOK
}

func (s *CredentialsService) screen() *CredentialScreen { return NewCredentialScreen(s.cfg()) }

// lifecycleDiff is the always-complete diff of an issue/revoke record
// (data-model §3): no secret, no hash, no purpose text.
func lifecycleDiff(v CredentialView, a Actor) map[string]interface{} {
	exp := ""
	if v.ExpiresAt != nil {
		exp = v.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return map[string]interface{}{
		"credential_kind": v.Kind,
		"binding":         v.Binding,
		"expires_at":      exp,
		"lease":           v.Lease,
		"token_prefix":    v.TokenPrefix,
		"purpose_set":     v.Purpose != "",
		"via":             viaOf(a),
	}
}

func (s *CredentialsService) announce(v CredentialView, change profile.ChangeKind) {
	if s.publish == nil {
		return
	}
	s.publish(newEvent(EventTypeCredentialsChanged, map[string]any{
		"kind": v.Kind, "id": v.ID, "token_name": v.TokenName, "change": string(change), "profile": v.Profile,
	}))
}

func (s *CredentialsService) writeLifecycle(ctx context.Context, a Actor, change profile.ChangeKind, v CredentialView, extra map[string]interface{}) {
	diff := lifecycleDiff(v, a)
	for k, val := range extra {
		diff[k] = val
	}
	clientID := ""
	if v.Kind == CredentialKindClient {
		clientID = v.ID
	}
	prev := ""
	if change == profile.ChangeRevoke {
		prev = v.Profile
	}
	writeChangeRecord(ctx, s.activity, s.logger, s.now(), a, changeRecord{
		change: change, profile: v.Profile, previousProfile: prev,
		clientID: clientID, tokenName: v.TokenName, diff: diff,
	})
}

// parseExpiry applies the ExpiryDefault to an empty value and
// auth.ParseTokenExpiry to any other, AFTER the screen (A22).
func parseIssueExpiry(raw string, def ExpiryDefault, now time.Time, mcp bool) (time.Time, error) {
	if raw == "" {
		switch def {
		case ExpiryTokenDefault:
			return now.Add(auth.DefaultTokenExpiry), nil
		case ExpiryClientCap:
			return now.Add(auth.MaxTokenExpiry), nil
		default:
			return time.Time{}, credErr(profile.CredentialErrorCodeMissingArgument, "expires_in", `missing required argument "expires_in"`, 400)
		}
	}
	t, err := auth.ParseTokenExpiry(raw, now)
	if err != nil {
		if !mcp {
			return time.Time{}, credErr(profile.CredentialErrorCodeInvalidExpiry, "expires_in", err.Error(), 400)
		}
		msg := `invalid argument "expires_in": use a duration such as 30m, 4h or 7d`
		if strings.Contains(err.Error(), "365 days") {
			msg = `invalid argument "expires_in": expiry duration cannot exceed 365 days`
		}
		return time.Time{}, credErr(profile.CredentialErrorCodeInvalidExpiry, "expires_in", msg, 400)
	}
	return t, nil
}

// ownerlessTokens filters a store listing to the ownerless namespace.
func ownerlessTokens(all []auth.AgentToken) []auth.AgentToken {
	out := make([]auth.AgentToken, 0, len(all))
	for i := range all {
		if all[i].UserID == "" {
			out = append(out, all[i])
		}
	}
	return out
}

// --- IssueToken -------------------------------------------------------------

// IssueToken mints an agent token. The first step is the secret-shaped input
// screen over every caller-supplied value (FR-020b); nothing is generated,
// stored, audited or published before every check passed. After the commit
// nothing can fail the call (FR-007): the view is projected from the
// committed record and audit/event are best-effort.
func (s *CredentialsService) IssueToken(ctx context.Context, a Actor, req IssueTokenRequest) (*IssuedCredentialResult, error) {
	if s == nil || s.tokens == nil {
		return nil, ErrCredentialsUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mcp := req.RequireProfile

	// 1. Screen (data-model §8.2 order): every raw value, every array element.
	if hit := s.screen().ScreenFields([]ScreenField{
		{Name: "name", Values: []string{req.Name}},
		{Name: "profile", Values: []string{req.Profile}},
		{Name: "profile_pin", Values: []string{req.ProfilePin}},
		{Name: "purpose", Values: []string{req.Purpose}},
		{Name: "expires_in", Values: []string{req.ExpiresIn}},
		{Name: "allowed_servers", Values: req.AllowedServers},
		{Name: "permissions", Values: req.Permissions},
	}); hit != nil {
		return nil, hit
	}

	// 2. Name syntax, then the reserved client- prefix.
	if err := validateIssueTokenName(req.Name, mcp); err != nil {
		return nil, err
	}
	if strings.HasPrefix(req.Name, auth.ClientTokenName("")) {
		return nil, credErr(profile.CredentialErrorCodeReservedIdentity, "name",
			`token names starting with "client-" are reserved for client credentials`, 400)
	}

	// 3. Profile alias reconciliation (REST only; never quotes either value).
	pin := req.Profile
	if req.ProfilePin != "" {
		if pin != "" && pin != req.ProfilePin {
			return nil, credErr(profile.CredentialErrorCodeInvalidArgument, "profile",
				`"profile" and "profile_pin" name different profiles; send one of them`, 400)
		}
		pin = req.ProfilePin
	}

	// 4. Profile presence (MCP: required, never All servers).
	if mcp {
		if !req.ProfilePresent {
			return nil, credErr(profile.CredentialErrorCodeMissingArgument, "profile", `create_token: missing required argument "profile"`, 400)
		}
		if pin == "" {
			return nil, credErr(profile.CredentialErrorCodeProfileRequired, "profile",
				"a worker credential must name a profile; All servers is not allowed here", 400)
		}
	}

	// 5. Permissions / scope.
	allowed, perms := req.AllowedServers, req.Permissions
	if mcp {
		allowed, perms = []string{"*"}, []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}
	} else {
		if len(perms) == 0 {
			if pin != "" {
				perms = []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}
			} else {
				perms = []string{auth.PermRead}
			}
		}
		if err := auth.ValidatePermissions(perms); err != nil {
			return nil, credErr(profile.CredentialErrorCodeInvalidArgument, "permissions", err.Error(), 400)
		}
	}

	// 6. Expiry, parsed from the raw text after the screen.
	now := s.now().UTC()
	def := req.Expiry
	if mcp {
		def = ExpiryRequired
	}
	expiresAt, err := parseIssueExpiry(req.ExpiresIn, def, now, mcp)
	if err != nil {
		if !mcp && req.ExpiresIn == "" && def == ExpiryRequired {
			return nil, err
		}
		if mcp && req.ExpiresIn == "" {
			return nil, credErr(profile.CredentialErrorCodeMissingArgument, "expires_in", `create_token: missing required argument "expires_in"`, 400)
		}
		return nil, err
	}

	// 7. Allowed servers (REST legacy scope).
	if !mcp {
		if req.ValidateServers != nil {
			if err := req.ValidateServers(allowed); err != nil {
				return nil, credErr(profile.CredentialErrorCodeInvalidArgument, "allowed_servers", err.Error(), 400)
			}
		}
		if len(allowed) == 0 {
			allowed = []string{"*"}
		}
	}

	// 8. Profile existence.
	cfg := s.cfg()
	if pin != "" && !profileExistsIn(cfg, pin) {
		if mcp {
			msg := "unknown profile"
			if profileNameEchoPattern.MatchString(pin) {
				msg = fmt.Sprintf("unknown profile %q", pin)
			}
			return nil, credErr(profile.CredentialErrorCodeUnknownProfile, "profile", msg, 400)
		}
		if cfg == nil {
			return nil, credErr(profile.CredentialErrorCodeUnknownProfile, "profile", "cannot validate profile_pin: configuration unavailable", 400)
		}
		available := make([]string, 0, len(cfg.Profiles))
		for i := range cfg.Profiles {
			available = append(available, cfg.Profiles[i].Name)
		}
		return nil, credErr(profile.CredentialErrorCodeUnknownProfile, "profile",
			fmt.Sprintf("unknown profile_pin %q (available: %s)", pin, strings.Join(available, ", ")), 400)
	}

	// 9. Duplicate precheck (ownerless namespace) and the token cap.
	var all []auth.AgentToken
	if mcp || req.EnforceGuard {
		listed, err := s.tokens.ListAgentTokens()
		if err != nil {
			return nil, fmt.Errorf("cannot read credentials: %w", err)
		}
		all = listed
	}
	if mcp {
		existing, err := s.tokens.GetAgentTokenByName(req.Name)
		if err != nil {
			return nil, fmt.Errorf("cannot read credentials: %w", err)
		}
		if existing != nil {
			state := CredentialStateOf(existing, now)
			return nil, &CredentialError{ErrCode: profile.CredentialErrorCodeIdentityExists, Field: "name", State: state, Status: 409,
				Msg: fmt.Sprintf("token %s already exists (%s); choose a new name", req.Name, state)}
		}
		if len(all) >= auth.MaxTokens {
			return nil, credErr(profile.CredentialErrorCodeTokenLimitReached, "",
				fmt.Sprintf("maximum number of agent tokens (%d) reached", auth.MaxTokens), 409)
		}
	}

	candidate := auth.AgentToken{
		Name: req.Name, AllowedServers: allowed, Permissions: perms, ExpiresAt: expiresAt, CreatedAt: now,
		ProfilePin: pin, Issuer: issuerOf(a), Purpose: req.Purpose, GuardBound: req.EnforceGuard,
	}
	if n := utf8.RuneCountInString(req.Purpose); n > auth.MaxCredentialPurpose {
		return nil, credErr(profile.CredentialErrorCodeInvalidArgument, "purpose",
			fmt.Sprintf(`invalid argument "purpose": at most %d characters`, auth.MaxCredentialPurpose), 400)
	}

	// 10. Binding guard over the candidate state (MCP: FR-012).
	if req.EnforceGuard {
		current := guardedOnly(all)
		next := append(append([]auth.AgentToken(nil), current...), candidate)
		if err := CheckBindingGuard(s.guard(), GuardState{Config: cfg, Tokens: current}, GuardState{Config: cfg, Tokens: next}); err != nil {
			return nil, err
		}
	}

	// 11. Mint: the commit point.
	raw, err := s.generate()
	if err != nil {
		s.logger.Error("failed to generate agent token", zap.Error(err))
		return nil, credErr("", "", "Failed to generate token", 500)
	}
	key, err := s.hmacKey()
	if err != nil {
		s.logger.Error("failed to get HMAC key", zap.Error(err))
		return nil, credErr("", "", "Failed to initialize token security", 500)
	}
	if err := s.tokens.CreateAgentToken(candidate, raw, key); err != nil {
		return nil, s.classifyCreateError(req.Name, mcp, err, now)
	}

	// Post-commit: project from the committed record; nothing below can fail.
	committed := candidate
	committed.TokenPrefix = auth.TokenPrefix(raw)
	view := ViewOf(&committed, cfg, now)
	s.writeLifecycle(ctx, a, profile.ChangeIssue, view, map[string]interface{}{"pin_source": "token_pin"})
	s.announce(view, profile.ChangeIssue)
	return &IssuedCredentialResult{View: view, Secret: raw, record: &committed}, nil
}

func validateIssueTokenName(name string, mcp bool) error {
	if mcp {
		switch {
		case name == "":
			return credErr(profile.CredentialErrorCodeMissingArgument, "name", `create_token: missing required argument "name"`, 400)
		case len(name) > 64 || !tokenNamePattern.MatchString(name):
			return credErr(profile.CredentialErrorCodeInvalidArgument, "name",
				`invalid argument "name": must start with a letter or digit and contain only letters, digits, '_' or '-', at most 64 characters`, 400)
		}
		return nil
	}
	switch {
	case name == "":
		return credErr(profile.CredentialErrorCodeInvalidArgument, "name", "token name is required", 400)
	case len(name) > 64:
		return credErr(profile.CredentialErrorCodeInvalidArgument, "name", "token name must be at most 64 characters", 400)
	case !tokenNamePattern.MatchString(name):
		return credErr(profile.CredentialErrorCodeInvalidArgument, "name",
			"token name must start with a letter or digit and contain only letters, digits, underscores, or hyphens", 400)
	}
	return nil
}

func (s *CredentialsService) classifyCreateError(name string, mcp bool, err error, now time.Time) error {
	switch {
	case errors.Is(err, storage.ErrAgentTokenNameExists) || strings.Contains(err.Error(), "already exists"):
		holder, gerr := s.tokens.GetAgentTokenByName(name)
		if mcp {
			state := CredentialStateActive
			if gerr == nil && holder != nil {
				state = CredentialStateOf(holder, now)
			}
			return &CredentialError{ErrCode: profile.CredentialErrorCodeIdentityExists, Field: "name", State: state, Status: 409,
				Msg: fmt.Sprintf("token %s already exists (%s); choose a new name", name, state)}
		}
		// Revocation is a soft delete: the record keeps its name because
		// activity.token_name references it (#1437 item 5).
		if gerr == nil && holder != nil && holder.Revoked {
			return credErr(profile.CredentialErrorCodeIdentityExists, "name",
				fmt.Sprintf("A revoked token named %q still holds this name for activity history; choose another name", name), 409)
		}
		return credErr(profile.CredentialErrorCodeIdentityExists, "name", fmt.Sprintf("A token named %q already exists", name), 409)
	case errors.Is(err, storage.ErrAgentTokenLimitReached):
		msg := fmt.Sprintf("Maximum number of agent tokens (%d) reached", auth.MaxTokens)
		if mcp {
			msg = fmt.Sprintf("maximum number of agent tokens (%d) reached", auth.MaxTokens)
		}
		return credErr(profile.CredentialErrorCodeTokenLimitReached, "", msg, 409)
	case errors.Is(err, storage.ErrAgentTokenOwnerLimitReached):
		return credErr(profile.CredentialErrorCodeTokenLimitReached, "",
			fmt.Sprintf("Maximum number of agent tokens for this owner (%d) reached", auth.MaxTokensPerOwner), 409)
	}
	s.logger.Error("failed to create agent token", zap.Error(err))
	return credErr("", "", "Failed to create token", 500)
}

// --- IssueClient ------------------------------------------------------------

// IssueClient mints a custom client credential through ClientsService. From
// REST (RefuseExistingRecord false) it keeps POST /clients' rules, including
// re-issuing over a revoked or expired record (A5); from MCP it refuses any
// existing record and requires a named profile and an explicit expiry.
func (s *CredentialsService) IssueClient(ctx context.Context, a Actor, req IssueClientRequest) (*IssuedCredentialResult, error) {
	if s == nil || s.clients == nil {
		return nil, ErrCredentialsUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mcp := req.RequireProfile
	modeText := ""
	if req.Mode != nil {
		modeText = *req.Mode
	}

	// 1. Screen every caller-supplied value (FR-020b).
	if hit := s.screen().ScreenFields([]ScreenField{
		{Name: "client", Values: []string{req.ID}},
		{Name: "display_name", Values: []string{req.DisplayName}},
		{Name: "profile", Values: []string{req.Profile}},
		{Name: "mode", Values: []string{modeText}},
		{Name: "expires_in", Values: []string{req.ExpiresIn}},
		{Name: "purpose", Values: []string{req.Purpose}},
	}); hit != nil {
		if !mcp && len(hit.Fields) > 0 && hit.Fields[0] == "client" {
			hit.Fields[0] = "id" // the REST field name
			sort.Strings(hit.Fields)
		}
		return nil, hit
	}

	now := req.Now
	if now.IsZero() {
		now = s.now().UTC()
	}
	if mcp {
		return s.issueClientMCP(ctx, a, req, now)
	}

	// REST: POST /clients' existing rules, with the expiry parse after the screen.
	if !auth.ValidClientID(req.ID) {
		return nil, &ValidationError{Field: "id", Message: fmt.Sprintf(
			"invalid client id %q: must be lower-case letters, digits, '-' or '_', start with a letter or digit, and be at most 56 characters", req.ID)}
	}
	if connect.FindClient(req.ID) != nil {
		return nil, &ValidationError{Field: "id", Message: fmt.Sprintf("client id %q is a supported client; use connect instead", req.ID)}
	}
	if n := utf8.RuneCountInString(req.DisplayName); n > auth.MaxClientDisplayName {
		return nil, &ValidationError{Field: "display_name", Message: fmt.Sprintf("display_name is too long (%d characters, max %d)", n, auth.MaxClientDisplayName)}
	}
	if n := utf8.RuneCountInString(req.Purpose); n > auth.MaxCredentialPurpose {
		return nil, &ValidationError{Field: "purpose", Message: fmt.Sprintf("purpose is too long (%d characters, max %d)", n, auth.MaxCredentialPurpose)}
	}
	def := req.Expiry
	if def == ExpiryRequired {
		def = ExpiryClientCap
	}
	expiresAt := req.expiresAt
	if expiresAt.IsZero() {
		parsed, err := parseIssueExpiry(req.ExpiresIn, def, now, false)
		if err != nil {
			return nil, &ValidationError{Field: "expires_in", Message: err.Error()}
		}
		expiresAt = parsed
	}
	profileArg := req.Profile
	return s.mintClientLocked(ctx, a, req, &profileArg, req.Mode, expiresAt)
}

func (s *CredentialsService) issueClientMCP(ctx context.Context, a Actor, req IssueClientRequest, now time.Time) (*IssuedCredentialResult, error) {
	// 3. Id syntax, then a connect-registry id.
	if req.ID == "" {
		return nil, credErr(profile.CredentialErrorCodeMissingArgument, "client", `create_client: missing required argument "client"`, 400)
	}
	if !auth.ValidClientID(req.ID) {
		return nil, credErr(profile.CredentialErrorCodeInvalidArgument, "client",
			`invalid argument "client": must be lower-case letters, digits, '-' or '_', at most 56 characters`, 400)
	}
	if connect.FindClient(req.ID) != nil {
		return nil, credErr(profile.CredentialErrorCodeReservedIdentity, "client",
			fmt.Sprintf("client id %q is a supported client; connect it from the Web UI or CLI instead", req.ID), 400)
	}
	// 4. Profile: missing, empty, unknown.
	if !req.ProfilePresent {
		return nil, credErr(profile.CredentialErrorCodeMissingArgument, "profile", `create_client: missing required argument "profile"`, 400)
	}
	if req.Profile == "" {
		return nil, credErr(profile.CredentialErrorCodeProfileRequired, "profile",
			"a worker credential must name a profile; All servers is not allowed here", 400)
	}
	cfg := s.cfg()
	if !profileExistsIn(cfg, req.Profile) {
		msg := "unknown profile"
		if profileNameEchoPattern.MatchString(req.Profile) {
			msg = fmt.Sprintf("unknown profile %q", req.Profile)
		}
		return nil, credErr(profile.CredentialErrorCodeUnknownProfile, "profile", msg, 400)
	}
	// 5. Expiry.
	if req.ExpiresIn == "" {
		return nil, credErr(profile.CredentialErrorCodeMissingArgument, "expires_in", `create_client: missing required argument "expires_in"`, 400)
	}
	expiresAt, err := parseIssueExpiry(req.ExpiresIn, ExpiryRequired, now, true)
	if err != nil {
		return nil, err
	}
	// 6. Mode, display name and purpose lengths.
	mode := auth.ProfileModeLocked
	if req.Mode != nil {
		mode = *req.Mode
	}
	if mode != auth.ProfileModeLocked && mode != auth.ProfileModeSwitchable {
		return nil, credErr(profile.CredentialErrorCodeInvalidArgument, "mode", `invalid argument "mode": must be locked or switchable`, 400)
	}
	if n := utf8.RuneCountInString(req.DisplayName); n > auth.MaxClientDisplayName {
		return nil, credErr(profile.CredentialErrorCodeInvalidArgument, "display_name",
			fmt.Sprintf(`invalid argument "display_name": at most %d characters`, auth.MaxClientDisplayName), 400)
	}
	if n := utf8.RuneCountInString(req.Purpose); n > auth.MaxCredentialPurpose {
		return nil, credErr(profile.CredentialErrorCodeInvalidArgument, "purpose",
			fmt.Sprintf(`invalid argument "purpose": at most %d characters`, auth.MaxCredentialPurpose), 400)
	}
	// 7. Any existing ownerless record (any state), or a regular token on client-<id>.
	if s.tokens != nil {
		existing, err := s.tokens.GetAgentTokenByName(auth.ClientTokenName(req.ID))
		if err != nil {
			return nil, fmt.Errorf("cannot read credentials: %w", err)
		}
		if existing != nil {
			state := CredentialStateOf(existing, now)
			if existing.Kind != auth.KindClient {
				state = "conflicting_token"
			}
			return nil, &CredentialError{ErrCode: profile.CredentialErrorCodeIdentityExists, Field: "client", State: state, Status: 409,
				Msg: fmt.Sprintf("client %s already exists (%s); choose a new id", req.ID, state)}
		}
	}
	profileArg := req.Profile
	return s.mintClientLocked(ctx, a, req, &profileArg, &mode, expiresAt)
}

// mintClientLocked runs the client issue path (guard + mint) and the
// post-commit effects (s.mu held).
func (s *CredentialsService) mintClientLocked(ctx context.Context, a Actor, req IssueClientRequest, profileArg, mode *string, expiresAt time.Time) (*IssuedCredentialResult, error) {
	issued, rec, err := s.clients.issueLockedRecord(req.ID, profileArg, mode, false, issueOptions{
		expiresAt: expiresAt, displayName: req.DisplayName, issuer: issuerOf(a), purpose: req.Purpose,
	})
	if err != nil {
		if req.RequireProfile && errors.Is(err, storage.ErrAgentTokenLimitReached) {
			return nil, credErr(profile.CredentialErrorCodeTokenLimitReached, "",
				fmt.Sprintf("maximum number of agent tokens (%d) reached", auth.MaxTokens), 409)
		}
		return nil, err
	}
	// Post-commit: nothing below can fail the call (FR-007, T015a).
	view := ViewOf(rec, s.cfg(), s.now())
	s.writeLifecycle(ctx, a, profile.ChangeIssue, view, map[string]interface{}{"mode": rec.ProfileMode})
	if s.publish != nil {
		s.publish(newEvent(EventTypeClientBindingChanged, map[string]any{
			"client_id": req.ID, "token_name": issued.TokenName, "profile": issued.Profile,
			"previous_profile": "", "mode": issued.Mode,
		}))
	}
	s.announce(view, profile.ChangeIssue)
	return &IssuedCredentialResult{View: view, Secret: issued.Secret, record: rec}, nil
}

// --- Revoke, List, Get ------------------------------------------------------

// Revoke marks one credential revoked (FR-006). An already-revoked credential
// answers Changed=false and writes nothing; an expired one is revoked
// (Changed=true). A client revoke goes through ClientsService.forgetLockedOpt
// (record kind `revoke`) and never waits for or is refused by an in-flight
// connect (A19). allowClientRecordByToken lets REST DELETE /tokens/{name}
// keep revoking a client record by its token name, as it always has.
func (s *CredentialsService) Revoke(ctx context.Context, a Actor, ref CredentialRef, allowClientRecordByToken bool) (*RevokeResult, error) {
	if s == nil {
		return nil, ErrCredentialsUnavailable
	}
	if (ref.Client == "") == (ref.Token == "") {
		return nil, credErr(profile.CredentialErrorCodeInvalidArgument, "client", "exactly one of client or token is required", 400)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ref.Client != "" {
		return s.revokeClientLocked(ctx, a, ref.Client)
	}
	if s.tokens == nil {
		return nil, ErrCredentialsUnavailable
	}
	before, err := s.tokens.GetAgentTokenByName(ref.Token)
	if err != nil {
		return nil, fmt.Errorf("cannot read credentials: %w", err)
	}
	if before == nil {
		return nil, s.notFound("token", ref.Token)
	}
	if before.Kind == auth.KindClient && !allowClientRecordByToken {
		return nil, credErr(profile.CredentialErrorCodeReservedIdentity, "token",
			fmt.Sprintf("token %s is a client credential; revoke it with client=%s", ref.Token, before.ClientID), 400)
	}
	cfg := s.cfg()
	if before.Revoked {
		return &RevokeResult{View: ViewOf(before, cfg, s.now())}, nil
	}
	after, err := s.revokeTokenStore(ref.Token)
	if err != nil {
		if errors.Is(err, storage.ErrAgentTokenNotFound) {
			return nil, s.notFound("token", ref.Token)
		}
		return nil, err
	}
	view := ViewOf(after, cfg, s.now())
	extra := map[string]interface{}{"changed": true}
	if after.Kind != auth.KindClient {
		extra["pin_source"] = "token_pin"
	}
	s.writeLifecycle(ctx, a, profile.ChangeRevoke, view, extra)
	s.announce(view, profile.ChangeRevoke)
	if s.clients != nil && s.clients.notifier != nil {
		s.clients.notifier.NotifyBindingChanged(after.Name)
	}
	return &RevokeResult{View: view, Changed: true}, nil
}

func (s *CredentialsService) revokeTokenStore(name string) (*auth.AgentToken, error) {
	if rep, ok := s.tokens.(tokenRevokeReporter); ok {
		_, after, err := rep.RevokeAgentTokenReport("", name)
		return after, err
	}
	if err := s.tokens.RevokeAgentToken(name); err != nil {
		return nil, err
	}
	after, err := s.tokens.GetAgentTokenByName(name)
	if err != nil || after == nil {
		return nil, storage.ErrAgentTokenNotFound
	}
	return after, nil
}

func (s *CredentialsService) notFound(kind, name string) error {
	field := "token"
	if kind == "client" {
		field = "client"
	}
	return credErr(profile.CredentialErrorCodeIdentityNotFound, field, fmt.Sprintf("%s %q not found", kind, name), 404)
}

func (s *CredentialsService) revokeClientLocked(ctx context.Context, a Actor, clientID string) (*RevokeResult, error) {
	if s.clients == nil {
		return nil, ErrCredentialsUnavailable
	}
	if !auth.ValidClientID(clientID) {
		return nil, credErr(profile.CredentialErrorCodeInvalidArgument, "client",
			`invalid argument "client": must be lower-case letters, digits, '-' or '_', at most 56 characters`, 400)
	}
	all, err := s.clients.records()
	if err != nil {
		return nil, err
	}
	rec := clientRecord(all, clientID)
	if rec == nil || rec.Kind != auth.KindClient {
		return nil, s.notFound("client", clientID)
	}
	cfg := s.cfg()
	if rec.Revoked {
		return &RevokeResult{View: ViewOf(rec, cfg, s.now()), ClientConfigUntouched: true}, nil
	}
	diff := map[string]interface{}{"via": viaOf(a), "changed": true, "client_config_untouched": true}
	view := ViewOf(rec, cfg, s.now())
	for k, v := range lifecycleDiff(view, a) {
		if _, set := diff[k]; !set {
			diff[k] = v
		}
	}
	revoked, err := s.clients.forgetRecordLocked(ctx, a, clientID, diff, false, profile.ChangeRevoke)
	if err != nil {
		var noCred *NoClientCredentialError
		if errors.As(err, &noCred) {
			return nil, s.notFound("client", clientID)
		}
		return nil, err
	}
	return &RevokeResult{View: ViewOf(revoked, cfg, s.now()), Changed: true, ClientConfigUntouched: true}, nil
}

// List returns the safe views of every client credential and every OWNERLESS
// agent token, filtered, sorted by kind then id (FR-005, FR-005a).
func (s *CredentialsService) List(f CredentialFilter) ([]CredentialView, error) {
	if s == nil || s.tokens == nil {
		return nil, ErrCredentialsUnavailable
	}
	all, err := s.tokens.ListAgentTokens()
	if err != nil {
		return nil, fmt.Errorf("cannot read credentials: %w", err)
	}
	cfg, now := s.cfg(), s.now()
	out := make([]CredentialView, 0, len(all))
	for _, t := range ownerlessTokens(all) {
		t := t
		v := ViewOf(&t, cfg, now)
		if v.Kind == CredentialKindClient && f.ExcludeClients {
			continue
		}
		if f.Kind != "" && f.Kind != "all" && f.Kind != v.Kind {
			continue
		}
		if f.Profile != "" && f.Profile != v.Profile {
			continue
		}
		if f.State != "" && f.State != "all" && f.State != v.State {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Get returns one credential's safe view, or identity_not_found.
func (s *CredentialsService) Get(ref CredentialRef) (*CredentialView, error) {
	if s == nil || s.tokens == nil {
		return nil, ErrCredentialsUnavailable
	}
	if (ref.Client == "") == (ref.Token == "") {
		return nil, credErr(profile.CredentialErrorCodeInvalidArgument, "client", "exactly one of client or token is required", 400)
	}
	name, kind := ref.Token, "token"
	if ref.Client != "" {
		if !auth.ValidClientID(ref.Client) {
			return nil, credErr(profile.CredentialErrorCodeInvalidArgument, "client",
				`invalid argument "client": must be lower-case letters, digits, '-' or '_', at most 56 characters`, 400)
		}
		name, kind = auth.ClientTokenName(ref.Client), "client"
	}
	rec, err := s.tokens.GetAgentTokenByName(name)
	if err != nil {
		return nil, fmt.Errorf("cannot read credentials: %w", err)
	}
	if rec == nil || (kind == "client") != (rec.Kind == auth.KindClient) {
		label := ref.Token
		if kind == "client" {
			label = ref.Client
		}
		return nil, s.notFound(kind, label)
	}
	v := ViewOf(rec, s.cfg(), s.now())
	return &v, nil
}

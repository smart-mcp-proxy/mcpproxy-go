package runtime

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// ClientsService is THE service behind every client-credential operation
// (Spec 108, FR-026): assign, lock, unlock, bulk, rotate, forget, add, list,
// plus the connect credential minter and the staged-rotation reconciler. REST,
// CLI, MCP, Web UI and macOS all call it, so records, notifications and the
// FR-008a guard cannot drift between surfaces.
//
// Every mutation holds one mutex across "guard check + write" (plan D3). The
// mutex is the runtime's bindingWriteMu, shared with the guarded config apply,
// so a config write and a binding write can never combine into a bypassable
// state.
type ClientsService struct {
	store    ClientCredentialStore
	hmacKey  func() ([]byte, error)
	cfg      func() *config.Config
	guard    func() BindingGuard
	activity func(*storage.ActivityRecord) error
	publish  func(Event)
	now      func() time.Time
	mu       *sync.Mutex
	logger   *zap.Logger

	notifier BindingNotifier
	reader   ClientConfigReader

	// upgrade is the connect port behind the admin-key upgrade, observe the
	// sink for on-demand credential classifications (Spec 108-f).
	upgrade AdminKeyUpgradePort
	observe func(clientID string, state profile.CredentialState)

	// inflight holds the per-client connect claim (FR-021a), guarded by mu: a
	// connect holds it from Issue until Commit, Abort or Release.
	inflight map[string]time.Time
}

// ClientCredentialStore is the token-store surface the service needs;
// *storage.Manager implements it.
type ClientCredentialStore interface {
	MintClientCredential(clientID, rawToken string, hmacKey []byte, mode, pin string, expiresAt time.Time) (*auth.AgentToken, error)
	MintClientCredentialNamed(clientID, rawToken string, hmacKey []byte, mode, pin string, expiresAt time.Time, displayName string) (*auth.AgentToken, error)
	StageClientCredentialRotation(clientID, newRawToken string, hmacKey []byte) (*auth.AgentToken, error)
	FinalizeClientCredentialRotation(clientID string) (*auth.AgentToken, error)
	RollbackClientCredentialRotation(clientID string) (*auth.AgentToken, error)
	ForgetClientCredential(clientID string) (*auth.AgentToken, error)
	UpdateClientCredentialBinding(clientID, pin, mode string) (before, after *auth.AgentToken, err error)
	ListAgentTokens() ([]auth.AgentToken, error)
}

// BindingNotifier delivers FR-026's live effects for a token whose binding
// changed: clear the stored set_profile selection of every session it
// authenticated and send notifications/tools/list_changed to each. The server
// implements it; it is called synchronously so clearing is complete before
// the mutation returns.
type BindingNotifier interface {
	NotifyBindingChanged(tokenName string)
}

// ClientConfigReader reads the credential a supported client's config holds
// (the reconciler's view of FR-021a). found is false when the config has no
// mcpproxy entry. connect.Service implements it.
type ClientConfigReader interface {
	ClientSecret(clientID string) (secret string, found bool, err error)
}

// Actor attributes a mutation on the profile_change record (FR-030).
type Actor struct {
	Kind    string          // actor_kind: api_key, socket, agent_token, cookie, cli_offline, ...
	Name    string          // actor_name: token name / user email, "" otherwise
	Surface profile.Surface // web, macos, cli, mcp, api
}

// ActorFromContext derives the actor from the request's AuthContext (D21).
func ActorFromContext(ctx context.Context, surface profile.Surface) Actor {
	a := Actor{Surface: surface}
	if surface == "" {
		a.Surface = profile.SurfaceAPI
	}
	ac := auth.AuthContextFromContext(ctx)
	if ac == nil {
		a.Kind = string(auth.CredentialKindAnonymous)
		return a
	}
	a.Kind = string(ac.CredentialKind)
	if a.Kind == "" {
		// REST contexts do not stamp CredentialKind; infer it from the type so
		// the record still names who acted.
		switch {
		case ac.Anonymous:
			a.Kind = string(auth.CredentialKindAnonymous)
		case ac.Type == auth.AuthTypeAgent:
			a.Kind = string(auth.CredentialKindAgentToken)
		case ac.IsAdmin():
			a.Kind = string(auth.CredentialKindAPIKey)
		default:
			a.Kind = string(auth.CredentialKindAnonymous)
		}
	}
	switch {
	case ac.AgentName != "":
		a.Name = ac.AgentName
	case ac.Email != "":
		a.Name = ac.Email
	}
	return a
}

// ClientCredentialView is a client's credential as the surfaces show it.
type ClientCredentialView struct {
	ID              string                  `json:"id"`
	DisplayName     string                  `json:"display_name,omitempty"`
	TokenName       string                  `json:"token_name"`
	Profile         string                  `json:"profile"`
	Mode            string                  `json:"mode"`
	CredentialState profile.CredentialState `json:"credential_state"`
	ExpiresAt       *time.Time              `json:"expires_at,omitempty"`
	ConnectedAt     *time.Time              `json:"connected_at,omitempty"`
	RotationPending bool                    `json:"rotation_pending,omitempty"`
}

// ValidationError is a 400 {error, field} refusal.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// NoClientCredentialError is the 409 no_client_credential refusal: the client
// has no active client credential, so a binding operation would have to mint
// one implicitly (FR-025 forbids it).
type NoClientCredentialError struct {
	ClientID string
	State    profile.CredentialState
}

func (e *NoClientCredentialError) Error() string {
	return fmt.Sprintf("client %s has no active client credential; connect it with a profile first", e.ClientID)
}

// Code is the wire `code` of the refusal.
func (e *NoClientCredentialError) Code() string { return profile.ErrorCodeNoClientCredential }

// ConnectInProgressError is the 409 connect_in_progress refusal: a connect of
// the client holds the in-flight claim (FR-021a), so another connect, rotate,
// finalize or binding change must wait for it.
type ConnectInProgressError struct{ ClientID string }

func (e *ConnectInProgressError) Error() string {
	return fmt.Sprintf("a connect of %s is already in progress; retry when it finishes", e.ClientID)
}

// Code is the wire `code` of the refusal.
func (e *ConnectInProgressError) Code() string { return profile.ErrorCodeConnectInProgress }

// CredentialSupersededError is the 409 credential_superseded refusal: the
// credential a connect wrote was replaced or revoked before it could be
// finalized, so the written secret must not be treated as live (FR-021a).
type CredentialSupersededError struct{ ClientID string }

func (e *CredentialSupersededError) Error() string {
	return fmt.Sprintf("the credential written for %s was replaced or revoked before it could be finalized; reconnect the client", e.ClientID)
}

// Code is the wire `code` of the refusal.
func (e *CredentialSupersededError) Code() string { return profile.ErrorCodeCredentialSuperseded }

// ClientsServiceDeps wires a ClientsService. Zero values are safe in tests:
// Now defaults to time.Now, Mu to a private mutex, Guard to the conservative
// evaluator, Logger to a no-op.
type ClientsServiceDeps struct {
	Store    ClientCredentialStore
	HMACKey  func() ([]byte, error)
	Config   func() *config.Config
	Guard    func() BindingGuard
	Activity func(*storage.ActivityRecord) error
	Publish  func(Event)
	Now      func() time.Time
	Mu       *sync.Mutex
	Logger   *zap.Logger
}

// NewClientsService builds the service from its dependencies.
func NewClientsService(d ClientsServiceDeps) *ClientsService {
	s := &ClientsService{
		store: d.Store, hmacKey: d.HMACKey, cfg: d.Config, guard: d.Guard,
		activity: d.Activity, publish: d.Publish, now: d.Now, mu: d.Mu, logger: d.Logger,
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

// SetNotifier installs the live-session notifier (the server).
func (s *ClientsService) SetNotifier(n BindingNotifier) { s.notifier = n }

// SetConfigReader installs the client-config reader used by the reconciler.
func (s *ClientsService) SetConfigReader(r ClientConfigReader) { s.reader = r }

// --- helpers -------------------------------------------------------------

// Records returns every ownerless token record that concerns a client
// credential: the kind=client records (any state) and any regular token that
// holds a client-<id> name (the FR-021 conflict). The REST clients list reads
// it once to decorate its rows.
func (s *ClientsService) Records() ([]auth.AgentToken, error) {
	all, err := s.records()
	if err != nil {
		return nil, err
	}
	out := make([]auth.AgentToken, 0, len(all))
	for i := range all {
		t := &all[i]
		if t.UserID != "" {
			continue
		}
		if t.Kind == auth.KindClient || strings.HasPrefix(t.Name, "client-") {
			out = append(out, *t)
		}
	}
	return out, nil
}

// Now is the service's clock (tests inject one); REST uses it so "expiring"
// and "last observed" agree with the service.
func (s *ClientsService) Now() time.Time { return s.now() }

// StateOf classifies a credential record (client | revoked | expired | none).
func (s *ClientsService) StateOf(t *auth.AgentToken) profile.CredentialState { return s.stateOf(t) }

// ObservedCredentialStates returns the credential states Warnings needs for the
// admin-key warning: the persisted last observation (Spec 108-f F11) of every
// connect-registry client that has no client credential record of its own.
// It reads the credential store only, never a client config (Spec 075). Both
// GET /clients and the needs-attention list call it, so they cannot disagree.
func (s *ClientsService) ObservedCredentialStates(observed map[string]storage.ClientCredentialObservation) (map[string]profile.CredentialState, error) {
	all, err := s.records()
	if err != nil {
		return nil, err
	}
	hasRecord := map[string]bool{}
	for i := range all {
		if all[i].Kind == auth.KindClient {
			hasRecord[all[i].ClientID] = true
		}
	}
	states := map[string]profile.CredentialState{}
	for id, obs := range observed {
		if hasRecord[id] || connect.FindClient(id) == nil {
			continue
		}
		if profile.CredentialState(obs.State) == profile.CredentialStateAdminKey {
			states[id] = profile.CredentialStateAdminKey
		}
	}
	return states, nil
}

func (s *ClientsService) records() ([]auth.AgentToken, error) {
	all, err := s.store.ListAgentTokens()
	if err != nil {
		return nil, fmt.Errorf("cannot read client credentials: %w", err)
	}
	return all, nil
}

// clientRecord finds the token named client-<id> among all (any kind).
func clientRecord(all []auth.AgentToken, clientID string) *auth.AgentToken {
	name := auth.ClientTokenName(clientID)
	for i := range all {
		if all[i].Name == name && all[i].UserID == "" {
			return &all[i]
		}
	}
	return nil
}

// stateOf reports the credential state of a record.
func (s *ClientsService) stateOf(t *auth.AgentToken) profile.CredentialState {
	switch {
	case t == nil || t.Kind != auth.KindClient:
		return profile.CredentialStateNone
	case t.Revoked:
		return profile.CredentialStateRevoked
	case !t.ExpiresAt.After(s.now()):
		return profile.CredentialStateExpired
	default:
		return profile.CredentialStateClient
	}
}

func (s *ClientsService) view(t *auth.AgentToken) *ClientCredentialView {
	exp := t.ExpiresAt
	return &ClientCredentialView{
		ID: t.ClientID, DisplayName: t.DisplayName, TokenName: t.Name, Profile: t.ProfilePin, Mode: t.ProfileMode,
		CredentialState: s.stateOf(t), ExpiresAt: &exp, ConnectedAt: t.ConnectedAt,
		RotationPending: t.PendingHash != "",
	}
}

func (s *ClientsService) profileExists(cfg *config.Config, name string) bool {
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

// candidateTokens returns every client credential record with clientID's
// replaced by next (nil next removes it) — the token half of a candidate
// GuardState.
func candidateTokens(all []auth.AgentToken, clientID string, next *auth.AgentToken) []auth.AgentToken {
	out := make([]auth.AgentToken, 0, len(all)+1)
	for i := range all {
		if all[i].Kind == auth.KindClient {
			if all[i].ClientID == clientID {
				continue
			}
			out = append(out, all[i])
		}
	}
	if next != nil {
		out = append(out, *next)
	}
	return out
}

func clientOnly(all []auth.AgentToken) []auth.AgentToken {
	out := make([]auth.AgentToken, 0, len(all))
	for i := range all {
		if all[i].Kind == auth.KindClient {
			out = append(out, all[i])
		}
	}
	return out
}

func (s *ClientsService) checkGuard(all []auth.AgentToken, clientID string, next *auth.AgentToken) error {
	cfg := s.cfg()
	current := GuardState{Config: cfg, Tokens: clientOnly(all)}
	candidate := GuardState{Config: cfg, Tokens: candidateTokens(all, clientID, next)}
	return CheckBindingGuard(s.guard(), current, candidate)
}

// resolveMode applies D11's defaults and validation. modeArg nil = "not
// specified": keep the record's mode (a binding change), or the default for a
// fresh credential.
func resolveMode(profileName string, modeArg *string, keep string) (string, error) {
	mode := keep
	switch {
	case modeArg != nil:
		mode = *modeArg
	case profileName == "":
		mode = auth.ProfileModeSwitchable
	case mode == "":
		mode = auth.ProfileModeLocked
	}
	if mode != auth.ProfileModeLocked && mode != auth.ProfileModeSwitchable {
		return "", &ValidationError{Field: "mode", Message: fmt.Sprintf("invalid mode %q: must be locked or switchable", mode)}
	}
	if mode == auth.ProfileModeLocked && profileName == "" {
		return "", &ValidationError{Field: "mode", Message: "a locked client needs a profile"}
	}
	return mode, nil
}

// --- profile_change records ----------------------------------------------

type changeRecord struct {
	change          profile.ChangeKind
	profile         string
	previousProfile string
	clientID        string
	tokenName       string
	diff            map[string]interface{}
}

func surfaceSource(s profile.Surface) storage.ActivitySource {
	switch s {
	case profile.SurfaceCLI:
		return storage.ActivitySourceCLI
	case profile.SurfaceMCP:
		return storage.ActivitySourceMCP
	default:
		return storage.ActivitySourceAPI
	}
}

func (s *ClientsService) writeChange(ctx context.Context, a Actor, c changeRecord) {
	writeChangeRecord(ctx, s.activity, s.logger, s.now(), a, c)
}

// writeChangeRecord builds and saves one `profile_change` activity record. It
// is shared by the clients service and the profiles/config funnel, so every
// record has one shape (FR-030).
func writeChangeRecord(ctx context.Context, activity func(*storage.ActivityRecord) error, logger *zap.Logger, now time.Time, a Actor, c changeRecord) {
	if activity == nil {
		return
	}
	meta := map[string]interface{}{
		"actor_kind":       a.Kind,
		"actor_name":       a.Name,
		"surface":          string(a.Surface),
		"change":           string(c.change),
		"profile":          c.profile,
		"previous_profile": c.previousProfile,
		"client_id":        c.clientID,
		"token_name":       c.tokenName,
	}
	if len(c.diff) > 0 {
		meta["diff"] = c.diff
	}
	rec := &storage.ActivityRecord{
		Type:      storage.ActivityTypeProfileChange,
		Source:    surfaceSource(a.Surface),
		Status:    "success",
		Timestamp: now.UTC(),
		RequestID: reqcontext.GetRequestID(ctx),
		Metadata:  meta,
		// Spec 108 FR-030/T063: first-class beside the metadata keys (kept for
		// one release) so /activity?client=&type=profile_change finds it.
		// Profile is the NEW profile; a profile_change is not a resolution, so
		// ProfileSource stays empty.
		Profile:   c.profile,
		ClientID:  c.clientID,
		TokenName: c.tokenName,
	}
	if err := activity(rec); err != nil {
		logger.Error("failed to write profile_change activity record", zap.String("client_id", c.clientID), zap.Error(err))
	}
}

func (s *ClientsService) announce(before, after *auth.AgentToken) {
	if s.publish != nil {
		prev := ""
		if before != nil {
			prev = before.ProfilePin
		}
		s.publish(newEvent(EventTypeClientBindingChanged, map[string]any{
			"client_id":        after.ClientID,
			"token_name":       after.Name,
			"profile":          after.ProfilePin,
			"previous_profile": prev,
			"mode":             after.ProfileMode,
		}))
	}
	if s.notifier != nil {
		s.notifier.NotifyBindingChanged(after.Name)
	}
}

// bindingChange classifies a before/after pair into its single profile_change
// record (data-model §5): a profile change is `assign` (the mode change, if
// any, rides in diff); with the same profile a switchable→locked flip is
// `lock` and locked→switchable is `unlock`.
func bindingChange(before, after *auth.AgentToken) (profile.ChangeKind, map[string]interface{}) {
	var diff map[string]interface{}
	if before.ProfileMode != after.ProfileMode {
		diff = map[string]interface{}{"mode": map[string]interface{}{"from": before.ProfileMode, "to": after.ProfileMode}}
	}
	if before.ProfilePin != after.ProfilePin {
		return profile.ChangeAssign, diff
	}
	if after.ProfileMode == auth.ProfileModeLocked {
		return profile.ChangeLock, diff
	}
	return profile.ChangeUnlock, diff
}

// --- operations ------------------------------------------------------------

// SetBinding reassigns a client's profile and/or mode (assign|lock|unlock).
// It applies only to an active client credential (FR-026), refuses a binding
// the FR-008a guard would leave bypassable, clears the stored selection of
// every live session of the token, emits client.binding_changed, notifies the
// sessions, writes one profile_change record and never touches the client's
// config file. modeArg nil keeps the current mode, except profile "" which
// means All servers (switchable).
func (s *ClientsService) SetBinding(ctx context.Context, a Actor, clientID, profileName string, modeArg *string) (*ClientCredentialView, error) {
	if !auth.ValidClientID(clientID) {
		return nil, &ValidationError{Field: "id", Message: fmt.Sprintf("invalid client id %q", clientID)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connectInFlight(clientID) {
		return nil, &ConnectInProgressError{ClientID: clientID}
	}
	return s.setBindingLocked(ctx, a, clientID, profileName, modeArg)
}

func (s *ClientsService) setBindingLocked(ctx context.Context, a Actor, clientID, profileName string, modeArg *string) (*ClientCredentialView, error) {
	all, err := s.records()
	if err != nil {
		return nil, err
	}
	rec := clientRecord(all, clientID)
	// The credential precondition (409) comes before request validation (400)
	// so a client without an active credential always gets no_client_credential.
	if state := s.stateOf(rec); state != profile.CredentialStateClient {
		return nil, &NoClientCredentialError{ClientID: clientID, State: state}
	}
	if profileName != "" && !s.profileExists(s.cfg(), profileName) {
		return nil, &ValidationError{Field: "profile", Message: fmt.Sprintf("unknown profile %q", profileName)}
	}
	mode, err := resolveMode(profileName, modeArg, rec.ProfileMode)
	if err != nil {
		return nil, err
	}
	if rec.ProfilePin == profileName && rec.ProfileMode == mode {
		return s.view(rec), nil // no-op: no record, no notification
	}
	next := *rec
	next.ProfilePin, next.ProfileMode = profileName, mode
	if err := s.checkGuard(all, clientID, &next); err != nil {
		return nil, err
	}
	before, after, err := s.store.UpdateClientCredentialBinding(clientID, profileName, mode)
	if err != nil {
		if errors.Is(err, storage.ErrClientCredentialNotActive) || errors.Is(err, storage.ErrClientCredentialNotFound) || errors.Is(err, storage.ErrClientCredentialConflict) {
			return nil, &NoClientCredentialError{ClientID: clientID, State: profile.CredentialStateNone}
		}
		return nil, err
	}
	change, diff := bindingChange(before, after)
	s.writeChange(ctx, a, changeRecord{
		change: change, profile: after.ProfilePin, previousProfile: before.ProfilePin,
		clientID: clientID, tokenName: after.Name, diff: diff,
	})
	s.announce(before, after)
	return s.view(after), nil
}

// Skipped is a bulk-move client that was not moved.
type Skipped struct {
	ClientID string `json:"client_id"`
	Code     string `json:"code"`
	Error    string `json:"error"`
}

// BulkAssign moves every active client credential bound to profile `from` to
// profile `to` (mode as in SetBinding). The guard and the credential
// precondition apply per client: refused clients are reported in skipped[]
// with the same code the single operation returns, the others are moved and
// each writes its own record.
func (s *ClientsService) BulkAssign(ctx context.Context, a Actor, from, to string, modeArg *string) (moved []string, skipped []Skipped, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.records()
	if err != nil {
		return nil, nil, err
	}
	var ids []string
	for i := range all {
		t := &all[i]
		if t.Kind == auth.KindClient && t.ProfilePin == from {
			ids = append(ids, t.ClientID)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if s.connectInFlight(id) {
			skipped = append(skipped, skipReason(id, &ConnectInProgressError{ClientID: id}))
			continue
		}
		if _, err := s.setBindingLocked(ctx, a, id, to, modeArg); err != nil {
			skipped = append(skipped, skipReason(id, err))
			continue
		}
		moved = append(moved, id)
	}
	return moved, skipped, nil
}

func skipReason(id string, err error) Skipped {
	var guardErr *BindingGuardError
	var noCred *NoClientCredentialError
	var busy *ConnectInProgressError
	var val *ValidationError
	switch {
	case errors.As(err, &busy):
		return Skipped{ClientID: id, Code: busy.Code(), Error: err.Error()}
	case errors.As(err, &guardErr):
		return Skipped{ClientID: id, Code: profile.ErrorCodeBindingBypassable, Error: err.Error()}
	case errors.As(err, &noCred):
		return Skipped{ClientID: id, Code: profile.ErrorCodeNoClientCredential, Error: err.Error()}
	case errors.As(err, &val):
		return Skipped{ClientID: id, Code: "invalid_" + val.Field, Error: err.Error()}
	default:
		return Skipped{ClientID: id, Code: "error", Error: err.Error()}
	}
}

// List returns every client credential (any state), sorted by client id.
func (s *ClientsService) List() ([]ClientCredentialView, error) {
	all, err := s.records()
	if err != nil {
		return nil, err
	}
	var out []ClientCredentialView
	for i := range all {
		if all[i].Kind == auth.KindClient {
			out = append(out, *s.view(&all[i]))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Get returns one client's credential view, or nil when it has none.
func (s *ClientsService) Get(clientID string) (*ClientCredentialView, error) {
	all, err := s.records()
	if err != nil {
		return nil, err
	}
	rec := clientRecord(all, clientID)
	if rec == nil || rec.Kind != auth.KindClient {
		return nil, nil
	}
	return s.view(rec), nil
}

// Forget revokes a client's credential (change=forget). disconnected records
// whether the config entry was removed with it.
func (s *ClientsService) Forget(ctx context.Context, a Actor, clientID string, disconnected bool) (*ClientCredentialView, error) {
	return s.forget(ctx, a, clientID, map[string]interface{}{"disconnected": disconnected})
}

func (s *ClientsService) forget(ctx context.Context, a Actor, clientID string, diff map[string]interface{}) (*ClientCredentialView, error) {
	if !auth.ValidClientID(clientID) {
		return nil, &ValidationError{Field: "id", Message: fmt.Sprintf("invalid client id %q", clientID)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forgetLocked(ctx, a, clientID, diff)
}

func (s *ClientsService) forgetLocked(ctx context.Context, a Actor, clientID string, diff map[string]interface{}) (*ClientCredentialView, error) {
	revoked, err := s.store.ForgetClientCredential(clientID)
	if err != nil {
		if errors.Is(err, storage.ErrClientCredentialNotFound) {
			return nil, &NoClientCredentialError{ClientID: clientID, State: profile.CredentialStateNone}
		}
		return nil, err
	}
	s.writeChange(ctx, a, changeRecord{
		change: profile.ChangeForget, profile: revoked.ProfilePin, previousProfile: revoked.ProfilePin,
		clientID: clientID, tokenName: revoked.Name, diff: diff,
	})
	return s.view(revoked), nil
}

// Rotate stages a rotation over an active credential and returns the new
// secret ONCE (custom clients paste it into their own config; supported
// clients go through connect). Both secrets authenticate until
// FinalizeRotation.
func (s *ClientsService) Rotate(ctx context.Context, a Actor, clientID string) (secret string, view *ClientCredentialView, err error) {
	if !auth.ValidClientID(clientID) {
		return "", nil, &ValidationError{Field: "id", Message: fmt.Sprintf("invalid client id %q", clientID)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connectInFlight(clientID) {
		return "", nil, &ConnectInProgressError{ClientID: clientID}
	}
	key, err := s.hmacKey()
	if err != nil {
		return "", nil, err
	}
	raw, err := auth.GenerateClientToken()
	if err != nil {
		return "", nil, err
	}
	staged, err := s.store.StageClientCredentialRotation(clientID, raw, key)
	if err != nil {
		if errors.Is(err, storage.ErrClientCredentialNotActive) || errors.Is(err, storage.ErrClientCredentialNotFound) {
			return "", nil, &NoClientCredentialError{ClientID: clientID, State: profile.CredentialStateNone}
		}
		return "", nil, err
	}
	return raw, s.view(staged), nil
}

// FinalizeRotation promotes a staged secret. Idempotent: no rotation in
// progress is a no-op success. A finalize that did promote writes the single
// `rotate` record (outcome finalized).
func (s *ClientsService) FinalizeRotation(ctx context.Context, a Actor, clientID string) (*ClientCredentialView, error) {
	if !auth.ValidClientID(clientID) {
		return nil, &ValidationError{Field: "id", Message: fmt.Sprintf("invalid client id %q", clientID)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connectInFlight(clientID) {
		return nil, &ConnectInProgressError{ClientID: clientID}
	}
	return s.finalizeLocked(ctx, a, clientID)
}

func (s *ClientsService) finalizeLocked(ctx context.Context, a Actor, clientID string) (*ClientCredentialView, error) {
	all, err := s.records()
	if err != nil {
		return nil, err
	}
	oldPrefix := ""
	if rec := clientRecord(all, clientID); rec != nil {
		oldPrefix = rec.TokenPrefix
	}
	final, err := s.store.FinalizeClientCredentialRotation(clientID)
	if err != nil {
		if errors.Is(err, storage.ErrClientCredentialNotRotating) {
			return s.view(final), nil
		}
		if errors.Is(err, storage.ErrClientCredentialNotFound) {
			return nil, &NoClientCredentialError{ClientID: clientID, State: profile.CredentialStateNone}
		}
		return nil, err
	}
	s.writeChange(ctx, a, changeRecord{
		change: profile.ChangeRotate, profile: final.ProfilePin, previousProfile: final.ProfilePin,
		clientID: clientID, tokenName: final.Name,
		diff: map[string]interface{}{
			"outcome": profile.RotationFinalized, "old_token_prefix": oldPrefix, "new_token_prefix": final.TokenPrefix,
		},
	})
	return s.view(final), nil
}

func (s *ClientsService) rollbackLocked(ctx context.Context, a Actor, clientID string) error {
	all, err := s.records()
	if err != nil {
		return err
	}
	rec := clientRecord(all, clientID)
	if rec == nil || rec.PendingHash == "" {
		return nil
	}
	dropped := rec.PendingPrefix
	kept, err := s.store.RollbackClientCredentialRotation(clientID)
	if err != nil {
		return err
	}
	s.writeChange(ctx, a, changeRecord{
		change: profile.ChangeRotate, profile: kept.ProfilePin, previousProfile: kept.ProfilePin,
		clientID: clientID, tokenName: kept.Name,
		diff: map[string]interface{}{
			"outcome": profile.RotationRolledBack, "old_token_prefix": kept.TokenPrefix, "new_token_prefix": dropped,
		},
	})
	return nil
}

// AddRequest creates a credential for a custom (non-connect-registry) client.
// ExpiresAt zero means the 365-day default; DisplayName is at most
// auth.MaxClientDisplayName characters.
type AddRequest struct {
	ID          string
	DisplayName string
	Profile     string
	Mode        *string
	ExpiresAt   time.Time
}

// Add mints a credential for a custom client and returns the secret once.
// Id rules are FR-021: the pattern, and never a supported-client id.
func (s *ClientsService) Add(ctx context.Context, a Actor, req AddRequest) (*ClientCredentialView, string, error) {
	if !auth.ValidClientID(req.ID) {
		return nil, "", &ValidationError{Field: "id", Message: fmt.Sprintf(
			"invalid client id %q: must be lower-case letters, digits, '-' or '_', start with a letter or digit, and be at most 56 characters", req.ID)}
	}
	if connect.FindClient(req.ID) != nil {
		return nil, "", &ValidationError{Field: "id", Message: fmt.Sprintf("client id %q is a supported client; use connect instead", req.ID)}
	}
	if n := utf8.RuneCountInString(req.DisplayName); n > auth.MaxClientDisplayName {
		return nil, "", &ValidationError{Field: "display_name", Message: fmt.Sprintf("display_name is too long (%d characters, max %d)", n, auth.MaxClientDisplayName)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	issued, err := s.issueLocked(req.ID, &req.Profile, req.Mode, false, issueOptions{expiresAt: req.ExpiresAt, displayName: req.DisplayName})
	if err != nil {
		return nil, "", err
	}
	s.recordMint(ctx, a, req.ID, issued)
	all, err := s.records()
	if err != nil {
		return nil, "", err
	}
	if rec := clientRecord(all, req.ID); rec != nil {
		return s.view(rec), issued.Secret, nil
	}
	return nil, issued.Secret, nil
}

func (s *ClientsService) recordMint(ctx context.Context, a Actor, clientID string, issued *connect.IssuedCredential) {
	s.writeChange(ctx, a, changeRecord{
		change: profile.ChangeAssign, profile: issued.Profile, previousProfile: "",
		clientID: clientID, tokenName: issued.TokenName,
		diff: map[string]interface{}{"mode": map[string]interface{}{"from": "", "to": issued.Mode}},
	})
	if s.publish != nil {
		s.publish(newEvent(EventTypeClientBindingChanged, map[string]any{
			"client_id": clientID, "token_name": issued.TokenName, "profile": issued.Profile,
			"previous_profile": "", "mode": issued.Mode,
		}))
	}
}

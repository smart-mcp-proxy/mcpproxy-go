//go:build server

package broker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// defaultRefreshThreshold is how close to expiry a cached credential may be
// before the resolver proactively refreshes it. A credential expiring within
// this window is treated as stale (FR-013).
const defaultRefreshThreshold = 60 * time.Second

// Sentinel errors returned by the resolver. They are deliberately coarse and
// secret-free so they can be surfaced to callers and audited (FR-014/FR-019).
var (
	// ErrUnauthenticated is returned when Resolve is called without a user
	// identity. Brokering is strictly per-user; an anonymous caller is rejected
	// before any store or upstream access (FR-014).
	ErrUnauthenticated = errors.New("credential resolver: unauthenticated caller")

	// ErrNoCredential is returned when no per-user credential can be produced and
	// no actionable connect flow is available. There is deliberately no shared or
	// static fallback (FR-014).
	ErrNoCredential = errors.New("credential resolver: no per-user credential available")

	// ErrBrokerNotConfigured is returned when the target server has no auth_broker
	// block. Such upstreams are not brokered and behave exactly as today.
	ErrBrokerNotConfigured = errors.New("credential resolver: server has no auth_broker configuration")
)

// Exchanger mints an upstream credential by exchanging the user's stored IdP
// subject token (token_exchange / entra_obo). *TokenExchanger satisfies it.
type Exchanger interface {
	Exchange(ctx context.Context, userID, serverKey string, cfg *config.AuthBrokerConfig) (*UpstreamCredential, error)
}

// Connector drives the per-user OAuth connect flow (Path B). *OAuthConnector
// satisfies it. The resolver uses Refresh to renew a near-expiry connect-flow
// credential and BuildAuthorizationURL to produce an actionable connect URL
// when the user has not yet connected the upstream.
type Connector interface {
	ServerKey() string
	BuildAuthorizationURL(userID string) (authURL, state string, err error)
	Refresh(ctx context.Context, userID string) (*UpstreamCredential, error)
}

// ConnectorProvider resolves the per-upstream OAuthConnector for a server. The
// REST layer (T8) supplies an implementation that assembles a ConnectorConfig
// from the server's auth_broker block plus the gateway's callback URL. It is
// only consulted for oauth_connect-mode upstreams.
type ConnectorProvider interface {
	ConnectorFor(server *config.ServerConfig) (Connector, error)
}

// NotConnectedError is returned when an oauth_connect upstream cannot produce a
// usable per-user credential and the user must (re)consent. It carries the
// authorize URL the caller redirects the user to (FR-013, actionable error) and
// a Reason that distinguishes a first-time connect from an expired credential
// whose refresh failed (so callers do not tell an already-connected user they
// have "never connected").
type NotConnectedError struct {
	ServerName string
	ConnectURL string
	Reason     string
}

func (e *NotConnectedError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("credential resolver: upstream %q requires connection (%s); connect at: %s",
			e.ServerName, e.Reason, e.ConnectURL)
	}
	return fmt.Sprintf("credential resolver: upstream %q is not connected for this user; connect at: %s",
		e.ServerName, e.ConnectURL)
}

// PolicyDecision is the verdict of the policy-decision seam evaluated before a
// resolved credential is returned. Allow=false blocks the injection.
type PolicyDecision struct {
	Allow  bool
	Reason string
}

// PolicyInput is the context handed to the policy seam.
type PolicyInput struct {
	UserID     string
	ServerName string
	ServerKey  string
	Credential *UpstreamCredential
}

// PolicyHook is the policy-decision seam (FR-015). No policy engine ships now;
// the resolver defaults to an allow-all hook. A future engine implements this
// interface without changing the resolver.
type PolicyHook interface {
	Evaluate(ctx context.Context, in PolicyInput) (PolicyDecision, error)
}

// PolicyHookFunc adapts a function to the PolicyHook interface.
type PolicyHookFunc func(ctx context.Context, in PolicyInput) (PolicyDecision, error)

// Evaluate implements PolicyHook.
func (f PolicyHookFunc) Evaluate(ctx context.Context, in PolicyInput) (PolicyDecision, error) {
	return f(ctx, in)
}

// allowAllPolicy is the default seam implementation: it permits every
// injection. It exists so the resolver always has a non-nil hook (FR-015).
type allowAllPolicy struct{}

func (allowAllPolicy) Evaluate(_ context.Context, _ PolicyInput) (PolicyDecision, error) {
	return PolicyDecision{Allow: true}, nil
}

// PolicyDeniedError is returned when the policy seam blocks a resolved
// credential from being injected.
type PolicyDeniedError struct {
	ServerName string
	Reason     string
}

func (e *PolicyDeniedError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("credential resolver: policy denied credential for %q: %s", e.ServerName, e.Reason)
	}
	return fmt.Sprintf("credential resolver: policy denied credential for %q", e.ServerName)
}

// ResolverDeps are the collaborators a CredentialResolver needs. Store and
// Exchanger are required for token-exchange upstreams; Connectors is required
// only for oauth_connect upstreams. Policy and Logger are optional.
type ResolverDeps struct {
	Store            CredentialStore
	Exchanger        Exchanger
	Connectors       ConnectorProvider
	Policy           PolicyHook
	Logger           *zap.Logger
	RefreshThreshold time.Duration
}

// CredentialResolver produces the per-user upstream credential to inject on a
// proxied request. It applies a strict per-user-only ordering (FR-013/FR-014):
//
//  1. a valid cached per-user credential (refreshed if near-expiry);
//  2. else a freshly token-exchanged / OBO credential from the stored IdP
//     subject token;
//  3. else, for oauth_connect upstreams the user has not connected, an
//     actionable NotConnectedError carrying the connect URL;
//  4. else ErrNoCredential.
//
// There is no shared or static fallback. Concurrent acquisitions for the same
// (user, server) are coalesced via single-flight so the upstream authorization
// server is not hit with duplicate flows.
type CredentialResolver struct {
	store     CredentialStore
	exchanger Exchanger
	conns     ConnectorProvider
	policy    PolicyHook
	logger    *zap.Logger

	refreshThreshold time.Duration
	group            singleflight.Group
}

// NewCredentialResolver constructs a resolver from its dependencies, applying
// defaults for the optional fields.
func NewCredentialResolver(deps ResolverDeps) *CredentialResolver {
	logger := deps.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	policy := deps.Policy
	if policy == nil {
		policy = allowAllPolicy{}
	}
	threshold := deps.RefreshThreshold
	if threshold <= 0 {
		threshold = defaultRefreshThreshold
	}
	return &CredentialResolver{
		store:            deps.Store,
		exchanger:        deps.Exchanger,
		conns:            deps.Connectors,
		policy:           policy,
		logger:           logger.Named("credential-resolver"),
		refreshThreshold: threshold,
	}
}

// Resolve returns the per-user credential to inject for (userID, server),
// applying the ordering described on CredentialResolver. The policy seam is
// evaluated per call after acquisition; credential acquisition itself is
// coalesced per (user, server) via single-flight.
func (r *CredentialResolver) Resolve(ctx context.Context, userID string, server *config.ServerConfig) (*UpstreamCredential, error) {
	if userID == "" {
		return nil, ErrUnauthenticated
	}
	if server == nil || server.AuthBroker == nil {
		return nil, ErrBrokerNotConfigured
	}
	if r.store == nil || !r.store.Enabled() {
		return nil, ErrStoreDisabled
	}

	serverKey := oauth.GenerateServerKey(server.Name, server.URL)

	// Coalesce concurrent acquisitions for the same (user, server) so duplicate
	// upstream token flows are not triggered (reuse the single-flight pattern).
	//
	// The flight runs the acquisition once for every co-pending caller. Detach
	// the caller's cancellation with context.WithoutCancel so the in-flight
	// acquisition is not aborted — and its error broadcast to all waiters — just
	// because whichever caller happened to start the flight cancelled (client
	// disconnect, timeout). Per-caller cancellation still applies below at the
	// policy/return layer, which uses the caller's original ctx.
	flightKey := userID + "\x00" + serverKey
	v, err, _ := r.group.Do(flightKey, func() (interface{}, error) {
		return r.acquire(context.WithoutCancel(ctx), userID, serverKey, server)
	})
	if err != nil {
		return nil, err
	}
	cred, ok := v.(*UpstreamCredential)
	if !ok || cred == nil {
		return nil, ErrNoCredential
	}

	// Policy-decision seam: evaluated per call, before the credential is handed
	// to the caller (FR-015). Default hook allows everything.
	decision, perr := r.policy.Evaluate(ctx, PolicyInput{
		UserID:     userID,
		ServerName: server.Name,
		ServerKey:  serverKey,
		Credential: cred,
	})
	if perr != nil {
		return nil, fmt.Errorf("credential resolver: policy evaluation failed: %w", perr)
	}
	if !decision.Allow {
		return nil, &PolicyDeniedError{ServerName: server.Name, Reason: decision.Reason}
	}
	return cred, nil
}

// acquire runs the per-user-only ordering for a single (user, server). It is
// invoked inside the single-flight group.
//
// Acquisition and refresh share a path per mode so a near-expiry cache miss does
// not trigger a redundant double acquisition. The Exchanger (T4) and Connector
// (T5) persist their results into the store themselves, so the resolver never
// calls store.Put — it only reads the cache via store.Get.
func (r *CredentialResolver) acquire(ctx context.Context, userID, serverKey string, server *config.ServerConfig) (*UpstreamCredential, error) {
	cfg := server.AuthBroker

	// 1. Serve a still-valid, not-near-expiry cached credential directly.
	cached, err := r.store.Get(userID, serverKey)
	hasCache := err == nil && cached != nil
	switch {
	case hasCache:
		if cached.IsValid() && !cached.ExpiresWithin(r.refreshThreshold) {
			return cached, nil
		}
		// Stale / near-expiry: renewed by the per-mode path below.
	case errors.Is(err, ErrNotFound):
		// No cache: acquired by the per-mode path below.
	default:
		// Unexpected store error (not "missing"): surface it.
		return nil, fmt.Errorf("credential resolver: load cached credential: %w", err)
	}

	switch cfg.Mode {
	case config.AuthBrokerModeTokenExchange, config.AuthBrokerModeEntraOBO:
		// 2. Token-exchange / OBO: the first-acquisition and refresh paths are
		// identical (re-mint from the stored IdP subject token), so a single
		// Exchange call covers both the cache-miss and near-expiry cases.
		if r.exchanger == nil {
			return nil, fmt.Errorf("credential resolver: no token exchanger configured for mode %q", cfg.Mode)
		}
		return r.exchanger.Exchange(ctx, userID, serverKey, cfg)

	case config.AuthBrokerModeOAuthConnect:
		conn, cerr := r.connectorFor(server)
		if cerr != nil {
			return nil, cerr
		}
		// A cached connect-flow credential means the user already connected:
		// renew transparently via the stored refresh token. Only when that
		// refresh fails do we ask the (already-connected) user to reconnect.
		if hasCache && cached.RefreshToken != "" {
			refreshed, rerr := conn.Refresh(ctx, userID)
			if rerr == nil {
				return refreshed, nil
			}
			r.logger.Warn("connect-flow credential refresh failed; user must reconnect",
				zap.String("server", server.Name), zap.Error(rerr))
			return nil, r.notConnected(conn, server, userID, "stored credential expired and refresh failed; reconnect required")
		}
		// 3. Never connected, or connected without a usable refresh token and now
		// expired — both require (re)consent through the connect flow.
		reason := "not connected"
		if hasCache {
			reason = "stored credential expired; reconnect required"
		}
		return nil, r.notConnected(conn, server, userID, reason)

	default:
		// 4. No recognised acquisition strategy and no per-user credential.
		return nil, ErrNoCredential
	}
}

// notConnected builds the actionable NotConnectedError carrying the upstream
// authorize URL the caller must redirect the user to, tagged with reason.
func (r *CredentialResolver) notConnected(conn Connector, server *config.ServerConfig, userID, reason string) error {
	authURL, _, aerr := conn.BuildAuthorizationURL(userID)
	if aerr != nil {
		return fmt.Errorf("credential resolver: build connect URL: %w", aerr)
	}
	return &NotConnectedError{ServerName: server.Name, ConnectURL: authURL, Reason: reason}
}

// connectorFor resolves the per-upstream connector, guarding against a missing
// provider (only oauth_connect upstreams need one).
func (r *CredentialResolver) connectorFor(server *config.ServerConfig) (Connector, error) {
	if r.conns == nil {
		return nil, fmt.Errorf("credential resolver: no connector provider configured for oauth_connect upstream %q", server.Name)
	}
	conn, err := r.conns.ConnectorFor(server)
	if err != nil {
		return nil, fmt.Errorf("credential resolver: resolve connector: %w", err)
	}
	return conn, nil
}

// Compile-time assertions that the concrete broker types satisfy the resolver's
// collaborator interfaces.
var (
	_ Exchanger = (*TokenExchanger)(nil)
	_ Connector = (*OAuthConnector)(nil)
)

//go:build server

package broker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"go.uber.org/zap"
)

// defaultStateTTL bounds how long a pending connect flow may sit between the
// authorize redirect and the callback. After this window the state is rejected
// and garbage-collected (confused-deputy / replay hardening, FR-011).
const defaultStateTTL = 10 * time.Minute

// connectFlowObtainedVia tags credentials acquired through the per-user OAuth
// connect flow (Path B). It distinguishes them from token-exchange creds in
// the store and in audit output (FR-012).
const connectFlowObtainedVia = "connect_flow"

// ConnectorConfig is the resolved, per-upstream configuration the OAuthConnector
// needs to drive a standard authorization-code + PKCE flow against an upstream
// authorization server. It is assembled by callers (the REST layer, T8) from
// the per-server config.AuthBrokerConfig plus the gateway's own callback URL.
type ConnectorConfig struct {
	// ServerName / ServerURL identify the upstream and derive the store's
	// serverKey via oauth.GenerateServerKey (matches the existing scheme).
	ServerName string
	ServerURL  string
	// AuthorizationEndpoint is the upstream AS authorize URL the user is
	// redirected to for consent.
	AuthorizationEndpoint string
	// TokenEndpoint is the upstream AS token URL used to exchange the auth code
	// and to refresh.
	TokenEndpoint string
	// ClientID / ClientSecret authenticate the gateway to the upstream AS. A
	// public client may leave ClientSecret empty (PKCE still protects the code).
	ClientID     string
	ClientSecret string
	// Scopes requested from the upstream AS.
	Scopes []string
	// RedirectURI is the gateway's own callback URL registered with the AS.
	RedirectURI string
	// Resource is the optional RFC 8707 audience the resulting token is scoped
	// to.
	Resource string
}

// validate checks the fields required to drive a connect flow.
func (c ConnectorConfig) validate() error {
	switch {
	case c.AuthorizationEndpoint == "":
		return fmt.Errorf("oauth connector: authorization_endpoint is required")
	case c.TokenEndpoint == "":
		return fmt.Errorf("oauth connector: token_endpoint is required")
	case c.ClientID == "":
		return fmt.Errorf("oauth connector: client_id is required")
	case c.RedirectURI == "":
		return fmt.Errorf("oauth connector: redirect_uri is required")
	}
	return nil
}

// pendingFlow tracks one in-flight connect flow between the authorize redirect
// and the callback. It binds the opaque state to the initiating user and the
// PKCE verifier so the callback can be matched back to its initiator
// (per-user state tracking, FR-011).
type pendingFlow struct {
	userID    string
	verifier  string
	createdAt time.Time
}

// OAuthConnector implements Path B of spec 074: a per-user, authorization-code
// + PKCE connect flow against an upstream authorization server that does not
// support token exchange. It issues authorize URLs, handles callbacks, persists
// the resulting per-user upstream credential encrypted (ObtainedVia=connect_flow),
// and refreshes transparently via the refresh token.
//
// One connector instance serves a single upstream; the store, however, is
// shared and isolates records per user.
type OAuthConnector struct {
	store     CredentialStore
	cfg       ConnectorConfig
	serverKey string
	client    *http.Client
	logger    *zap.Logger

	mu      sync.Mutex
	pending map[string]*pendingFlow

	// now and stateTTL are injectable for tests.
	now      func() time.Time
	stateTTL time.Duration
}

// NewOAuthConnector builds a connector for one upstream. It returns an error if
// the configuration is missing fields required to run a connect flow.
func NewOAuthConnector(store CredentialStore, cfg ConnectorConfig, logger *zap.Logger) (*OAuthConnector, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &OAuthConnector{
		store:     store,
		cfg:       cfg,
		serverKey: oauth.GenerateServerKey(cfg.ServerName, cfg.ServerURL),
		client:    &http.Client{Timeout: 30 * time.Second},
		logger:    logger.Named("oauth-connector").With(zap.String("server", cfg.ServerName)),
		pending:   make(map[string]*pendingFlow),
		now:       time.Now,
		stateTTL:  defaultStateTTL,
	}, nil
}

// ServerKey returns the store key this connector persists credentials under.
func (c *OAuthConnector) ServerKey() string { return c.serverKey }

// BuildAuthorizationURL starts a connect flow for userID. It generates a PKCE
// verifier/challenge and an opaque state, records the pending flow, and returns
// the upstream authorize URL (to which the gateway redirects the user) plus the
// state token. Explicit per-user consent at the AS plus the unguessable state
// is the confused-deputy avoidance required by FR-011.
func (c *OAuthConnector) BuildAuthorizationURL(userID string) (authURL, state string, err error) {
	if userID == "" {
		return "", "", fmt.Errorf("oauth connector: userID is required")
	}
	verifier, err := randomURLSafe(32)
	if err != nil {
		return "", "", fmt.Errorf("oauth connector: generate verifier: %w", err)
	}
	state, err = randomURLSafe(32)
	if err != nil {
		return "", "", fmt.Errorf("oauth connector: generate state: %w", err)
	}
	challenge := codeChallengeS256(verifier)

	c.mu.Lock()
	c.gcExpiredLocked()
	c.pending[state] = &pendingFlow{userID: userID, verifier: verifier, createdAt: c.now()}
	c.mu.Unlock()

	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.cfg.ClientID},
		"redirect_uri":          {c.cfg.RedirectURI},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	if len(c.cfg.Scopes) > 0 {
		params.Set("scope", strings.Join(c.cfg.Scopes, " "))
	}
	if c.cfg.Resource != "" {
		params.Set("resource", c.cfg.Resource)
	}

	sep := "?"
	if strings.Contains(c.cfg.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return c.cfg.AuthorizationEndpoint + sep + params.Encode(), state, nil
}

// Complete handles a successful callback. It validates state (must be a known,
// unexpired, one-time pending flow), exchanges the code for an upstream token
// using the bound PKCE verifier, and stores the per-user credential encrypted
// with ObtainedVia=connect_flow. The state is consumed regardless of outcome so
// it cannot be replayed.
func (c *OAuthConnector) Complete(ctx context.Context, state, code string) (*UpstreamCredential, error) {
	flow, err := c.consume(state)
	if err != nil {
		return nil, err
	}
	if code == "" {
		return nil, fmt.Errorf("oauth connector: empty authorization code")
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.cfg.RedirectURI},
		"client_id":     {c.cfg.ClientID},
		"code_verifier": {flow.verifier},
	}
	tok, err := c.postToken(ctx, form)
	if err != nil {
		return nil, err
	}

	cred := c.credentialFromToken(tok, "")
	if err := c.store.Put(flow.userID, c.serverKey, cred); err != nil {
		return nil, fmt.Errorf("oauth connector: persist credential: %w", err)
	}
	c.logger.Info("stored per-user upstream credential via connect flow",
		zap.String("user_id", flow.userID))
	return cred, nil
}

// Deny handles a denied or failed callback (e.g. the AS returned
// error=access_denied). It clears the pending flow and stores nothing.
func (c *OAuthConnector) Deny(state, reason string) error {
	c.mu.Lock()
	delete(c.pending, state)
	c.mu.Unlock()
	c.logger.Info("connect flow denied by user", zap.String("reason", reason))
	return nil
}

// Refresh mints a fresh access token for userID from the stored refresh token
// and re-persists the credential. It is the transparent auto-refresh path
// (FR-012). An absent or empty refresh token is an error.
func (c *OAuthConnector) Refresh(ctx context.Context, userID string) (*UpstreamCredential, error) {
	existing, err := c.store.Get(userID, c.serverKey)
	if err != nil {
		return nil, fmt.Errorf("oauth connector: load credential: %w", err)
	}
	if existing.RefreshToken == "" {
		return nil, fmt.Errorf("oauth connector: no refresh token for user %q", userID)
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {existing.RefreshToken},
		"client_id":     {c.cfg.ClientID},
	}
	if len(c.cfg.Scopes) > 0 {
		form.Set("scope", strings.Join(c.cfg.Scopes, " "))
	}
	if c.cfg.Resource != "" {
		form.Set("resource", c.cfg.Resource)
	}
	tok, err := c.postToken(ctx, form)
	if err != nil {
		return nil, err
	}

	// Preserve the prior refresh token when the AS does not rotate it.
	cred := c.credentialFromToken(tok, existing.RefreshToken)
	if err := c.store.Put(userID, c.serverKey, cred); err != nil {
		return nil, fmt.Errorf("oauth connector: persist refreshed credential: %w", err)
	}
	c.logger.Debug("refreshed per-user upstream credential", zap.String("user_id", userID))
	return cred, nil
}

// oauthTokenResponse is the subset of the OAuth token endpoint response we
// consume. Named distinctly from token_exchanger.go's tokenResponse (RFC 8693)
// to avoid a same-package redeclaration in the broker package.
type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// postToken sends a form-encoded request to the upstream token endpoint and
// decodes the response. The gateway authenticates with client_secret when set.
func (c *OAuthConnector) postToken(ctx context.Context, form url.Values) (*oauthTokenResponse, error) {
	if c.cfg.ClientSecret != "" {
		form.Set("client_secret", c.cfg.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth connector: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth connector: token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("oauth connector: read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth connector: token endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tok oauthTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("oauth connector: parse token response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("oauth connector: token response missing access_token")
	}
	return &tok, nil
}

// credentialFromToken maps a token response into a stored UpstreamCredential.
// fallbackRefresh is used when the response omits a refresh token (so a
// non-rotating AS does not drop the user's refresh capability).
func (c *OAuthConnector) credentialFromToken(tok *oauthTokenResponse, fallbackRefresh string) *UpstreamCredential {
	tokenType := tok.TokenType
	if tokenType == "" {
		tokenType = "Bearer"
	}
	refresh := tok.RefreshToken
	if refresh == "" {
		refresh = fallbackRefresh
	}
	var expiresAt time.Time
	if tok.ExpiresIn > 0 {
		expiresAt = c.now().Add(time.Duration(tok.ExpiresIn) * time.Second).UTC()
	}
	var scopes []string
	if tok.Scope != "" {
		scopes = strings.Fields(tok.Scope)
	}
	return &UpstreamCredential{
		Type:         "oauth2",
		AccessToken:  tok.AccessToken,
		RefreshToken: refresh,
		ExpiresAt:    expiresAt,
		Scopes:       scopes,
		TokenType:    tokenType,
		Audience:     c.cfg.Resource,
		ObtainedVia:  connectFlowObtainedVia,
		UpdatedAt:    c.now().UTC(),
	}
}

// consume validates and removes a pending flow by state. It rejects unknown and
// expired states; a returned flow has been deleted so state is single-use.
func (c *OAuthConnector) consume(state string) (*pendingFlow, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	flow, ok := c.pending[state]
	if !ok {
		return nil, fmt.Errorf("oauth connector: unknown or already-used state")
	}
	delete(c.pending, state)
	if c.now().Sub(flow.createdAt) > c.stateTTL {
		return nil, fmt.Errorf("oauth connector: state expired")
	}
	return flow, nil
}

// gcExpiredLocked drops expired pending flows. Caller holds c.mu.
func (c *OAuthConnector) gcExpiredLocked() {
	cutoff := c.now().Add(-c.stateTTL)
	for k, v := range c.pending {
		if v.createdAt.Before(cutoff) {
			delete(c.pending, k)
		}
	}
}

// randomURLSafe returns nBytes of cryptographically-random data, base64url
// (no padding) encoded — suitable for PKCE verifiers and opaque state tokens.
func randomURLSafe(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// codeChallengeS256 computes the PKCE S256 challenge for a verifier.
func codeChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

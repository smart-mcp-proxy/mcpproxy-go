//go:build server

package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// RFC 8693 / Microsoft Entra OAuth grant-type and token-type URNs (FR-007/FR-008).
const (
	grantTypeTokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange"
	grantTypeJWTBearer     = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	tokenTypeAccessToken   = "urn:ietf:params:oauth:token-type:access_token"
	entraRequestedTokenUse = "on_behalf_of"
)

// defaultExchangeTimeout bounds a single token-endpoint round trip.
const defaultExchangeTimeout = 30 * time.Second

// TokenExchanger mints upstream credentials on behalf of a user by exchanging
// that user's stored IdP subject token (T3) at an authorization server, then
// caches the result in the CredentialStore (FR-007/FR-008/FR-009).
//
// Two strategies are supported, selected by AuthBrokerConfig.Mode:
//   - token_exchange: RFC 8693 OAuth 2.0 Token Exchange.
//   - entra_obo:      Microsoft Entra On-Behalf-Of (jwt-bearer + on_behalf_of).
//
// golang.org/x/oauth2 has no native RFC 8693 support (golang/oauth2#409), so
// the exchange request is hand-rolled.
type TokenExchanger struct {
	store      CredentialStore
	httpClient *http.Client
	logger     *zap.Logger
}

// NewTokenExchanger constructs an exchanger over the given credential store. A
// nil httpClient gets a default client with a bounded timeout.
func NewTokenExchanger(store CredentialStore, httpClient *http.Client, logger *zap.Logger) *TokenExchanger {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultExchangeTimeout}
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &TokenExchanger{
		store:      store,
		httpClient: httpClient,
		logger:     logger.Named("token-exchanger"),
	}
}

// tokenResponse is the success body of a token-endpoint response (RFC 8693 §2.2.1
// and the standard OAuth token response used by Entra OBO).
type tokenResponse struct {
	AccessToken     string `json:"access_token"`
	IssuedTokenType string `json:"issued_token_type"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int64  `json:"expires_in"`
	Scope           string `json:"scope"`
	RefreshToken    string `json:"refresh_token"`
}

// tokenErrorResponse is the OAuth 2.0 error body (RFC 6749 §5.2). Only the
// machine-readable error code is ever surfaced; error_description may reflect
// caller-supplied input and is treated as untrusted (never returned to callers).
type tokenErrorResponse struct {
	ErrorCode        string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// Exchange reads the user's stored IdP subject token, performs the configured
// token exchange, caches the resulting credential under (userID, serverKey),
// and returns it. Nothing is cached when the exchange fails.
func (e *TokenExchanger) Exchange(ctx context.Context, userID, serverKey string, cfg *config.AuthBrokerConfig) (*UpstreamCredential, error) {
	if cfg == nil {
		return nil, fmt.Errorf("token exchange: nil auth_broker config")
	}

	// Subject token = the stored IdP token from T3 (keyed by userID alone).
	subject, err := e.store.Get(userID, "")
	if err != nil {
		return nil, fmt.Errorf("token exchange: no IdP subject token for user: %w", err)
	}
	if subject.AccessToken == "" {
		return nil, fmt.Errorf("token exchange: stored IdP subject token is empty")
	}

	form, err := buildExchangeForm(cfg, subject.AccessToken)
	if err != nil {
		return nil, err
	}

	cred, err := e.post(ctx, cfg, form)
	if err != nil {
		return nil, err
	}

	// Cache only on success (FR-009).
	if perr := e.store.Put(userID, serverKey, cred); perr != nil {
		return nil, fmt.Errorf("token exchange: cache credential: %w", perr)
	}
	return cred, nil
}

// buildExchangeForm assembles the POST body for the configured mode.
func buildExchangeForm(cfg *config.AuthBrokerConfig, subjectToken string) (url.Values, error) {
	form := url.Values{}
	scope := strings.Join(cfg.Scopes, " ")

	switch cfg.Mode {
	case config.AuthBrokerModeTokenExchange:
		form.Set("grant_type", grantTypeTokenExchange)
		form.Set("subject_token", subjectToken)
		form.Set("subject_token_type", tokenTypeAccessToken)
		form.Set("requested_token_type", tokenTypeAccessToken)
		if cfg.Resource != "" {
			form.Set("resource", cfg.Resource)
		}
	case config.AuthBrokerModeEntraOBO:
		form.Set("grant_type", grantTypeJWTBearer)
		form.Set("assertion", subjectToken)
		form.Set("requested_token_use", entraRequestedTokenUse)
	default:
		return nil, fmt.Errorf("token exchange: unsupported auth_broker mode %q", cfg.Mode)
	}

	if scope != "" {
		form.Set("scope", scope)
	}
	// Client authentication via client_secret_post.
	if cfg.ClientID != "" {
		form.Set("client_id", cfg.ClientID)
	}
	if cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}
	return form, nil
}

// post executes the token-endpoint round trip and maps the response onto an
// UpstreamCredential. Errors are sanitized: only the HTTP status and OAuth
// error code are surfaced, never the response body or request secrets.
func (e *TokenExchanger) post(ctx context.Context, cfg *config.AuthBrokerConfig, form url.Values) (*UpstreamCredential, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("token exchange: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		// A transport error may embed the endpoint URL but not request secrets.
		return nil, fmt.Errorf("token exchange: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("token exchange: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, e.sanitizedError(resp.StatusCode, body)
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("token exchange: malformed success response (status %d)", resp.StatusCode)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("token exchange: response missing access_token (status %d)", resp.StatusCode)
	}

	return credentialFromResponse(cfg, &tr), nil
}

// sanitizedError maps an authorization-server error response onto a safe error
// that names only the HTTP status and the standard OAuth error code. The
// error_description and raw body are deliberately dropped — they may reflect
// caller input or secrets (FR-008/FR-009).
func (e *TokenExchanger) sanitizedError(status int, body []byte) error {
	var te tokenErrorResponse
	_ = json.Unmarshal(body, &te)
	if te.ErrorCode != "" {
		e.logger.Warn("token exchange rejected by authorization server",
			zap.Int("status", status),
			zap.String("error", te.ErrorCode))
		return fmt.Errorf("token exchange failed: status %d, error %q", status, te.ErrorCode)
	}
	e.logger.Warn("token exchange rejected by authorization server",
		zap.Int("status", status))
	return fmt.Errorf("token exchange failed: status %d", status)
}

// credentialFromResponse converts a successful token response into the stored
// credential model, recording how it was obtained and the requested audience.
func credentialFromResponse(cfg *config.AuthBrokerConfig, tr *tokenResponse) *UpstreamCredential {
	now := time.Now().UTC()
	cred := &UpstreamCredential{
		Type:         "oauth2",
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		TokenType:    tr.TokenType,
		Audience:     cfg.Resource,
		ObtainedVia:  cfg.Mode,
		UpdatedAt:    now,
	}
	if cred.TokenType == "" {
		cred.TokenType = "Bearer"
	}
	if tr.ExpiresIn > 0 {
		cred.ExpiresAt = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	// Prefer the granted scope from the response; fall back to the requested set.
	if granted := strings.Fields(tr.Scope); len(granted) > 0 {
		cred.Scopes = granted
	} else {
		cred.Scopes = cfg.Scopes
	}
	return cred
}

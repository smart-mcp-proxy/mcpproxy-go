package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 113 (113-a): OAuth refresh serialization. The reactive path (the token
// store mcp-go reads before every request) and the proactive path
// (RefreshOAuthTokenDirect) both run their refreshes as flights of
// oauth.DefaultRefreshCoordinator, with the same refresh function.

// isStaticOAuthClient reports whether the client id comes from oauth.client_id.
func (c *Client) isStaticOAuthClient() bool {
	return c.config != nil && c.config.OAuth != nil && c.config.OAuth.ClientID != ""
}

// staticClientSecret is oauth.client_secret (redacted from refresh errors).
func (c *Client) staticClientSecret() string {
	if c.config == nil || c.config.OAuth == nil {
		return ""
	}
	return c.config.OAuth.ClientSecret
}

// extraParamsClientID reports whether oauth.extra_params carries a client_id
// (injected into every token request by oauth.OAuthTransportWrapper).
func (c *Client) extraParamsClientID() bool {
	return c.extraParamValue("client_id") != ""
}

// extraParamValue returns oauth.extra_params[key] (case-insensitive key).
// OAuthTransportWrapper injects extra params into every token request, so a
// client_secret given there is a sent credential too.
func (c *Client) extraParamValue(key string) string {
	if c.config == nil || c.config.OAuth == nil {
		return ""
	}
	for k, v := range c.config.OAuth.ExtraParams {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// oauthRefreshFunc returns the one refresh function both triggers use (FR-003):
// mcp-go's handler.RefreshToken when the live handler has a client id (static
// clients, and DCR clients whose stored registration matches or is unknown),
// otherwise the stored DCR credentials. A refresh is never sent with an empty
// client_id.
func (c *Client) oauthRefreshFunc(handler *transport.OAuthHandler) oauth.RefreshFunc {
	return func(ctx context.Context, rec *storage.OAuthTokenRecord) (*client.Token, error) {
		handlerClientID := handler.GetClientID()
		switch {
		case handlerClientID != "" && (c.isStaticOAuthClient() || rec.ClientID == "" || rec.ClientID == handlerClientID),
			handlerClientID == "" && rec.ClientID == "" && c.extraParamsClientID():
			// The second case: a client id configured only through
			// oauth.extra_params is injected into the token request by
			// oauth.OAuthTransportWrapper, so the request is not sent with
			// an empty client_id.
			// Persists through the handler's token store, whose SaveToken
			// compare-and-swaps against the flight generation in ctx.
			tok, err := handler.RefreshToken(ctx, rec.RefreshToken)
			// mcp-go embeds the provider's error_description or raw body
			// in the error text; scrub it before it reaches logs/events.
			return tok, oauth.ScrubRefreshError(err, rec.RefreshToken, rec.ClientSecret, handler.GetClientSecret(),
				c.staticClientSecret(), c.extraParamValue("client_secret"))
		case rec.ClientID != "" && c.storage != nil:
			return c.refreshWithStoredCredentials(ctx, handler, rec)
		default:
			return nil, oauth.ErrNoClientCredentials
		}
	}
}

// refreshWithStoredCredentials refreshes with the DCR client stored on the
// record and persists the result through one read-modify-write transaction
// that only applies while the stored grant is still the flight's generation
// (FR-006, FR-006a). It never writes back a record read before the network call.
func (c *Client) refreshWithStoredCredentials(ctx context.Context, handler *transport.OAuthHandler, rec *storage.OAuthTokenRecord) (*client.Token, error) {
	c.logger.Info("Using stored DCR credentials for token refresh",
		zap.String("server", c.config.Name),
		zap.String("client_id", rec.ClientID[:min(8, len(rec.ClientID))]+"..."))

	metadata, err := handler.GetServerMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server metadata for %s: %w", c.config.Name, err)
	}
	if metadata.TokenEndpoint == "" {
		return nil, fmt.Errorf("token endpoint not found in server metadata for %s", c.config.Name)
	}

	newToken, err := c.refreshTokenWithStoredCredentials(ctx, metadata.TokenEndpoint, rec)
	if err != nil {
		return nil, err
	}

	serverKey := oauth.GenerateServerKey(c.config.Name, c.config.URL)
	gen, inFlight := oauth.FlightGeneration(ctx)
	discarded := false
	err = c.storage.UpdateOAuthToken(serverKey, func(cur *storage.OAuthTokenRecord) error {
		if inFlight && !oauth.GenerationMatches(cur, gen) {
			discarded = true
			return storage.ErrSkipOAuthTokenUpdate
		}
		cur.AccessToken = newToken.AccessToken
		if newToken.RefreshToken != "" {
			cur.RefreshToken = newToken.RefreshToken
		}
		if newToken.TokenType != "" {
			cur.TokenType = newToken.TokenType
		}
		cur.ExpiresAt = newToken.ExpiresAt
		// Ensure DisplayName is set for legacy tokens that predate the DisplayName field.
		// Without this, CleanupOrphanedOAuthTokens could misclassify the token as orphaned.
		if cur.DisplayName == "" {
			cur.DisplayName = c.config.Name
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to save refreshed token for %s: %w", c.config.Name, err)
	}

	refreshToken := newToken.RefreshToken
	if refreshToken == "" {
		refreshToken = rec.RefreshToken
	}
	tok := &client.Token{
		AccessToken:  newToken.AccessToken,
		RefreshToken: refreshToken,
		TokenType:    newToken.TokenType,
		ExpiresAt:    newToken.ExpiresAt,
	}
	if discarded {
		c.logger.Info("Discarded a refresh result superseded by a newer grant",
			zap.String("server", c.config.Name))
		return tok, nil
	}

	oauth.DefaultRefreshCoordinator().NoteTokenSaved(serverKey)
	oauth.GetTokenStoreManager().NotifyTokenSaved(c.config.Name, newToken.ExpiresAt)
	c.logger.Info("OAuth token refreshed via stored DCR credentials",
		zap.String("server", c.config.Name),
		zap.Time("new_expiry", newToken.ExpiresAt))
	return tok, nil
}

// oauthRefreshRequest builds the coordinator request for this server; the
// caller sets ObservedRefreshToken and Trigger.
func (c *Client) oauthRefreshRequest(serverKey string, handler *transport.OAuthHandler) oauth.RefreshRequest {
	return oauth.RefreshRequest{
		Key:          serverKey,
		ServerName:   c.config.Name,
		Load:         func() (*storage.OAuthTokenRecord, error) { return c.storage.GetOAuthToken(serverKey) },
		Refresh:      c.oauthRefreshFunc(handler),
		StaticClient: c.isStaticOAuthClient(),
		ClearClient: func(expectedClientID, expectedRefreshToken string) (bool, error) {
			return c.storage.ClearOAuthClientCredentialsIf(serverKey, expectedClientID, expectedRefreshToken)
		},
	}
}

// bindOAuthRefresher binds the live mcp-go OAuth handler's refresh function to
// the token store handed to mcp-go (Spec 113 FR-002), so the store refreshes
// through the coordinator and mcp-go never runs its own unserialized refresh.
// It reads c.client without locking: callers hold c.mu (Connect paths) or own
// the client they just assigned.
func (c *Client) bindOAuthRefresher(oauthConfig *client.OAuthConfig) {
	if oauthConfig == nil || oauthConfig.TokenStore == nil {
		return
	}
	handler := extractOAuthHandler(c.client)
	if handler == nil {
		return
	}
	oauth.BindRefresher(oauthConfig.TokenStore, oauth.RefreshBinding{
		Refresh:      c.oauthRefreshFunc(handler),
		StaticClient: c.isStaticOAuthClient(),
	})
}

// annotateCodeExchangeError applies the FR-009 one-re-registration rule to an
// authorization-code exchange failure of the login flow.
// handler is the OAuth handler that ran the exchange (its client id).
func (c *Client) annotateCodeExchangeError(handler *transport.OAuthHandler, err error) error {
	if err == nil {
		return nil
	}
	clientID := ""
	if handler != nil {
		clientID = handler.GetClientID()
	}
	return oauth.DefaultRefreshCoordinator().AnnotateCodeExchangeError(
		oauth.GenerateServerKey(c.config.Name, c.config.URL), c.isStaticOAuthClient(), clientID, err)
}

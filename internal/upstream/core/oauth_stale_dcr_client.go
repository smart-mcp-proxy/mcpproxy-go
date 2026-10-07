package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	uptransport "github.com/mark3labs/mcp-go/client/transport"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// authorizeProbeHTTPClient is the HTTP client recoverStaleDCRClient probes
// the authorization endpoint with; nil uses a default client. Tests override
// it.
var authorizeProbeHTTPClient *http.Client

// applyExtraParamsToAuthURL appends the extra OAuth parameters (RFC 8707
// resource, oauth.extra_params, ...) to an authorization URL. extraParams holds
// both the auto-detected values (CreateOAuthConfigWithExtraParams) and the
// manual config. Shared by every authorization-URL emission path (issue #271).
// On a parse failure the URL is returned unchanged.
func (c *Client) applyExtraParamsToAuthURL(authURL string, extraParams map[string]string) string {
	if len(extraParams) == 0 {
		return authURL
	}
	parsedURL, err := url.Parse(authURL)
	if err != nil {
		c.logger.Warn("Failed to parse authorization URL for extra params",
			zap.String("server", c.config.Name),
			logSafeErrorField(err))
		return authURL
	}
	query := parsedURL.Query()
	for key, value := range extraParams {
		query.Set(key, value)
		c.logger.Debug("Added extra OAuth parameter to authorization URL",
			zap.String("server", c.config.Name),
			zap.String("key", key),
			zap.String("value", oauth.AuditRedaction.ExtraParamValue(key, value)))
	}
	parsedURL.RawQuery = query.Encode()
	c.logger.Info("✅ Appended extra OAuth parameters to authorization URL",
		zap.String("server", c.config.Name),
		zap.Int("extra_params_count", len(extraParams)))
	return parsedURL.String()
}

// persistDCRRegistration stores a client registration obtained by Dynamic
// Client Registration together with the callback port and exact redirect URI
// it was registered for (Spec 022), so later logins and token refreshes reuse
// it. It returns the callback port it stored, or errDCRRegistrationSuperseded
// when another login stored a registration first.
func (c *Client) persistDCRRegistration(clientID, clientSecret string) (int, error) {
	if c.storage == nil || clientID == "" {
		return 0, nil
	}
	serverKey := oauth.GenerateServerKey(c.config.Name, c.config.URL)
	var callbackPort int
	var redirectURI string
	if callbackServer, exists := oauth.GetCallbackServer(c.config.Name); exists {
		callbackPort = callbackServer.Port
		redirectURI = callbackServer.RedirectURI
	}
	saved, err := c.storage.SaveOAuthClientCredentialsIfUnset(serverKey, clientID, clientSecret, callbackPort, redirectURI)
	if err != nil {
		return callbackPort, err
	}
	if !saved {
		return callbackPort, errDCRRegistrationSuperseded
	}
	return callbackPort, nil
}

// errDCRRegistrationSuperseded: another login stored its own client
// registration while this login's DCR call was in flight. Every DCR in this
// file runs only when storage held no client id, so the save is conditional
// on that still being true; the stored registration wins, and this flow must
// stop rather than continue with a client storage no longer holds (its grant
// would later be saved next to the other login's client_id).
var errDCRRegistrationSuperseded = errors.New("another sign-in stored a new OAuth client registration while this one was registering")

// supersededDCRFlowError is returned by the ordinary DCR branch of every
// authorize-URL path when errDCRRegistrationSuperseded fires.
func (c *Client) supersededDCRFlowError(correlationID string) error {
	return c.signInAgainFlowError(correlationID, fmt.Sprintf("Server '%s': %v", c.config.Name, errDCRRegistrationSuperseded))
}

// signInAgainFlowError reports a stale-client recovery that stopped without
// handing out a URL; the next sign-in starts from what storage now holds.
func (c *Client) signInAgainFlowError(correlationID, message string) error {
	return scrubbedFlowError(&contracts.OAuthFlowError{
		Success:       false,
		ErrorType:     contracts.OAuthErrorFlowFailed,
		ErrorCode:     contracts.OAuthCodeFlowFailed,
		ServerName:    c.config.Name,
		CorrelationID: correlationID,
		Message:       message,
		Details:       &contracts.OAuthErrorDetails{ServerURL: c.logSafeURL()},
		Suggestion:    "Sign in again; a fresh client registration will be used.",
		DebugHint:     fmt.Sprintf("For logs: mcpproxy upstream logs %s", c.config.Name),
	})
}

// registerOAuthClientSafely runs Dynamic Client Registration on the handler,
// converting a panic (mcp-go dereferences missing server metadata) into an
// error.
func (c *Client) registerOAuthClientSafely(ctx context.Context, oauthHandler *uptransport.OAuthHandler) (regErr error) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Warn("OAuth RegisterClient panicked - server metadata missing or malformed",
				zap.String("server", c.config.Name),
				zap.Any("panic", r))
			regErr = fmt.Errorf("server does not support dynamic client registration: metadata missing")
		}
	}()
	return oauthHandler.RegisterClient(ctx, "mcpproxy-go")
}

// authorizationURLSafely builds the authorization URL, converting a panic into
// an error.
func (c *Client) authorizationURLSafely(ctx context.Context, oauthHandler *uptransport.OAuthHandler, state, codeChallenge string) (authURL string, err error) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("GetAuthorizationURL panicked",
				zap.String("server", c.config.Name),
				zap.Any("panic", r))
			err = fmt.Errorf("internal error (panic recovered): %v", r)
		}
	}()
	return oauthHandler.GetAuthorizationURL(ctx, state, codeChallenge)
}

// recoverStaleDCRClient guards every authorization-URL emission path
// (getAuthorizationURLQuick, handleOAuthAuthorization,
// handleOAuthAuthorizationWithResult) against a persisted Dynamic Client
// Registration that the authorization server has since deleted.
//
// Cloudflare's MCP OAuth servers delete DCR clients; the persisted client_id
// was reused forever, the browser landed on HTTP 400
// {"error":"invalid_request","error_description":"Invalid client_id"}, and the
// server never redirected back, so the login hung with no signal. The token
// endpoint's equivalent (invalid_client) is handled on the refresh path by
// Spec 113-a; this is the login-path counterpart.
//
// It acts only when persistedClientID is a DCR client_id reused from storage —
// never an operator static oauth.client_id, never one registered moments ago
// in this same attempt (callers pass "" then), and never when the final URL
// carries a different client_id (oauth.extra_params override). authURL must be
// the FINAL URL (extra params applied). It probes authURL once without
// following redirects; when the server identifies the client as unknown it
// compare-and-clears the stored registration, re-registers exactly once,
// persists the new registration, rebuilds the URL and probes it once more.
// If the fresh client is rejected too, a structured error is returned (no
// loop). Every probe failure is fail-open: authURL is returned unchanged.
func (c *Client) recoverStaleDCRClient(ctx context.Context, oauthHandler *uptransport.OAuthHandler, persistedClientID, authURL, state, codeChallenge string, extraParams map[string]string, correlationID string) (string, error) {
	if persistedClientID == "" || oauthHandler == nil {
		return authURL, nil
	}
	if c.config.OAuth != nil && c.config.OAuth.ClientID != "" {
		return authURL, nil // operator static client_id: never probed or cleared
	}
	if _, overridden := extraParams["client_id"]; overridden {
		// An operator-supplied client_id in oauth.extra_params is re-applied
		// over every rebuilt URL, so a re-registration could never take effect
		// (and a second probe would test the old id again).
		return authURL, nil
	}
	if oauthHandler.GetClientID() != persistedClientID || authURLClientID(authURL) != persistedClientID {
		return authURL, nil
	}

	probe := oauth.ProbeAuthorizationClient(ctx, authorizeProbeHTTPClient, authURL)
	if !probe.ClientRejected {
		if probe.Err != nil {
			c.logger.Debug("Authorization endpoint probe failed - proceeding with the persisted DCR client_id",
				zap.String("server", c.config.Name),
				logSafeErrorField(probe.Err))
		}
		return authURL, nil
	}
	rejection := oauth.ScrubUpstreamText(probe.Summary())

	c.logger.Warn("⚠️ Authorization server rejected the persisted DCR client_id - clearing it and re-registering once",
		zap.String("server", c.config.Name),
		zap.String("correlation_id", correlationID),
		zap.String("old_client_id", persistedClientID),
		zap.String("authorize_response", rejection))

	serverKey := oauth.GenerateServerKey(c.config.Name, c.config.URL)
	if c.storage != nil {
		cleared, clearErr := c.storage.ClearOAuthClientCredentialsIfClientID(serverKey, persistedClientID)
		if clearErr != nil {
			c.logger.Warn("Failed to clear the rejected DCR client registration",
				zap.String("server", c.config.Name),
				logSafeErrorField(clearErr))
		} else if !cleared {
			// Another login replaced the rejected registration after this
			// attempt read it. Registering yet another client here would
			// overwrite the winner's credentials and pair its grant with the
			// wrong client_id, so stop and let the next sign-in use the
			// registration that is now stored.
			c.logger.Info("Stored DCR registration already replaced by another login - not re-registering",
				zap.String("server", c.config.Name),
				zap.String("correlation_id", correlationID),
				zap.String("old_client_id", persistedClientID))
			return "", c.signInAgainFlowError(correlationID, fmt.Sprintf("Server '%s' rejected the stored OAuth client registration (client_id %s: %s), and another sign-in has already replaced it",
				c.config.Name, persistedClientID, rejection))
		}
	}

	// mcp-go's handler keeps its client_secret when a registration response
	// carries none (and asks for client_secret_post because one is set), so
	// re-registering on this handler would pair the old secret with the new
	// client_id at code exchange. The rejected registration is already
	// removed; the next sign-in builds a fresh handler and registers cleanly.
	if oauthHandler.GetClientSecret() != "" {
		c.logger.Warn("Rejected DCR registration was a confidential client - removed; the next sign-in registers a new client",
			zap.String("server", c.config.Name),
			zap.String("correlation_id", correlationID),
			zap.String("old_client_id", persistedClientID))
		return "", c.signInAgainFlowError(correlationID, fmt.Sprintf("Server '%s' rejected the stored OAuth client registration (client_id %s: %s); it has been removed",
			c.config.Name, persistedClientID, rejection))
	}

	if regErr := c.registerOAuthClientSafely(ctx, oauthHandler); regErr != nil {
		c.logger.Warn("⚠️ Re-registration after a rejected DCR client_id failed",
			zap.String("server", c.config.Name),
			zap.String("old_client_id", persistedClientID),
			logSafeErrorField(regErr))
		return "", scrubbedFlowError(&contracts.OAuthFlowError{
			Success:       false,
			ErrorType:     contracts.OAuthErrorDCRFailed,
			ErrorCode:     contracts.OAuthCodeDCRFailed,
			ServerName:    c.config.Name,
			CorrelationID: correlationID,
			Message: fmt.Sprintf("Server '%s' rejected the stored OAuth client registration (client_id %s: %s) and re-registering a new client failed: %v",
				c.config.Name, persistedClientID, rejection, regErr),
			Details: &contracts.OAuthErrorDetails{
				ServerURL: c.logSafeURL(),
				DCRStatus: &contracts.DCRStatus{Attempted: true, Success: false, Error: regErr.Error()},
			},
			Suggestion: "Register an OAuth app with the provider and set oauth.client_id in the server config.",
			DebugHint:  fmt.Sprintf("For logs: mcpproxy upstream logs %s", c.config.Name),
		})
	}

	newClientID := oauthHandler.GetClientID()
	if newClientID == "" || newClientID == persistedClientID {
		return "", scrubbedFlowError(&contracts.OAuthFlowError{
			Success:       false,
			ErrorType:     contracts.OAuthErrorDCRFailed,
			ErrorCode:     contracts.OAuthCodeDCRFailed,
			ServerName:    c.config.Name,
			CorrelationID: correlationID,
			Message: fmt.Sprintf("Server '%s' rejected the stored OAuth client registration (client_id %s: %s) and re-registration did not return a new client_id",
				c.config.Name, persistedClientID, rejection),
			Details: &contracts.OAuthErrorDetails{
				ServerURL: c.logSafeURL(),
				DCRStatus: &contracts.DCRStatus{Attempted: true, Success: false, Error: "registration returned no new client_id"},
			},
			Suggestion: "Register an OAuth app with the provider and set oauth.client_id in the server config.",
			DebugHint:  fmt.Sprintf("For logs: mcpproxy upstream logs %s", c.config.Name),
		})
	}

	if callbackPort, saveErr := c.persistDCRRegistration(newClientID, oauthHandler.GetClientSecret()); saveErr != nil && !errors.Is(saveErr, errDCRRegistrationSuperseded) {
		c.logger.Warn("Failed to persist the re-registered DCR credentials - token refresh may fail later",
			zap.String("server", c.config.Name),
			logSafeErrorField(saveErr))
	} else if saveErr != nil {
		// Another login stored its own registration while this one was being
		// registered. Using ours would leave storage and this flow on
		// different clients, so stop and let the next sign-in use theirs.
		c.logger.Info("Another login stored a client registration during re-registration - not using this one",
			zap.String("server", c.config.Name),
			zap.String("correlation_id", correlationID),
			zap.String("discarded_client_id", newClientID))
		return "", c.signInAgainFlowError(correlationID, fmt.Sprintf("Server '%s' rejected the stored OAuth client registration (client_id %s: %s), and another sign-in stored a new one in the meantime",
			c.config.Name, persistedClientID, rejection))
	} else {
		c.logger.Info("✅ Re-registered OAuth client after the persisted DCR client_id was rejected",
			zap.String("server", c.config.Name),
			zap.String("correlation_id", correlationID),
			zap.String("old_client_id", persistedClientID),
			zap.String("new_client_id", newClientID),
			zap.Int("callback_port", callbackPort))
	}

	newURL, urlErr := c.authorizationURLSafely(ctx, oauthHandler, state, codeChallenge)
	if urlErr != nil {
		return "", scrubbedFlowError(&contracts.OAuthFlowError{
			Success:       false,
			ErrorType:     contracts.OAuthErrorFlowFailed,
			ErrorCode:     contracts.OAuthCodeFlowFailed,
			ServerName:    c.config.Name,
			CorrelationID: correlationID,
			Message:       fmt.Sprintf("Failed to get authorization URL after re-registering the OAuth client: %v", urlErr),
			Details:       &contracts.OAuthErrorDetails{ServerURL: c.logSafeURL()},
			Suggestion:    "Check server OAuth configuration and try again.",
			DebugHint:     fmt.Sprintf("For logs: mcpproxy upstream logs %s", c.config.Name),
		})
	}
	newURL = c.applyExtraParamsToAuthURL(newURL, extraParams)

	// Exactly one re-registration per attempt: probe the fresh client once
	// and stop if it is rejected too.
	second := oauth.ProbeAuthorizationClient(ctx, authorizeProbeHTTPClient, newURL)
	if second.ClientRejected {
		secondRejection := oauth.ScrubUpstreamText(second.Summary())
		c.logger.Error("❌ Authorization server rejected the freshly registered OAuth client too - not retrying",
			zap.String("server", c.config.Name),
			zap.String("correlation_id", correlationID),
			zap.String("old_client_id", persistedClientID),
			zap.String("new_client_id", newClientID),
			zap.String("authorize_response", secondRejection))
		if c.storage != nil {
			if _, clearErr := c.storage.ClearOAuthClientCredentialsIfClientID(serverKey, newClientID); clearErr != nil {
				c.logger.Warn("Failed to clear the rejected fresh DCR client registration",
					zap.String("server", c.config.Name),
					logSafeErrorField(clearErr))
			}
		}
		return "", scrubbedFlowError(&contracts.OAuthFlowError{
			Success:       false,
			ErrorType:     contracts.OAuthErrorFlowFailed,
			ErrorCode:     contracts.OAuthCodeFlowFailed,
			ServerName:    c.config.Name,
			CorrelationID: correlationID,
			Message: fmt.Sprintf("Server '%s' rejected the stored OAuth client registration (client_id %s: %s) and also rejected the freshly registered client (client_id %s: %s); the authorization server does not accept its own dynamically registered clients",
				c.config.Name, persistedClientID, rejection, newClientID, secondRejection),
			Details: &contracts.OAuthErrorDetails{
				ServerURL: c.logSafeURL(),
				DCRStatus: &contracts.DCRStatus{Attempted: true, Success: true},
			},
			Suggestion: "Register an OAuth app with the provider and set oauth.client_id in the server config, or contact the server administrator.",
			DebugHint:  fmt.Sprintf("For logs: mcpproxy upstream logs %s", c.config.Name),
		})
	}
	return newURL, nil
}

// authURLClientID returns the client_id query parameter of an authorization
// URL ("" when absent or unparsable).
func authURLClientID(authURL string) string {
	u, err := url.Parse(authURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("client_id")
}

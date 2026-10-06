package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// OAuthHealthFields is the OAuth token state a health.HealthCalculatorInput
// needs. Every surface that calls health.CalculateHealth (REST/tray via
// GetAllServers, the MCP upstream_servers list) derives it here so an empty
// status is never mistaken for "no token".
type OAuthHealthFields struct {
	// Config is the serialized OAuth config for the server map; nil when the
	// server does not use OAuth. OAuthRequired == (Config != nil).
	Config                map[string]interface{}
	Authenticated         bool
	Status                string // "authenticated", "expired", "error", "none" or ""
	TokenExpiresAt        time.Time
	HasRefreshToken       bool
	CallTimeOAuthRequired bool
}

// OAuthHealthState derives the OAuth token state for the named server from the
// persisted token store, the config and the last upstream error.
// rawLastError is the uncollapsed last error.
func (r *Runtime) OAuthHealthState(name string, cfg *config.ServerConfig, rawLastError string) OAuthHealthFields {
	var f OAuthHealthFields
	var (
		oauthConfig     map[string]interface{}
		authenticated   bool
		oauthStatus     string
		tokenExpiresAt  time.Time
		hasRefreshToken bool
	)
	var url string
	if cfg != nil {
		url = cfg.URL
		// Serialize OAuth config if present (explicit config)
		if cfg.OAuth != nil {
			oauthConfig = map[string]interface{}{
				"client_id":    cfg.OAuth.ClientID,
				"scopes":       cfg.OAuth.Scopes,
				"extra_params": cfg.OAuth.ExtraParams,
				"pkce_enabled": cfg.OAuth.PKCEEnabled,
				// auth_url, token_url will be populated from OAuth runtime state if available
				"auth_url":  "",
				"token_url": "",
			}
		}

		// GH #1172: a token record left behind by an earlier OAuth login
		// is not evidence about a server that now authenticates with a
		// static Authorization header — see StoredOAuthTokenInPlay.
		tokenInPlay := r.StoredOAuthTokenInPlay(name, cfg)

		// Check if server has valid OAuth token in storage
		// IMPORTANT: This runs for ALL servers with a URL, including autodiscovery servers
		// PersistentTokenStore uses serverKey (name + URL hash), not just server name
		// We need to generate the same key format: "servername_hash16"
		if url != "" && r.storageManager != nil && tokenInPlay {
			r.logger.Debug("Checking OAuth token in storage",
				zap.String("server", name),
				// #1158: the configured upstream URL routinely carries a
				// `?token=` credential; the host and path stay readable.
				zap.String("url", oauth.AuditRedaction.URLValue(url)),
				zap.Bool("has_explicit_oauth_config", cfg.OAuth != nil))

			// Generate server key matching PersistentTokenStore format
			combined := fmt.Sprintf("%s|%s", name, url)
			hash := sha256.Sum256([]byte(combined))
			hashStr := hex.EncodeToString(hash[:])
			serverKey := fmt.Sprintf("%s_%s", name, hashStr[:16])

			r.logger.Debug("Generated OAuth token lookup key",
				zap.String("server", name),
				zap.String("server_key", serverKey))

			token, err := r.storageManager.GetOAuthToken(serverKey)
			r.logger.Debug("OAuth token lookup result",
				zap.String("server", name),
				zap.String("server_key", serverKey),
				zap.Bool("token_nil", token == nil),
				zap.Error(err))

			if err == nil && token != nil {
				authenticated = true
				tokenExpiresAt = token.ExpiresAt
				hasRefreshToken = token.RefreshToken != ""
				r.logger.Info("OAuth token found for server",
					zap.String("server", name),
					zap.String("server_key", serverKey),
					zap.Time("expires_at", token.ExpiresAt),
					zap.Bool("has_refresh_token", hasRefreshToken))

				// For autodiscovery servers (no explicit OAuth config), create minimal oauthConfig
				if oauthConfig == nil {
					oauthConfig = map[string]interface{}{
						"autodiscovery": true,
					}
				}

				// Add token expiration info to oauth config
				if !token.ExpiresAt.IsZero() {
					oauthConfig["token_expires_at"] = token.ExpiresAt.Format(time.RFC3339)
					// Check if token is expired
					isValid := time.Now().Before(token.ExpiresAt)
					oauthConfig["token_valid"] = isValid
					if isValid {
						oauthStatus = string(oauth.OAuthStatusAuthenticated)
					} else {
						oauthStatus = string(oauth.OAuthStatusExpired)
					}
				} else {
					// No expiration means token is valid indefinitely
					oauthConfig["token_valid"] = true
					oauthStatus = string(oauth.OAuthStatusAuthenticated)
				}
			} else {
				// No token found - check if OAuth config exists to determine status
				if oauthConfig != nil {
					oauthStatus = string(oauth.OAuthStatusNone)
				}
			}
		}
	}

	// Check for OAuth error in last_error - this indicates OAuth autodiscovery detected
	// an OAuth-required server that has no token (user needs to authenticate)
	if oauthStatus != string(oauth.OAuthStatusExpired) && rawLastError != "" {
		if oauth.IsOAuthError(rawLastError) {
			// If we have no oauthConfig yet, this is an autodiscovery server that needs OAuth
			if oauthConfig == nil {
				oauthConfig = map[string]interface{}{
					"autodiscovery": true,
				}
				// Set status to "none" - user hasn't authenticated yet
				oauthStatus = string(oauth.OAuthStatusNone)
			} else {
				// Has config but error - token might be invalid
				oauthStatus = string(oauth.OAuthStatusError)
			}
		}
	}
	if r.upstreamManager != nil {
		if client, exists := r.upstreamManager.GetClient(name); exists && client != nil {
			f.CallTimeOAuthRequired = client.IsOAuthCallRequired()
		}
	}
	f.Config = oauthConfig
	f.Authenticated = authenticated
	f.Status = oauthStatus
	f.TokenExpiresAt = tokenExpiresAt
	f.HasRefreshToken = hasRefreshToken
	return f
}

package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"

	"github.com/mark3labs/mcp-go/client"
	transport "github.com/mark3labs/mcp-go/client/transport"
	"go.uber.org/zap"
)

const (
	// TokenRefreshGracePeriod defines how long before expiration we should trigger a refresh.
	// This prevents race conditions where a token expires during an API call.
	// Setting this to 5 minutes allows proactive token refresh before expiration.
	TokenRefreshGracePeriod = 5 * time.Minute
)

// PersistentTokenStore implements client.TokenStore using BBolt storage
type PersistentTokenStore struct {
	serverName string // Original server name (used for RefreshManager)
	serverKey  string // Unique key combining server name and URL (used for storage)
	storage    *storage.BoltDB
	logger     *zap.Logger
}

// NewPersistentTokenStore creates a new persistent token store for a server
func NewPersistentTokenStore(serverName, serverURL string, storage *storage.BoltDB) client.TokenStore {
	// Create unique key combining server name and URL to handle servers with same name but different URLs
	serverKey := GenerateServerKey(serverName, serverURL)

	return &PersistentTokenStore{
		serverName: serverName,
		serverKey:  serverKey,
		storage:    storage,
		logger:     zap.L().Named("persistent-token-store").With(zap.String("server", serverName), zap.String("server_key", serverKey)),
	}
}

// GenerateServerKey creates a unique key for a server by combining name and URL
// Exported for use by connection.go when persisting DCR credentials
func GenerateServerKey(serverName, serverURL string) string {
	// Create a unique identifier by combining server name and URL
	combined := fmt.Sprintf("%s|%s", serverName, serverURL)

	// Generate SHA256 hash for consistent length and uniqueness
	hash := sha256.Sum256([]byte(combined))
	hashStr := hex.EncodeToString(hash[:])

	// Return first 16 characters of hash for readability (still highly unique)
	key := fmt.Sprintf("%s_%s", serverName, hashStr[:16])

	// Log key generation for debugging server key mismatches
	zap.L().Debug("Generated OAuth server key",
		zap.String("server_name", serverName),
		zap.String("server_url", serverURL),
		zap.String("generated_key", key))

	return key
}

// GetToken retrieves the OAuth token from persistent storage
func (p *PersistentTokenStore) GetToken(ctx context.Context) (*client.Token, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p.logger.Debug("🔍 Loading OAuth token from persistent storage",
		zap.String("server_name", p.serverName),
		zap.String("server_key", p.serverKey))

	record, err := p.storage.GetOAuthToken(p.serverKey)
	if err != nil {
		p.logger.Debug("❌ No stored OAuth token found",
			zap.String("server_name", p.serverName),
			zap.String("server_key", p.serverKey),
			zap.Error(err))
		return nil, transport.ErrNoToken
	}

	now := time.Now()
	timeUntilExpiry := record.ExpiresAt.Sub(now)
	isExpired := now.After(record.ExpiresAt)
	needsRefresh := timeUntilExpiry < TokenRefreshGracePeriod

	// Log token status for debugging
	if isExpired {
		p.logger.Warn("⚠️ OAuth token has expired and needs refresh",
			zap.String("server_key", p.serverKey),
			zap.Time("expires_at", record.ExpiresAt),
			zap.Duration("expired_since", -timeUntilExpiry),
			zap.Bool("has_refresh_token", record.RefreshToken != ""))
	} else if needsRefresh {
		p.logger.Info("⏰ OAuth token will expire soon, proactive refresh recommended",
			zap.String("server_key", p.serverKey),
			zap.Time("expires_at", record.ExpiresAt),
			zap.Duration("time_until_expiry", timeUntilExpiry),
			zap.Duration("grace_period", TokenRefreshGracePeriod),
			zap.Bool("has_refresh_token", record.RefreshToken != ""))
	} else {
		p.logger.Debug("✅ OAuth token is valid and not expiring soon",
			zap.String("server_key", p.serverKey),
			zap.Time("expires_at", record.ExpiresAt),
			zap.Duration("time_until_expiry", timeUntilExpiry),
			zap.Bool("has_refresh_token", record.RefreshToken != ""))
	}

	// Join scopes back into space-separated string
	scope := strings.Join(record.Scopes, " ")

	// Adjust ExpiresAt to trigger proactive refresh within grace period
	// This prevents race conditions where tokens expire during API calls
	//
	// IMPORTANT: Only apply grace period if the token has enough remaining lifetime.
	// For short-lived tokens (e.g., 30 seconds), subtracting 5 minutes would make
	// them appear expired immediately, causing unnecessary re-authentication.
	adjustedExpiresAt := record.ExpiresAt
	if timeUntilExpiry > TokenRefreshGracePeriod {
		// Token has enough lifetime - apply full grace period for proactive refresh
		adjustedExpiresAt = record.ExpiresAt.Add(-TokenRefreshGracePeriod)
		p.logger.Debug("Applied grace period adjustment for proactive refresh",
			zap.Duration("grace_period", TokenRefreshGracePeriod),
			zap.Time("original_expires_at", record.ExpiresAt),
			zap.Time("adjusted_expires_at", adjustedExpiresAt))
	} else if timeUntilExpiry > 0 {
		// Token is short-lived but not yet expired - use actual expiration
		// This allows the token to be used until it actually expires
		p.logger.Debug("Skipping grace period for short-lived token",
			zap.Duration("time_until_expiry", timeUntilExpiry),
			zap.Duration("grace_period", TokenRefreshGracePeriod),
			zap.Time("expires_at", record.ExpiresAt))
	}
	// If timeUntilExpiry <= 0, token is already expired - adjustedExpiresAt stays as record.ExpiresAt

	token := &client.Token{
		AccessToken:  record.AccessToken,
		RefreshToken: record.RefreshToken,
		TokenType:    record.TokenType,
		ExpiresAt:    adjustedExpiresAt,
		Scope:        scope,
	}

	// Log token metadata for debugging (using the new logging utility)
	LogTokenMetadata(p.logger, TokenMetadata{
		TokenType:       record.TokenType,
		ExpiresAt:       record.ExpiresAt,
		ExpiresIn:       timeUntilExpiry,
		Scope:           scope,
		HasRefreshToken: record.RefreshToken != "",
	})

	// Warn if returning an expired token without a refresh token - mcp-go cannot refresh this
	if isExpired && record.RefreshToken == "" {
		p.logger.Warn("⚠️ Returning expired token WITHOUT refresh_token - refresh will fail",
			zap.String("server_name", p.serverName),
			zap.String("server_key", p.serverKey),
			zap.Time("expired_at", record.ExpiresAt))
	} else if record.RefreshToken == "" {
		p.logger.Warn("⚠️ Token has no refresh_token - cannot be refreshed when it expires",
			zap.String("server_name", p.serverName),
			zap.String("server_key", p.serverKey),
			zap.Time("expires_at", record.ExpiresAt))
	}

	// Return the token - mcp-go library will check IsExpired() and handle refresh if needed
	// For long-lived tokens, we subtract the grace period from ExpiresAt to trigger refresh earlier
	// For short-lived tokens, we use the actual expiration to avoid falsely marking them as expired
	return token, nil
}

// SaveToken stores the OAuth token to persistent storage
func (p *PersistentTokenStore) SaveToken(ctx context.Context, token *client.Token) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	now := time.Now()
	timeUntilExpiry := token.ExpiresAt.Sub(now)

	p.logger.Info("💾 Saving OAuth token to persistent storage",
		zap.String("server_key", p.serverKey),
		zap.String("token_type", token.TokenType),
		zap.Time("expires_at", token.ExpiresAt),
		zap.Duration("valid_for", timeUntilExpiry),
		zap.Bool("has_refresh_token", token.RefreshToken != ""),
		zap.String("scope", token.Scope))

	// Parse scopes from token.Scope (space-separated string)
	var scopes []string
	if token.Scope != "" {
		scopes = strings.Split(token.Scope, " ")
	}

	// Read existing record to preserve DCR credentials (ClientID, ClientSecret,
	// CallbackPort, RedirectURI) that were saved by UpdateOAuthClientCredentials
	// during the Dynamic Client Registration flow. Without this, SaveOAuthToken
	// overwrites the entire record and DCR credentials are lost, making token
	// refresh impossible after restart.
	var clientID, clientSecret, redirectURI string
	var callbackPort int
	var created time.Time
	if existing, err := p.storage.GetOAuthToken(p.serverKey); err == nil {
		clientID = existing.ClientID
		clientSecret = existing.ClientSecret
		callbackPort = existing.CallbackPort
		redirectURI = existing.RedirectURI
		created = existing.Created
	}
	if created.IsZero() {
		created = now
	}

	record := &storage.OAuthTokenRecord{
		ServerName:   p.serverKey,  // Storage key (for bucket lookup)
		DisplayName:  p.serverName, // Actual server name (for RefreshManager)
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		ExpiresAt:    token.ExpiresAt,
		Scopes:       scopes,
		ClientID:     clientID,     // Preserve DCR credentials
		ClientSecret: clientSecret, // Preserve DCR credentials
		CallbackPort: callbackPort, // Preserve DCR callback port
		RedirectURI:  redirectURI,  // Preserve DCR redirect URI
		Created:      created,
		Updated:      now,
	}

	err := p.storage.SaveOAuthToken(record)
	if err != nil {
		p.logger.Error("❌ Failed to save OAuth token to persistent storage",
			zap.String("server", p.serverName),
			zap.String("server_key", p.serverKey),
			zap.Error(err))
		return fmt.Errorf("failed to save OAuth token: %w", err)
	}

	p.logger.Info("✅ OAuth token saved to persistent storage successfully",
		zap.String("server", p.serverName),
		zap.String("server_key", p.serverKey),
		zap.Duration("valid_for", timeUntilExpiry))

	// Log token metadata for debugging (using the standard logging utility)
	LogTokenMetadata(p.logger, TokenMetadata{
		TokenType:       token.TokenType,
		ExpiresAt:       token.ExpiresAt,
		ExpiresIn:       timeUntilExpiry,
		Scope:           token.Scope,
		HasRefreshToken: token.RefreshToken != "",
	})

	// Notify RefreshManager about the new token so it can schedule proactive refresh
	// Use serverName (not serverKey) so RefreshManager can look up the actual server
	globalTokenStoreManager.NotifyTokenSaved(p.serverName, token.ExpiresAt)

	return nil
}

// ClearToken removes the OAuth token from persistent storage
func (p *PersistentTokenStore) ClearToken() error {
	p.logger.Info("🗑️ Clearing OAuth token from persistent storage",
		zap.String("server_key", p.serverKey))

	err := p.storage.DeleteOAuthToken(p.serverKey)
	if err != nil {
		p.logger.Error("❌ Failed to clear OAuth token from persistent storage",
			zap.String("server_key", p.serverKey),
			zap.Error(err))
		return fmt.Errorf("failed to clear OAuth token: %w", err)
	}

	p.logger.Info("✅ OAuth token cleared from persistent storage successfully",
		zap.String("server_key", p.serverKey))
	return nil
}

package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"mcpproxy-go/internal/storage"

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
	serverKey string // Unique key combining server name and URL
	storage   *storage.BoltDB
	logger    *zap.Logger
}

// NewPersistentTokenStore creates a new persistent token store for a server
func NewPersistentTokenStore(serverName, serverURL string, storage *storage.BoltDB) client.TokenStore {
	// Create unique key combining server name and URL to handle servers with same name but different URLs
	serverKey := generateServerKey(serverName, serverURL)

	return &PersistentTokenStore{
		serverKey: serverKey,
		storage:   storage,
		logger:    zap.L().Named("persistent-token-store").With(zap.String("server_key", serverKey)),
	}
}

// generateServerKey creates a unique key for a server by combining name and URL
func generateServerKey(serverName, serverURL string) string {
	// Create a unique identifier by combining server name and URL
	combined := fmt.Sprintf("%s|%s", serverName, serverURL)

	// Generate SHA256 hash for consistent length and uniqueness
	hash := sha256.Sum256([]byte(combined))
	hashStr := hex.EncodeToString(hash[:])

	// Return first 16 characters of hash for readability (still highly unique)
	return fmt.Sprintf("%s_%s", serverName, hashStr[:16])
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
		zap.String("server_key", p.serverKey))

	record, err := p.storage.GetOAuthToken(p.serverKey)
	if err != nil {
		p.logger.Debug("❌ No stored OAuth token found",
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
	adjustedExpiresAt := record.ExpiresAt.Add(-TokenRefreshGracePeriod)

	token := &client.Token{
		AccessToken:  record.AccessToken,
		RefreshToken: record.RefreshToken,
		TokenType:    record.TokenType,
		ExpiresAt:    adjustedExpiresAt, // Use grace period for proactive refresh
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

	// Return the token - mcp-go library will check IsExpired() and handle refresh if needed
	// By subtracting the grace period from ExpiresAt, we trigger refresh earlier
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

	record := &storage.OAuthTokenRecord{
		ServerName:   p.serverKey,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		ExpiresAt:    token.ExpiresAt,
		Scopes:       scopes,
		Created:      now,
		Updated:      now,
	}

	err := p.storage.SaveOAuthToken(record)
	if err != nil {
		p.logger.Error("❌ Failed to save OAuth token to persistent storage",
			zap.String("server_key", p.serverKey),
			zap.Error(err))
		return fmt.Errorf("failed to save OAuth token: %w", err)
	}

	p.logger.Info("✅ OAuth token saved to persistent storage successfully",
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

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

	p.logger.Info("🔍 Loading OAuth token from persistent storage",
		zap.String("server_key", p.serverKey))

	record, err := p.storage.GetOAuthToken(p.serverKey)
	if err != nil {
		p.logger.Info("❌ No stored OAuth token found",
			zap.String("server_key", p.serverKey),
			zap.Error(err))
		return nil, transport.ErrNoToken
	}

	// Check if token is expired
	if time.Now().After(record.ExpiresAt) {
		p.logger.Info("Stored OAuth token is expired",
			zap.Time("expires_at", record.ExpiresAt),
			zap.Time("current_time", time.Now()))
		// Return expired token - OAuth client will handle refresh
	}

	// Join scopes back into space-separated string
	scope := strings.Join(record.Scopes, " ")

	token := &client.Token{
		AccessToken:  record.AccessToken,
		RefreshToken: record.RefreshToken,
		TokenType:    record.TokenType,
		ExpiresAt:    record.ExpiresAt,
		Scope:        scope,
	}

	p.logger.Info("✅ OAuth token loaded from persistent storage",
		zap.String("server_key", p.serverKey),
		zap.String("token_type", record.TokenType),
		zap.Time("expires_at", record.ExpiresAt),
		zap.Strings("scopes", record.Scopes),
		zap.Bool("expired", time.Now().After(record.ExpiresAt)))

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

	p.logger.Info("💾 Saving OAuth token to persistent storage",
		zap.String("token_type", token.TokenType),
		zap.Time("expires_at", token.ExpiresAt))

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
		Created:      time.Now(),
		Updated:      time.Now(),
	}

	err := p.storage.SaveOAuthToken(record)
	if err != nil {
		p.logger.Error("Failed to save OAuth token to persistent storage", zap.Error(err))
		return fmt.Errorf("failed to save OAuth token: %w", err)
	}

	p.logger.Info("✅ OAuth token saved to persistent storage successfully")
	return nil
}

// ClearToken removes the OAuth token from persistent storage
func (p *PersistentTokenStore) ClearToken() error {
	p.logger.Info("🗑️ Clearing OAuth token from persistent storage")

	err := p.storage.DeleteOAuthToken(p.serverKey)
	if err != nil {
		p.logger.Error("Failed to clear OAuth token from persistent storage", zap.Error(err))
		return fmt.Errorf("failed to clear OAuth token: %w", err)
	}

	p.logger.Info("✅ OAuth token cleared from persistent storage successfully")
	return nil
}

package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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

	// binding is set only on the store instance handed to mcp-go, once the
	// live OAuth handler exists (Spec 113 FR-002). Unbound stores keep the
	// read-only behaviour.
	binding atomic.Pointer[RefreshBinding]
	now     func() time.Time
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
		now:        time.Now,
	}
}

func (p *PersistentTokenStore) bindRefresher(b *RefreshBinding) { p.binding.Store(b) }

func (p *PersistentTokenStore) loadRecord() (*storage.OAuthTokenRecord, error) {
	return p.storage.GetOAuthToken(p.serverKey)
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
		zap.String("server_url", logSafeURL(serverURL)),
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

	// DCR (Dynamic Client Registration) creates a minimal record with only
	// client credentials but no access token. Treat these as "no token" to
	// prevent scanForNewTokens() from triggering reconnect loops (issue #305).
	if record.AccessToken == "" {
		p.logger.Debug("⏳ OAuth record exists but has no access token (DCR-only), treating as no token",
			zap.String("server_name", p.serverName),
			zap.String("server_key", p.serverKey),
			zap.Bool("has_client_id", record.ClientID != ""))
		return nil, transport.ErrNoToken
	}

	if b := p.binding.Load(); b != nil {
		return reactiveToken(ctx, p.now(), reactiveTarget{
			key: p.serverKey, name: p.serverName, logger: p.logger,
			load: p.loadRecord,
			clear: func(expected string) (bool, error) {
				return p.storage.ClearOAuthClientCredentialsIf(p.serverKey, expected)
			},
		}, record, b)
	}

	now := time.Now()
	// A zero ExpiresAt means the authorization server did not return an `expires_in`
	// claim. Treat such tokens as never-expiring (matches Go's oauth2 convention and
	// HasValidToken/HasPersistedToken) instead of perpetually-expired.
	hasExpiry := !record.ExpiresAt.IsZero()
	timeUntilExpiry := time.Duration(0)
	isExpired := false
	needsRefresh := false
	if hasExpiry {
		timeUntilExpiry = record.ExpiresAt.Sub(now)
		isExpired = now.After(record.ExpiresAt)
		needsRefresh = timeUntilExpiry < TokenRefreshGracePeriod
	}

	// Log token status for debugging
	switch {
	case !hasExpiry:
		p.logger.Debug("✅ OAuth token has no expiration, treating as long-lived",
			zap.String("server_key", p.serverKey),
			zap.Bool("has_refresh_token", record.RefreshToken != ""))
	case isExpired:
		p.logger.Warn("⚠️ OAuth token has expired and needs refresh",
			zap.String("server_key", p.serverKey),
			zap.Time("expires_at", record.ExpiresAt),
			zap.Duration("expired_since", -timeUntilExpiry),
			zap.Bool("has_refresh_token", record.RefreshToken != ""))
	case needsRefresh:
		p.logger.Info("⏰ OAuth token will expire soon, proactive refresh recommended",
			zap.String("server_key", p.serverKey),
			zap.Time("expires_at", record.ExpiresAt),
			zap.Duration("time_until_expiry", timeUntilExpiry),
			zap.Duration("grace_period", TokenRefreshGracePeriod),
			zap.Bool("has_refresh_token", record.RefreshToken != ""))
	default:
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

	// Spec 113 FR-006: one read-modify-write transaction. Fields this write
	// does not own — the DCR credentials (ClientID, ClientSecret,
	// CallbackPort, RedirectURI) saved by UpdateOAuthClientCredentials, and
	// Created — keep the values they have inside the transaction, so a
	// concurrent DCR write is never lost.
	// FR-006a: a token produced by a refresh flight is only written while the
	// stored grant is still the one that flight started from.
	flightGen, inFlight := FlightGeneration(ctx)
	discarded := false
	err := p.storage.UpdateOAuthToken(p.serverKey, func(rec *storage.OAuthTokenRecord) error {
		if inFlight && !GenerationMatches(rec, flightGen) {
			discarded = true
			return storage.ErrSkipOAuthTokenUpdate
		}
		rec.DisplayName = p.serverName // Actual server name (for RefreshManager)
		rec.AccessToken = token.AccessToken
		rec.RefreshToken = token.RefreshToken
		rec.TokenType = token.TokenType
		rec.ExpiresAt = token.ExpiresAt
		rec.Scopes = scopes
		return nil
	})
	if err != nil {
		p.logger.Error("❌ Failed to save OAuth token to persistent storage",
			zap.String("server", p.serverName),
			zap.String("server_key", p.serverKey),
			zap.Error(err))
		return fmt.Errorf("failed to save OAuth token: %w", err)
	}
	if discarded {
		p.logger.Info("Discarded a refresh result superseded by a newer grant",
			zap.String("server", p.serverName),
			zap.String("server_key", p.serverKey))
		return nil
	}
	DefaultRefreshCoordinator().NoteTokenSaved(p.serverKey)

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

// RefreshBinding is what a token store needs to refresh through the
// RefreshCoordinator on its own (Spec 113 FR-002). It is bound by core once
// the live mcp-go OAuth handler exists.
type RefreshBinding struct {
	// Refresh performs the network refresh (handler or stored-DCR sub-path).
	Refresh RefreshFunc
	// StaticClient is true when the client id comes from oauth.client_id.
	StaticClient bool
}

type refreshBindable interface {
	bindRefresher(b *RefreshBinding)
}

// BindRefresher binds b to store if the store supports reactive refresh
// (PersistentTokenStore, or the CLI in-memory store). Binding again replaces
// the previous binding (reconnects create a new handler). It reports whether
// the store was bound.
func BindRefresher(store client.TokenStore, b RefreshBinding) bool {
	if b.Refresh == nil {
		return false
	}
	rb, ok := store.(refreshBindable)
	if !ok {
		return false
	}
	rb.bindRefresher(&b)
	return true
}

// shortTokenRefreshMargin caps the refresh margin of tokens whose lifetime is
// shorter than TokenRefreshGracePeriod.
const shortTokenRefreshMargin = 30 * time.Second

// needsReactiveRefresh applies the refresh margin (FR-002): the grace period
// for normal tokens; for tokens shorter than the grace period, half their
// lifetime capped at 30 s, so a short token is not refreshed on every request.
// The lifetime is estimated from the record's Updated time.
func needsReactiveRefresh(rec *storage.OAuthTokenRecord, now time.Time) bool {
	if rec.ExpiresAt.IsZero() {
		return false
	}
	remaining := rec.ExpiresAt.Sub(now)
	var lifetime time.Duration
	if !rec.Updated.IsZero() {
		lifetime = rec.ExpiresAt.Sub(rec.Updated)
	}
	if lifetime <= 0 || lifetime > TokenRefreshGracePeriod {
		return remaining < TokenRefreshGracePeriod
	}
	margin := lifetime / 2
	if margin > shortTokenRefreshMargin {
		margin = shortTokenRefreshMargin
	}
	return remaining < margin
}

type reactiveTarget struct {
	key, name string
	logger    *zap.Logger
	load      func() (*storage.OAuthTokenRecord, error)
	clear     func(expectedClientID string) (bool, error)
}

// reactiveToken is GetToken for a refresher-bound store. It refreshes through
// the coordinator when the token is inside its refresh margin and returns the
// token with ExpiresAt zeroed: mcp-go's Token.IsExpired treats a zero expiry
// as never expiring, so OAuthHandler.getValidToken never starts its own,
// unserialized refresh. Expiry is owned by mcpproxy; the persisted record
// keeps the real expires_at, and an upstream 401 still triggers the existing
// authorization-required path.
func reactiveToken(ctx context.Context, now time.Time, t reactiveTarget, rec *storage.OAuthTokenRecord, b *RefreshBinding) (*client.Token, error) {
	tok := tokenFromRecord(rec)
	expired := !rec.ExpiresAt.IsZero() && !now.Before(rec.ExpiresAt)

	if needsReactiveRefresh(rec, now) {
		if rec.RefreshToken == "" {
			if expired {
				return nil, fmt.Errorf("%w: access token expired and no refresh token is stored", transport.ErrOAuthAuthorizationRequired)
			}
		} else {
			newTok, _, err := DefaultRefreshCoordinator().Do(ctx, RefreshRequest{
				Key: t.key, ServerName: t.name, ObservedRefreshToken: rec.RefreshToken,
				Trigger: RefreshTriggerReactive, Load: t.load, Refresh: b.Refresh,
				StaticClient: b.StaticClient, ClearClient: t.clear,
			})
			switch {
			case err == nil:
				tok = newTok
			case ctx.Err() != nil:
				return nil, ctx.Err()
			case !expired:
				// The current access token still works; keep serving it and
				// let the next request (or the proactive schedule) retry.
				if t.logger != nil {
					t.logger.Warn("OAuth refresh failed; access token still valid, continuing to use it",
						zap.Time("expires_at", rec.ExpiresAt), zap.Error(err))
				}
			case errIsTerminal(err):
				return nil, fmt.Errorf("%w: %w", transport.ErrOAuthAuthorizationRequired, err)
			case errors.Is(err, ErrTokenRefreshTransient):
				return nil, err
			default:
				return nil, fmt.Errorf("%w: %w", ErrTokenRefreshTransient, err)
			}
		}
	}

	out := *tok
	out.ExpiresAt = time.Time{}
	return &out, nil
}

// coordinatedMemoryTokenStore is the CLI in-memory token store (no BBolt).
// Once bound it refreshes through the RefreshCoordinator keyed by server name
// (Spec 113 FR-014); unbound it behaves like client.MemoryTokenStore.
type coordinatedMemoryTokenStore struct {
	serverName string
	mu         sync.Mutex // serializes the FR-006a compare-and-swap in SaveToken
	inner      *client.MemoryTokenStore
	binding    atomic.Pointer[RefreshBinding]
}

func newCoordinatedMemoryTokenStore(serverName string) *coordinatedMemoryTokenStore {
	return &coordinatedMemoryTokenStore{serverName: serverName, inner: client.NewMemoryTokenStore()}
}

func (m *coordinatedMemoryTokenStore) key() string { return "cli:" + m.serverName }

func (m *coordinatedMemoryTokenStore) bindRefresher(b *RefreshBinding) { m.binding.Store(b) }

func (m *coordinatedMemoryTokenStore) loadRecord() (*storage.OAuthTokenRecord, error) {
	tok, err := m.inner.GetToken(context.Background())
	if err != nil {
		return nil, err
	}
	return recordFromToken(m.key(), m.serverName, tok), nil
}

// GetToken implements client.TokenStore.
func (m *coordinatedMemoryTokenStore) GetToken(ctx context.Context) (*client.Token, error) {
	tok, err := m.inner.GetToken(ctx)
	if err != nil {
		return nil, err
	}
	b := m.binding.Load()
	if b == nil {
		return tok, nil
	}
	return reactiveToken(ctx, time.Now(), reactiveTarget{key: m.key(), name: m.serverName, load: m.loadRecord},
		recordFromToken(m.key(), m.serverName, tok), b)
}

// SaveToken implements client.TokenStore.
func (m *coordinatedMemoryTokenStore) SaveToken(ctx context.Context, token *client.Token) error {
	m.mu.Lock()
	if gen, inFlight := FlightGeneration(ctx); inFlight {
		if cur, err := m.loadRecord(); err != nil || !GenerationMatches(cur, gen) {
			m.mu.Unlock()
			return nil // superseded by a newer grant (FR-006a)
		}
	}
	err := m.inner.SaveToken(ctx, token)
	m.mu.Unlock()
	if err == nil {
		DefaultRefreshCoordinator().NoteTokenSaved(m.key())
	}
	return err
}

func recordFromToken(key, name string, tok *client.Token) *storage.OAuthTokenRecord {
	var scopes []string
	if tok.Scope != "" {
		scopes = strings.Split(tok.Scope, " ")
	}
	return &storage.OAuthTokenRecord{
		ServerName: key, DisplayName: name,
		AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, TokenType: tok.TokenType,
		ExpiresAt: tok.ExpiresAt, Scopes: scopes,
	}
}

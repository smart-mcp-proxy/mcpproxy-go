package oauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/storage"

	"github.com/mark3labs/mcp-go/client"
	"go.uber.org/zap"
)

const (
	// Default OAuth redirect URI base - port will be dynamically assigned
	DefaultRedirectURIBase = "http://127.0.0.1"
	DefaultRedirectPath    = "/oauth/callback"
)

// CallbackServerManager manages OAuth callback servers for dynamic port allocation
type CallbackServerManager struct {
	servers map[string]*CallbackServer
	mu      sync.RWMutex
	logger  *zap.Logger
}

// CallbackServer represents an active OAuth callback server
type CallbackServer struct {
	Port         int
	RedirectURI  string
	Server       *http.Server
	CallbackChan chan map[string]string
	logger       *zap.Logger
}

var globalCallbackManager = &CallbackServerManager{
	servers: make(map[string]*CallbackServer),
	logger:  zap.L().Named("oauth-callback"),
}

// Global token store manager to persist tokens across client instances
type TokenStoreManager struct {
	stores                  map[string]client.TokenStore
	completedOAuth          map[string]time.Time // Track successful OAuth completions
	mu                      sync.RWMutex
	logger                  *zap.Logger
	oauthCompletionCallback func(serverName string) // Callback when OAuth completes
}

var globalTokenStoreManager = &TokenStoreManager{
	stores:         make(map[string]client.TokenStore),
	completedOAuth: make(map[string]time.Time),
	logger:         zap.L().Named("oauth-tokens"),
}

// GetOrCreateTokenStore returns a shared token store for the given server
func (m *TokenStoreManager) GetOrCreateTokenStore(serverName string) client.TokenStore {
	m.mu.Lock()
	defer m.mu.Unlock()

	if store, exists := m.stores[serverName]; exists {
		m.logger.Info("Reusing existing token store",
			zap.String("server", serverName),
			zap.String("note", "tokens should be available if OAuth was completed"))
		return store
	}

	store := client.NewMemoryTokenStore()
	m.stores[serverName] = store
	m.logger.Info("Created new token store", zap.String("server", serverName))
	return store
}

// HasTokenStore checks if a token store exists for the server in memory (for debugging)
// Note: This only checks the in-memory store, not persisted tokens in BBolt.
// Use HasPersistedToken() to check for tokens in persistent storage.
func (m *TokenStoreManager) HasTokenStore(serverName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.stores[serverName]
	return exists
}

// HasPersistedToken checks if a token exists in persistent storage (BBolt) for the server.
// This is the preferred method to check for existing tokens as it reflects actual token availability.
func HasPersistedToken(serverName, serverURL string, boltStorage *storage.BoltDB) (hasToken bool, hasRefreshToken bool, isExpired bool) {
	if boltStorage == nil {
		return false, false, false
	}

	serverKey := generateServerKey(serverName, serverURL)
	record, err := boltStorage.GetOAuthToken(serverKey)
	if err != nil || record == nil {
		return false, false, false
	}

	hasToken = record.AccessToken != ""
	hasRefreshToken = record.RefreshToken != ""
	isExpired = time.Now().After(record.ExpiresAt)
	return
}

// GetPersistedRefreshToken retrieves the refresh token from persistent storage if available.
// Returns empty string if no token exists or token has no refresh_token.
func GetPersistedRefreshToken(serverName, serverURL string, boltStorage *storage.BoltDB) string {
	if boltStorage == nil {
		return ""
	}

	serverKey := generateServerKey(serverName, serverURL)
	record, err := boltStorage.GetOAuthToken(serverKey)
	if err != nil || record == nil {
		return ""
	}

	return record.RefreshToken
}

// TokenRefreshResult contains the result of a token refresh attempt.
type TokenRefreshResult struct {
	Success     bool
	NewToken    *storage.OAuthTokenRecord
	Error       error
	Attempt     int
	MaxAttempts int
}

// RefreshTokenConfig contains configuration for token refresh operations.
type RefreshTokenConfig struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// DefaultRefreshConfig returns the default token refresh configuration.
func DefaultRefreshConfig() RefreshTokenConfig {
	return RefreshTokenConfig{
		MaxAttempts:    3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     10 * time.Second,
	}
}

// GetTokenStoreManager returns the global token store manager for debugging
func GetTokenStoreManager() *TokenStoreManager {
	return globalTokenStoreManager
}

// SetOAuthCompletionCallback sets a callback function to be called when OAuth completes
func (m *TokenStoreManager) SetOAuthCompletionCallback(callback func(serverName string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.oauthCompletionCallback = callback
}

// MarkOAuthCompleted records that OAuth was successfully completed for a server
// This method is used by CLI processes to notify server processes about OAuth completion
func (m *TokenStoreManager) MarkOAuthCompleted(serverName string) {
	m.mu.Lock()
	callback := m.oauthCompletionCallback
	completionTime := time.Now()
	m.completedOAuth[serverName] = completionTime
	m.mu.Unlock()

	m.logger.Info("OAuth completion recorded",
		zap.String("server", serverName),
		zap.Time("completion_time", completionTime))

	// Trigger in-process callback if available (for same process)
	if callback != nil {
		m.logger.Info("Triggering in-process OAuth completion callback",
			zap.String("server", serverName))
		callback(serverName)
	} else {
		m.logger.Info("No in-process callback registered - OAuth completion will be handled via database events",
			zap.String("server", serverName))
	}
}

// MarkOAuthCompletedWithDB records OAuth completion in the database for cross-process notification
// This is the new preferred method that works across different processes
func (m *TokenStoreManager) MarkOAuthCompletedWithDB(serverName string, storage DatabaseOAuthNotifier) error {
	m.mu.Lock()
	completionTime := time.Now()
	m.completedOAuth[serverName] = completionTime
	m.mu.Unlock()

	// Save to database for cross-process notification
	event := &CompletionEvent{
		ServerName:  serverName,
		CompletedAt: completionTime,
	}

	if err := storage.SaveOAuthCompletionEvent(event); err != nil {
		m.logger.Error("Failed to save OAuth completion event to database",
			zap.String("server", serverName),
			zap.Error(err))
		return err
	}

	m.logger.Info("OAuth completion saved to database for cross-process notification",
		zap.String("server", serverName),
		zap.Time("completion_time", completionTime))

	return nil
}

// DatabaseOAuthNotifier interface for database-based OAuth completion notifications
type DatabaseOAuthNotifier interface {
	SaveOAuthCompletionEvent(event *CompletionEvent) error
}

// OAuthCompletionEvent represents an OAuth completion event (re-exported from storage)
type CompletionEvent = storage.OAuthCompletionEvent

// HasRecentOAuthCompletion checks if OAuth was recently completed for a server
func (m *TokenStoreManager) HasRecentOAuthCompletion(serverName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	completionTime, exists := m.completedOAuth[serverName]
	if !exists {
		return false
	}

	// Consider OAuth recent if completed within last 5 minutes
	isRecent := time.Since(completionTime) < 5*time.Minute
	m.logger.Debug("Checking OAuth completion status",
		zap.String("server", serverName),
		zap.Time("completion_time", completionTime),
		zap.Bool("is_recent", isRecent))

	return isRecent
}

// CreateOAuthConfig creates an OAuth configuration for dynamic client registration
// This implements proper callback server coordination required for Cloudflare OAuth
func CreateOAuthConfig(serverConfig *config.ServerConfig, storage *storage.BoltDB) *client.OAuthConfig {
	startTime := time.Now()
	logger := zap.L().Named("oauth")

	logger.Debug("🚀 Starting OAuth config creation",
		zap.String("server", serverConfig.Name),
		zap.String("url", serverConfig.URL))

	// Defer logging of total duration
	defer func() {
		logger.Debug("⏱️ OAuth config creation completed",
			zap.String("server", serverConfig.Name),
			zap.Duration("total_duration", time.Since(startTime)))
	}()

	logger.Debug("Creating OAuth config for dynamic registration",
		zap.String("server", serverConfig.Name))

	// Scope discovery waterfall (FR-003):
	// 1. Config-specified scopes (highest priority - manual override)
	// 2. RFC 9728 Protected Resource Metadata
	// 3. RFC 8414 Authorization Server Metadata
	// 4. Empty scopes (server specifies via WWW-Authenticate)
	var scopes []string

	// Priority 1: Check config-specified scopes first (manual override)
	if serverConfig.OAuth != nil && len(serverConfig.OAuth.Scopes) > 0 {
		scopes = serverConfig.OAuth.Scopes
		logger.Info("✅ Using config-specified OAuth scopes",
			zap.String("server", serverConfig.Name),
			zap.Strings("scopes", scopes))
	}

	// Priority 2: Try RFC 9728 Protected Resource Metadata discovery
	if len(scopes) == 0 {
		baseURL, err := parseBaseURL(serverConfig.URL)
		if err == nil && baseURL != "" {
			logger.Debug("Attempting Protected Resource Metadata scope discovery (RFC 9728)",
				zap.String("server", serverConfig.Name),
				zap.String("base_url", baseURL))

			// Make a preflight HEAD request to get WWW-Authenticate header
			resp, err := http.Head(serverConfig.URL)
			if err == nil && resp.StatusCode == 401 {
				wwwAuth := resp.Header.Get("WWW-Authenticate")
				if metadataURL := ExtractResourceMetadataURL(wwwAuth); metadataURL != "" {
					discoveredScopes, err := DiscoverScopesFromProtectedResource(metadataURL, 5*time.Second)
					if err == nil && len(discoveredScopes) > 0 {
						scopes = discoveredScopes
						logger.Info("✅ Auto-discovered OAuth scopes from Protected Resource Metadata (RFC 9728)",
							zap.String("server", serverConfig.Name),
							zap.String("metadata_url", metadataURL),
							zap.Strings("scopes", scopes))
					} else if err != nil {
						logger.Debug("Protected Resource Metadata discovery failed",
							zap.String("server", serverConfig.Name),
							zap.Error(err))
					} else {
						// err == nil but no scopes returned
						logger.Warn("Protected Resource Metadata returned no scopes - some clients wait for this before showing OAuth UI",
							zap.String("server", serverConfig.Name),
							zap.String("metadata_url", metadataURL))
					}
				} else {
					logger.Warn("WWW-Authenticate header missing resource_metadata; OAuth clients may refuse to launch browser until PRM exists",
						zap.String("server", serverConfig.Name),
						zap.Any("www_authenticate", resp.Header["Www-Authenticate"]))
				}
			} else if err != nil {
				logger.Warn("Preflight request for WWW-Authenticate header failed; OAuth clients may not see login button",
					zap.String("server", serverConfig.Name),
					zap.Error(err))
			} else {
				logger.Warn("Preflight request did not return 401; server did not advertise WWW-Authenticate metadata",
					zap.String("server", serverConfig.Name),
					zap.Int("status_code", resp.StatusCode))
			}
		}
	}

	// Priority 3: Fallback to RFC 8414 Authorization Server Metadata
	if len(scopes) == 0 {
		baseURL, err := parseBaseURL(serverConfig.URL)
		if err == nil && baseURL != "" {
			logger.Debug("Attempting Authorization Server Metadata scope discovery (RFC 8414)",
				zap.String("server", serverConfig.Name),
				zap.String("base_url", baseURL))

			discoveredScopes, err := DiscoverScopesFromAuthorizationServer(baseURL, 5*time.Second)
			if err == nil && len(discoveredScopes) > 0 {
				scopes = discoveredScopes
				logger.Info("✅ Auto-discovered OAuth scopes from Authorization Server Metadata (RFC 8414)",
					zap.String("server", serverConfig.Name),
					zap.Strings("scopes", scopes))
			} else if err == nil {
				logger.Warn("Authorization Server Metadata returned no scopes; some OAuth clients will wait until scopes_supported is published",
					zap.String("server", serverConfig.Name),
					zap.String("metadata_url", baseURL+"/.well-known/oauth-authorization-server"))
			} else {
				logger.Warn("Authorization Server Metadata discovery failed",
					zap.String("server", serverConfig.Name),
					zap.String("metadata_url", baseURL+"/.well-known/oauth-authorization-server"),
					zap.Error(err))
			}
		}
	}

	// Priority 4: Final fallback to empty scopes (valid OAuth 2.1)
	if len(scopes) == 0 {
		scopes = []string{}
		logger.Warn("No OAuth scopes discovered; falling back to empty list. Some providers (e.g., Google) require `openid`/`email` to be set manually.",
			zap.String("server", serverConfig.Name),
			zap.String("hint", "Set oauth.scopes in server config or ensure PRM/AS metadata advertises scopes_supported"))
	}

	// Start callback server first to get the exact port (as documented in successful approach)
	logger.Info("🔧 Starting OAuth callback server with dynamic port allocation",
		zap.String("server", serverConfig.Name),
		zap.String("approach", "MCPProxy callback server coordination for exact URI matching"))

	// Start our own callback server to get exact port for Cloudflare OAuth
	callbackServer, err := globalCallbackManager.StartCallbackServer(serverConfig.Name)
	if err != nil {
		logger.Error("Failed to start OAuth callback server",
			zap.String("server", serverConfig.Name),
			zap.Error(err))
		return nil
	}

	logger.Info("Using exact redirect URI from allocated callback server",
		zap.String("server", serverConfig.Name),
		zap.String("redirect_uri", callbackServer.RedirectURI),
		zap.Int("port", callbackServer.Port))

	logger.Info("OAuth callback server started successfully",
		zap.String("server", serverConfig.Name),
		zap.String("redirect_uri", callbackServer.RedirectURI),
		zap.Int("port", callbackServer.Port))

	// Try to construct explicit metadata URLs to avoid timeout issues during auto-discovery
	// Extract base URL from server URL for .well-known endpoints
	baseURL, err := parseBaseURL(serverConfig.URL)
	if err != nil {
		logger.Warn("Failed to parse base URL for OAuth metadata",
			zap.String("server", serverConfig.Name),
			zap.String("url", serverConfig.URL),
			zap.Error(err))
		baseURL = ""
	}

	var authServerMetadataURL string
	if baseURL != "" {
		authServerMetadataURL = baseURL + "/.well-known/oauth-authorization-server"
		logger.Info("Using explicit OAuth metadata URL to avoid auto-discovery timeouts",
			zap.String("server", serverConfig.Name),
			zap.String("metadata_url", authServerMetadataURL))
	} else {
		logger.Info("Skipping OAuth metadata URL due to URL parsing issues",
			zap.String("server", serverConfig.Name))
	}

	// Use persistent token store to persist tokens across daemon restarts if storage is available
	var tokenStore client.TokenStore
	if storage != nil {
		tokenStore = NewPersistentTokenStore(serverConfig.Name, serverConfig.URL, storage)
		logger.Info("🔧 Using persistent token store for OAuth tokens",
			zap.String("server", serverConfig.Name),
			zap.String("storage", "BBolt database"))

		// Check if token exists in persistent storage
		existingToken, err := tokenStore.GetToken(context.Background())
		if err != nil {
			logger.Info("🔍 No existing token found in persistent storage",
				zap.String("server", serverConfig.Name),
				zap.Error(err))
		} else {
			logger.Info("✅ Found existing token in persistent storage",
				zap.String("server", serverConfig.Name),
				zap.Time("expires_at", existingToken.ExpiresAt),
				zap.Bool("expired", time.Now().After(existingToken.ExpiresAt)))
		}
	} else {
		tokenStore = globalTokenStoreManager.GetOrCreateTokenStore(serverConfig.Name)
		logger.Info("🔧 Using in-memory token store for OAuth tokens (CLI mode)",
			zap.String("server", serverConfig.Name),
			zap.String("storage", "memory"))
	}

	// Extract extra OAuth parameters (e.g., RFC 8707 resource parameter)
	var extraParams map[string]string
	var httpClient *http.Client

	if serverConfig.OAuth != nil && len(serverConfig.OAuth.ExtraParams) > 0 {
		extraParams = serverConfig.OAuth.ExtraParams

		// Log extra params with selective masking for security
		masked := maskExtraParams(extraParams)
		logger.Debug("OAuth extra parameters configured",
			zap.String("server", serverConfig.Name),
			zap.Any("extra_params", masked))

		// Create HTTP client with wrapper to inject extra params
		wrapper := NewOAuthTransportWrapper(http.DefaultTransport, extraParams, logger)
		httpClient = &http.Client{
			Transport: wrapper,
			Timeout:   30 * time.Second,
		}

		logger.Info("✅ Created OAuth HTTP client with extra params wrapper",
			zap.String("server", serverConfig.Name),
			zap.Int("extra_params_count", len(extraParams)))
	}

	// Check if static OAuth credentials are provided in config
	// If not provided, will attempt DCR or fall back to public client OAuth with PKCE
	var clientID, clientSecret string
	var registrationMode string

	if serverConfig.OAuth != nil && serverConfig.OAuth.ClientID != "" {
		// Use static credentials from config
		clientID = serverConfig.OAuth.ClientID
		clientSecret = serverConfig.OAuth.ClientSecret
		registrationMode = "static credentials"
		logger.Info("✅ Using static OAuth credentials from config",
			zap.String("server", serverConfig.Name),
			zap.String("client_id", clientID))
	} else {
		// Empty credentials - will attempt DCR or use public client OAuth with PKCE
		clientID = ""
		clientSecret = ""
		registrationMode = "public client (PKCE)"
		logger.Info("🔓 No OAuth credentials provided - will attempt DCR or use public client mode",
			zap.String("server", serverConfig.Name),
			zap.String("mode", "Public client OAuth with PKCE"))
	}

	oauthConfig := &client.OAuthConfig{
		ClientID:              clientID,
		ClientSecret:          clientSecret,
		RedirectURI:           callbackServer.RedirectURI, // Exact redirect URI with allocated port
		Scopes:                scopes,
		TokenStore:            tokenStore,            // Shared token store for this server
		PKCEEnabled:           true,                  // Always enable PKCE for security
		AuthServerMetadataURL: authServerMetadataURL, // Explicit metadata URL for proper discovery
		HTTPClient:            httpClient,            // Custom HTTP client with extra params wrapper (if configured)
	}

	logger.Info("OAuth config created successfully",
		zap.String("server", serverConfig.Name),
		zap.Strings("scopes", scopes),
		zap.Bool("pkce_enabled", true),
		zap.String("redirect_uri", callbackServer.RedirectURI),
		zap.String("auth_server_metadata_url", authServerMetadataURL),
		zap.String("registration_mode", registrationMode),
		zap.String("discovery_mode", "explicit metadata URL"), // Using explicit metadata URL to avoid discovery timeouts
		zap.String("token_store", "shared"))                   // Using shared token store for token persistence

	return oauthConfig
}

// StartCallbackServer starts a new OAuth callback server for the given server name
func (m *CallbackServerManager) StartCallbackServer(serverName string) (*CallbackServer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if we already have a server for this name
	if existing, exists := m.servers[serverName]; exists {
		m.logger.Debug("Reusing existing callback server",
			zap.String("server", serverName),
			zap.Int("port", existing.Port))
		return existing, nil
	}

	// Allocate a dynamic port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to allocate dynamic port: %w", err)
	}

	// Extract the dynamically allocated port
	addr := listener.Addr().(*net.TCPAddr)
	port := addr.Port
	redirectURI := fmt.Sprintf("%s:%d%s", DefaultRedirectURIBase, port, DefaultRedirectPath)

	// Create callback channel
	callbackChan := make(chan map[string]string, 1)

	// Create HTTP server with dedicated mux
	mux := http.NewServeMux()
	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second, // Security: prevent Slowloris attacks
		ReadTimeout:       30 * time.Second, // Extended timeout for OAuth discovery
		WriteTimeout:      30 * time.Second, // Extended timeout for OAuth responses
	}

	// Create callback server instance
	callbackServer := &CallbackServer{
		Port:         port,
		RedirectURI:  redirectURI,
		Server:       server,
		CallbackChan: callbackChan,
		logger:       m.logger.With(zap.String("server", serverName), zap.Int("port", port)),
	}

	// Set up HTTP handler for OAuth callback
	mux.HandleFunc(DefaultRedirectPath, func(w http.ResponseWriter, r *http.Request) {
		callbackServer.handleCallback(w, r)
	})

	// Add a debug handler for the root path to see all requests
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		callbackServer.logger.Info("📥 HTTP request received on callback server",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("query", r.URL.RawQuery),
			zap.String("user_agent", r.UserAgent()),
			zap.String("remote_addr", r.RemoteAddr))

		if r.URL.Path == DefaultRedirectPath {
			callbackServer.handleCallback(w, r)
		} else {
			w.Header().Set("Content-Type", "text/html")
			debugPage := fmt.Sprintf(`
				<html>
					<body>
						<h1>OAuth Callback Server Debug</h1>
						<p>Path: %s</p>
						<p>Expected: %s</p>
						<p>Server: %s</p>
						<p>Port: %d</p>
					</body>
				</html>
			`, r.URL.Path, DefaultRedirectPath, serverName, port)
			if _, err := w.Write([]byte(debugPage)); err != nil {
				callbackServer.logger.Error("Error writing debug page", zap.Error(err))
			}
		}
	})

	// Start the server using the existing listener
	go func() {
		defer listener.Close()
		callbackServer.logger.Info("Starting OAuth callback server")

		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			callbackServer.logger.Error("OAuth callback server error", zap.Error(err))
		} else {
			callbackServer.logger.Info("OAuth callback server stopped")
		}
	}()

	// Store the server
	m.servers[serverName] = callbackServer

	callbackServer.logger.Info("OAuth callback server started successfully",
		zap.String("redirect_uri", redirectURI))

	return callbackServer, nil
}

// handleCallback handles OAuth callback requests
func (c *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	c.logger.Info("🎯 OAuth callback received",
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("query", r.URL.RawQuery),
		zap.String("remote_addr", r.RemoteAddr),
		zap.String("user_agent", r.UserAgent()))

	// Extract query parameters
	params := make(map[string]string)
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			params[key] = values[0]
		}
	}

	// Log specific OAuth parameters
	c.logger.Info("🔍 OAuth callback parameters extracted",
		zap.String("code", params["code"]),
		zap.String("state", params["state"]),
		zap.String("error", params["error"]),
		zap.String("error_description", params["error_description"]),
		zap.Int("total_params", len(params)))

	// Send parameters to the channel (non-blocking)
	select {
	case c.CallbackChan <- params:
		c.logger.Info("✅ OAuth callback parameters sent to channel successfully",
			zap.Any("params", params))
	default:
		c.logger.Error("❌ OAuth callback channel full, dropping parameters - THIS IS BAD!",
			zap.Any("params", params))
	}

	// Respond to the user
	w.Header().Set("Content-Type", "text/html")
	successPage := `
		<html>
			<body>
				<h1>Authorization Successful</h1>
				<p>You can now close this window and return to the application.</p>
				<script>
					setTimeout(function() {
						window.close();
					}, 2000);
				</script>
			</body>
		</html>
	`
	if _, err := w.Write([]byte(successPage)); err != nil {
		c.logger.Error("Error writing OAuth callback response", zap.Error(err))
	}
}

// GetCallbackServer retrieves the callback server for a given server name
func (m *CallbackServerManager) GetCallbackServer(serverName string) (*CallbackServer, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	server, exists := m.servers[serverName]
	return server, exists
}

// GetCallbackServer is a global helper to access callback servers
func GetCallbackServer(serverName string) (*CallbackServer, bool) {
	return globalCallbackManager.GetCallbackServer(serverName)
}

// StopCallbackServer stops and removes the callback server for a given server name
func (m *CallbackServerManager) StopCallbackServer(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	server, exists := m.servers[serverName]
	if !exists {
		return nil // Already stopped or never started
	}

	// Shutdown the server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Server.Shutdown(ctx); err != nil {
		m.logger.Error("Error shutting down OAuth callback server",
			zap.String("server", serverName),
			zap.Error(err))
	}

	// Close the callback channel
	close(server.CallbackChan)

	// Remove from map
	delete(m.servers, serverName)

	m.logger.Info("OAuth callback server stopped",
		zap.String("server", serverName),
		zap.Int("port", server.Port))

	return nil
}

// GetGlobalCallbackManager returns the global callback manager instance
func GetGlobalCallbackManager() *CallbackServerManager {
	return globalCallbackManager
}

// ShouldUseOAuth determines if OAuth should be attempted for a given server
// Headers are tried first if configured, then OAuth as fallback on auth errors
func ShouldUseOAuth(serverConfig *config.ServerConfig) bool {
	logger := zap.L().Named("oauth")

	// Check if OAuth is disabled for tests
	if os.Getenv("MCPPROXY_DISABLE_OAUTH") == "true" {
		logger.Debug("OAuth disabled for tests", zap.String("server", serverConfig.Name))
		return false
	}

	// Only HTTP and SSE transports support OAuth
	if serverConfig.Protocol == "stdio" {
		logger.Debug("OAuth not supported for stdio protocol", zap.String("server", serverConfig.Name))
		return false
	}

	// If headers are configured, try headers first, not OAuth
	if len(serverConfig.Headers) > 0 {
		logger.Debug("Headers configured - will try headers first, OAuth as fallback if needed",
			zap.String("server", serverConfig.Name),
			zap.Int("header_count", len(serverConfig.Headers)))
		return false
	}

	// For HTTP/SSE servers without headers, try OAuth-enabled clients
	logger.Debug("No headers configured - OAuth-enabled client will be used",
		zap.String("server", serverConfig.Name),
		zap.String("protocol", serverConfig.Protocol))

	return true
}

// IsOAuthConfigured checks if server has OAuth configuration in config file
// This is mainly for informational purposes - we don't require pre-configuration
func IsOAuthConfigured(serverConfig *config.ServerConfig) bool {
	return serverConfig.OAuth != nil
}

// parseBaseURL extracts the base URL (scheme + host) from a full URL
func parseBaseURL(fullURL string) (string, error) {
	if fullURL == "" {
		return "", fmt.Errorf("empty URL")
	}

	// Handle URLs that might not have a scheme
	if !strings.HasPrefix(fullURL, "http://") && !strings.HasPrefix(fullURL, "https://") {
		fullURL = "https://" + fullURL
	}

	u, err := url.Parse(fullURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid URL: missing scheme or host")
	}

	return fmt.Sprintf("%s://%s", u.Scheme, u.Host), nil
}

// IsOAuthCapable determines if a server can use OAuth authentication
// Returns true if:
//  1. OAuth is explicitly configured in config, OR
//  2. Server uses HTTP-based protocol (OAuth auto-detection available)
func IsOAuthCapable(serverConfig *config.ServerConfig) bool {
	// Explicitly configured
	if serverConfig.OAuth != nil {
		return true
	}

	// Auto-detection available for HTTP-based protocols
	protocol := strings.ToLower(serverConfig.Protocol)
	switch protocol {
	case "http", "sse", "streamable-http", "auto":
		return true // OAuth can be auto-detected
	case "stdio":
		return false // OAuth not applicable for stdio
	default:
		// Unknown protocol - assume HTTP-based and try OAuth
		return true
	}
}

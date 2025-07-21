package oauth

import (
	"context"
	"fmt"
	"mcpproxy-go/internal/config"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"go.uber.org/zap"
)

const (
	// Default OAuth redirect URI base - port will be dynamically assigned
	DefaultRedirectURIBase = "http://127.0.0.1"
	DefaultRedirectPath    = "/oauth/callback"

	// Default OAuth scopes for MCP
	DefaultScopes = "mcp.read,mcp.write"
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

// CreateOAuthConfig creates an OAuth configuration for dynamic client registration
// This implements proper callback server coordination required for Cloudflare OAuth
func CreateOAuthConfig(serverConfig *config.ServerConfig) *client.OAuthConfig {
	logger := zap.L().Named("oauth")

	logger.Debug("Creating OAuth config for dynamic registration",
		zap.String("server", serverConfig.Name))

	// Use default scopes - specific scopes can be overridden in server config if needed
	scopes := []string{"mcp.read", "mcp.write"}
	if serverConfig.OAuth != nil && len(serverConfig.OAuth.Scopes) > 0 {
		scopes = serverConfig.OAuth.Scopes
		logger.Debug("Using custom scopes from config",
			zap.String("server", serverConfig.Name),
			zap.Strings("scopes", scopes))
	}

	// Start callback server and get the dynamic port
	callbackServer, err := globalCallbackManager.StartCallbackServer(serverConfig.Name)
	if err != nil {
		logger.Error("Failed to start OAuth callback server",
			zap.String("server", serverConfig.Name),
			zap.Error(err))
		// Fallback to a semi-random port if dynamic allocation fails
		// This is a last resort and may not work with strict OAuth providers
		redirectURI := "http://127.0.0.1:8085/oauth/callback"
		logger.Warn("Using fallback redirect URI",
			zap.String("server", serverConfig.Name),
			zap.String("redirect_uri", redirectURI))

		// Continue with fallback URI
		callbackServer = &CallbackServer{
			Port:        8085,
			RedirectURI: redirectURI,
		}
	}

	logger.Info("OAuth callback server started successfully",
		zap.String("server", serverConfig.Name),
		zap.String("redirect_uri", callbackServer.RedirectURI),
		zap.Int("port", callbackServer.Port))

	// Determine the correct OAuth server metadata URL based on the server URL
	var authServerMetadataURL string

	oauthConfig := &client.OAuthConfig{
		ClientID:              "",                         // Will be obtained via Dynamic Client Registration
		ClientSecret:          "",                         // Will be obtained via Dynamic Client Registration
		RedirectURI:           callbackServer.RedirectURI, // Exact redirect URI with allocated port
		Scopes:                scopes,
		TokenStore:            client.NewMemoryTokenStore(),
		PKCEEnabled:           true,                  // Always enable PKCE for security
		AuthServerMetadataURL: authServerMetadataURL, // Explicit metadata URL for proper discovery
	}

	logger.Info("OAuth config created for dynamic registration",
		zap.String("server", serverConfig.Name),
		zap.Strings("scopes", scopes),
		zap.Bool("pkce_enabled", true),
		zap.String("redirect_uri", callbackServer.RedirectURI),
		zap.String("discovery_mode", "automatic")) // Changed from explicit metadata URL to automatic discovery

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
	c.logger.Info("OAuth callback received",
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("query", r.URL.RawQuery))

	// Extract query parameters
	params := make(map[string]string)
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			params[key] = values[0]
		}
	}

	// Send parameters to the channel (non-blocking)
	select {
	case c.CallbackChan <- params:
		c.logger.Info("OAuth callback parameters sent to channel",
			zap.Any("params", params))
	default:
		c.logger.Warn("OAuth callback channel full, dropping parameters")
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

package oauthserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// OAuthTestServer is the main test server instance managing all OAuth endpoints.
type OAuthTestServer struct {
	server    *http.Server
	listener  net.Listener
	addr      string
	issuerURL string
	options   Options
	keyRing   *KeyRing

	// State storage
	clients       map[string]*Client
	authCodes     map[string]*AuthorizationCode
	deviceCodes   map[string]*DeviceCode
	refreshTokens map[string]*RefreshTokenData
	issuedTokens  []TokenInfo

	mu sync.RWMutex

	// Test reference
	t *testing.T
}

// ServerResult contains everything needed to configure a test client.
type ServerResult struct {
	// Server URLs
	IssuerURL                   string
	AuthorizationEndpoint       string
	TokenEndpoint               string
	JWKSURL                     string
	RegistrationEndpoint        string // Empty if DCR disabled
	DeviceAuthorizationEndpoint string // Empty if device code disabled
	ProtectedResourceURL        string // For WWW-Authenticate detection tests

	// Pre-registered test client (confidential)
	ClientID     string
	ClientSecret string

	// Pre-registered public client (for PKCE flows)
	PublicClientID string

	// Shutdown function - must be called after tests
	Shutdown func() error

	// Internal server reference for advanced testing
	Server *OAuthTestServer
}

// StartOnPort creates and starts an OAuth test server on a specific port.
// This version is for standalone server usage (not in tests).
// Pass nil for t when running outside of tests.
func StartOnPort(t *testing.T, port int, opts Options) *ServerResult {
	// Apply defaults to options
	opts.applyDefaults()

	// Create key ring
	keyRing, err := NewKeyRing()
	if err != nil {
		if t != nil {
			t.Fatalf("Failed to create key ring: %v", err)
		}
		panic(fmt.Sprintf("Failed to create key ring: %v", err))
	}

	// Create listener on specific port
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		if t != nil {
			t.Fatalf("Failed to create listener: %v", err)
		}
		panic(fmt.Sprintf("Failed to create listener on %s: %v", addr, err))
	}

	issuerURL := fmt.Sprintf("http://%s", listener.Addr().String())

	// Create server
	s := &OAuthTestServer{
		listener:      listener,
		addr:          listener.Addr().String(),
		issuerURL:     issuerURL,
		options:       opts,
		keyRing:       keyRing,
		clients:       make(map[string]*Client),
		authCodes:     make(map[string]*AuthorizationCode),
		deviceCodes:   make(map[string]*DeviceCode),
		refreshTokens: make(map[string]*RefreshTokenData),
		issuedTokens:  make([]TokenInfo, 0),
		t:             t,
	}

	// Register pre-configured clients
	confidentialClient := s.registerTestClient(false)
	publicClient := s.registerTestClient(true)

	// Register any additional clients from options
	for _, cfg := range opts.Clients {
		s.registerClientFromConfig(cfg)
	}

	// Set up HTTP routes
	mux := http.NewServeMux()
	s.setupRoutes(mux)

	s.server = &http.Server{
		Handler: mux,
	}

	// Start server in background
	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			if t != nil {
				t.Errorf("Server error: %v", err)
			}
		}
	}()

	// Wait for server to be ready (non-fatal for standalone mode)
	s.waitForReadyStandalone()

	// Build result
	result := &ServerResult{
		IssuerURL:             issuerURL,
		AuthorizationEndpoint: issuerURL + "/authorize",
		TokenEndpoint:         issuerURL + "/token",
		JWKSURL:               issuerURL + "/jwks.json",
		ProtectedResourceURL:  issuerURL + "/protected",
		ClientID:              confidentialClient.ClientID,
		ClientSecret:          confidentialClient.ClientSecret,
		PublicClientID:        publicClient.ClientID,
		Shutdown:              s.Shutdown,
		Server:                s,
	}

	if opts.EnableDCR {
		result.RegistrationEndpoint = issuerURL + "/registration"
	}
	if opts.EnableDeviceCode {
		result.DeviceAuthorizationEndpoint = issuerURL + "/device_authorization"
	}

	return result
}

// Start creates and starts a new OAuth test server.
// The server listens on an ephemeral port on localhost.
// Returns ServerResult containing URLs and credentials for testing.
// Call result.Shutdown() to stop the server after tests complete.
func Start(t *testing.T, opts Options) *ServerResult {
	t.Helper()

	// Apply defaults to options
	opts.applyDefaults()

	// Create key ring
	keyRing, err := NewKeyRing()
	if err != nil {
		t.Fatalf("Failed to create key ring: %v", err)
	}

	// Create listener on ephemeral port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	addr := listener.Addr().String()
	issuerURL := fmt.Sprintf("http://%s", addr)

	// Create server
	s := &OAuthTestServer{
		listener:      listener,
		addr:          addr,
		issuerURL:     issuerURL,
		options:       opts,
		keyRing:       keyRing,
		clients:       make(map[string]*Client),
		authCodes:     make(map[string]*AuthorizationCode),
		deviceCodes:   make(map[string]*DeviceCode),
		refreshTokens: make(map[string]*RefreshTokenData),
		issuedTokens:  make([]TokenInfo, 0),
		t:             t,
	}

	// Register pre-configured clients
	confidentialClient := s.registerTestClient(false) // confidential
	publicClient := s.registerTestClient(true)        // public

	// Register any additional clients from options
	for _, cfg := range opts.Clients {
		s.registerClientFromConfig(cfg)
	}

	// Set up HTTP routes
	mux := http.NewServeMux()
	s.setupRoutes(mux)

	s.server = &http.Server{
		Handler: mux,
	}

	// Start server in background
	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			t.Errorf("Server error: %v", err)
		}
	}()

	// Wait for server to be ready
	s.waitForReady()

	// Build result
	result := &ServerResult{
		IssuerURL:             issuerURL,
		AuthorizationEndpoint: issuerURL + "/authorize",
		TokenEndpoint:         issuerURL + "/token",
		JWKSURL:               issuerURL + "/jwks.json",
		ProtectedResourceURL:  issuerURL + "/protected",
		ClientID:              confidentialClient.ClientID,
		ClientSecret:          confidentialClient.ClientSecret,
		PublicClientID:        publicClient.ClientID,
		Shutdown:              s.Shutdown,
		Server:                s,
	}

	if opts.EnableDCR {
		result.RegistrationEndpoint = issuerURL + "/registration"
	}
	if opts.EnableDeviceCode {
		result.DeviceAuthorizationEndpoint = issuerURL + "/device_authorization"
	}

	return result
}

// setupRoutes configures all HTTP endpoints.
func (s *OAuthTestServer) setupRoutes(mux *http.ServeMux) {
	// Discovery endpoints (based on detection mode)
	if s.options.DetectionMode == Discovery || s.options.DetectionMode == Both {
		mux.HandleFunc("/.well-known/oauth-authorization-server", s.handleDiscovery)
		mux.HandleFunc("/.well-known/openid-configuration", s.handleDiscovery)
	}

	// JWKS endpoint
	mux.HandleFunc("/jwks.json", s.handleJWKS)

	// Core OAuth endpoints
	if s.options.EnableAuthCode {
		mux.HandleFunc("/authorize", s.handleAuthorize)
	}
	mux.HandleFunc("/token", s.handleToken)

	// DCR endpoint
	if s.options.EnableDCR {
		mux.HandleFunc("/registration", s.handleRegistration)
	}

	// Device code endpoints
	if s.options.EnableDeviceCode {
		mux.HandleFunc("/device_authorization", s.handleDeviceAuthorization)
		mux.HandleFunc("/device_verification", s.handleDeviceVerification)
	}

	// Protected resource (for WWW-Authenticate detection)
	if s.options.DetectionMode == WWWAuthenticate || s.options.DetectionMode == Both {
		mux.HandleFunc("/protected", s.handleProtected)
	}

	// Callback endpoint for testing - displays received OAuth parameters
	mux.HandleFunc("/callback", s.handleCallback)

	// MCP endpoint with OAuth protection - for testing mcpproxy OAuth client
	mux.HandleFunc("/mcp", s.handleMCP)
}

// handleCallback handles the OAuth callback and displays the received parameters.
// This is used for Playwright E2E testing to verify the OAuth flow.
func (s *OAuthTestServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	errorCode := r.URL.Query().Get("error")
	errorDesc := r.URL.Query().Get("error_description")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	html := `<!DOCTYPE html>
<html>
<head><title>OAuth Callback</title></head>
<body>
<h1>OAuth Callback Received</h1>
<div id="callback-params">
<p>Code: <span id="code">` + code + `</span></p>
<p>State: <span id="state">` + state + `</span></p>
<p>Error: <span id="error">` + errorCode + `</span></p>
<p>Error Description: <span id="error_description">` + errorDesc + `</span></p>
</div>
</body>
</html>`
	w.Write([]byte(html))
}

// waitForReady waits for the server to be ready to accept connections.
func (s *OAuthTestServer) waitForReady() {
	client := &http.Client{Timeout: 100 * time.Millisecond}
	for i := 0; i < 50; i++ {
		resp, err := client.Get(s.issuerURL + "/jwks.json")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.t.Fatalf("Server failed to become ready")
}

// waitForReadyStandalone waits for server without test dependency.
func (s *OAuthTestServer) waitForReadyStandalone() {
	client := &http.Client{Timeout: 100 * time.Millisecond}
	for i := 0; i < 50; i++ {
		resp, err := client.Get(s.issuerURL + "/jwks.json")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	// In standalone mode, just log a warning instead of panicking
	fmt.Printf("Warning: Server may not be fully ready\n")
}

// Shutdown stops the OAuth test server.
func (s *OAuthTestServer) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}

// registerTestClient creates a pre-registered test client.
func (s *OAuthTestServer) registerTestClient(isPublic bool) *Client {
	clientID := generateRandomString(16)
	var clientSecret string
	if !isPublic {
		clientSecret = generateRandomString(32)
	}

	client := &Client{
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		RedirectURIs:  []string{"http://127.0.0.1/callback", "http://localhost/callback"},
		GrantTypes:    []string{"authorization_code", "refresh_token"},
		ResponseTypes: []string{"code"},
		Scopes:        s.options.SupportedScopes,
		ClientName:    "Test Client",
		IsPublic:      isPublic,
		CreatedAt:     time.Now(),
	}

	s.mu.Lock()
	s.clients[clientID] = client
	s.mu.Unlock()

	return client
}

// registerClientFromConfig creates a client from ClientConfig.
func (s *OAuthTestServer) registerClientFromConfig(cfg ClientConfig) *Client {
	client := &Client{
		ClientID:      cfg.ClientID,
		ClientSecret:  cfg.ClientSecret,
		RedirectURIs:  cfg.RedirectURIs,
		GrantTypes:    cfg.GrantTypes,
		ResponseTypes: cfg.ResponseTypes,
		Scopes:        cfg.Scopes,
		ClientName:    cfg.ClientName,
		IsPublic:      cfg.ClientSecret == "",
		CreatedAt:     time.Now(),
	}

	// Apply defaults
	if len(client.GrantTypes) == 0 {
		client.GrantTypes = []string{"authorization_code", "refresh_token"}
	}
	if len(client.ResponseTypes) == 0 {
		client.ResponseTypes = []string{"code"}
	}
	if len(client.Scopes) == 0 {
		client.Scopes = s.options.SupportedScopes
	}

	s.mu.Lock()
	s.clients[cfg.ClientID] = client
	s.mu.Unlock()

	return client
}

// RegisterClient programmatically registers a client.
// Returns the registered client with generated credentials.
func (s *OAuthTestServer) RegisterClient(cfg ClientConfig) (*Client, error) {
	// Generate client ID if not provided
	if cfg.ClientID == "" {
		cfg.ClientID = generateRandomString(16)
	}

	// Generate client secret if not a public client
	if cfg.ClientSecret == "" && len(cfg.RedirectURIs) > 0 {
		// Check if it should be a public client based on auth method
		// For simplicity, generate a secret unless explicitly public
		cfg.ClientSecret = generateRandomString(32)
	}

	client := s.registerClientFromConfig(cfg)
	return client, nil
}

// GetClient retrieves a client by ID.
func (s *OAuthTestServer) GetClient(clientID string) (*Client, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	client, exists := s.clients[clientID]
	return client, exists
}

// SetErrorMode updates error injection at runtime.
func (s *OAuthTestServer) SetErrorMode(mode ErrorMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options.ErrorMode = mode
}

// GetAuthorizationCodes returns pending authorization codes (for debugging).
func (s *OAuthTestServer) GetAuthorizationCodes() []AuthCodeInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	codes := make([]AuthCodeInfo, 0, len(s.authCodes))
	for _, ac := range s.authCodes {
		codes = append(codes, AuthCodeInfo{
			Code:      ac.Code,
			ClientID:  ac.ClientID,
			Scopes:    ac.Scopes,
			ExpiresAt: ac.ExpiresAt,
			Used:      ac.Used,
		})
	}
	return codes
}

// AuthCodeInfo contains information about an authorization code (for debugging).
type AuthCodeInfo struct {
	Code      string
	ClientID  string
	Scopes    []string
	ExpiresAt time.Time
	Used      bool
}

// generateRandomString creates a random hex string of specified length.
func generateRandomString(length int) string {
	b := make([]byte, length/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Note: Handler methods are implemented in separate files:
// - handleAuthorize: authorize.go
// - handleToken: token.go
// - handleDiscovery: discovery.go
// - handleJWKS: jwks.go
// - handleRegistration: dcr.go (to be implemented)
// - handleDeviceAuthorization, handleDeviceVerification: device.go (to be implemented)
// - handleProtected: protected.go (to be implemented)

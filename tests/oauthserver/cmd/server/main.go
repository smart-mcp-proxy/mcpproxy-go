// Standalone OAuth test server for browser-based testing (Playwright).
//
// Usage:
//
//	go run ./tests/oauthserver/cmd/server -port 9000
//	go run ./tests/oauthserver/cmd/server -port 9000 -no-dcr -no-device-code
//	go run ./tests/oauthserver/cmd/server -port 9000 -detection=www-authenticate
//
// This starts the OAuth test server on http://localhost:9000 with:
//   - Authorization endpoint: /authorize
//   - Token endpoint: /token
//   - Discovery: /.well-known/oauth-authorization-server
//   - JWKS: /.well-known/jwks.json
//   - DCR: /registration (if enabled)
//   - Device code: /device_authorization (if enabled)
//
// Test credentials: testuser / testpass
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mcpproxy-go/tests/oauthserver"
)

func main() {
	// Server settings
	port := flag.Int("port", 9000, "Port to listen on")

	// Feature toggles
	noAuthCode := flag.Bool("no-auth-code", false, "Disable authorization code flow")
	noDeviceCode := flag.Bool("no-device-code", false, "Disable device code flow (RFC 8628)")
	noDCR := flag.Bool("no-dcr", false, "Disable dynamic client registration (RFC 7591)")
	noClientCreds := flag.Bool("no-client-credentials", false, "Disable client credentials flow")
	noRefreshToken := flag.Bool("no-refresh-token", false, "Disable refresh tokens")

	// Security settings
	requirePKCE := flag.Bool("require-pkce", true, "Require PKCE for authorization code flow (RFC 7636)")
	requireResource := flag.Bool("require-resource", false, "Require RFC 8707 resource indicator")

	// Detection mode
	detectionMode := flag.String("detection", "both", "OAuth detection mode: discovery, www-authenticate, explicit, both")

	// Token lifetimes
	accessTokenTTL := flag.Duration("access-token-ttl", time.Hour, "Access token expiry duration")
	refreshTokenTTL := flag.Duration("refresh-token-ttl", 24*time.Hour, "Refresh token expiry duration")

	// Error injection
	tokenError := flag.String("token-error", "", "Inject token endpoint error: invalid_client, invalid_grant, invalid_scope, server_error")
	authError := flag.String("auth-error", "", "Inject auth endpoint error: access_denied, invalid_request")

	flag.Parse()

	// Parse detection mode
	var dm oauthserver.DetectionMode
	switch strings.ToLower(*detectionMode) {
	case "discovery":
		dm = oauthserver.Discovery
	case "www-authenticate", "wwwauthenticate":
		dm = oauthserver.WWWAuthenticate
	case "explicit":
		dm = oauthserver.Explicit
	case "both":
		dm = oauthserver.Both
	default:
		log.Fatalf("Invalid detection mode: %s (valid: discovery, www-authenticate, explicit, both)", *detectionMode)
	}

	// Build error mode
	var errMode oauthserver.ErrorMode
	switch *tokenError {
	case "invalid_client":
		errMode.TokenInvalidClient = true
	case "invalid_grant":
		errMode.TokenInvalidGrant = true
	case "invalid_scope":
		errMode.TokenInvalidScope = true
	case "server_error":
		errMode.TokenServerError = true
	}
	switch *authError {
	case "access_denied":
		errMode.AuthAccessDenied = true
	case "invalid_request":
		errMode.AuthInvalidRequest = true
	}

	opts := oauthserver.Options{
		// Feature toggles (inverted from no-* flags)
		EnableAuthCode:          !*noAuthCode,
		EnableDeviceCode:        !*noDeviceCode,
		EnableDCR:               !*noDCR,
		EnableClientCredentials: !*noClientCreds,
		EnableRefreshToken:      !*noRefreshToken,

		// Security
		RequirePKCE:              *requirePKCE,
		RequireResourceIndicator: *requireResource,

		// Detection
		DetectionMode: dm,

		// Token lifetimes
		AccessTokenExpiry:  *accessTokenTTL,
		RefreshTokenExpiry: *refreshTokenTTL,

		// Error injection
		ErrorMode: errMode,

		// Pre-register test-client for Playwright tests
		Clients: []oauthserver.ClientConfig{
			{
				ClientID:     "test-client",
				ClientName:   "Test Client",
				RedirectURIs: []string{
					"http://127.0.0.1/callback",
					"http://localhost/callback",
					"http://127.0.0.1:9000/callback", // Allow callback on same port as OAuth server
				},
			},
		},
	}

	server := oauthserver.StartOnPort(nil, *port, opts)

	fmt.Println("========================================")
	fmt.Println("OAuth Test Server")
	fmt.Println("========================================")
	fmt.Printf("Listening on:      http://localhost:%d\n", *port)
	fmt.Printf("Issuer:            %s\n", server.IssuerURL)
	fmt.Println("")
	fmt.Println("Endpoints:")
	if opts.EnableAuthCode {
		fmt.Printf("  Authorization:   %s\n", server.AuthorizationEndpoint)
	}
	fmt.Printf("  Token:           %s\n", server.TokenEndpoint)
	fmt.Printf("  JWKS:            %s\n", server.JWKSURL)
	if dm == oauthserver.Discovery || dm == oauthserver.Both {
		fmt.Printf("  Discovery:       %s/.well-known/oauth-authorization-server\n", server.IssuerURL)
	}
	if dm == oauthserver.WWWAuthenticate || dm == oauthserver.Both {
		fmt.Printf("  Protected:       %s/protected (for WWW-Authenticate detection)\n", server.IssuerURL)
	}
	if opts.EnableDCR {
		fmt.Printf("  DCR:             %s/registration\n", server.IssuerURL)
	}
	if opts.EnableDeviceCode {
		fmt.Printf("  Device Auth:     %s/device_authorization\n", server.IssuerURL)
	}
	fmt.Println("")
	fmt.Println("Features:")
	fmt.Printf("  Auth Code:       %s\n", boolToEnabled(opts.EnableAuthCode))
	fmt.Printf("  Device Code:     %s (RFC 8628)\n", boolToEnabled(opts.EnableDeviceCode))
	fmt.Printf("  DCR:             %s (RFC 7591)\n", boolToEnabled(opts.EnableDCR))
	fmt.Printf("  Client Creds:    %s\n", boolToEnabled(opts.EnableClientCredentials))
	fmt.Printf("  Refresh Token:   %s\n", boolToEnabled(opts.EnableRefreshToken))
	fmt.Printf("  PKCE Required:   %s (RFC 7636)\n", boolToEnabled(opts.RequirePKCE))
	fmt.Printf("  Resource Req:    %s (RFC 8707)\n", boolToEnabled(opts.RequireResourceIndicator))
	fmt.Printf("  Detection Mode:  %s\n", *detectionMode)
	fmt.Println("")
	fmt.Println("Test Credentials:  testuser / testpass")
	fmt.Printf("Public Client ID:  %s\n", server.PublicClientID)
	fmt.Printf("Confidential ID:   %s\n", server.ClientID)
	fmt.Printf("Confidential Secret: %s\n", server.ClientSecret)
	fmt.Println("")
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println("========================================")

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down...")
	server.Shutdown()
	log.Println("OAuth test server stopped")
}

func boolToEnabled(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

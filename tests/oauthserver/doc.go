// Package oauthserver provides a reusable OAuth 2.1 test server for E2E testing.
//
// The test server implements the following OAuth 2.1 specifications:
//   - RFC 6749: OAuth 2.0 Authorization Framework
//   - RFC 7636: PKCE (Proof Key for Code Exchange)
//   - RFC 7591: Dynamic Client Registration (DCR)
//   - RFC 8414: OAuth Authorization Server Metadata
//   - RFC 8628: Device Authorization Grant
//   - RFC 8707: Resource Indicators
//   - RFC 9728: OAuth Protected Resource Metadata
//
// # Quick Start
//
// Create a test server with default options:
//
//	func TestOAuth(t *testing.T) {
//	    server := oauthserver.Start(t, oauthserver.Options{})
//	    defer server.Shutdown()
//
//	    // Use server.AuthorizationEndpoint, server.TokenEndpoint, etc.
//	    // Use server.ClientID and server.ClientSecret for confidential clients
//	    // Use server.PublicClientID for public clients with PKCE
//	}
//
// # Features
//
// The test server supports multiple OAuth flows:
//
//   - Authorization Code with PKCE (default enabled)
//   - Client Credentials Grant
//   - Device Authorization Grant (RFC 8628)
//   - Dynamic Client Registration (RFC 7591)
//   - Refresh Token Grant
//   - Resource Indicators (RFC 8707)
//
// # Configuration
//
// Use Options to customize server behavior:
//
//	server := oauthserver.Start(t, oauthserver.Options{
//	    EnableDeviceCode: true,     // Enable device code flow
//	    EnableDCR:        true,     // Enable dynamic client registration
//	    AccessTokenExpiry: 5*time.Minute,
//	    ErrorMode: oauthserver.ErrorMode{
//	        TokenInvalidClient: true, // Inject invalid_client errors
//	    },
//	})
//
// # Detection Modes
//
// The server supports multiple OAuth detection methods:
//
//   - Discovery: Serves /.well-known/oauth-authorization-server
//   - WWWAuthenticate: Returns 401 with WWW-Authenticate header on /protected
//   - Explicit: No automatic discovery (for manual configuration tests)
//   - Both: Discovery + WWW-Authenticate
//
// # Error Injection
//
// Use ErrorMode to inject specific OAuth errors for testing error handling:
//
//	server := oauthserver.Start(t, oauthserver.Options{
//	    ErrorMode: oauthserver.ErrorMode{
//	        TokenInvalidClient:    true,  // invalid_client error
//	        TokenInvalidGrant:     true,  // invalid_grant error
//	        TokenServerError:      true,  // HTTP 500 error
//	        TokenSlowResponse:     5*time.Second, // Add delay
//	    },
//	})
//
// # JWKS Rotation
//
// Test key rotation scenarios:
//
//	server := oauthserver.Start(t, oauthserver.Options{})
//	defer server.Shutdown()
//
//	// Get initial key ID
//	jwks1 := fetchJWKS(server.JWKSURL)
//
//	// Rotate to a new key
//	newKid, _ := server.Server.KeyRing.RotateKey()
//
//	// Both keys are now in JWKS
//	jwks2 := fetchJWKS(server.JWKSURL)
//
// # Device Code Flow
//
// For programmatic testing without browser interaction:
//
//	server := oauthserver.Start(t, oauthserver.Options{
//	    EnableDeviceCode: true,
//	})
//	defer server.Shutdown()
//
//	// Request device code
//	resp := requestDeviceCode(server)
//
//	// Programmatically approve
//	server.Server.ApproveDeviceCode(resp.UserCode)
//
//	// Poll for token
//	token := pollForToken(server, resp.DeviceCode)
//
// # Dynamic Client Registration
//
//	server := oauthserver.Start(t, oauthserver.Options{
//	    EnableDCR: true,
//	})
//	defer server.Shutdown()
//
//	// Register a new client
//	client := registerClient(server.IssuerURL+"/registration", req)
//
//	// Use registered client for auth code flow
//	authCodeFlow(server, client.ClientID, client.ClientSecret)
package oauthserver

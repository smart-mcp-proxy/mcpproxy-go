// Standalone OAuth test server for browser-based testing (Playwright).
//
// Usage:
//
//	go run ./tests/oauthserver/cmd/server -port 9000
//
// This starts the OAuth test server on http://localhost:9000 with:
//   - Authorization endpoint: /authorize
//   - Token endpoint: /token
//   - Discovery: /.well-known/oauth-authorization-server
//   - JWKS: /.well-known/jwks.json
//   - DCR: /registration
//   - Device code: /device_authorization
//
// Test credentials: testuser / testpass
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"mcpproxy-go/tests/oauthserver"
)

func main() {
	port := flag.Int("port", 9000, "Port to listen on")
	requirePKCE := flag.Bool("require-pkce", true, "Require PKCE for authorization code flow")
	requireResource := flag.Bool("require-resource", false, "Require RFC 8707 resource indicator")
	flag.Parse()

	opts := oauthserver.Options{
		RequirePKCE:              *requirePKCE,
		RequireResourceIndicator: *requireResource,
		DetectionMode:            oauthserver.Both,
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
	fmt.Printf("Authorization:     %s\n", server.AuthorizationEndpoint)
	fmt.Printf("Token:             %s\n", server.TokenEndpoint)
	fmt.Printf("JWKS:              %s\n", server.JWKSURL)
	fmt.Printf("Discovery:         %s/.well-known/oauth-authorization-server\n", server.IssuerURL)
	fmt.Printf("DCR:               %s/registration\n", server.IssuerURL)
	fmt.Printf("Device Auth:       %s/device_authorization\n", server.IssuerURL)
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

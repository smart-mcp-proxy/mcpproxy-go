// Standalone OAuth test server for browser-based testing (Playwright).
//
// Usage:
//
//	go run ./tests/oauthserver/cmd/server -port 9000
//	go run ./tests/oauthserver/cmd/server -port 9000 -no-dcr -no-device-code
//	go run ./tests/oauthserver/cmd/server -port 9000 -detection=www-authenticate
//
// Fake OpenID Provider for the server edition (Spec 107, research D2):
//
//	go run ./tests/oauthserver/cmd/server -port 9000 -oidc \
//	  -redirect-uri http://127.0.0.1:18080/api/v1/auth/callback \
//	  -user 'alice@example.com:pass:eng,sre' -user 'bob@example.com:pass:' \
//	  -groups-claim groups
//
// Tamper knobs for the US2 matrix: -token-error <name>, -groups-error <name>,
// -userinfo-error <name>, -userinfo-sub-mismatch, -email-verified=false,
// -discovery-http-token-endpoint, -token-endpoint-redirect (see -help).
//
// This starts the OAuth test server on http://localhost:9000 with:
//   - Authorization endpoint: /authorize
//   - Token endpoint: /token
//   - Discovery: /.well-known/oauth-authorization-server and /.well-known/openid-configuration
//   - JWKS: /jwks.json
//   - DCR: /registration (if enabled)
//   - Device code: /device_authorization (if enabled)
//   - Userinfo: /userinfo (with -oidc)
//
// Test credentials: testuser / testpass (or the -user entries)
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// cliConfig is everything main needs beyond the server options.
type cliConfig struct {
	Port          int
	DetectionName string
	Options       oauthserver.Options
}

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

// parseFlags maps the CLI arguments onto oauthserver.Options (T034).
func parseFlags(args []string) (oauthserver.Options, error) {
	cfg, err := parseCLI(args, io.Discard)
	return cfg.Options, err
}

// parseCLI parses args (without the program name) with a private FlagSet so
// it is reusable from tests; usage and parse errors are written to usageOut.
func parseCLI(args []string, usageOut io.Writer) (cliConfig, error) {
	fs := flag.NewFlagSet("oauthserver", flag.ContinueOnError)
	fs.SetOutput(usageOut)

	// Server settings
	port := fs.Int("port", 9000, "Port to listen on")

	// Feature toggles
	noAuthCode := fs.Bool("no-auth-code", false, "Disable authorization code flow")
	noDeviceCode := fs.Bool("no-device-code", false, "Disable device code flow (RFC 8628)")
	noDCR := fs.Bool("no-dcr", false, "Disable dynamic client registration (RFC 7591)")
	noClientCreds := fs.Bool("no-client-credentials", false, "Disable client credentials flow")
	noRefreshToken := fs.Bool("no-refresh-token", false, "Disable refresh tokens")

	// Security settings
	requirePKCE := fs.Bool("require-pkce", true, "Require PKCE for authorization code flow (RFC 7636)")
	requireResource := fs.Bool("require-resource", false, "Require RFC 8707 resource indicator")

	// Compatibility modes
	runlayerMode := fs.Bool("runlayer-mode", false, "Mimic Runlayer's strict validation (implies -require-resource, returns Pydantic-style 422 errors)")

	// Detection mode
	detectionMode := fs.String("detection", "both", "OAuth detection mode: discovery, www-authenticate, explicit, both")

	// Per-method MCP authorisation (GH #1271 repro)
	perMethodAuth := fs.Bool("per-method-auth", false, "Serve initialize/tools/list on /mcp anonymously and require a token only for tools/call (Google Gmail MCP behaviour)")

	// Token lifetimes
	accessTokenTTL := fs.Duration("access-token-ttl", time.Hour, "Access token expiry duration")
	refreshTokenTTL := fs.Duration("refresh-token-ttl", 24*time.Hour, "Refresh token expiry duration")

	// OIDC (Spec 107)
	oidc := fs.Bool("oidc", false, "Act as an OpenID Provider: id_token in the auth-code response, /userinfo, EC key in the JWKS")
	var users stringList
	fs.Var(&users, "user", "OIDC user as email:password[:group1,group2] (repeatable; groups may be empty)")
	var redirectURIs stringList
	fs.Var(&redirectURIs, "redirect-uri", "Extra redirect URI accepted for every pre-registered client (repeatable)")
	groupsClaim := fs.String("groups-claim", "groups", "Name of the groups claim in the id_token and /userinfo")

	// Error injection
	tokenError := fs.String("token-error", "", "Inject token endpoint error: invalid_client, invalid_grant, invalid_scope, server_error; with -oidc also an id_token defect: bad-signature, wrong-iss, wrong-aud, multi-aud-no-azp, expired, nbf-future, no-nonce, alg-none, hs256, unknown-kid, key-alg-mismatch")
	authError := fs.String("auth-error", "", "Inject auth endpoint error: access_denied, invalid_request")
	groupsError := fs.String("groups-error", "", "Groups-claim defect (with -oidc): non-array, overage, absent")
	userinfoError := fs.String("userinfo-error", "", "Userinfo defect (with -oidc): sub-mismatch, redirect, non-json, unavailable")
	userinfoSubMismatch := fs.Bool("userinfo-sub-mismatch", false, "Alias of -userinfo-error sub-mismatch")
	emailVerified := fs.Bool("email-verified", true, "email_verified claim value (with -oidc); false injects EmailVerifiedFalse")
	discoveryHTTPTokenEndpoint := fs.Bool("discovery-http-token-endpoint", false, "Discovery advertises a plain-http, non-loopback token_endpoint (with -oidc)")
	tokenEndpointRedirect := fs.Bool("token-endpoint-redirect", false, "/token answers 302 to another origin (with -oidc)")

	if err := fs.Parse(args); err != nil {
		return cliConfig{}, err
	}

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
		return cliConfig{}, fmt.Errorf("invalid -detection %q (valid: discovery, www-authenticate, explicit, both)", *detectionMode)
	}

	// Build error mode
	var errMode oauthserver.ErrorMode
	if err := applyTokenError(&errMode, *tokenError, *oidc); err != nil {
		return cliConfig{}, err
	}
	switch *authError {
	case "":
	case "access_denied":
		errMode.AuthAccessDenied = true
	case "invalid_request":
		errMode.AuthInvalidRequest = true
	default:
		return cliConfig{}, fmt.Errorf("invalid -auth-error %q (valid: access_denied, invalid_request)", *authError)
	}
	switch *groupsError {
	case "":
	case "non-array":
		errMode.GroupsNonArray = true
	case "overage":
		errMode.GroupsOverageMarker = true
	case "absent":
		errMode.GroupsAbsentEverywhere = true
	default:
		return cliConfig{}, fmt.Errorf("invalid -groups-error %q (valid: non-array, overage, absent)", *groupsError)
	}
	switch *userinfoError {
	case "":
	case "sub-mismatch":
		errMode.UserinfoSubMismatch = true
	case "redirect":
		errMode.UserinfoRedirect = true
	case "non-json":
		errMode.UserinfoNonJSON = true
	case "unavailable":
		errMode.UserinfoUnavailable = true
	default:
		return cliConfig{}, fmt.Errorf("invalid -userinfo-error %q (valid: sub-mismatch, redirect, non-json, unavailable)", *userinfoError)
	}
	if *userinfoSubMismatch {
		errMode.UserinfoSubMismatch = true
	}
	errMode.EmailVerifiedFalse = !*emailVerified
	errMode.DiscoveryHTTPTokenEndpoint = *discoveryHTTPTokenEndpoint
	errMode.TokenEndpointRedirect = *tokenEndpointRedirect
	if !*oidc && (*groupsError != "" || *userinfoError != "" || *userinfoSubMismatch || !*emailVerified ||
		*discoveryHTTPTokenEndpoint || *tokenEndpointRedirect) {
		return cliConfig{}, fmt.Errorf("the OIDC tamper flags need -oidc")
	}

	// Users: email:password[:group1,group2]
	validUsers := map[string]string{}
	var userClaims map[string]map[string]any
	for _, spec := range users {
		email, password, claims, err := parseUserSpec(spec, *groupsClaim)
		if err != nil {
			return cliConfig{}, err
		}
		validUsers[email] = password
		if userClaims == nil {
			userClaims = map[string]map[string]any{}
		}
		userClaims[email] = claims
	}
	if len(validUsers) == 0 {
		validUsers = nil // keep the testuser/testpass default
	}

	// Runlayer mode implies require-resource
	effectiveRequireResource := *requireResource || *runlayerMode

	testClientRedirects := []string{
		"http://127.0.0.1/callback",
		"http://localhost/callback",
		"http://127.0.0.1:9000/callback", // Allow callback on same port as OAuth server
		// mcpproxy's own loopback callback path (internal/oauth
		// DefaultRedirectPath) — needed to drive a real login
		// against this server.
		"http://127.0.0.1/oauth/callback",
		"http://localhost/oauth/callback",
	}
	testClientRedirects = append(testClientRedirects, redirectURIs...)

	opts := oauthserver.Options{
		// Feature toggles (inverted from no-* flags)
		EnableAuthCode:          !*noAuthCode,
		EnableDeviceCode:        !*noDeviceCode,
		EnableDCR:               !*noDCR,
		EnableClientCredentials: !*noClientCreds,
		EnableRefreshToken:      !*noRefreshToken,

		// Security
		RequirePKCE:              *requirePKCE,
		RequireResourceIndicator: effectiveRequireResource,

		// Compatibility modes
		RunlayerMode: *runlayerMode,

		// Detection
		DetectionMode:    dm,
		MCPPerMethodAuth: *perMethodAuth,

		// Token lifetimes
		AccessTokenExpiry:  *accessTokenTTL,
		RefreshTokenExpiry: *refreshTokenTTL,

		// Error injection
		ErrorMode: errMode,

		// OIDC
		OIDC:               *oidc,
		ValidUsers:         validUsers,
		UserClaims:         userClaims,
		ClientRedirectURIs: []string(redirectURIs),
		GroupsClaim:        *groupsClaim,

		// Pre-register test-client for Playwright tests
		Clients: []oauthserver.ClientConfig{
			{
				ClientID:     "test-client",
				ClientName:   "Test Client",
				RedirectURIs: testClientRedirects,
			},
		},
	}

	return cliConfig{Port: *port, DetectionName: *detectionMode, Options: opts}, nil
}

// applyTokenError maps -token-error onto ErrorMode.
func applyTokenError(errMode *oauthserver.ErrorMode, name string, oidc bool) error {
	switch name {
	case "":
		return nil
	case "invalid_client":
		errMode.TokenInvalidClient = true
		return nil
	case "invalid_grant":
		errMode.TokenInvalidGrant = true
		return nil
	case "invalid_scope":
		errMode.TokenInvalidScope = true
		return nil
	case "server_error":
		errMode.TokenServerError = true
		return nil
	}

	idTokenKnobs := map[string]*bool{
		"bad-signature":    &errMode.IDTokenBadSignature,
		"wrong-iss":        &errMode.IDTokenWrongIssuer,
		"wrong-aud":        &errMode.IDTokenWrongAudience,
		"multi-aud-no-azp": &errMode.IDTokenMultiAudNoAzp,
		"expired":          &errMode.IDTokenExpired,
		"nbf-future":       &errMode.IDTokenNbfFuture,
		"no-nonce":         &errMode.IDTokenNoNonce,
		"alg-none":         &errMode.IDTokenAlgNone,
		"hs256":            &errMode.IDTokenHS256,
		"unknown-kid":      &errMode.IDTokenUnknownKid,
		"key-alg-mismatch": &errMode.IDTokenKeyAlgMismatch,
	}
	knob, ok := idTokenKnobs[name]
	if !ok {
		return fmt.Errorf("invalid -token-error %q (valid: invalid_client, invalid_grant, invalid_scope, server_error, bad-signature, wrong-iss, wrong-aud, multi-aud-no-azp, expired, nbf-future, no-nonce, alg-none, hs256, unknown-kid, key-alg-mismatch)", name)
	}
	if !oidc {
		return fmt.Errorf("-token-error %q is an id_token defect and needs -oidc", name)
	}
	*knob = true
	return nil
}

// parseUserSpec parses "email:password[:group1,group2]" into the ValidUsers
// entry and the UserClaims record (sub derived from the email, name = the
// local part, email_verified true, groups under groupsClaim).
func parseUserSpec(spec, groupsClaim string) (email, password string, claims map[string]any, err error) {
	parts := strings.SplitN(spec, ":", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", nil, fmt.Errorf("invalid -user %q: want email:password[:group1,group2]", spec)
	}
	email, password = parts[0], parts[1]
	groups := []any{}
	if len(parts) == 3 {
		for _, g := range strings.Split(parts[2], ",") {
			if g = strings.TrimSpace(g); g != "" {
				groups = append(groups, g)
			}
		}
	}
	sum := sha256.Sum256([]byte(email))
	claims = map[string]any{
		"sub":            "sub-" + hex.EncodeToString(sum[:8]),
		"email":          email,
		"email_verified": true,
		"name":           strings.SplitN(email, "@", 2)[0],
		groupsClaim:      groups,
	}
	return email, password, claims, nil
}

func main() {
	cfg, err := parseCLI(os.Args[1:], os.Stderr)
	if err != nil {
		if err == flag.ErrHelp {
			os.Exit(0) // usage already printed
		}
		log.Fatalf("%v", err)
	}
	opts := cfg.Options

	server := oauthserver.StartOnPort(nil, cfg.Port, opts)

	fmt.Println("========================================")
	if opts.OIDC {
		fmt.Println("OAuth Test Server (OpenID Provider mode)")
	} else {
		fmt.Println("OAuth Test Server")
	}
	fmt.Println("========================================")
	fmt.Printf("Listening on:      http://localhost:%d\n", cfg.Port)
	fmt.Printf("Issuer:            %s\n", server.IssuerURL)
	fmt.Println("")
	fmt.Println("Endpoints:")
	if opts.EnableAuthCode {
		fmt.Printf("  Authorization:   %s\n", server.AuthorizationEndpoint)
	}
	fmt.Printf("  Token:           %s\n", server.TokenEndpoint)
	fmt.Printf("  JWKS:            %s\n", server.JWKSURL)
	dm := opts.DetectionMode
	if dm == oauthserver.Discovery || dm == oauthserver.Both {
		fmt.Printf("  Discovery:       %s/.well-known/oauth-authorization-server\n", server.IssuerURL)
		if opts.OIDC {
			fmt.Printf("  OIDC Discovery:  %s/.well-known/openid-configuration\n", server.IssuerURL)
		}
	}
	if opts.OIDC {
		fmt.Printf("  Userinfo:        %s\n", server.UserinfoEndpoint)
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
	fmt.Printf("  Runlayer Mode:   %s (Pydantic 422 errors)\n", boolToEnabled(opts.RunlayerMode))
	fmt.Printf("  Detection Mode:  %s\n", cfg.DetectionName)
	fmt.Printf("  OIDC:            %s\n", boolToEnabled(opts.OIDC))
	if opts.OIDC {
		fmt.Printf("  Groups Claim:    %s\n", opts.GroupsClaim)
		for _, uri := range opts.ClientRedirectURIs {
			fmt.Printf("  Redirect URI:    %s\n", uri)
		}
		if tamper := describeTamper(opts.ErrorMode); tamper != "" {
			fmt.Printf("  Tamper:          %s\n", tamper)
		}
	}
	fmt.Println("")
	if len(opts.ValidUsers) == 0 {
		fmt.Println("Test Credentials:  testuser / testpass")
	} else {
		fmt.Println("Test Credentials (username / password / groups):")
		for user, pass := range opts.ValidUsers {
			fmt.Printf("  %s / %s / %v\n", user, pass, opts.UserClaims[user][opts.GroupsClaim])
		}
	}
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

// describeTamper names the active OIDC tamper knobs for the banner.
func describeTamper(m oauthserver.ErrorMode) string {
	var on []string
	for name, set := range map[string]bool{
		"IDTokenBadSignature": m.IDTokenBadSignature, "IDTokenWrongIssuer": m.IDTokenWrongIssuer,
		"IDTokenWrongAudience": m.IDTokenWrongAudience, "IDTokenMultiAudNoAzp": m.IDTokenMultiAudNoAzp,
		"IDTokenExpired": m.IDTokenExpired, "IDTokenNbfFuture": m.IDTokenNbfFuture,
		"IDTokenNoNonce": m.IDTokenNoNonce, "IDTokenAlgNone": m.IDTokenAlgNone,
		"IDTokenHS256": m.IDTokenHS256, "IDTokenUnknownKid": m.IDTokenUnknownKid,
		"IDTokenKeyAlgMismatch": m.IDTokenKeyAlgMismatch, "EmailVerifiedFalse": m.EmailVerifiedFalse,
		"GroupsNonArray": m.GroupsNonArray, "GroupsOverageMarker": m.GroupsOverageMarker,
		"GroupsAbsentEverywhere": m.GroupsAbsentEverywhere, "UserinfoSubMismatch": m.UserinfoSubMismatch,
		"UserinfoRedirect": m.UserinfoRedirect, "UserinfoNonJSON": m.UserinfoNonJSON,
		"UserinfoUnavailable": m.UserinfoUnavailable, "DiscoveryHTTPTokenEndpoint": m.DiscoveryHTTPTokenEndpoint,
		"TokenEndpointRedirect": m.TokenEndpointRedirect, "AuthAccessDenied": m.AuthAccessDenied,
	} {
		if set {
			on = append(on, name)
		}
	}
	return strings.Join(on, ", ")
}

func boolToEnabled(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

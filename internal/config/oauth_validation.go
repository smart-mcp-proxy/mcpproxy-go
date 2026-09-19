package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// OAuthCallbackPath is the loopback path mcpproxy's OAuth callback server
// listens on when `oauth.redirect_uri` does not specify its own path (or is
// unset, in which case mcpproxy allocates a dynamic port entirely).
//
// It lives here rather than in internal/oauth because internal/oauth imports
// this package (never the other way round) and the config layer has to validate
// `oauth.redirect_uri` where the operator types it. internal/oauth aliases it as
// DefaultRedirectPath.
const OAuthCallbackPath = "/oauth/callback"

// LoopbackIPv4Host / LoopbackIPv6Host are the addresses the callback listener
// binds for the two loopback families a pinned redirect URI can name.
const (
	LoopbackIPv4Host = "127.0.0.1"
	LoopbackIPv6Host = "::1"
)

// Reserved OAuth 2.0 parameters that cannot be overridden via extra_params
var reservedOAuthParams = map[string]bool{
	"client_id":             true,
	"client_secret":         true,
	"redirect_uri":          true,
	"response_type":         true,
	"scope":                 true,
	"state":                 true,
	"code_challenge":        true,
	"code_challenge_method": true,
	"grant_type":            true,
	"code":                  true,
	"refresh_token":         true,
	"code_verifier":         true,
}

// ValidateOAuthExtraParams validates that extra_params does not attempt to override reserved OAuth 2.0 parameters
func ValidateOAuthExtraParams(params map[string]string) error {
	if len(params) == 0 {
		return nil
	}

	var reservedKeys []string
	for key := range params {
		if reservedOAuthParams[strings.ToLower(key)] {
			reservedKeys = append(reservedKeys, key)
		}
	}

	if len(reservedKeys) > 0 {
		return fmt.Errorf("extra_params cannot override reserved OAuth 2.0 parameters: %s", strings.Join(reservedKeys, ", "))
	}

	return nil
}

// LoopbackBindHost maps the hostname of a pinned redirect URI onto the address
// the callback listener must bind.
//
// "localhost" deliberately binds 127.0.0.1 rather than resolving it: the URI
// string sent to the provider keeps the operator's spelling (providers match it
// literally), while the listener stays on the family mcpproxy can guarantee.
// Mainstream browsers fall back from ::1 to 127.0.0.1 via Happy Eyeballs, and
// docs/configuration.md steers operators to 127.0.0.1 for that reason.
func LoopbackBindHost(hostname string) string {
	if hostname == LoopbackIPv6Host {
		return LoopbackIPv6Host
	}
	return LoopbackIPv4Host
}

// ParseLoopbackRedirectURI validates an operator-pinned OAuth redirect URI (the
// per-server `oauth.redirect_uri` config field) and returns the loopback host to
// bind, the port it pins, and the callback path the listener must serve.
//
// Providers such as GitHub OAuth Apps require the callback URL to match the
// registered one exactly and reject wildcards, so operators need a way to nail
// the loopback port down instead of letting mcpproxy allocate a fresh one per
// login. The URI must therefore be an RFC 8252 loopback redirect with an
// explicit port, e.g.
//
//	http://127.0.0.1:54108/oauth/callback
//
// The path is taken verbatim from the URI rather than pinned to
// OAuthCallbackPath: some authorization servers publish a single shared OAuth
// application whose registered redirect path an operator cannot change (issue
// #1304), and mcpproxy's own callback path is an implementation detail the
// provider has no reason to agree with. A URI with no path at all (e.g.
// "http://127.0.0.1:54108") defaults to "/" - what an HTTP client actually
// requests for a path-less URL - not to OAuthCallbackPath, so the listener
// matches what the provider will really send. OAuthCallbackPath remains the
// default only when oauth.redirect_uri is unset entirely (dynamic allocation).
// Anything else about the URI is rejected with an actionable error rather than
// silently downgraded to dynamic allocation.
func ParseLoopbackRedirectURI(rawURI string) (bindHost string, port int, path string, err error) {
	trimmed := strings.TrimSpace(rawURI)
	if trimmed == "" {
		return "", 0, "", fmt.Errorf("oauth.redirect_uri is empty")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", 0, "", fmt.Errorf("oauth.redirect_uri %q is not a valid URL: %w", trimmed, err)
	}

	if parsed.Scheme != "http" {
		return "", 0, "", fmt.Errorf("oauth.redirect_uri %q must use the http scheme for a loopback redirect (RFC 8252), got %q", trimmed, parsed.Scheme)
	}

	hostname := parsed.Hostname()
	switch hostname {
	case LoopbackIPv4Host, "localhost", LoopbackIPv6Host:
	default:
		return "", 0, "", fmt.Errorf("oauth.redirect_uri %q must use a loopback host (127.0.0.1, localhost or ::1), got %q", trimmed, hostname)
	}

	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", 0, "", fmt.Errorf("oauth.redirect_uri %q must not contain a query string or fragment", trimmed)
	}

	portStr := parsed.Port()
	if portStr == "" {
		return "", 0, "", fmt.Errorf("oauth.redirect_uri %q must include an explicit port to pin (e.g. http://127.0.0.1:54108%s)", trimmed, OAuthCallbackPath)
	}

	parsedPort, err := strconv.Atoi(portStr)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return "", 0, "", fmt.Errorf("oauth.redirect_uri %q has an invalid port %q (expected 1-65535)", trimmed, portStr)
	}

	callbackPath := parsed.Path
	if callbackPath == "" {
		callbackPath = "/"
	}

	return LoopbackBindHost(hostname), parsedPort, callbackPath, nil
}

// Validate performs validation on OAuthConfig
func (o *OAuthConfig) Validate() error {
	if o == nil {
		return nil
	}

	// Validate extra params
	if err := ValidateOAuthExtraParams(o.ExtraParams); err != nil {
		return fmt.Errorf("oauth config validation failed: %w", err)
	}

	// A malformed redirect_uri is a permanent, undiagnosable connect failure
	// (the provider rejects a callback URL that does not match the registered
	// one, and mcpproxy refuses to silently downgrade to a random port).
	// Reject it where the operator is typing it instead.
	if strings.TrimSpace(o.RedirectURI) != "" {
		if _, _, _, err := ParseLoopbackRedirectURI(o.RedirectURI); err != nil {
			return fmt.Errorf("oauth config validation failed: %w", err)
		}
	}

	return nil
}

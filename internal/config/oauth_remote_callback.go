package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const OAuthRemoteCallbackPrefix = "/oauth/callback/"

// ValidateOAuthCallbackBaseURL accepts an explicit HTTPS origin for a reverse
// proxy. It is never inferred from untrusted Host or forwarded headers.
func ValidateOAuthCallbackBaseURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || strings.TrimSpace(raw) != raw || u == nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return fmt.Errorf("oauth.callback_base_url must be an absolute HTTPS origin without credentials, path, query or fragment")
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("oauth.callback_base_url port %q must be between 1 and 65535", port)
		}
	}
	if host := u.Hostname(); strings.HasPrefix(u.Host, "[") && net.ParseIP(host) == nil {
		return fmt.Errorf("oauth.callback_base_url host %q is not a valid IPv6 literal", host)
	}
	return nil
}

func OAuthRemoteCallbackURL(base, serverName string) (string, error) {
	if err := ValidateOAuthCallbackBaseURL(base); err != nil {
		return "", err
	}
	if base == "" {
		return "", nil
	}
	// "." and ".." survive PathEscape and the mux would normalise them away.
	if serverName == "." || serverName == ".." {
		return "", fmt.Errorf("server name %q cannot be used in a remote OAuth callback path", serverName)
	}
	return strings.TrimSuffix(base, "/") + OAuthRemoteCallbackPrefix + url.PathEscape(serverName), nil
}

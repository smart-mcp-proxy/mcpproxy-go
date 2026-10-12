package config

import (
	"fmt"
	"net/url"
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
	return nil
}

func OAuthRemoteCallbackURL(base, serverName string) (string, error) {
	if err := ValidateOAuthCallbackBaseURL(base); err != nil {
		return "", err
	}
	if base == "" {
		return "", nil
	}
	return strings.TrimSuffix(base, "/") + OAuthRemoteCallbackPrefix + url.PathEscape(serverName), nil
}

package oauth

import (
	"fmt"
	"net/url"
)

// OAuthTransportWrapper provides utility methods for OAuth parameter injection
// Note: This is a simplified implementation. Full integration with mcp-go's OAuth
// flow would require upstream changes to support extra parameters natively.
type OAuthTransportWrapper struct {
	extraParams map[string]string
}

// NewOAuthTransportWrapper creates a wrapper that can inject extra parameters
func NewOAuthTransportWrapper(extraParams map[string]string) *OAuthTransportWrapper {
	return &OAuthTransportWrapper{
		extraParams: extraParams,
	}
}

// InjectExtraParamsIntoURL adds extra parameters to OAuth URL
func (w *OAuthTransportWrapper) InjectExtraParamsIntoURL(baseURL string) (string, error) {
	if len(w.extraParams) == 0 {
		return baseURL, nil
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid OAuth URL: %w", err)
	}

	q := u.Query()
	for key, value := range w.extraParams {
		q.Set(key, value)
	}
	u.RawQuery = q.Encode()

	return u.String(), nil
}

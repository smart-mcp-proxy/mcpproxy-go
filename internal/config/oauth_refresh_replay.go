package config

import "fmt"

// RefreshReplayAllowed is fail-closed for hand-edited configurations too.
// Enabling this asserts that the pinned endpoint returns the same token pair
// when a consumed refresh token is retried within the 30-second flight budget.
func (o *OAuthConfig) RefreshReplayAllowed() bool {
	return o != nil && o.RefreshReplayEnabled != nil && *o.RefreshReplayEnabled &&
		o.TokenEndpoint != "" && validateOAuthEndpointURL(o.TokenEndpoint) == nil
}

// ValidateRefreshReplay checks a complete, merged OAuth configuration. Partial
// patches may inherit their endpoint, so every write gate (ValidateDetailed,
// the MCP upstream_servers add and patch doors) calls this after merging.
// The error deliberately excludes the endpoint value.
func (o *OAuthConfig) ValidateRefreshReplay() error {
	if o == nil || o.RefreshReplayEnabled == nil || !*o.RefreshReplayEnabled {
		return nil
	}
	if o.TokenEndpoint == "" || validateOAuthEndpointURL(o.TokenEndpoint) != nil {
		return fmt.Errorf("oauth.refresh_replay_enabled requires a valid explicit oauth.token_endpoint with idempotent refresh replay support")
	}
	return nil
}

func (c *Config) oauthRefreshReplayErrors() []ValidationError {
	var errs []ValidationError
	for i, s := range c.Servers {
		if s == nil {
			continue
		}
		if err := s.OAuth.ValidateRefreshReplay(); err != nil {
			errs = append(errs, ValidationError{Field: fmt.Sprintf("mcpServers[%d].oauth.refresh_replay_enabled", i), Message: err.Error()})
		}
	}
	return errs
}

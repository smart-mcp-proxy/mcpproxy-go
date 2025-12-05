package config

import (
	"fmt"
	"strings"
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

// Validate performs validation on OAuthConfig
func (o *OAuthConfig) Validate() error {
	if o == nil {
		return nil
	}

	// Validate extra params
	if err := ValidateOAuthExtraParams(o.ExtraParams); err != nil {
		return fmt.Errorf("oauth config validation failed: %w", err)
	}

	return nil
}

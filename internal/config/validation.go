package config

import (
	"fmt"
	"strings"
)

// reservedOAuthParams contains OAuth 2.0/2.1 parameters that cannot be overridden
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
	"token_type":            true,
}

// ValidateOAuthExtraParams ensures extra_params don't override reserved parameters
func ValidateOAuthExtraParams(params map[string]string) error {
	for key := range params {
		if reservedOAuthParams[strings.ToLower(key)] {
			return fmt.Errorf("extra_params cannot override reserved OAuth parameter: %s", key)
		}
	}
	return nil
}

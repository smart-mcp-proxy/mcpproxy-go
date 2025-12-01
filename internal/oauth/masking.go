package oauth

import "strings"

// maskOAuthSecret masks an OAuth secret by showing the first 3 and last 4 characters.
// For secrets shorter than 8 characters, it returns "***".
//
// This is used for displaying client IDs, client secrets, and other sensitive values
// in logs and command output for debugging purposes.
func maskOAuthSecret(secret string) string {
	if len(secret) <= 8 {
		return "***"
	}
	// Show first 3 and last 4 chars: "abc***xyz9"
	return secret[:3] + "***" + secret[len(secret)-4:]
}

// isResourceParam determines if a parameter key represents a public resource URL
// that should be displayed in full for debugging purposes.
//
// Resource URLs are not sensitive (they're public endpoints), so they can be
// shown without masking in logs and command output.
func isResourceParam(key string) bool {
	keyLower := strings.ToLower(key)
	// Resource URLs and audience parameters are public, not sensitive
	return strings.HasPrefix(keyLower, "resource") || keyLower == "audience"
}

// maskExtraParams applies selective masking to OAuth extra parameters.
//
// Resource URLs and audience parameters are displayed in full since they are
// public endpoints. Other parameters are masked as they might contain secrets.
//
// This function is used when displaying extra_params in:
// - auth status command output
// - auth login command output
// - debug logs
func maskExtraParams(params map[string]string) map[string]string {
	if len(params) == 0 {
		return params
	}

	masked := make(map[string]string, len(params))
	for k, v := range params {
		if isResourceParam(k) {
			// Show resource URLs in full (public endpoints)
			masked[k] = v
		} else if containsSensitiveKeyword(k) {
			// Likely sensitive - mask completely
			masked[k] = "***"
		} else {
			// Default: partial masking for safety
			masked[k] = maskOAuthSecret(v)
		}
	}
	return masked
}

// containsSensitiveKeyword checks if a parameter key likely contains sensitive data
// based on common naming patterns for secrets.
func containsSensitiveKeyword(key string) bool {
	keyLower := strings.ToLower(key)
	sensitiveKeywords := []string{"key", "secret", "token", "password", "credential"}

	for _, keyword := range sensitiveKeywords {
		if strings.Contains(keyLower, keyword) {
			return true
		}
	}
	return false
}

package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsAuthError_DoesNotMatchStrategyWrapper verifies that the substring "auth"
// inside the strategy-name wrapper ("headers-auth strategy", "no-auth strategy")
// does NOT cause a non-auth transport/parse error to be misclassified as an
// authentication failure. Misclassification triggered an unintended OAuth
// fallback and surfaced a misleading "OAuth authentication required" message
// to users whose static Bearer token was actually fine (e.g. when the upstream
// returned HTTP 502 with a non-JSON body).
func TestIsAuthError_DoesNotMatchStrategyWrapper(t *testing.T) {
	c := &Client{}

	// Real scenario: upstream returns 502 with non-JSON body; initialize parse
	// fails; tryHeadersAuth wraps with "headers-auth strategy".
	upstream502 := errors.New("MCP initialize failed during headers-auth strategy: failed to unmarshal response: unexpected end of JSON input")
	assert.False(t, c.isAuthError(upstream502),
		"a JSON-parse failure wrapped by the headers-auth strategy must not be classified as an auth error")

	noAuthWrapper := errors.New("MCP initialize failed during no-auth strategy: failed to unmarshal response: unexpected end of JSON input")
	assert.False(t, c.isAuthError(noAuthWrapper),
		"a JSON-parse failure wrapped by the no-auth strategy must not be classified as an auth error")

	sseWrapper := errors.New("MCP initialize failed during SSE headers-auth strategy: context deadline exceeded")
	assert.False(t, c.isAuthError(sseWrapper),
		"a timeout wrapped by the SSE headers-auth strategy must not be classified as an auth error")
}

// TestIsAuthError_MatchesGenuineAuthFailures verifies that real 403 / explicit
// authentication-failure responses still trigger the fallback chain.
// Note: 401/Unauthorized is intentionally classified as an OAuth error (see
// isOAuthError), so it is exercised via TestIsAuthError_IgnoresOAuthErrors.
func TestIsAuthError_MatchesGenuineAuthFailures(t *testing.T) {
	c := &Client{}

	cases := []string{
		"server returned status 403 Forbidden",
		"403: forbidden",
		"authentication failed for user",
		"authorization failed",
	}
	for _, msg := range cases {
		assert.True(t, c.isAuthError(errors.New(msg)),
			"expected isAuthError to match genuine auth failure: %q", msg)
	}
}

// TestIsAuthError_IgnoresOAuthErrors ensures OAuth-classified errors are not
// double-classified as generic auth errors (the OAuth branch handles them).
func TestIsAuthError_IgnoresOAuthErrors(t *testing.T) {
	c := &Client{}
	err := errors.New("no valid token available, authorization required")
	assert.True(t, c.isOAuthError(err), "sanity: should be an OAuth error")
	assert.False(t, c.isAuthError(err),
		"OAuth errors must not also be classified as generic auth errors")
}

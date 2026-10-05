package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	transport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
)

// mcpGoJSONError reproduces mcp-go's extractOAuthError for a non-2xx JSON body:
// fmt.Errorf("%s: %w", context, oauthErr) with a value OAuthError.
func mcpGoJSONError(code string) error {
	return fmt.Errorf("refresh token request failed: %w", transport.OAuthError{ErrorCode: code, ErrorDescription: "desc"})
}

func TestClassifyRefreshError_Structured(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	resetErr := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}

	tests := []struct {
		name       string
		err        error
		wantClass  RefreshErrorClass
		wantStatus int
		terminal   bool
		label      string
	}{
		{"nil", nil, RefreshClassSuccess, 0, false, "success"},
		{"invalid_grant wrapped by mcp-go", mcpGoJSONError("invalid_grant"), RefreshClassInvalidGrant, 0, true, "failed_invalid_grant"},
		{"invalid_grant 200-with-error body", fmt.Errorf("refresh token request failed: %w", transport.OAuthError{ErrorCode: "invalid_grant"}), RefreshClassInvalidGrant, 0, true, "failed_invalid_grant"},
		{"invalid_grant wrapped twice", fmt.Errorf("OAuth refresh failed for s: %w", mcpGoJSONError("invalid_grant")), RefreshClassInvalidGrant, 0, true, "failed_invalid_grant"},
		{"invalid_client", mcpGoJSONError("invalid_client"), RefreshClassInvalidClient, 0, true, "failed_invalid_client"},
		{"unauthorized_client", mcpGoJSONError("unauthorized_client"), RefreshClassTerminalOther, 0, true, "failed_other"},
		{"unsupported_grant_type", mcpGoJSONError("unsupported_grant_type"), RefreshClassTerminalOther, 0, true, "failed_other"},
		{"invalid_scope", mcpGoJSONError("invalid_scope"), RefreshClassTerminalOther, 0, true, "failed_other"},
		{"server_error code", mcpGoJSONError("server_error"), RefreshClassServerError, 0, false, "failed_server_error"},
		{"temporarily_unavailable code", mcpGoJSONError("temporarily_unavailable"), RefreshClassServerError, 0, false, "failed_server_error"},
		{"unknown oauth code", mcpGoJSONError("weird_code"), RefreshClassOther, 0, false, "failed_other"},
		{"mcp-go 503 html body", errors.New("refresh token request failed with status 503: <html>bad gateway</html>"), RefreshClassServerError, 503, false, "failed_server_error"},
		{"mcp-go 429", fmt.Errorf("OAuth refresh failed for x: %w", errors.New("refresh token request failed with status 429: slow down")), RefreshClassServerError, 429, false, "failed_server_error"},
		{"mcp-go 400 non-json", errors.New("refresh token request failed with status 400: nope"), RefreshClassOther, 400, false, "failed_other"},
		{"manual path typed 401 invalid_client", &RefreshHTTPError{Status: 401, OAuthCode: "invalid_client"}, RefreshClassInvalidClient, 401, true, "failed_invalid_client"},
		{"manual path typed 400 invalid_grant", fmt.Errorf("wrapped: %w", &RefreshHTTPError{Status: 400, OAuthCode: "invalid_grant"}), RefreshClassInvalidGrant, 400, true, "failed_invalid_grant"},
		{"manual path typed 502 no code", &RefreshHTTPError{Status: 502}, RefreshClassServerError, 502, false, "failed_server_error"},
		{"manual path typed 418 no code", &RefreshHTTPError{Status: 418}, RefreshClassOther, 418, false, "failed_other"},
		{"net dial refused", fmt.Errorf("failed to send refresh token request: %w", &netURLErr{dialErr}), RefreshClassNetwork, 0, false, "failed_network"},
		{"net reset", fmt.Errorf("x: %w", resetErr), RefreshClassNetwork, 0, false, "failed_network"},
		{"syscall reset bare", fmt.Errorf("x: %w", syscall.ECONNRESET), RefreshClassNetwork, 0, false, "failed_network"},
		{"deadline", fmt.Errorf("x: %w", context.DeadlineExceeded), RefreshClassNetwork, 0, false, "failed_network"},
		{"transient sentinel", fmt.Errorf("%w: cooldown", ErrTokenRefreshTransient), RefreshClassServerError, 0, false, "failed_server_error"},
		{"server not found", errors.New("server not found: gcw2"), RefreshClassServerGone, 0, true, "failed_server_gone"},
		{"server does not use OAuth", fmt.Errorf("x: %w", ErrServerNotOAuth), RefreshClassServerGone, 0, true, "failed_server_gone"},
		{"no client credentials", fmt.Errorf("x: %w", ErrNoClientCredentials), RefreshClassTerminalOther, 0, true, "failed_other"},
		{"typed terminal failure keeps class", &RefreshFailure{Class: RefreshClassInvalidClient, Message: "m", Err: errors.New("e")}, RefreshClassInvalidClient, 0, true, "failed_invalid_client"},
		// Substring fallbacks for non-compliant bodies (each pattern covered).
		{"fallback invalid_grant text", errors.New("invalid_grant: refresh token expired"), RefreshClassInvalidGrant, 0, true, "failed_invalid_grant"},
		{"fallback refresh token expired", errors.New("refresh token expired"), RefreshClassInvalidGrant, 0, true, "failed_invalid_grant"},
		{"fallback refresh token revoked", errors.New("refresh token revoked"), RefreshClassInvalidGrant, 0, true, "failed_invalid_grant"},
		{"fallback refresh token invalid", errors.New("refresh token invalid"), RefreshClassInvalidGrant, 0, true, "failed_invalid_grant"},
		{"fallback invalid_client text", errors.New("token refresh failed: invalid_client"), RefreshClassInvalidClient, 0, true, "failed_invalid_client"},
		{"fallback timeout", errors.New("connection timeout"), RefreshClassNetwork, 0, false, "failed_network"},
		{"fallback refused", errors.New("connection refused"), RefreshClassNetwork, 0, false, "failed_network"},
		{"fallback reset", errors.New("connection reset by peer"), RefreshClassNetwork, 0, false, "failed_network"},
		{"fallback no such host", errors.New("lookup x: no such host"), RefreshClassNetwork, 0, false, "failed_network"},
		{"fallback dial tcp", errors.New("dial tcp: something"), RefreshClassNetwork, 0, false, "failed_network"},
		{"fallback network", errors.New("network is unreachable"), RefreshClassNetwork, 0, false, "failed_network"},
		{"fallback EOF", errors.New("unexpected EOF"), RefreshClassNetwork, 0, false, "failed_network"},
		{"fallback deadline text", errors.New("context deadline exceeded"), RefreshClassNetwork, 0, false, "failed_network"},
		{"unknown", errors.New("unknown server error"), RefreshClassOther, 0, false, "failed_other"},
		{"server_error plain text stays other", errors.New("OAuth server_error"), RefreshClassOther, 0, false, "failed_other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cls, status := ClassifyRefreshError(tt.err)
			assert.Equal(t, tt.wantClass, cls)
			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.terminal, cls.IsTerminal(), "terminal")
			assert.Equal(t, tt.label, cls.MetricLabel(), "metric label")
		})
	}
}

// netURLErr mimics *url.Error wrapping a net.OpError.
type netURLErr struct{ err error }

func (e *netURLErr) Error() string { return "Post \"https://as/token\": " + e.err.Error() }
func (e *netURLErr) Unwrap() error { return e.err }

func TestRefreshHTTPErrorDoesNotLeakBody(t *testing.T) {
	e := &RefreshHTTPError{Status: 400, OAuthCode: "invalid_grant", Detail: "scrubbed detail"}
	assert.Contains(t, e.Error(), "400")
	assert.Contains(t, e.Error(), "invalid_grant")
	assert.Contains(t, e.Error(), "scrubbed detail")
}

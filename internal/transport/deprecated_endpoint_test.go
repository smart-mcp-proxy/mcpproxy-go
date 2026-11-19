package transport

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

// GH #1929 CI (Build Binaries linux/arm64): TestNetworkRecovery_StaticHeaderAndOAuthRecoverWithin60s
// failed with "endpoint deprecated (410 Gone)" after the network returned: the
// text classifier matched "410" anywhere, including in a port number.
func TestLooksLikeDeprecatedEndpoint(t *testing.T) {
	notDeprecated := []error{
		errors.New(`transport error: failed to send request: Post "http://127.0.0.1:41039/mcp": dial tcp 127.0.0.1:41039: connect: connection refused`),
		errors.New(`Post "http://127.0.0.1:34101/mcp": EOF`),
		errors.New(`initialize: read tcp 127.0.0.1:52410->127.0.0.1:8410: connection reset by peer`),
		errors.New(`request to 10.4.10.4:9000 timed out`),
		fmt.Errorf("initialize: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused: 410")}),
		errors.New("the gonelike field is unknown"),
		// A TLS failure quoting a URL whose path says "deprecated".
		fmt.Errorf("initialize: %w", &url.Error{Op: "Post", URL: "https://h/deprecated-api/mcp", Err: errors.New("x509: certificate signed by unknown authority")}),
		nil,
	}
	for _, err := range notDeprecated {
		assert.False(t, LooksLikeDeprecatedEndpoint(err), "%v", err)
	}

	deprecated := []error{
		NewEndpointDeprecatedError("u", "m", "", ""),
		errors.New("request failed with status 410: Gone"),
		errors.New("unexpected status code: 410"),
		errors.New("HTTP 410 Gone"),
		errors.New("410 Gone"),
		errors.New("the SSE transport has been removed; use the HTTP transport"),
		errors.New("this endpoint is deprecated"),
	}
	for _, err := range deprecated {
		assert.True(t, LooksLikeDeprecatedEndpoint(err), "%v", err)
	}
}

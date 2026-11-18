package managed

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Review r7 on GH #1929: crypto/tls reports a TLS alert the server sent as a
// *net.OpError with Op "remote error". A rejected client certificate is a
// credential fault, not a network outage, so the typed socket rule must not
// call it a connection error (which would show "Reconnecting…").
func TestTLSAlertIsNotASocketFailure(t *testing.T) {
	alert := fmt.Errorf("transport: %w", &net.OpError{Op: "remote error", Err: errors.New("tls: certificate required")})
	assert.False(t, (&Client{}).isConnectionError(alert), "a TLS alert is not a connection error")
	assert.False(t, isTransientHealthCheckError(alert), "a TLS alert is not a transient miss")

	refused := fmt.Errorf("transport: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connectex: No connection could be made")})
	assert.True(t, (&Client{}).isConnectionError(refused), "a dial failure stays a connection error")
}

package managed

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// GH #1929 (CI, windows-latest): Windows words socket failures differently
// from Unix. A dropped network there surfaced as "connectex: No connection
// could be made because the target machine actively refused it." or
// "wsarecv: An existing connection was forcibly closed by the remote host.",
// which none of the text classifiers knew, so a connected server's health
// check never noticed the outage and the server stayed Ready.
func TestNetworkErrorClassifiers_KnowWindowsWordings(t *testing.T) {
	windows := []error{
		errors.New(`transport error: failed to send request: Post "http://127.0.0.1:64438/mcp": dial tcp 127.0.0.1:64438: connectex: No connection could be made because the target machine actively refused it.`),
		errors.New(`read tcp 127.0.0.1:50311->127.0.0.1:64438: wsarecv: An existing connection was forcibly closed by the remote host.`),
		errors.New(`write tcp 127.0.0.1:50311->127.0.0.1:64438: wsasend: An existing connection was aborted by the software in your host machine.`),
	}
	mc := newNetworkRecoveryClient(t, "https://mcp.example.com/mcp")
	for _, err := range windows {
		assert.True(t, mc.isConnectionError(err), "a connection error: %v", err)
		assert.False(t, isTransientHealthCheckError(err), "a hard failure that trips the health check at once: %v", err)
		assert.True(t, mc.isRemoteNetworkFailure(err), "a network failure: %v", err)
	}
}

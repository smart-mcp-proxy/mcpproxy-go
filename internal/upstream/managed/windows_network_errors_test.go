package managed

import (
	"errors"
	"fmt"
	"net"
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

type timeoutErr struct{}

func (timeoutErr) Error() string {
	return "A connection attempt failed because the connected party did not properly respond after a period of time"
}
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// Review r6: classify by type, not only by wording. Any *net.OpError is a
// connection error; a timeout is a soft health-check miss, everything else
// (unreachable network/host, refused, reset — in any OS's words) is hard.
func TestNetworkErrorClassifiers_UseTheErrorType(t *testing.T) {
	mc := newNetworkRecoveryClient(t, "https://mcp.example.com/mcp")
	unreachable := fmt.Errorf("transport error: %w", &net.OpError{Op: "dial", Net: "tcp",
		Err: errors.New("connectex: A socket operation was attempted to an unreachable network.")})
	assert.True(t, mc.isConnectionError(unreachable))
	assert.False(t, isTransientHealthCheckError(unreachable), "an unreachable network is a hard failure")
	assert.True(t, mc.isRemoteNetworkFailure(unreachable))

	timedOut := fmt.Errorf("transport error: %w", &net.OpError{Op: "dial", Net: "tcp", Err: timeoutErr{}})
	assert.True(t, mc.isConnectionError(timedOut))
	assert.True(t, isTransientHealthCheckError(timedOut), "a timeout stays a soft miss")
}

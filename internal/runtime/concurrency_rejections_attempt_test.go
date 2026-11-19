package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
)

// Spec 124 D2: a limiter rejection carries the id of the attempt being
// admitted, the one its dispatch path minted, so several shed children of one
// HTTP request do not collapse onto the transport request id they all share.
func TestAttemptRequestID_PrefersTheAttemptOverTheTransportID(t *testing.T) {
	transport := reqcontext.WithRequestID(context.Background(), "http-req-1")

	assert.Equal(t, "http-req-1", attemptRequestID(transport), "a caller that minted no attempt id keeps the transport id")

	childA := reqcontext.WithAttemptID(transport, "child-a")
	childB := reqcontext.WithAttemptID(transport, "child-b")
	assert.Equal(t, "child-a", attemptRequestID(childA))
	assert.Equal(t, "child-b", attemptRequestID(childB))
	assert.NotEqual(t, attemptRequestID(childA), attemptRequestID(childB), "two children of one request are two attempts")
}

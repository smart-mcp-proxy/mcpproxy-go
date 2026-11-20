package server

import (
	"context"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 125 FR-019a (review round 2): the REST handler uses the caller-aware
// queue read only when its controller offers it. *Server is that controller,
// so it must keep offering it, or scoped requests would read skill approvals.
func TestServerOffersTheCallerAwareReviewQueue(t *testing.T) {
	var controller interface{} = (*Server)(nil)
	_, ok := controller.(interface {
		GetReviewQueueWithSkills(ctx context.Context, includeSkills bool) (*runtime.ReviewQueue, error)
	})
	if !ok {
		t.Fatal("*Server must implement GetReviewQueueWithSkills")
	}
}

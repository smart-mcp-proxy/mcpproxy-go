package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Spec 117 FR-018 (#1610): an approval or enable refused because the
// server's latest capture is incomplete answers 503 with the
// ErrReviewNotRecorded text, never a generic 500.
func TestWriteApprovalWriteError_ReviewNotRecordedIs503(t *testing.T) {
	s := &Server{logger: zap.NewNop().Sugar()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/a/tools/approve", nil)
	err := fmt.Errorf("server 'a': %w", toolreview.ErrReviewNotRecorded)

	s.writeApprovalWriteError(rec, req, err, "Failed to approve tools")

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), toolreview.ErrReviewNotRecorded.Error())
}

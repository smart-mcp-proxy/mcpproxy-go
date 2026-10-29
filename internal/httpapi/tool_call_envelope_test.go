package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/stretchr/testify/require"
)

type toolCallEnvelopeProbe struct {
	*baseController
	wantsEnvelope bool
}

func (p *toolCallEnvelopeProbe) GetCurrentConfig() *config.Config { return nil }

func (p *toolCallEnvelopeProbe) CallTool(ctx context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
	p.wantsEnvelope = reqcontext.ToolResultEnvelope(ctx)
	return []interface{}{map[string]interface{}{"type": "text", "text": "ok"}}, nil
}

func TestToolCallResultEnvelopeHeaderIsOptIn(t *testing.T) {
	for _, tc := range []struct {
		name, header string
		want         bool
	}{
		{"ordinary REST", "", false},
		{"CLI opt-in", "1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &toolCallEnvelopeProbe{baseController: &baseController{}}
			srv := &Server{controller: probe}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/tools/call", strings.NewReader(`{"tool_name":"call_tool_read","arguments":{}}`))
			if tc.header != "" {
				req.Header.Set("X-MCPProxy-Result-Envelope", tc.header)
			}
			w := httptest.NewRecorder()
			srv.handleCallTool(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, tc.want, probe.wantsEnvelope)
		})
	}
}

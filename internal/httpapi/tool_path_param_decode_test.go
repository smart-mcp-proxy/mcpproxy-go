package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// TestToolSubresource_EncodedToolNameUnescaped: chi routes on RawPath, so the
// {tool} param arrives percent-encoded whenever the encoding differs from Go's
// default (the Web UI sends encodeURIComponent(toolName), which encodes ':' and
// '/'). Tool names such as "ns:erase" or "ns/erase" must be decoded before the
// exact-match approval lookup, the same way {id} is (MCP-1118).
func TestToolSubresource_EncodedToolNameUnescaped(t *testing.T) {
	for _, tc := range []struct{ encoded, decoded string }{
		{"ns%3Aerase", "ns:erase"},
		{"ns%2Ferase", "ns/erase"},
	} {
		newCtrl := func() *mockToolQuarantineController {
			return &mockToolQuarantineController{
				apiKey: "test-key",
				approvals: []*storage.ToolApprovalRecord{{
					ServerName:          "github",
					ToolName:            tc.decoded,
					Status:              storage.ToolApprovalStatusChanged,
					PreviousDescription: "old",
					CurrentDescription:  "new",
				}},
			}
		}

		t.Run("GET diff "+tc.decoded, func(t *testing.T) {
			server := NewServer(newCtrl(), zap.NewNop().Sugar(), nil)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/servers/github/tools/"+tc.encoded+"/diff", nil)
			req.Header.Set("X-API-Key", "test-key")
			w := httptest.NewRecorder()
			server.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
			var resp map[string]interface{}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			data := resp["data"].(map[string]interface{})
			assert.Equal(t, "new", data["current_description"])
		})

		t.Run("POST enabled "+tc.decoded, func(t *testing.T) {
			ctrl := newCtrl()
			server := NewServer(ctrl, zap.NewNop().Sugar(), nil)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/github/tools/"+tc.encoded+"/enabled",
				bytes.NewBufferString(`{"enabled":false}`))
			req.Header.Set("X-API-Key", "test-key")
			w := httptest.NewRecorder()
			server.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
			assert.Equal(t, tc.decoded, ctrl.setToolEnabledTool)
		})
	}
}

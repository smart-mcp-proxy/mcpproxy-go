package cliclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/stretchr/testify/require"
)

func TestCallToolPreservesResultShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{"legacy content array", `[{"type":"text","text":"{\"ok\":true}"},{"type":"image","data":"abc","mimeType":"image/png"}]`, `{"content":[{"type":"text","text":"{\"ok\":true}"},{"type":"image","data":"abc","mimeType":"image/png"}],"isError":false}`},
		{"full result", `{"content":[{"type":"text","text":"hello"}],"structuredContent":{"count":2},"isError":true,"_meta":{"source":"test"}}`, `{"content":[{"type":"text","text":"hello"}],"structuredContent":{"count":2},"isError":true,"_meta":{"source":"test"}}`},
		{"structured only", `{"structuredContent":{"count":2}}`, `{"content":[],"structuredContent":{"count":2},"isError":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/tools/call", r.URL.Path)
				require.Equal(t, "1", r.Header.Get("X-MCPProxy-Result-Envelope"))
				_, _ = w.Write([]byte(`{"success":true,"data":` + tc.data + `}`))
			}))
			defer srv.Close()
			got, err := cliclient.NewClient(srv.URL, nil).CallTool(context.Background(), "call_tool_read", nil)
			require.NoError(t, err)
			actual, err := json.Marshal(got)
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(actual))
		})
	}
}

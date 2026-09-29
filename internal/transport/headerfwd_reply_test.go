package transport

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 112 FR-008: mcp-go reuses the tools/call context (key B) when it POSTs
// replies to server-initiated requests that arrive inside the tools/call SSE
// stream. Those replies must not carry forwarded headers.
func TestCreateHTTPClient_ServerInitiatedReplyCarriesNoForwardedHeaders(t *testing.T) {
	for _, serverReq := range []string{"ping", "roots/list", "sampling/createMessage", "no/such/method"} {
		t.Run(serverReq, func(t *testing.T) {
			var callHdr, replyHdr http.Header
			replied := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				body, _ := io.ReadAll(r.Body)
				var msg struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				_ = json.Unmarshal(body, &msg)
				switch msg.Method {
				case "initialize":
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"x","version":"1"}}}`, msg.ID)
				case "tools/call":
					callHdr = r.Header.Clone()
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(http.StatusOK)
					fl := w.(http.Flusher)
					_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"srv-1\",\"method\":%q,\"params\":{}}\n\n", serverReq)
					fl.Flush()
					select {
					case <-replied:
					case <-time.After(5 * time.Second):
					}
					_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"content\":[]}}\n\n", msg.ID)
					fl.Flush()
				case "":
					replyHdr = r.Header.Clone()
					w.WriteHeader(http.StatusAccepted)
					close(replied)
				default:
					w.WriteHeader(http.StatusAccepted)
				}
			}))
			defer srv.Close()

			cfg := &HTTPTransportConfig{URL: srv.URL, Headers: map[string]string{"X-Static": "s"}}
			c, err := CreateHTTPClient(cfg)
			require.NoError(t, err)
			defer c.Close()
			callTool(t, c, outboundCtx(t, "X-User-Id", fwdSentinel))

			require.NotNil(t, callHdr, "tools/call POST not seen")
			assert.Equal(t, fwdSentinel, callHdr.Get("X-User-Id"))
			require.NotNil(t, replyHdr, "reply POST not seen")
			assert.Empty(t, replyHdr.Get("X-User-Id"), "reply to server-initiated request leaked forwarded header")
			assert.Equal(t, "s", replyHdr.Get("X-Static"))
		})
	}
}

func TestToolsCallOnlyBody(t *testing.T) {
	cases := map[string]bool{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`: true,
		` {"jsonrpc":"2.0","id":1,"method":"tools/call"}`:            true,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`:             false,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`:                   false,
		`{"jsonrpc":"2.0","id":1,"result":{}}`:                       false,
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32601}}`:           false,
		`{"jsonrpc":"2.0","method":"tools/call"}`:                    false, // notification (no id)
		`[{"jsonrpc":"2.0","id":1,"method":"tools/call"}]`:           false,
		`{"jsonrpc":"2.0","id":1,"method":"Tools/Call"}`:             false,
		`not json`: false,
		``:         false,
	}
	for body, want := range cases {
		assert.Equal(t, want, isToolsCallBody([]byte(body)), body)
	}
}

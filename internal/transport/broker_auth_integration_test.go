package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// newCapturingMCPServer stands up a minimal streamable-HTTP MCP endpoint that
// records the inbound Authorization header and answers initialize so the
// mcp-go client completes a real request — proving the brokered credential
// reaches the wire (spec 074 FR-016/FR-017).
func newCapturingMCPServer(t *testing.T, gotAuth *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth = r.Header.Get("Authorization")

		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(req.ID),
			"result": map[string]any{
				"protocolVersion": mcp.LATEST_PROTOCOL_VERSION,
				"serverInfo":      map[string]any{"name": "cap", "version": "1"},
				"capabilities":    map[string]any{},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func doInitialize(t *testing.T, cfg *HTTPTransportConfig, sse bool) {
	t.Helper()
	var (
		c   interface{ Start(context.Context) error }
		err error
	)
	if sse {
		cl, e := CreateSSEClient(cfg)
		c, err = cl, e
	} else {
		cl, e := CreateHTTPClient(cfg)
		c, err = cl, e
	}
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Initialize triggers the first POST carrying the headers.
	type initer interface {
		Initialize(context.Context, mcp.InitializeRequest) (*mcp.InitializeResult, error)
	}
	if i, ok := c.(initer); ok {
		_, _ = i.Initialize(ctx, mcp.InitializeRequest{})
	}
}

// FR-017 across the SSE path: the brokered per-user token is on the wire of the
// initial SSE GET stream and the inbound/configured gateway token is not.
func TestCreateSSEClient_InjectsBrokeredAuthOnWire(t *testing.T) {
	var gotAuth string
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("ResponseWriter is not a Flusher")
			return
		}
		// Tell the client where to POST messages, then hold the stream open.
		_, _ = w.Write([]byte("event: endpoint\ndata: " + srv.URL + "/message\n\n"))
		fl.Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("/message", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	cfg := &HTTPTransportConfig{
		URL:     srv.URL + "/sse",
		Headers: map[string]string{"Authorization": "Bearer INBOUND-GATEWAY"},
		BrokeredAuth: &BrokeredAuth{
			Header: "Authorization", Format: "Bearer {token}", Token: "per-user-SSE",
		},
	}
	sseClient, err := CreateSSEClient(cfg)
	if err != nil {
		t.Fatalf("create SSE client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sseClient.Start(ctx); err != nil {
		t.Fatalf("start SSE client: %v", err)
	}
	defer sseClient.Close()

	if gotAuth != "Bearer per-user-SSE" {
		t.Fatalf("SSE outbound Authorization = %q, want brokered per-user token (inbound must be replaced)", gotAuth)
	}
}

// FR-017 across the HTTP path: the brokered per-user token is on the wire and
// the inbound/configured gateway token is not.
func TestCreateHTTPClient_InjectsBrokeredAuthOnWire(t *testing.T) {
	var gotAuth string
	srv := newCapturingMCPServer(t, &gotAuth)
	defer srv.Close()

	cfg := &HTTPTransportConfig{
		URL:     srv.URL,
		Headers: map[string]string{"Authorization": "Bearer INBOUND-GATEWAY"},
		BrokeredAuth: &BrokeredAuth{
			Header: "Authorization", Format: "Bearer {token}", Token: "per-user-HTTP",
		},
	}
	doInitialize(t, cfg, false)

	if gotAuth != "Bearer per-user-HTTP" {
		t.Fatalf("HTTP outbound Authorization = %q, want brokered per-user token (inbound must be replaced)", gotAuth)
	}
}

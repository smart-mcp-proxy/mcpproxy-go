package dialgate

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

// T4.17 behaviour (Sol review round 2): the capability parity test checks the
// wrappers' method sets; these send real traffic through the production
// wrappers and fail when a forwarding is dropped. Server-to-client ping over
// stdio and Streamable HTTP needs SetRequestHandler forwarded (mcp-go
// registers its handler only on a BidirectionalInterface); SSE connection
// loss needs SetConnectionLostHandler forwarded.

func TestWrappedStdio_ServerToClientPing(t *testing.T) {
	serverOut, clientIn := io.Pipe() // what the "server" writes, the client reads
	clientOutR, clientOut := io.Pipe()
	t.Cleanup(func() { _ = serverOut.Close(); _ = clientIn.Close(); _ = clientOutR.Close(); _ = clientOut.Close() })

	g := New()
	wrapped := WrapTransport(g, transport.NewIO(serverOut, clientOut, nil))
	cl := client.NewClient(wrapped)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.Start(ctx))
	t.Cleanup(func() { _ = cl.Close() })

	replies := bufio.NewReader(clientOutR)
	ping := func(id int) map[string]any {
		go func() { _, _ = fmt.Fprintf(clientIn, `{"jsonrpc":"2.0","id":%d,"method":"ping"}`+"\n", id) }()
		line, err := replies.ReadString('\n')
		require.NoError(t, err)
		var msg map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &msg))
		return msg
	}

	msg := ping(7)
	require.EqualValues(t, 7, msg["id"])
	require.Contains(t, msg, "result", "the client answers the server's ping through the wrapper: %v", msg)
	require.NotContains(t, msg, "error")

	g.Retire()
	msg = ping(8)
	require.EqualValues(t, 8, msg["id"])
	require.Contains(t, msg, "error", "a retired instance does no work for its upstream (T4.17 f): %v", msg)
}

func TestWrappedStreamableHTTP_ServerToClientPing(t *testing.T) {
	answers := make(chan map[string]any, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var msg map[string]any
		_ = json.NewDecoder(r.Body).Decode(&msg)
		if _, isRequest := msg["method"]; !isRequest {
			answers <- msg // the client's answer to our ping
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"srv-1\",\"method\":\"ping\"}\n\n")
		fl.Flush()
		select {
		case <-time.After(5 * time.Second):
		case got := <-answers:
			answers <- got
		}
		_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%v,\"result\":{}}\n\n", msg["id"])
		fl.Flush()
	}))
	t.Cleanup(srv.Close)

	g := New()
	inner, err := transport.NewStreamableHTTP(srv.URL, transport.WithHTTPBasicClient(g.HTTPClient(0)))
	require.NoError(t, err)
	wrapped := WrapTransport(g, inner)
	cl := client.NewClient(wrapped)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.Start(ctx))
	t.Cleanup(func() { _ = cl.Close() })

	_, err = wrapped.SendRequest(ctx, transport.JSONRPCRequest{JSONRPC: mcp.JSONRPC_VERSION, ID: mcp.NewRequestId(int64(1)), Method: "tools/list"})
	require.NoError(t, err)

	select {
	case got := <-answers:
		require.Equal(t, "srv-1", got["id"])
		require.Contains(t, got, "result", "the client answers the server's ping through the wrapper: %v", got)
		require.NotContains(t, got, "error")
	case <-time.After(5 * time.Second):
		t.Fatal("the client never answered the server-to-client ping")
	}
}

func TestWrappedSSE_ConnectionLostFires(t *testing.T) {
	closeStream := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, "event: endpoint\ndata: /message\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-closeStream: // end the stream: the client reads EOF
			case <-r.Context().Done():
			}
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	t.Cleanup(srv.Close)

	g := New()
	inner, err := transport.NewSSE(srv.URL+"/sse", transport.WithHTTPClient(g.HTTPClient(0)))
	require.NoError(t, err)
	cl := client.NewClient(WrapTransport(g, inner))
	lost := make(chan error, 1)
	cl.OnConnectionLost(func(err error) {
		select {
		case lost <- err:
		default:
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, cl.Start(ctx))
	t.Cleanup(func() { _ = cl.Close() })

	close(closeStream)
	select {
	case <-lost:
	case <-time.After(5 * time.Second):
		t.Fatal("OnConnectionLost did not fire through the wrapper when the SSE stream closed")
	}
}

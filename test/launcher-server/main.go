// Command launcher-server is a tiny HTTP MCP server used as the child
// process in the e2e test for spec 046 (local launcher for HTTP/SSE
// upstreams). It is NOT a production MCP implementation — it speaks
// just enough of the protocol for mcpproxy's StreamableHTTP transport
// to complete the initialize handshake and answer a tools/list call.
//
// Why: the unit + integration tests in internal/upstream/launcher cover
// the spawn / WaitForURL / Stop semantics in isolation. To prove the
// feature actually works end-to-end — that a `mcpproxy serve` boot
// launches this binary, connects to it via HTTP, completes the MCP
// handshake, and reaps it on disable / restart / shutdown — we need a
// real upstream the e2e harness can drive. Anything more complex than
// "implement initialize + tools/list + tools/call" is out of scope.
//
// Usage: launcher-server --port N [--addr 127.0.0.1] [--quiet]
//
// On SIGTERM/SIGINT the HTTP listener is gracefully shut down (5s
// deadline) and the process exits with code 0. Heartbeat lines are
// printed to stdout once per second by default so the per-server log
// in mcpproxy demonstrably captures the child's output. --quiet
// suppresses the heartbeat.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// The protocol version we advertise. Matches mark3labs/mcp-go's current
// LATEST_PROTOCOL_VERSION at the time this fixture was written. mcp-go's
// initialize negotiation accepts a mismatch (warns but proceeds) so this
// doesn't need to be in lock-step with library bumps.
const protocolVersion = "2025-11-25"

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	port := flag.Int("port", 0, "TCP port to bind (required)")
	addr := flag.String("addr", "127.0.0.1", "Bind address")
	quiet := flag.Bool("quiet", false, "Suppress 1-second heartbeat log lines")
	flag.Parse()

	if *port == 0 {
		fmt.Fprintln(os.Stderr, "launcher-server: --port is required")
		os.Exit(2)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", handleMCP)

	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", *addr, *port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	listenErr := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			listenErr <- err
		}
		close(listenErr)
	}()

	fmt.Printf("[launcher-server] listening on %s (pid=%d)\n", server.Addr, os.Getpid())

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)

	var heartbeat <-chan time.Time
	if !*quiet {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		heartbeat = ticker.C
	}

	for {
		select {
		case sig := <-sigs:
			fmt.Printf("[launcher-server] received %v, shutting down\n", sig)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = server.Shutdown(ctx)
			cancel()
			return
		case err, ok := <-listenErr:
			if ok && err != nil {
				log.Fatalf("launcher-server: listen failed: %v", err)
			}
			return
		case <-heartbeat:
			fmt.Println("[launcher-server] tick")
		}
	}
}

// handleMCP implements the StreamableHTTP server side. mcpproxy POSTs
// JSON-RPC frames here with `Accept: application/json, text/event-stream`
// and we reply with `Content-Type: application/json` (single-shot
// response). We do NOT implement the GET-for-server-push side of the
// streamable transport: the fixture never originates notifications.
func handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		// fall through
	case http.MethodGet:
		// Some clients open a GET for server-push events. We tell them
		// we don't have any (405 ⇒ client falls back to POST-only mode).
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		// Session termination — accept and exit silently.
		w.WriteHeader(http.StatusAccepted)
		return
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Notifications carry no id and expect no response body.
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"
	if isNotification {
		fmt.Printf("[launcher-server] notification method=%s\n", req.Method)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	fmt.Printf("[launcher-server] request method=%s id=%s\n", req.Method, string(req.ID))

	var (
		result any
		rpcErr *rpcError
	)
	switch req.Method {
	case "initialize":
		result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "launcher-test-fixture",
				"version": "0.1.0",
			},
		}
	case "tools/list":
		result = map[string]any{
			"tools": []map[string]any{
				{
					"name":        "ping",
					"description": "Returns 'pong' so the e2e test can prove a launched HTTP MCP server actually answers tool calls.",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{},
					},
				},
			},
		}
	case "tools/call":
		// We don't even bother parsing the name — there's only one tool.
		result = map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "pong from launcher-test-fixture"},
			},
			"isError": false,
		}
	case "ping":
		// Heartbeat method some clients send. Reply with an empty result.
		result = map[string]any{}
	default:
		rpcErr = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}

	resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Already wrote status; nothing useful to do.
		log.Printf("[launcher-server] encode response: %v", err)
	}
}

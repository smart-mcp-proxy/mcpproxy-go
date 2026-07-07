// Command mcpfixture is a deterministic MCP upstream fixture for the release
// QA gate (Spec 081, FR-006/FR-007). One binary serves the same two tools over
// three transports so gate matrix cells never depend on third-party services:
//
//	mcpfixture --transport stdio                     # newline-delimited JSON-RPC on stdin/stdout
//	mcpfixture --transport http --port 18080         # streamable-http at http://127.0.0.1:18080/mcp
//	mcpfixture --transport sse  --port 18081         # legacy SSE at http://127.0.0.1:18081/sse (+ POST /message)
//
// Transports are served by mark3labs/mcp-go's server package — the same
// library whose CLIENT side mcpproxy pins (internal/upstream/core uses
// transport.CreateSSEClient / NewStreamableHTTP / NewStdio), so the wire
// contract is protocol-correct by construction instead of hand-rolled:
//   - sse: GET /sse opens the event stream and announces the message endpoint
//     ("endpoint" event, relative URL resolved against the client's base URL);
//     requests are POSTed to /message?sessionId=… and responses stream back as
//     SSE "message" events.
//   - http: POST /mcp single-shot JSON responses (StreamableHTTPServer).
//   - stdio: newline-delimited JSON-RPC frames (StdioServer).
//
// Tools (deterministic, FR-007c):
//   - echo — returns the call arguments back as JSON text:
//     {"echo": {...arguments...}}
//   - ping — returns {"message":"pong","counter":N,"instance_id":"…"}; the
//     counter increases monotonically per process and the instance_id is
//     random per process start, so kill/restart tests (FR-007d) can prove the
//     reconnected upstream is a NEW instance (counter reset, new id).
//
// SIGTERM/SIGINT exits cleanly with code 0 on every transport (docker stop
// sends SIGTERM to PID 1, and the gate kills fixtures between matrix steps).
//
// Docker cell (FR-009) argv composition, verified against
// internal/upstream/core/isolation.go + connection_docker.go:
//
//	server config: command="/mcpfixture", args=["--transport","stdio"],
//	               isolation.image="mcpfixture:gate"
//	DetectRuntimeType(filepath.Base("/mcpfixture")) → "binary" (default case)
//	TransformCommandForContainer(binary) → command+args passed through unchanged
//	GetDockerImage → buildFullImageName("mcpfixture:gate") → "docker.io/library/mcpfixture:gate"
//	  (no slash ⇒ registry+library prefix; Docker normalizes local short tags
//	   the same way, so a locally built `mcpfixture:gate` resolves without a pull)
//	final argv: docker run --rm -i --name mcpproxy-<server>-<rand> <labels>
//	            [--log-opt …] [--network <mode>] [limits] [-e K=V …]
//	            docker.io/library/mcpfixture:gate /mcpfixture --transport stdio
//
// Because the container command is always appended explicitly after the image,
// the image must NOT set an ENTRYPOINT (it would prefix the argv and break
// `/mcpfixture --transport stdio`); it ships CMD ["/mcpfixture","--transport","stdio"]
// as a default for bare `docker run` instead. See cmd/mcpfixture/Dockerfile and
// scripts/gate/build-fixture-image.sh.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	fixtureName    = "mcpfixture"
	fixtureVersion = "0.1.0"

	transportStdio = "stdio"
	transportHTTP  = "http"
	transportSSE   = "sse"

	shutdownTimeout = 5 * time.Second
)

// pingCounter increases monotonically per tools/call of `ping` within one
// process. It intentionally resets on restart so reconnect tests can detect a
// fresh instance.
var pingCounter atomic.Int64

// instanceID is random per process start — the second discriminator (besides
// the counter reset) that kill/restart tests use to distinguish instances.
var instanceID = newInstanceID()

func newInstanceID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// Deterministic fallback still unique enough across restarts.
		return fmt.Sprintf("pid-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// pingResult is the JSON payload returned by the ping tool.
type pingResult struct {
	Message    string `json:"message"`
	Counter    int64  `json:"counter"`
	InstanceID string `json:"instance_id"`
}

func main() {
	transportFlag := flag.String("transport", transportStdio, "Transport to serve: stdio|http|sse")
	port := flag.Int("port", 0, "TCP port to bind (required for http/sse)")
	addr := flag.String("addr", "127.0.0.1", "Bind address (http/sse)")
	flag.Parse()

	mcpSrv := newFixtureServer()

	// A stdio parent (mcpproxy, or mcp-go's stdio client) may close our stderr
	// pipe before we write the final shutdown line; without this, Go's runtime
	// treats EPIPE on fd 1/2 as a fatal SIGPIPE and the fixture would die with
	// a non-zero "signal: broken pipe" status instead of exiting cleanly.
	signal.Ignore(syscall.SIGPIPE)

	// All operational logging goes to stderr: stdout is the protocol channel
	// for the stdio transport.
	fmt.Fprintf(os.Stderr, "[mcpfixture] transport=%s instance_id=%s pid=%d\n", *transportFlag, instanceID, os.Getpid())

	switch *transportFlag {
	case transportStdio:
		// server.ServeStdio installs its own SIGTERM/SIGINT handler and
		// returns context.Canceled after a signal — treat that as clean.
		if err := server.ServeStdio(mcpSrv); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "[mcpfixture] stdio serve failed: %v\n", err)
			os.Exit(1)
		}
	case transportHTTP, transportSSE:
		if *port == 0 {
			fmt.Fprintln(os.Stderr, "[mcpfixture] --port is required for http/sse transports")
			os.Exit(2)
		}
		listenAddr := fmt.Sprintf("%s:%d", *addr, *port)
		if err := serveNetwork(*transportFlag, mcpSrv, listenAddr); err != nil {
			fmt.Fprintf(os.Stderr, "[mcpfixture] %s serve failed: %v\n", *transportFlag, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "[mcpfixture] unknown --transport %q (want stdio|http|sse)\n", *transportFlag)
		os.Exit(2)
	}

	fmt.Fprintln(os.Stderr, "[mcpfixture] clean shutdown")
}

// newFixtureServer builds the MCP server with the two deterministic tools.
func newFixtureServer() *server.MCPServer {
	s := server.NewMCPServer(fixtureName, fixtureVersion, server.WithToolCapabilities(false))

	// Both tools are annotated read-only: without annotations the MCP spec
	// default is destructiveHint=true and mcpproxy's intent validation
	// (Spec 018) would reject them through call_tool_read.
	s.AddTool(mcp.NewTool("echo",
		mcp.WithDescription("Returns the provided arguments back as JSON text."),
		mcp.WithString("text", mcp.Description("Text to echo back.")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), handleEcho)

	s.AddTool(mcp.NewTool("ping",
		mcp.WithDescription("Returns pong with a per-process monotonically increasing counter and a per-process instance id."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	), handlePing)

	return s
}

func handleEcho(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	if args == nil {
		args = map[string]any{}
	}
	payload, err := json.Marshal(map[string]any{"echo": args})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal echo payload: %v", err)), nil
	}
	return mcp.NewToolResultText(string(payload)), nil
}

func handlePing(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	payload, err := json.Marshal(pingResult{
		Message:    "pong",
		Counter:    pingCounter.Add(1),
		InstanceID: instanceID,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal ping payload: %v", err)), nil
	}
	return mcp.NewToolResultText(string(payload)), nil
}

// networkServer is the common surface of mcp-go's StreamableHTTPServer and
// SSEServer that serveNetwork needs.
type networkServer interface {
	Start(addr string) error
	Shutdown(ctx context.Context) error
}

// serveNetwork runs the given transport until SIGTERM/SIGINT, then shuts it
// down gracefully (5s deadline). A listen failure surfaces as the returned
// error.
func serveNetwork(transportName string, mcpSrv *server.MCPServer, listenAddr string) error {
	var srv networkServer
	switch transportName {
	case transportHTTP:
		srv = server.NewStreamableHTTPServer(mcpSrv)
	case transportSSE:
		// No WithBaseURL: the endpoint event advertises a relative
		// /message?sessionId=… URL, which mcp-go's SSE client (the one
		// mcpproxy pins) resolves against its base URL.
		srv = server.NewSSEServer(mcpSrv)
	default:
		return fmt.Errorf("unknown network transport %q", transportName)
	}

	errCh := make(chan error, 1)
	go func() {
		err := srv.Start(listenAddr)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	fmt.Fprintf(os.Stderr, "[mcpfixture] listening on %s\n", listenAddr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	select {
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "[mcpfixture] received %v, shutting down\n", sig)
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("shutdown: %w", err)
		}
		// Drain the Start goroutine so a late listen error is not lost.
		if err, ok := <-errCh; ok && err != nil {
			return err
		}
		return nil
	case err, ok := <-errCh:
		if ok && err != nil {
			return err
		}
		return nil
	}
}

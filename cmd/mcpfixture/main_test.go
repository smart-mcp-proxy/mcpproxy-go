package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// skipIfWindows guards the exec/signal-based transport tests. The fixture binary
// is Linux-only gate infrastructure — it is built and run exclusively on the
// ubuntu-latest gate runner and inside the linux/amd64 Docker image, never on
// Windows. The tests spawn the real process and exercise Unix signal/restart
// semantics (SIGTERM) that Windows does not support, so they are skipped there.
func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("mcpfixture exec/signal transport tests are Linux-only gate infra (gate runs on ubuntu-latest)")
	}
}

// fixtureBin is the compiled fixture binary, built once in TestMain. The
// transport tests exercise the real executable (exec-based) so the SIGTERM →
// restart behavior (FR-007d, FR-022) is proven against what the gate and the
// Docker image actually run, not an in-process approximation.
var fixtureBin string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "mcpfixture-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mkdtemp: %v\n", err)
		os.Exit(1)
	}
	fixtureBin = filepath.Join(tmp, "mcpfixture")
	build := exec.Command("go", "build", "-o", fixtureBin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build fixture: %v\n%s", err, out)
		os.RemoveAll(tmp)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// initializeClient completes the MCP handshake on an already-started client.
func initializeClient(t *testing.T, c *client.Client) *mcp.InitializeResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "mcpfixture-test", Version: "0.0.0"}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}

	res, err := c.Initialize(ctx, initReq)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if res.ServerInfo.Name != fixtureName {
		t.Fatalf("serverInfo.name = %q, want %q", res.ServerInfo.Name, fixtureName)
	}
	return res
}

// assertToolsList verifies tools/list returns exactly echo and ping.
func assertToolsList(t *testing.T, c *client.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	if len(res.Tools) != 2 || !names["echo"] || !names["ping"] {
		t.Fatalf("tools/list = %v, want exactly {echo, ping}", names)
	}
}

// callPing invokes the ping tool and returns the decoded payload.
func callPing(t *testing.T, c *client.Client) pingResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "ping"
	res, err := c.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("tools/call ping: %v", err)
	}
	if res.IsError {
		t.Fatalf("ping returned isError: %+v", res.Content)
	}
	text := textContent(t, res)
	var pr pingResult
	if err := json.Unmarshal([]byte(text), &pr); err != nil {
		t.Fatalf("decode ping payload %q: %v", text, err)
	}
	if pr.Message != "pong" {
		t.Fatalf("ping message = %q, want pong", pr.Message)
	}
	if pr.InstanceID == "" {
		t.Fatal("ping instance_id is empty")
	}
	return pr
}

// callEcho invokes echo and asserts the arguments round-trip.
func callEcho(t *testing.T, c *client.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "echo"
	req.Params.Arguments = map[string]any{"text": "hello-gate", "extra": float64(42)}
	res, err := c.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("tools/call echo: %v", err)
	}
	if res.IsError {
		t.Fatalf("echo returned isError: %+v", res.Content)
	}
	var payload struct {
		Echo map[string]any `json:"echo"`
	}
	text := textContent(t, res)
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("decode echo payload %q: %v", text, err)
	}
	if payload.Echo["text"] != "hello-gate" || payload.Echo["extra"] != float64(42) {
		t.Fatalf("echo payload = %v, want the original arguments back", payload.Echo)
	}
}

func textContent(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("tool result has no content")
	}
	tc, ok := mcp.AsTextContent(res.Content[0])
	if !ok {
		t.Fatalf("tool result content[0] is %T, want text", res.Content[0])
	}
	return tc.Text
}

// roundTrip runs the full FR-007(a-c) check on a connected client: handshake
// already done by the caller; verifies tools/list and both tool calls, and
// returns the first ping payload (counter must be 1 on a fresh instance).
func roundTrip(t *testing.T, c *client.Client) pingResult {
	t.Helper()
	assertToolsList(t, c)
	first := callPing(t, c)
	if first.Counter != 1 {
		t.Fatalf("first ping counter = %d, want 1 (fresh instance)", first.Counter)
	}
	second := callPing(t, c)
	if second.Counter != 2 {
		t.Fatalf("second ping counter = %d, want 2 (monotonic)", second.Counter)
	}
	if second.InstanceID != first.InstanceID {
		t.Fatalf("instance_id changed within one process: %q → %q", first.InstanceID, second.InstanceID)
	}
	callEcho(t, c)
	return first
}

// --- stdio -----------------------------------------------------------------

func TestStdioRoundTripAndRestart(t *testing.T) {
	skipIfWindows(t)
	newStdioClient := func() *client.Client {
		c, err := client.NewStdioMCPClient(fixtureBin, nil, "--transport", "stdio")
		if err != nil {
			t.Fatalf("start stdio client: %v", err)
		}
		return c
	}

	c1 := newStdioClient()
	initializeClient(t, c1)
	first := roundTrip(t, c1)
	if err := c1.Close(); err != nil {
		t.Fatalf("close first stdio client: %v", err)
	}

	// Restart: new process must present a reset counter and a new instance id.
	c2 := newStdioClient()
	defer c2.Close()
	initializeClient(t, c2)
	restarted := roundTrip(t, c2)
	if restarted.InstanceID == first.InstanceID {
		t.Fatalf("restarted instance_id %q equals original — restart not detected", restarted.InstanceID)
	}
}

func TestStdioSIGTERMExitsCleanly(t *testing.T) {
	skipIfWindows(t)
	cmd := exec.Command(fixtureBin, "--transport", "stdio")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Give ServeStdio a moment to install its signal handler.
	time.Sleep(200 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}
	waitExit(t, cmd, 0)
}

// --- http / sse ------------------------------------------------------------

func TestHTTPRoundTripAndRestart(t *testing.T) {
	skipIfWindows(t)
	testNetworkTransport(t, transportHTTP, func(port int) (*client.Client, error) {
		return client.NewStreamableHttpClient(fmt.Sprintf("http://127.0.0.1:%d/mcp", port))
	})
}

func TestSSERoundTripAndRestart(t *testing.T) {
	skipIfWindows(t)
	testNetworkTransport(t, transportSSE, func(port int) (*client.Client, error) {
		return client.NewSSEMCPClient(fmt.Sprintf("http://127.0.0.1:%d/sse", port))
	})
}

// testNetworkTransport covers FR-007(a-d) for a network transport: start the
// fixture process, handshake + list + call round-trip, SIGTERM it, restart on
// the same port, and verify the reconnected instance is fresh.
func testNetworkTransport(t *testing.T, transportName string, newClient func(port int) (*client.Client, error)) {
	t.Helper()
	port := freePort(t)

	proc := startFixtureProcess(t, transportName, port)
	c1 := connectNetworkClient(t, newClient, port)
	initializeClient(t, c1)
	first := roundTrip(t, c1)
	c1.Close()

	// FR-007d: forcibly terminate, expect clean exit, restart on the same port.
	if err := proc.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM fixture: %v", err)
	}
	waitExit(t, proc, 0)

	startFixtureProcess(t, transportName, port)
	c2 := connectNetworkClient(t, newClient, port)
	defer c2.Close()
	initializeClient(t, c2)
	restarted := roundTrip(t, c2)
	if restarted.InstanceID == first.InstanceID {
		t.Fatalf("restarted instance_id %q equals original — restart not detected", restarted.InstanceID)
	}
}

// startFixtureProcess starts the fixture binary on the given port and
// registers cleanup that SIGKILLs it if the test did not already reap it.
func startFixtureProcess(t *testing.T, transportName string, port int) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(fixtureBin, "--transport", transportName, "--port", fmt.Sprint(port))
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fixture (%s): %v", transportName, err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	waitForListen(t, port)
	return cmd
}

// connectNetworkClient builds a client and starts its transport, retrying
// briefly: after a restart the TCP port can be up before the HTTP mux is.
func connectNetworkClient(t *testing.T, newClient func(port int) (*client.Client, error), port int) *client.Client {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		c, err := newClient(port)
		if err == nil {
			// Persistent context, NOT a cancelled timeout ctx: the SSE
			// transport binds its event-stream goroutine to the Start
			// context, and cancelling it drops the session (the same
			// pitfall internal/upstream/core/connection_http.go documents).
			err = c.Start(context.Background())
			if err == nil {
				return c
			}
			c.Close()
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("connect client on port %d: %v", port, lastErr)
	return nil
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func waitForListen(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("fixture never listened on %s", addr)
}

func waitExit(t *testing.T, cmd *exec.Cmd, wantCode int) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		if code := cmd.ProcessState.ExitCode(); code != wantCode {
			t.Fatalf("exit code = %d, want %d", code, wantCode)
		}
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("fixture did not exit within 15s of SIGTERM")
	}
}

package upstream

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Spec 116 PR4 test fixtures (quickstart §T4): a counting MCP upstream (every
// HTTP request and every Authorization header it receives), a stdio marker
// command (one marker file per launch) and a one-shot park on an instance's
// dial gate.

// countingUpstream is an in-process MCP server (streamable HTTP or SSE)
// behind a handler that counts requests and Authorization headers.
type countingUpstream struct {
	srv      *httptest.Server
	url      string
	requests atomic.Int64
	auth     atomic.Int64
	// holdCall, when set, blocks the `slow` tool until it is closed.
	holdCall chan struct{}
	inCall   chan struct{}
	callOnce sync.Once
	// holdList, when set, holds every tools/list request until the request
	// ends (the client side closes it); inList is closed by the first one.
	holdList atomic.Bool
	inList   chan struct{}
	listOnce sync.Once
}

func newCountingUpstream(t *testing.T, kind string) *countingUpstream {
	t.Helper()
	up := &countingUpstream{holdCall: make(chan struct{}), inCall: make(chan struct{}), inList: make(chan struct{})}
	mcpSrv := server.NewMCPServer("retire-fixture", "1.0.0", server.WithToolCapabilities(true))
	mcpSrv.AddTool(mcp.NewTool("echo", mcp.WithDescription("echo")), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	mcpSrv.AddTool(mcp.NewTool("slow", mcp.WithDescription("blocks until released")), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		up.callOnce.Do(func() { close(up.inCall) })
		select {
		case <-up.holdCall:
		case <-ctx.Done():
		}
		return mcp.NewToolResultText("slow done"), nil
	})

	up.srv = httptest.NewUnstartedServer(nil)
	up.srv.Start()
	base := up.srv.URL
	var inner http.Handler
	switch kind {
	case "sse":
		inner = server.NewSSEServer(mcpSrv, server.WithBaseURL(base))
		up.url = base + "/sse"
	default:
		inner = server.NewStreamableHTTPServer(mcpSrv)
		up.url = base + "/mcp"
	}
	up.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up.requests.Add(1)
		if r.Header.Get("Authorization") != "" {
			up.auth.Add(1)
		}
		if up.holdList.Load() && r.Body != nil {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			if bytes.Contains(body, []byte(`"tools/list"`)) {
				up.listOnce.Do(func() { close(up.inList) })
				<-r.Context().Done()
				return
			}
		}
		inner.ServeHTTP(w, r)
	})
	t.Cleanup(func() {
		up.release()
		up.srv.CloseClientConnections()
		up.srv.Close()
	})
	return up
}

func (u *countingUpstream) release() {
	select {
	case <-u.holdCall:
	default:
		close(u.holdCall)
	}
}

func (u *countingUpstream) counts() (requests, auth int64) {
	return u.requests.Load(), u.auth.Load()
}

func httpServerConfig(name, url, kind string) *config.ServerConfig {
	proto := "streamable-http"
	if kind == "sse" {
		proto = "sse"
	}
	return &config.ServerConfig{
		Name:     name,
		URL:      url,
		Protocol: proto,
		Enabled:  true,
		Headers:  map[string]string{"Authorization": "Bearer qa-secret"},
	}
}

// markerCommand returns a stdio server config whose command creates a fresh
// marker file in dir every time it is launched, starts a descendant that
// creates a child-* file, then idles. Neither the shell nor the descendant
// execs away its command line, so both keep dir in their arguments for
// processAlive to find (the trailing `:` stops sh from exec'ing its last
// command), and the descendant shows whether the whole process group, not
// only the immediate child, is killed.
func markerCommand(t *testing.T, name, dir string) *config.ServerConfig {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("marker launcher uses sh")
	}
	return &config.ServerConfig{
		Name:     name,
		Command:  "/bin/sh",
		Args:     []string{"-c", `touch "` + dir + `/marker-$$"; /bin/sh -c 'touch "` + dir + `/child-$$"; sleep 30; :' & wait; :`},
		Protocol: "stdio",
		Enabled:  true,
	}
}

func markers(t *testing.T, dir string) int {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "marker-*"))
	require.NoError(t, err)
	return len(m)
}

// descendants counts the child-* files markerCommand's descendants created.
func descendants(t *testing.T, dir string) int {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "child-*"))
	require.NoError(t, err)
	return len(m)
}

// parkFirstAdmit parks the first admitted section (a launch or a dial) of
// inst's gate before it takes the gate's read lock: the window after the
// instance's last registry lookup and before it launches or sends anything.
func parkFirstAdmit(inst *managed.Client) (parked <-chan struct{}, release func()) {
	p := make(chan struct{})
	r := make(chan struct{})
	var once atomic.Bool
	inst.Gate().SetHooks(&dialgate.Hooks{BeforeAdmit: func() {
		if once.CompareAndSwap(false, true) {
			close(p)
			<-r
		}
	}})
	var relOnce sync.Once
	return p, func() { relOnce.Do(func() { close(r) }) }
}

func connectAsync(inst *managed.Client) <-chan error {
	errc := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		errc <- inst.Connect(ctx)
	}()
	return errc
}

func waitErr(t *testing.T, errc <-chan error) error {
	t.Helper()
	select {
	case err := <-errc:
		return err
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for the parked operation")
		return nil
	}
}

func setStdioLaunchHookForTest(fn func()) (restore func()) {
	return core.SetStdioLaunchHookForTest(fn)
}

// processAlive reports whether a live (non-zombie) process has needle in its
// command line.
func processAlive(t *testing.T, needle string) bool {
	t.Helper()
	out, err := exec.Command("ps", "-axo", "stat=,command=").Output()
	require.NoError(t, err)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, needle) || strings.HasPrefix(line, "Z") {
			continue
		}
		if strings.Contains(line, "ps -axo") {
			continue
		}
		return true
	}
	return false
}

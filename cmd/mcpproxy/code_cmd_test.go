package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"

	"go.uber.org/zap"
)

// TestCodeExecClientTimeout_TracksServerBudget pins that the client-side
// deadline for a daemon code execution always outlives the server-side
// execution budget requested with --timeout.
func TestCodeExecClientTimeout_TracksServerBudget(t *testing.T) {
	tests := []struct {
		name      string
		timeoutMS int
		want      time.Duration
	}{
		{"default budget", 120000, 150 * time.Second},
		{"smallest budget still gets slack", 1, 30*time.Second + time.Millisecond},
		{"largest allowed budget", 600000, 630 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := codeExecClientTimeout(tc.timeoutMS)
			if got != tc.want {
				t.Fatalf("codeExecClientTimeout(%d) = %s, want %s", tc.timeoutMS, got, tc.want)
			}
			if got <= time.Duration(tc.timeoutMS)*time.Millisecond {
				t.Fatalf("codeExecClientTimeout(%d) = %s, must exceed the server budget", tc.timeoutMS, got)
			}
		})
	}
}

const (
	codeExecChildEnv         = "MCPPROXY_TEST_CODE_EXEC_CHILD"
	codeExecFallbackChildEnv = "MCPPROXY_TEST_CODE_EXEC_FALLBACK_CHILD"
)

// useMissingCodeConfig points the code command at a config file that does not
// exist, so a child process can never read the developer's real
// ~/.mcpproxy/mcp_config.json (and never open the real DataDir BBolt store)
// when a flaked daemon ping sends it down the standalone fallback.
func useMissingCodeConfig(t *testing.T) string {
	t.Helper()

	missing := filepath.Join(t.TempDir(), "absent", "mcp_config.json")
	previous := codeConfigPath
	codeConfigPath = missing
	t.Cleanup(func() { codeConfigPath = previous })
	return missing
}

// TestRunCodeExecClientMode_ExecOutlivesPingDeadline pins that the short
// daemon-connectivity ping deadline does not bound the execution request:
// daemon mode must keep waiting for a result that arrives long after the ping
// budget has elapsed. The command path calls os.Exit on failure, so the real
// path runs in a child process and this test asserts on its exit code.
func TestRunCodeExecClientMode_ExecOutlivesPingDeadline(t *testing.T) {
	const execDelay = 3 * time.Second

	if os.Getenv(codeExecChildEnv) == "1" {
		codeTimeout = 60000
		codeMaxToolCalls = 0
		codeAllowedSrvs = nil
		codeLanguage = "javascript"
		useMissingCodeConfig(t)

		client := cliclient.NewClientWithAPIKey(os.Getenv(codeExecChildEnv+"_ENDPOINT"), "", nil)
		if err := runCodeExecClientMode(client, "({ value: 42 })", map[string]interface{}{}, zap.NewNop()); err != nil {
			t.Fatalf("runCodeExecClientMode returned error: %v", err)
		}
		return
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		case "/api/v1/code/exec":
			select {
			case <-time.After(execDelay):
			case <-r.Context().Done():
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":     true,
				"result": map[string]interface{}{"value": 42},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestRunCodeExecClientMode_ExecOutlivesPingDeadline$", "-test.timeout=60s")
	cmd.Env = append(os.Environ(),
		codeExecChildEnv+"=1",
		codeExecChildEnv+"_ENDPOINT="+srv.URL,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("daemon-mode code exec aborted before the daemon replied (%v)\nchild output:\n%s", err, out)
	}
	// A flaked ping would silently fall back to standalone mode, where the
	// trivial script also succeeds - the child would exit 0 without ever
	// exercising the daemon path this test pins. Require the evidence.
	if !strings.Contains(string(out), "Using daemon mode") {
		t.Fatalf("child never took the daemon path (ping likely fell back to standalone)\nchild output:\n%s", out)
	}
}

// TestRunCodeExecClientMode_PingFailureFallbackWithoutConfig pins that when the
// daemon ping fails and no configuration can be loaded, the standalone fallback
// reports a configuration error instead of dereferencing a nil config.
func TestRunCodeExecClientMode_PingFailureFallbackWithoutConfig(t *testing.T) {
	if os.Getenv(codeExecFallbackChildEnv) == "1" {
		codeTimeout = 1000
		codeMaxToolCalls = 0
		codeAllowedSrvs = nil
		codeLanguage = "javascript"
		useMissingCodeConfig(t)

		// Port 1 refuses connections immediately, so the ping fails fast and
		// the standalone fallback runs with no loadable configuration.
		client := cliclient.NewClientWithAPIKey("http://127.0.0.1:1", "", nil)
		_ = runCodeExecClientMode(client, "({ value: 42 })", map[string]interface{}{}, zap.NewNop())
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestRunCodeExecClientMode_PingFailureFallbackWithoutConfig$", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), codeExecFallbackChildEnv+"=1")

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected the fallback to exit non-zero without a loadable config\nchild output:\n%s", out)
	}
	if strings.Contains(string(out), "panic:") {
		t.Fatalf("standalone fallback panicked instead of reporting a config error\nchild output:\n%s", out)
	}
	if !strings.Contains(string(out), "Error loading configuration") {
		t.Fatalf("expected a configuration error from the standalone fallback\nchild output:\n%s", out)
	}
}

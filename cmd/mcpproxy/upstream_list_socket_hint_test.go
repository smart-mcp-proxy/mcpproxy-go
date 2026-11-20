//go:build !windows

package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runUpstreamList end to end over a unix socket: the socket is an
// administrator connection (no key is sent), so an empty install gets the
// first-server hint even when an agent token is exported. Review r3/r4: the
// hint must follow the key the client actually sends, which only the
// wiring in runUpstreamList decides.
func TestRunUpstreamList_SocketAdminGetsHintDespiteAgentTokenInEnv(t *testing.T) {
	clearDaemonEnv(t)
	t.Setenv("MCPPROXY_API_KEY", "mcp_agt_0123456789abcdef")
	setOutputGlobals(t, "table", false)
	oldSource := upstreamListSource
	t.Cleanup(func() { upstreamListSource = oldSource })

	// macOS caps unix socket paths at 104 bytes; t.TempDir() can exceed it.
	dir, err := os.MkdirTemp("/tmp", "mcpx-hint-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	ln, err := net.Listen("unix", filepath.Join(dir, "mcpproxy.sock"))
	if err != nil {
		t.Fatal(err)
	}
	var sawKey string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "" {
			sawKey = r.Header.Get("X-API-Key")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/servers" {
			_, _ = io.WriteString(w, `{"success":true,"data":{"servers":[]}}`)
			return
		}
		_, _ = io.WriteString(w, `{"success":true,"data":{}}`)
	}))
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	cfgPath := filepath.Join(dir, "mcp_config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"listen":"127.0.0.1:0","data_dir":"`+dir+`","mcpServers":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldPath := upstreamConfigPath
	upstreamConfigPath = cfgPath
	t.Cleanup(func() { upstreamConfigPath = oldPath })

	out := captureOutput(func() {
		if err := runUpstreamList(nil, nil); err != nil {
			t.Fatalf("runUpstreamList: %v", err)
		}
	})
	if sawKey != "" {
		t.Fatalf("a socket connection must send no API key, saw one")
	}
	if !strings.Contains(out, "No upstream servers yet") {
		t.Errorf("socket admin on an empty install must get the hint despite an agent token in the env, got:\n%s", out)
	}
	if strings.Contains(out, "mcpproxy serve") {
		t.Errorf("the daemon answered over the socket; the hint must not say to start it:\n%s", out)
	}
}

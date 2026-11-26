//go:build darwin || windows

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy-tray/internal/api"
	"github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy-tray/internal/monitor"
)

// Once the monitor has seen the tray's own core exit, shutdown must not ask the
// status API for a PID: nobody answers (3s of retries), or a core the tray did
// not start does, and would be terminated.
func TestHandleShutdownSkipsVerificationAfterCoreExit(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	logger := zap.NewNop().Sugar()
	pm := monitor.NewProcessMonitor(&monitor.ProcessConfig{Binary: os.Args[0], Args: []string{"-test.run=^$"}}, logger, nil)
	if err := pm.Start(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); pm.GetExitInfo() == nil; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("child did not exit")
		}
	}

	cpl := &CoreProcessLauncher{
		logger:         logger,
		apiClient:      api.NewClient(srv.URL, logger),
		processMonitor: pm,
		coreOwnership:  coreOwnershipTrayManaged,
	}
	start := time.Now()
	cpl.handleShutdown()
	t.Logf("handleShutdown took %s, status requests: %d", time.Since(start).Round(time.Millisecond), hits.Load())
	if n := hits.Load(); n != 0 {
		t.Errorf("status API queried %d times after the core exited", n)
	}
}

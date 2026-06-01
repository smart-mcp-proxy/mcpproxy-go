package core

import (
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestStderrMonitoring_StartStopRace reproduces the Connect-vs-Disconnect race
// on the stderr-monitoring lifecycle fields (stderrMonitoringCtx/Cancel/WG).
// StartStderrMonitoring runs from connectStdio during a reconcile-driven Connect
// while StopStderrMonitoring runs from Disconnect during Manager.ShutdownAll, with
// no synchronization on those fields — the -race detector flags WG.Add (Start)
// vs WG.Wait (Stop). Run under `go test -race`: trips without monitoringMu, green
// with it. A reused empty stderr reader returns EOF immediately so monitorStderr
// exits at once and the loop stays fast.
func TestStderrMonitoring_StartStopRace(t *testing.T) {
	c := &Client{
		transportType: transportStdio,
		stderr:        strings.NewReader(""),
		logger:        zap.NewNop(),
		config:        &config.ServerConfig{Name: "race"},
	}

	const iterations = 500
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			c.StartStderrMonitoring()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			c.StopStderrMonitoring()
		}
	}()

	wg.Wait()
	c.StopStderrMonitoring()
}

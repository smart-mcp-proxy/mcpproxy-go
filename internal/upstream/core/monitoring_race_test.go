package core

import (
	"io"
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

// TestStderrMonitoring_AbandonedMonitorNoRace models the round-5 escape: the
// monitor goroutine is still alive when Stop is called (its stderr Read blocks),
// so Stop hits the 500ms timeout and abandons it. With the old reused-WaitGroup
// design the abandoned WG.Wait raced the next cycle's WG.Add; the per-cycle done
// channel + ctx-as-param design must keep concurrent Start/Stop race-free even
// while a prior monitor lingers. A blocking pipe keeps monitorStderr alive;
// closing the writer on cleanup lets the leaked goroutines exit.
func TestStderrMonitoring_AbandonedMonitorNoRace(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	c := &Client{
		transportType: transportStdio,
		stderr:        pr, // Read blocks until the writer is closed
		logger:        zap.NewNop(),
		config:        &config.ServerConfig{Name: "race"},
	}

	const cycles = 4 // each Stop times out at 500ms; keep small
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < cycles; i++ {
			c.StartStderrMonitoring()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < cycles; i++ {
			c.StopStderrMonitoring()
		}
	}()
	wg.Wait()
	c.StopStderrMonitoring()
}

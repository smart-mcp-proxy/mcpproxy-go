//go:build windows

package monitor

import (
	"os"
	"os/signal"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestHelperWaitForInterrupt is the child process: it exits cleanly only on SIGINT.
func TestHelperWaitForInterrupt(t *testing.T) {
	if os.Getenv("MONITOR_HELPER") != "1" {
		t.Skip("helper process")
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	select {
	case <-ch:
		os.Exit(0)
	case <-time.After(time.Minute):
		os.Exit(7)
	}
}

// Stop must reach a windowless child gracefully instead of waiting out the force-kill timeout.
func TestStopIsGracefulForWindowlessProcess(t *testing.T) {
	pm := NewProcessMonitor(&ProcessConfig{
		Binary: os.Args[0],
		Args:   []string{"-test.run=^TestHelperWaitForInterrupt$"},
		Env:    append(os.Environ(), "MONITOR_HELPER=1"),
	}, zap.NewNop().Sugar(), nil)
	if err := pm.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second) // let the child install its signal handler

	started := time.Now()
	if err := pm.Stop(); err != nil {
		t.Fatalf("Stop() = %v, want graceful exit", err)
	}
	if d := time.Since(started); d > 10*time.Second {
		t.Fatalf("Stop() took %s", d)
	}
	if code := pm.GetExitInfo().Code; code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
}

//go:build windows

package monitor

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
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
	fmt.Println("ready")
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
		Binary:        os.Args[0],
		Args:          []string{"-test.run=^TestHelperWaitForInterrupt$"},
		Env:           append(os.Environ(), "MONITOR_HELPER=1"),
		CaptureOutput: true,
	}, zap.NewNop().Sugar(), nil)
	if err := pm.Start(); err != nil {
		t.Fatal(err)
	}
	// A CTRL_BREAK that lands before the child's handler kills it outright.
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if out, _ := pm.GetOutput(); strings.Contains(out, "ready") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never reported ready")
		}
	}

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

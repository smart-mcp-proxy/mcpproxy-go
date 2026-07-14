package arms

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

// TestTSCG_ShimTimeoutKillsProcess guards the hang-risk fix: a shim that never
// answers (event loop alive forever, stdin ignored) must make the arm error
// within the configured timeout — the child is killed, never left as a zombie
// while EncodeTool blocks indefinitely.
func TestTSCG_ShimTimeoutKillsProcess(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}
	dir := t.TempDir()
	// A shim that keeps the node event loop alive forever and never writes a
	// response record.
	if err := os.WriteFile(filepath.Join(dir, "shim.mjs"), []byte("setInterval(() => {}, 1000);\n"), 0o644); err != nil {
		t.Fatalf("write fake shim: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatalf("mkdir node_modules: %v", err)
	}

	arm := NewTSCGAt(dir)
	if err := arm.Available(); err != nil {
		t.Fatalf("fake shim dir should be available: %v", err)
	}
	arm.timeout = 2 * time.Second

	start := time.Now()
	_, err := arm.EncodeTool(bench.Tool{ToolID: "s:t", Server: "s", Name: "t", Description: "d"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("EncodeTool must fail when the shim never responds")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error should name the timeout, got: %v", err)
	}
	// 2s timeout + WaitDelay headroom; generous bound for slow CI.
	if elapsed > 30*time.Second {
		t.Errorf("EncodeTool took %v — the shim was not killed within the timeout", elapsed)
	}
}

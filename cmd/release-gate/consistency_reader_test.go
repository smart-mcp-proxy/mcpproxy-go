package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Sol 6.1 round 1 (livecmd finding 3): after a timed-out command the reader
// is dead — a late reply must never be taken as the answer to a later
// command (the reload phase would otherwise read the stale live snapshot).
func TestWebUIReader_TimeoutIsTerminal(t *testing.T) {
	ctx := context.Background()
	// Answers each command after 400ms — longer than the reader's timeout.
	cmd := exec.CommandContext(ctx, "sh", "-c", `while read l; do sleep 0.4; echo '{"ok":true,"rows":[]}'; done`)
	r, err := startReaderCmd(cmd)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer r.close()
	r.timeout = 100 * time.Millisecond
	if _, err := r.do(ctx, "snapshot"); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("first command: err=%v, want timeout", err)
	}
	time.Sleep(600 * time.Millisecond) // the late reply has arrived by now
	r.timeout = 2 * time.Second
	if line, err := r.do(ctx, "reload"); err == nil {
		t.Fatalf("dead reader answered %q", line)
	}
}

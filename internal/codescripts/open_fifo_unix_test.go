//go:build !windows

package codescripts

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolve_FIFOIsRejectedPromptly pins that a non-regular file which BLOCKS
// on open cannot park the handler goroutine. Opening a FIFO read-only waits for
// a writer forever, and a blocked open(2) is not interruptible by context
// cancellation — so the open must not block in the first place, leaving the
// existing regular-file re-verify to reject it.
func TestResolve_FIFOIsRejectedPromptly(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "blocking.js")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("filesystem does not support FIFOs: %v", err)
	}

	type outcome struct {
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		_, _, err := Resolve(dir, "blocking", "")
		done <- outcome{err: err}
	}()

	select {
	case got := <-done:
		require.Error(t, got.err, "a FIFO is not a regular file and must be rejected")
		var invalid *InvalidError
		require.True(t, errors.As(got.err, &invalid), "want *InvalidError, got %T: %v", got.err, got.err)
		assert.Equal(t, ReasonNonRegular, invalid.Reason)
	case <-time.After(10 * time.Second):
		t.Fatal("Resolve blocked on a FIFO instead of rejecting it — the open has no writer and never returns")
	}
}

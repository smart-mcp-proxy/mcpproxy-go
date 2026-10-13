package runtime

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestPromptsRefreshDebouncer_CoalescesBurst verifies a burst of triggers
// collapses to one fire per window (F13), and that a trigger after the window
// opens a fresh one.
func TestPromptsRefreshDebouncer_CoalescesBurst(t *testing.T) {
	var fires int32
	d := newPromptsRefreshDebouncer(50*time.Millisecond, func() {
		atomic.AddInt32(&fires, 1)
	})

	for i := 0; i < 10; i++ {
		d.trigger()
	}
	time.Sleep(120 * time.Millisecond)
	if got := atomic.LoadInt32(&fires); got != 1 {
		t.Fatalf("expected exactly 1 fire for a burst, got %d", got)
	}

	// A trigger after the window opens a fresh window and fires again.
	d.trigger()
	time.Sleep(120 * time.Millisecond)
	if got := atomic.LoadInt32(&fires); got != 2 {
		t.Fatalf("expected 2 fires after a second window, got %d", got)
	}
}

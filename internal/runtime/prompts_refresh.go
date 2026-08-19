package runtime

import (
	"sync"
	"time"
)

// promptsRefreshDebounceWindow bounds how long a burst of upstream
// notifications/prompts/list_changed notifications is held before a single
// RefreshPrompts fan-out is triggered (F13). RefreshPrompts re-lists prompts
// across EVERY connected server (one 30s-budget ListPrompts per server), and a
// shared config push can make several upstreams fire within milliseconds, so a
// trailing-edge window collapses the burst to one aggregation. 1s trades a
// barely-perceptible staleness for a large reduction in redundant fan-outs.
const promptsRefreshDebounceWindow = time.Second

// promptsRefreshDebouncer coalesces upstream prompts/list_changed signals into
// at most one fire() per window. Trailing-edge: fire() runs once, `window` after
// the FIRST trigger of a burst; triggers landing inside the armed window are
// absorbed.
type promptsRefreshDebouncer struct {
	mu     sync.Mutex
	timer  *time.Timer
	window time.Duration
	fire   func()
}

func newPromptsRefreshDebouncer(window time.Duration, fire func()) *promptsRefreshDebouncer {
	return &promptsRefreshDebouncer{window: window, fire: fire}
}

func (d *promptsRefreshDebouncer) trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		return // already armed; this trigger is coalesced
	}
	d.timer = time.AfterFunc(d.window, func() {
		d.mu.Lock()
		d.timer = nil
		d.mu.Unlock()
		d.fire()
	})
}

package upstream

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

type recordingHandler struct {
	mu     sync.Mutex
	titles []string
}

func (h *recordingHandler) SendNotification(n *Notification) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.titles = append(h.titles, n.Title)
}

func (h *recordingHandler) count(title string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, t := range h.titles {
		if t == title {
			n++
		}
	}
	return n
}

// GH #1537: a speculative Pending Auth park is re-probed every
// GaveUpProbeInterval (PendingAuth → Connecting → PendingAuth). The sign-in
// prompt must fire once per park episode, not on every probe.
func TestStateChangeNotifier_ProbeReparkNotifiesOnce(t *testing.T) {
	h := &recordingHandler{}
	nm := NewNotificationManager()
	nm.AddHandler(h)
	notify := StateChangeNotifier(nm, "s")

	probe := &types.ConnectionInfo{State: types.StatePendingAuth, PendingAuthProbe: true}
	connecting := &types.ConnectionInfo{State: types.StateConnecting}

	notify(types.StateConnecting, types.StatePendingAuth, probe)
	for i := 0; i < 3; i++ { // three probe cycles
		notify(types.StatePendingAuth, types.StateConnecting, connecting)
		notify(types.StateConnecting, types.StatePendingAuth, probe)
	}
	waitForCount(t, h, "Authentication Required", 1)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, h.count("Authentication Required"), "probe re-parks must not re-prompt")

	// Recovery ends the episode; a later park prompts again.
	notify(types.StateConnecting, types.StateReady, &types.ConnectionInfo{State: types.StateReady})
	notify(types.StateReady, types.StateConnecting, connecting)
	notify(types.StateConnecting, types.StatePendingAuth, probe)
	waitForCount(t, h, "Authentication Required", 2)
}

// A confirmed park keeps the historical behaviour: one prompt per transition
// into Pending Auth.
func TestStateChangeNotifier_ConfirmedParkNotifies(t *testing.T) {
	h := &recordingHandler{}
	nm := NewNotificationManager()
	nm.AddHandler(h)
	notify := StateChangeNotifier(nm, "s")

	confirmed := &types.ConnectionInfo{State: types.StatePendingAuth}
	notify(types.StateConnecting, types.StatePendingAuth, confirmed)
	notify(types.StatePendingAuth, types.StateConnecting, &types.ConnectionInfo{State: types.StateConnecting})
	notify(types.StateConnecting, types.StatePendingAuth, confirmed)
	waitForCount(t, h, "Authentication Required", 2)
}

func waitForCount(t *testing.T, h *recordingHandler, title string, want int) {
	t.Helper()
	require.Eventually(t, func() bool { return h.count(title) >= want }, 2*time.Second, 5*time.Millisecond,
		"want %d %q notifications, got %d", want, title, h.count(title))
}

// A failed probe that ends in Error (e.g. a network failure) between two
// speculative parks is the same unconfirmed episode, and SetError's callback
// is asynchronous, so it must not reset the prompt state: a stale Error
// arriving after a newer park would otherwise re-enable a repeat prompt.
func TestStateChangeNotifier_ErrorDoesNotResetProbeEpisode(t *testing.T) {
	h := &recordingHandler{}
	nm := NewNotificationManager()
	nm.AddHandler(h)
	notify := StateChangeNotifier(nm, "s")

	probe := &types.ConnectionInfo{State: types.StatePendingAuth, PendingAuthProbe: true}
	connecting := &types.ConnectionInfo{State: types.StateConnecting}

	notify(types.StateConnecting, types.StatePendingAuth, probe)
	notify(types.StatePendingAuth, types.StateConnecting, connecting)
	notify(types.StateConnecting, types.StatePendingAuth, probe)
	// Stale, asynchronously delivered Error from an earlier attempt.
	notify(types.StateConnecting, types.StateError, &types.ConnectionInfo{State: types.StateError})
	notify(types.StatePendingAuth, types.StateConnecting, connecting)
	notify(types.StateConnecting, types.StatePendingAuth, probe)

	waitForCount(t, h, "Authentication Required", 1)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, h.count("Authentication Required"))
}

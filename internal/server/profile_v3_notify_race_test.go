//go:build !server

package server

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// F5.1: a profile edit followed by an UNRELATED publication (empty delta) before
// the worker observes the first snapshot must still be delivered: a batch is
// satisfied by any snapshot at or after its own, not only by its exact pointer.
func TestProfileV3Notify_UnrelatedPublishRacingAProfileEditStillDelivers(t *testing.T) {
	a := &config.Config{Profiles: []config.ProfileConfig{{Name: "ro", Servers: []string{"a"}}}}
	b := &config.Config{Profiles: []config.ProfileConfig{{Name: "ro", Servers: []string{"a", "b"}}}}
	c := &config.Config{ToolsLimit: 21, Profiles: b.Profiles} // unrelated: empty delta against b

	var mu sync.Mutex
	var got []string
	n := newProfileChangeNotifier(&MCPProxyServer{config: c}, func(name, change string) {
		mu.Lock()
		got = append(got, name+":"+change)
		mu.Unlock()
	})
	n.waitFor = 300 * time.Millisecond
	n.enqueue(a, b)
	n.enqueue(b, c) // empty delta; the runtime already moved on to c
	n.start()
	defer n.close()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 1
	}, 2*time.Second, 5*time.Millisecond, "the profile change was dropped after an unrelated publication")
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"ro:update"}, got)
}

// F5.2: delivery is held while a rename is rewriting session selections, so the
// client's re-list cannot beat the rewrite.
func TestProfileV3Notify_DeliveryWaitsForAHeldRename(t *testing.T) {
	a := &config.Config{Profiles: []config.ProfileConfig{{Name: "ro", Servers: []string{"a"}}}}
	b := &config.Config{Profiles: []config.ProfileConfig{{Name: "ro2", Servers: []string{"a"}}}}

	var mu sync.Mutex
	var got []string
	n := newProfileChangeNotifier(&MCPProxyServer{config: b}, func(name, change string) {
		mu.Lock()
		got = append(got, name+":"+change)
		mu.Unlock()
	})
	p := &MCPProxyServer{profileNotifier: n}
	release := p.HoldProfileNotifications()
	n.enqueue(a, b)
	n.start()
	defer n.close()

	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	assert.Empty(t, got, "nothing is delivered while the rename holds the notifier")
	mu.Unlock()

	release()
	release() // idempotent
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) > 0
	}, 2*time.Second, 5*time.Millisecond, "delivery resumes once the rename finished")
}

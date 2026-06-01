package supervisor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
)

// blockingUpstream is a test double whose ConnectServer blocks until released,
// so a test can hold a Connect "in flight" and observe whether Close() (the
// upstream disconnect path driven by Supervisor.Stop) overlaps it.
//
// This reproduces the MCP-770 root cause: runtime.Close -> Supervisor.Stop ->
// ShutdownAll/Disconnect must NOT run while a reconcile-dispatched Connect is
// still executing on the same client.
type blockingUpstream struct {
	mu sync.Mutex

	connectStarted chan struct{} // closed when ConnectServer first enters
	releaseConnect chan struct{} // ConnectServer blocks until this is closed

	connectInFlight bool // true while ConnectServer is blocked
	overlapDetected bool // set true if Close() ran while a Connect was in flight
	closed          bool

	states  map[string]*ServerState
	eventCh chan Event
}

func newBlockingUpstream() *blockingUpstream {
	return &blockingUpstream{
		connectStarted: make(chan struct{}),
		releaseConnect: make(chan struct{}),
		states:         make(map[string]*ServerState),
		eventCh:        make(chan Event, 10),
	}
}

func (b *blockingUpstream) AddServer(name string, cfg *config.ServerConfig) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.states[name] = &ServerState{Name: name, Config: cfg, Enabled: cfg.Enabled, Connected: false}
	return nil
}

func (b *blockingUpstream) RemoveServer(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.states, name)
	return nil
}

func (b *blockingUpstream) ConnectServer(_ context.Context, name string) error {
	b.mu.Lock()
	b.connectInFlight = true
	// Signal exactly once that a connect is in flight.
	select {
	case <-b.connectStarted:
	default:
		close(b.connectStarted)
	}
	b.mu.Unlock()

	<-b.releaseConnect // block here, simulating a slow Connect

	b.mu.Lock()
	b.connectInFlight = false
	if state, ok := b.states[name]; ok {
		state.Connected = true
	}
	b.mu.Unlock()
	return nil
}

func (b *blockingUpstream) DisconnectServer(string) error    { return nil }
func (b *blockingUpstream) ConnectAll(context.Context) error { return nil }

func (b *blockingUpstream) GetServerState(name string) (*ServerState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.states[name]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, nil
}

func (b *blockingUpstream) GetAllStates() map[string]*ServerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]*ServerState, len(b.states))
	for k, v := range b.states {
		cp := *v
		out[k] = &cp
	}
	return out
}

func (b *blockingUpstream) IsUserLoggedOut(string) bool { return false }
func (b *blockingUpstream) Subscribe() <-chan Event     { return b.eventCh }
func (b *blockingUpstream) Unsubscribe(<-chan Event)    {}

func (b *blockingUpstream) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.connectInFlight {
		b.overlapDetected = true
	}
	b.closed = true
}

func (b *blockingUpstream) release() { close(b.releaseConnect) }

func (b *blockingUpstream) sawOverlap() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.overlapDetected
}

// TestSupervisor_Stop_DrainsInFlightConnectBeforeClose is the MCP-783 regression
// guard. Stop() must wait for in-flight reconcile action goroutines (here, a slow
// Connect) to finish before it disconnects upstream clients via upstream.Close().
// Before the drain fix, Stop() returned immediately and Close() overlapped the
// still-running Connect — the root of the MCP-770 race cascade.
func TestSupervisor_Stop_DrainsInFlightConnectBeforeClose(t *testing.T) {
	cfg := &config.Config{
		Listen:  "127.0.0.1:8080",
		Servers: []*config.ServerConfig{{Name: "slow-server", Enabled: true, Quarantined: false}},
	}
	configSvc := configsvc.NewService(cfg, "/tmp/config.json", zap.NewNop())
	defer configSvc.Close()

	up := newBlockingUpstream()
	sup := New(configSvc, up, zap.NewNop())

	// Dispatch the Connect action (runs in its own goroutine).
	require.NoError(t, sup.reconcile(configSvc.Current()))

	// Wait until Connect is actually in flight (blocked on releaseConnect).
	select {
	case <-up.connectStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("ConnectServer never started")
	}

	// Call Stop() in the background; it must block on draining the in-flight Connect.
	stopReturned := make(chan struct{})
	go func() {
		sup.Stop()
		close(stopReturned)
	}()

	// Stop() must NOT return while Connect is still in flight.
	select {
	case <-stopReturned:
		t.Fatal("Stop() returned before in-flight Connect completed (no drain)")
	case <-time.After(200 * time.Millisecond):
		// expected: Stop is draining
	}

	// Release the Connect; Stop() should now complete.
	up.release()
	select {
	case <-stopReturned:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() did not return after Connect was released")
	}

	require.False(t, up.sawOverlap(),
		"upstream.Close() overlapped an in-flight Connect — drain-before-disconnect failed")
}

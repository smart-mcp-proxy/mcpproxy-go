// Package dialgate is the per-instance retirement barrier of Spec 116 PR4
// (#1564, FR-022..FR-026).
//
// Every managed upstream instance owns one Gate. Everything the instance
// sends to the outside world passes through it:
//
//   - every TCP dial of its HTTP, SSE and OAuth traffic (Gate.DialContext,
//     Gate.Transport), and every Write on a connection it dialed (gatedConn);
//   - every process it launches (stdio, Docker, the local launcher, the OAuth
//     browser): the launch runs inside an admitted section (Gate.Admit) and the
//     process is registered with the gate there (Gate.TrackProcess);
//   - every MCP request and notification its mcp-go transport sends
//     (WrapTransport).
//
// Retire closes the gate for good. After Retire returns nothing new starts:
// a dial, a launch or a Write that begins afterwards fails with ErrRetired
// without touching the network or the OS, every tracked connection is closed
// and every tracked process group is killed. A section that was admitted
// before the latch finishes before Retire returns (the write-lock barrier),
// and anything it registered while Retire was draining is closed or killed
// by the second sweep.
//
// Lock order (spec plan.md "Lock order"): Gate.mu is L5 and Gate.connsMu is
// L5'. The read lock is held only around ONE leaf operation (a dial, one
// conn Write, or a process start plus its registration) and never nested;
// connsMu is a leaf taken under the read lock in a dial section and without
// mu in Retire. Retire is called by the upstream manager under its own
// locks (L3 W + L4 W); the gate never calls back into the manager.
package dialgate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// ErrRetired is returned by every gated operation on a retired instance. It
// is internal: the managed client maps it to the "server unavailable" error
// before it reaches a surface (FR-027).
var ErrRetired = errors.New("upstream instance retired (removed, replaced or quarantined)")

// retireWarnAfter is the Retire duration above which a warning is logged
// (FR-025).
const retireWarnAfter = time.Second

// Hooks are test seams. Nil fields do nothing; production never sets them.
type Hooks struct {
	// BeforeAdmit runs before an admitted section or a dial takes the read
	// lock (the quickstart's `dialgate.beforeAdmit` park point).
	BeforeAdmit func()
	// BeforeTrack runs inside a dial section after the dial succeeded and
	// before the connection is registered (T4.16).
	BeforeTrack func()
	// AfterSweepBeforeBarrier runs inside Retire after the first sweep and
	// before the write-lock barrier (T4.16).
	AfterSweepBeforeBarrier func()
	// SkipTrackLatch makes track ignore the latch, so a test can prove the
	// second sweep closes a connection registered during the drain (T4.16).
	SkipTrackLatch bool
}

// Gate is one instance's retirement barrier. The zero value is not usable;
// use New.
type Gate struct {
	mu      sync.RWMutex // L5. R: one admitted leaf operation. W: Retire's barrier only.
	retired atomic.Bool  // set under connsMu
	ctx     context.Context
	cancel  context.CancelFunc

	connsMu sync.Mutex // L5'. Guards conns and procs.
	conns   map[*gatedConn]struct{}
	procs   map[*Process]struct{}

	retireOnce    sync.Once
	retireWaiting atomic.Bool

	httpOnce sync.Once
	httpT    atomic.Pointer[http.Transport]

	hooks  atomic.Pointer[Hooks]
	logger atomic.Pointer[zap.Logger]
	label  atomic.Pointer[string]
}

// New returns an open gate. A gate that is never retired (the CLI client, a
// transient helper) behaves like no gate at all apart from connection
// tracking.
func New() *Gate {
	ctx, cancel := context.WithCancel(context.Background())
	return &Gate{
		ctx:    ctx,
		cancel: cancel,
		conns:  make(map[*gatedConn]struct{}),
		procs:  make(map[*Process]struct{}),
	}
}

// SetLogger sets the logger and the server label Retire reports a slow
// retirement with.
func (g *Gate) SetLogger(l *zap.Logger, label string) {
	if l != nil {
		g.logger.Store(l)
	}
	g.label.Store(&label)
}

// SetHooks installs test seams (nil removes them). Test-only.
func (g *Gate) SetHooks(h *Hooks) { g.hooks.Store(h) }

var noHooks = &Hooks{}

func (g *Gate) hook() *Hooks {
	if h := g.hooks.Load(); h != nil {
		return h
	}
	return noHooks
}

// Context is cancelled by Retire. Long-lived work an instance starts (the
// stdio child's exec context, the SSE stream) runs under it.
func (g *Gate) Context() context.Context { return g.ctx }

// Retired reports whether Retire has latched.
func (g *Gate) Retired() bool { return g.retired.Load() }

// RetireWaiting reports whether Retire is blocked on its barrier, waiting for
// an admitted section to finish. Test probe.
func (g *Gate) RetireWaiting() bool { return g.retireWaiting.Load() }

// Admit opens an admitted section: a single leaf operation (a process start
// and its registration, a browser launch) that Retire must either see
// complete or prevent. It returns ErrRetired once the gate is retired, and
// ctx's error when ctx is already done. The caller must call release once
// (extra calls are no-ops) and must not start another gated operation (a
// dial, a gated Write, another Admit) before releasing it.
func (g *Gate) Admit(ctx context.Context) (release func(), err error) {
	if fn := g.hook().BeforeAdmit; fn != nil {
		fn()
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if g.retired.Load() {
		return nil, ErrRetired
	}
	g.mu.RLock()
	if g.retired.Load() {
		g.mu.RUnlock()
		return nil, ErrRetired
	}
	var once sync.Once
	return func() { once.Do(g.mu.RUnlock) }, nil
}

// DialFunc is the shape of net.Dialer.DialContext and http.Transport.DialContext.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// DialContext gates next: the dial and the registration of the connection it
// returns run in ONE admitted section (FR-025c, review R5.1), so a connection
// is never handed to its caller, and so never written, before it is tracked.
// Retire cancels an in-progress dial.
func (g *Gate) DialContext(next DialFunc) DialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		release, err := g.Admit(ctx)
		if err != nil {
			return nil, err
		}
		defer release()

		dctx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(g.ctx, cancel)
		conn, err := next(dctx, network, addr)
		stop()
		cancel()
		if err != nil {
			if g.retired.Load() {
				return nil, fmt.Errorf("%w: %w", ErrRetired, err)
			}
			return nil, err
		}
		gc := &gatedConn{Conn: conn, g: g}
		if fn := g.hook().BeforeTrack; fn != nil {
			fn()
		}
		if err := g.track(gc); err != nil {
			return nil, err
		}
		return gc, nil
	}
}

// track registers c. It runs inside the admitted dial section. The latch is
// read under connsMu, the mutex Retire sets it under, so a connection
// registered after Retire's first sweep is closed here and one registered
// before it is closed by the sweep.
func (g *Gate) track(c *gatedConn) error {
	g.connsMu.Lock()
	if g.retired.Load() && !g.hook().SkipTrackLatch {
		g.connsMu.Unlock()
		_ = c.Conn.Close()
		return ErrRetired
	}
	g.conns[c] = struct{}{}
	g.connsMu.Unlock()
	return nil
}

func (g *Gate) untrack(c *gatedConn) {
	g.connsMu.Lock()
	delete(g.conns, c)
	g.connsMu.Unlock()
}

// Process is a process registered with the gate.
type Process struct {
	g    *Gate
	kill func()
}

// TrackProcess registers a process that was just started inside an admitted
// section. kill must terminate the process and everything it spawned (the
// process group, the Windows Job), must be safe to call more than once and
// must not block for long. When the gate is already retired, kill runs at
// once and ErrRetired is returned.
func (g *Gate) TrackProcess(kill func()) (*Process, error) {
	p := &Process{g: g, kill: kill}
	g.connsMu.Lock()
	if g.retired.Load() {
		g.connsMu.Unlock()
		p.Kill()
		return nil, ErrRetired
	}
	g.procs[p] = struct{}{}
	g.connsMu.Unlock()
	return p, nil
}

// Kill runs the process's kill function. Safe on nil.
func (p *Process) Kill() {
	if p != nil && p.kill != nil {
		p.kill()
	}
}

// Untrack removes the process from the gate: it exited, or the instance's
// own teardown owns it now. Safe on nil.
func (p *Process) Untrack() {
	if p == nil || p.g == nil {
		return
	}
	p.g.connsMu.Lock()
	delete(p.g.procs, p)
	p.g.connsMu.Unlock()
}

// Retire closes the gate permanently. It is idempotent and bounded: it sets
// the latch (under connsMu), cancels the gate context, closes every tracked
// connection, kills every tracked process, waits for the admitted sections
// that began before the latch (the write-lock barrier), then closes and kills
// whatever was registered during that drain (FR-025, reviews R2.4, R5.1).
// Every admitted section is a leaf operation whose connection is closed or
// whose start has returned, so the barrier cannot wait on the network.
//
// Process kills are signals (SIGKILL to the process group; Job termination
// on Windows): Retire does not wait for the kernel to reap the children,
// whose exit status belongs to the transport's own Close.
func (g *Gate) Retire() {
	g.retireOnce.Do(g.retire)
}

func (g *Gate) retire() {
	start := time.Now()

	g.connsMu.Lock()
	g.retired.Store(true)
	conns, procs := g.snapshotLocked()
	g.conns = make(map[*gatedConn]struct{})
	g.connsMu.Unlock()

	g.cancel()
	closeConns(conns)
	killProcs(procs)

	if fn := g.hook().AfterSweepBeforeBarrier; fn != nil {
		fn()
	}

	g.retireWaiting.Store(true)
	g.mu.Lock()
	g.mu.Unlock() //nolint:staticcheck // SA2001: the empty critical section is the barrier
	g.retireWaiting.Store(false)

	// Second sweep: anything registered while the barrier drained, and a
	// second kill of every process group (a child may have forked between
	// the first kill and its own death).
	g.connsMu.Lock()
	conns, procs = g.snapshotLocked()
	g.conns = make(map[*gatedConn]struct{})
	g.procs = make(map[*Process]struct{})
	g.connsMu.Unlock()
	closeConns(conns)
	killProcs(procs)

	if t := g.httpT.Load(); t != nil {
		t.CloseIdleConnections()
	}

	if d := time.Since(start); d > retireWarnAfter {
		if l := g.logger.Load(); l != nil {
			label := ""
			if p := g.label.Load(); p != nil {
				label = *p
			}
			l.Warn("Upstream instance retirement was slow",
				zap.String("server", label),
				zap.Duration("duration", d))
		}
	}
}

// snapshotLocked returns the tracked connections and processes. Caller holds
// connsMu.
func (g *Gate) snapshotLocked() ([]*gatedConn, []*Process) {
	conns := make([]*gatedConn, 0, len(g.conns))
	for c := range g.conns {
		conns = append(conns, c)
	}
	procs := make([]*Process, 0, len(g.procs))
	for p := range g.procs {
		procs = append(procs, p)
	}
	return conns, procs
}

func closeConns(conns []*gatedConn) {
	for _, c := range conns {
		_ = c.Conn.Close()
	}
}

func killProcs(procs []*Process) {
	for _, p := range procs {
		p.Kill()
	}
}

// gatedConn gates every Write on a connection the gate dialed.
type gatedConn struct {
	net.Conn
	g *Gate
}

// Write refuses once the gate is retired, without touching the socket. A
// Write that began before the latch holds the read lock until it returns;
// its connection was tracked before it was handed out, so Retire's first
// sweep closes it and unblocks the write.
func (c *gatedConn) Write(b []byte) (int, error) {
	if c.g.retired.Load() {
		return 0, ErrRetired
	}
	c.g.mu.RLock()
	defer c.g.mu.RUnlock()
	if c.g.retired.Load() {
		return 0, ErrRetired
	}
	return c.Conn.Write(b)
}

// Close untracks and closes the connection.
func (c *gatedConn) Close() error {
	c.g.untrack(c)
	return c.Conn.Close()
}

// defaultDialer mirrors http.DefaultTransport's dialer.
var defaultDialer = &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}

// defaultBase is the base every gated transport clones when the caller has no
// tuned base of its own: http.DefaultTransport's settings (proxy from the
// environment, HTTP/2, idle pool, timeouts). It is never used directly, only
// cloned, and is the one allowlisted reference to http.DefaultTransport on
// the upstream path (FR-024a).
func defaultBase() *http.Transport {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t
	}
	return &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true}
}

// Transport returns a clone of base (http.DefaultTransport's settings when
// nil) whose dials go through the gate, so every connection it opens is
// tracked and every Write on it is gated (FR-024). TLS runs on top of the
// gated connection; a custom DialTLS on base is dropped so the TLS
// handshake's writes are gated too.
func (g *Gate) Transport(base *http.Transport) *http.Transport {
	if base == nil {
		base = defaultBase()
	}
	t := base.Clone()
	next := DialFunc(t.DialContext)
	if t.DialContext == nil {
		next = defaultDialer.DialContext
	}
	t.DialContext = g.DialContext(next)
	t.DialTLSContext = nil
	t.DialTLS = nil //nolint:staticcheck // SA1019: cleared so it can never bypass the gate
	t.Dial = nil    //nolint:staticcheck // SA1019: cleared so it can never bypass the gate
	return t
}

// HTTPTransport is the instance's shared gated transport over the default
// base: OAuth discovery, dynamic client registration, token exchange and
// refresh, and the streamable-HTTP branches that used http.DefaultTransport.
// Sharing one transport per instance keeps connection reuse within the
// instance; Retire closes its idle connections.
func (g *Gate) HTTPTransport() *http.Transport {
	g.httpOnce.Do(func() { g.httpT.Store(g.Transport(nil)) })
	return g.httpT.Load()
}

// HTTPClient returns an *http.Client over the instance's shared gated
// transport.
func (g *Gate) HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: g.HTTPTransport(), Timeout: timeout}
}

// IsRetired reports whether err carries ErrRetired.
func IsRetired(err error) bool { return errors.Is(err, ErrRetired) }

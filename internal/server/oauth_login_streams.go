package server

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Spec 132 FR-031 / SC-006: revoking a login, forgetting its client, switching
// the feature off (even off and straight back on), rotating its tokens or the
// access token's expiry must also end what is already open on that login (a
// long-lived SSE stream, a slow request, a stalled upload), not only the next
// request. oauthResourceAuth authenticates once per request, so every
// authenticated request is tracked here.
//
// Ending a request is two steps: its context is cancelled AND its transport I/O
// is interrupted with immediate read and write deadlines, because a handler
// blocked in a write to a client that stopped reading (or a body read from one
// that stalled) never looks at its context, and these routes clear the server's
// deadlines.
//
// The registry never calls into the runtime while a runtime lock is held and
// takes no runtime lock itself: a watcher goroutine re-reads each tracked grant
// from storage (OAuthLoginService.GrantLive, a plain read) every
// oauthStreamCheckInterval and whenever the binding notifier wakes it, and
// compares the switch-off epoch (an atomic counter) captured at authentication
// with the current one. An expiry timer bound to the access token's lifetime
// interrupts the request when the token runs out. Nothing is allocated until the
// first OAuth request, the watcher exits when the last tracked request ends, and
// server shutdown stops and joins it.

const oauthStreamCheckInterval = time.Second

type oauthStream struct {
	grantID   string
	expiresAt time.Time // the access token's expiry when it was authenticated
	epoch     uint64    // the switch-off epoch when it was authenticated
	timer     *time.Timer

	mu        sync.Mutex
	finished  bool
	interrupt func()
}

// end interrupts the request unless it already finished (a deadline set on a
// connection that moved on to its next request would break that request).
func (st *oauthStream) end() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.finished {
		st.interrupt()
	}
}

type oauthStreamRegistry struct {
	mu      sync.Mutex
	next    uint64
	entries map[uint64]*oauthStream
	running bool
	stopped bool
	wake    chan struct{}
	stop    chan struct{}
	wg      sync.WaitGroup
}

// resetOAuthStreams starts a fresh tracking lifecycle for a new server run: the
// previous run's registry (already stopped and joined by stopOAuthStreams) is
// dropped, and tracking is accepted again. Allocation stays lazy.
func (s *Server) resetOAuthStreams() {
	s.oauthStreamsMu.Lock()
	s.oauthStreamsRun++ // requests of the previous run can no longer register
	s.oauthStreamsStopping = false
	s.oauthStreams.Store(nil)
	s.oauthStreamsMu.Unlock()
}

// wakeOAuthStreams asks the watcher to re-check now. It allocates nothing and
// never blocks: with no registry (default-off installs) it does nothing.
func (s *Server) wakeOAuthStreams() {
	if r := s.oauthStreams.Load(); r != nil {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
}

// oauthRunGen is the server-run generation a request captures BEFORE it
// authenticates; trackOAuthRequest refuses a request of another generation, so
// a request of a stopped run that was still paused in authentication cannot
// register in the next run.
func (s *Server) oauthRunGen() uint64 {
	s.oauthStreamsMu.Lock()
	defer s.oauthStreamsMu.Unlock()
	return s.oauthStreamsRun
}

// trackOAuthRequest registers an authenticated request of grantID, authenticated
// at switch-off epoch in server run run. interrupt ends it (context cancelled, transport I/O
// interrupted). The returned release must be called when the request ends. ok is
// false when the server is stopping or the request belongs to another run: nothing is tracked and no watcher starts,
// and the caller must not serve the request.
func (s *Server) trackOAuthRequest(grantID string, expiresAt time.Time, epoch, run uint64, interrupt func()) (release func(), ok bool) {
	st := &oauthStream{grantID: grantID, expiresAt: expiresAt, epoch: epoch, interrupt: interrupt}
	// The stopping flag, the registry's creation and the entry's insertion are
	// one critical section: a track after stopOAuthStreams has marked the run
	// stopped is refused, whether or not a registry ever existed.
	s.oauthStreamsMu.Lock()
	if s.oauthStreamsStopping || run != s.oauthStreamsRun {
		s.oauthStreamsMu.Unlock()
		return func() {}, false
	}
	reg := s.oauthStreams.Load()
	if reg == nil {
		reg = &oauthStreamRegistry{entries: map[uint64]*oauthStream{}, wake: make(chan struct{}, 1), stop: make(chan struct{})}
		s.oauthStreams.Store(reg)
	}
	reg.mu.Lock()
	reg.next++
	id := reg.next
	reg.entries[id] = st
	start := !reg.running
	if start {
		reg.running = true
		reg.wg.Add(1)
	}
	reg.mu.Unlock()
	s.oauthStreamsMu.Unlock()

	if d := time.Until(expiresAt); d > 0 {
		st.timer = time.AfterFunc(d, st.end)
	} else {
		st.end()
	}
	if start {
		go s.watchOAuthStreams(reg)
	}
	return func() {
		reg.mu.Lock()
		delete(reg.entries, id)
		reg.mu.Unlock()
		st.mu.Lock()
		st.finished = true
		if st.timer != nil {
			st.timer.Stop()
		}
		st.mu.Unlock()
	}, true
}

// watchOAuthStreams re-validates the tracked requests until none is left or the
// server shuts down.
func (s *Server) watchOAuthStreams(reg *oauthStreamRegistry) {
	defer reg.wg.Done()
	ticker := time.NewTicker(oauthStreamCheckInterval)
	defer ticker.Stop()
	for {
		reg.mu.Lock()
		if len(reg.entries) == 0 || reg.stopped {
			reg.running = false
			reg.mu.Unlock()
			return
		}
		type key struct {
			grant string
			exp   time.Time
		}
		groups := map[key][]*oauthStream{}
		for _, e := range reg.entries {
			k := key{e.grantID, e.expiresAt}
			groups[k] = append(groups[k], e)
		}
		reg.mu.Unlock()

		svc := s.oauthLoginSvc()
		var epoch uint64
		if svc != nil {
			epoch = svc.DisableEpoch()
		}
		for k, list := range groups {
			live := svc != nil && svc.GrantLive(k.grant, k.exp)
			for _, e := range list {
				// A request authenticated before the latest switch-off is dead
				// even when the feature is on again.
				if !live || e.epoch < epoch {
					e.end()
				}
			}
		}
		select {
		case <-ticker.C:
		case <-reg.wake:
		case <-reg.stop:
		}
	}
}

// stopOAuthStreams interrupts every tracked request, stops the watcher and
// waits for it. It runs at server shutdown, before the HTTP server waits for
// its active handlers. Nothing is tracked afterwards.
func (s *Server) stopOAuthStreams() {
	s.oauthStreamsMu.Lock()
	s.oauthStreamsStopping = true // also with no registry: late tracking is refused
	reg := s.oauthStreams.Load()
	s.oauthStreamsMu.Unlock()
	if reg == nil {
		return
	}
	reg.mu.Lock()
	if reg.stopped {
		reg.mu.Unlock()
		reg.wg.Wait()
		return
	}
	reg.stopped = true
	close(reg.stop)
	list := make([]*oauthStream, 0, len(reg.entries))
	for _, e := range reg.entries {
		list = append(list, e)
	}
	reg.mu.Unlock()
	for _, e := range list {
		e.end()
	}
	reg.wg.Wait()
}

// serveTracked serves next for an authenticated browser-login request and ends
// the request when its login stops being valid. grantID and expiresAt come from
// the authentication, epoch from BEFORE it (so a switch-off in between counts).
func (s *Server) serveTracked(w http.ResponseWriter, r *http.Request, next http.Handler, grantID string, expiresAt time.Time, epoch, run uint64) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	rc := http.NewResponseController(w)
	release, ok := s.trackOAuthRequest(grantID, expiresAt, epoch, run, func() {
		cancel()
		// Interrupt blocked transport I/O too; errors (a writer that cannot
		// take deadlines) are ignored, the context cancel still applies.
		now := time.Now()
		_ = rc.SetReadDeadline(now)
		_ = rc.SetWriteDeadline(now)
	})
	if !ok {
		// The server is stopping: the MCP handler is not invoked.
		oauthJSONError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "the server is shutting down")
		return
	}
	defer release()
	next.ServeHTTP(w, r.WithContext(ctx))
}

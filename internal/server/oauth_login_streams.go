package server

import (
	"context"
	"sync"
	"time"
)

// Spec 132 FR-031 / SC-006: revoking a login, forgetting its client, switching
// the feature off, rotating its tokens or the access token's expiry must also
// end what is already open on that login (a long-lived SSE stream, a slow
// request), not only the next request. oauthResourceAuth authenticates once
// per request, so every authenticated request is tracked here with the cancel
// of its context.
//
// The registry never calls into the runtime while a runtime lock is held and
// takes no runtime lock itself: a watcher goroutine re-reads each tracked grant
// from storage (OAuthLoginService.GrantLive, a plain read) every
// oauthStreamCheckInterval and whenever the binding notifier wakes it (grant
// revoke, replay revoke, client revoke or forget all call it), and an expiry
// timer bound to the access token's lifetime cancels the request when the token
// runs out. Nothing is allocated until the first OAuth request, and the watcher
// exits when the last tracked request ends.

const oauthStreamCheckInterval = time.Second

type oauthStream struct {
	grantID   string
	expiresAt time.Time // the access token's expiry when it was authenticated
	cancel    context.CancelFunc
	timer     *time.Timer
}

type oauthStreamRegistry struct {
	mu      sync.Mutex
	next    uint64
	entries map[uint64]*oauthStream
	running bool
	wake    chan struct{}
}

// oauthStreamsRegistry returns the registry, creating it on first use.
func (s *Server) oauthStreamsRegistry() *oauthStreamRegistry {
	if r := s.oauthStreams.Load(); r != nil {
		return r
	}
	s.oauthStreams.CompareAndSwap(nil, &oauthStreamRegistry{entries: map[uint64]*oauthStream{}, wake: make(chan struct{}, 1)})
	return s.oauthStreams.Load()
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

// trackOAuthRequest registers an authenticated request of grantID. cancel ends
// its context. The returned release must be called when the request ends; it
// stops the expiry timer, forgets the entry and cancels the context.
func (s *Server) trackOAuthRequest(grantID string, expiresAt time.Time, cancel context.CancelFunc) (release func()) {
	reg := s.oauthStreamsRegistry()
	st := &oauthStream{grantID: grantID, expiresAt: expiresAt, cancel: cancel}
	if d := time.Until(expiresAt); d > 0 {
		st.timer = time.AfterFunc(d, cancel)
	} else {
		cancel()
	}
	reg.mu.Lock()
	reg.next++
	id := reg.next
	reg.entries[id] = st
	start := !reg.running
	reg.running = true
	reg.mu.Unlock()
	if start {
		go s.watchOAuthStreams(reg)
	}
	return func() {
		reg.mu.Lock()
		delete(reg.entries, id)
		reg.mu.Unlock()
		if st.timer != nil {
			st.timer.Stop()
		}
		cancel()
	}
}

// watchOAuthStreams re-validates the tracked grants until none is left.
func (s *Server) watchOAuthStreams(reg *oauthStreamRegistry) {
	ticker := time.NewTicker(oauthStreamCheckInterval)
	defer ticker.Stop()
	for {
		reg.mu.Lock()
		if len(reg.entries) == 0 {
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
		for k, list := range groups {
			if svc != nil && svc.GrantLive(k.grant, k.exp) {
				continue
			}
			for _, e := range list {
				e.cancel()
			}
		}
		select {
		case <-ticker.C:
		case <-reg.wake:
		}
	}
}

package oauth

import (
	"sync"
	"time"
)

// Issue #975: a manual sign-in and a background reconnect used to race each
// other on the same callback server. Dispatching callbacks by `state` fixes the
// correctness half of that (the code always reaches the flow that minted the
// state); this registry fixes the noise half by letting the automatic OAuth
// path stand down while the user is still completing a manual login.
//
// It is deliberately a plain in-memory flag with an expiry, checked and never
// waited on: no lock is held across an OAuth call, so it cannot deadlock the
// reconnect path. A missed release expires on its own rather than suppressing
// reconnects forever.

// manualFlowWindow is the default lifetime of a manual-flow claim. It matches
// the manager's 30-minute manual OAuth context.
const manualFlowWindow = 30 * time.Minute

type manualFlowRegistry struct {
	mu sync.Mutex
	// active maps server name -> claim. Only the newest claim can clear it.
	active map[string]*manualFlowClaim
}

type manualFlowClaim struct {
	expiresAt time.Time
}

var globalManualFlows = &manualFlowRegistry{
	active: make(map[string]*manualFlowClaim),
}

// BeginManualFlow records that a user-initiated OAuth sign-in is in flight for
// serverName and returns a release function. Pass window <= 0 to use the
// default. The returned release only clears the claim it created, so a
// superseded flow releasing late cannot unsuppress a newer one.
func BeginManualFlow(serverName string, window time.Duration) (release func()) {
	if window <= 0 {
		window = manualFlowWindow
	}

	claim := &manualFlowClaim{expiresAt: time.Now().Add(window)}

	globalManualFlows.mu.Lock()
	globalManualFlows.active[serverName] = claim
	globalManualFlows.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			globalManualFlows.mu.Lock()
			defer globalManualFlows.mu.Unlock()
			if current, exists := globalManualFlows.active[serverName]; exists && current == claim {
				delete(globalManualFlows.active, serverName)
			}
		})
	}
}

// IsManualFlowActive reports whether a user-initiated OAuth sign-in is currently
// in flight for serverName. Expired claims are dropped on read.
func IsManualFlowActive(serverName string) bool {
	globalManualFlows.mu.Lock()
	defer globalManualFlows.mu.Unlock()

	claim, exists := globalManualFlows.active[serverName]
	if !exists {
		return false
	}
	if time.Now().After(claim.expiresAt) {
		delete(globalManualFlows.active, serverName)
		return false
	}
	return true
}

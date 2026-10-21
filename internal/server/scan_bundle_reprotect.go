package server

import (
	"context"
	"sync"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// Post-activation re-protection sweep (Spec 101 FR-019, SC-009; tasks B17).
//
// When a new TPA bundle activates, already-approved tools may match a signature
// that did not exist when they were approved. The sweep re-checks them from
// cached data only: leg (i) refreshes the held-scan evidence of pending and
// changed tools, leg (ii) runs a cached informational scan of every
// non-quarantined server with approved tools. It never approves, unquarantines
// or revokes anything (RT-02): findings flow toward human review only.

// bundleReprotectState is the sweep's bookkeeping. The zero value is ready to
// use; the func and duration fields are test seams (nil/zero = production).
type bundleReprotectState struct {
	mu      sync.Mutex
	closed  bool
	nextID  int
	cancels map[int]context.CancelFunc
	// last is the digest of the most recent sweep started and latest the
	// digest of the newest activation seen: the same digest re-fired starts
	// nothing, and a sweep whose digest is no longer the latest is abandoned.
	last   string
	latest string
	// runMu serializes sweeps.
	runMu sync.Mutex

	statusSink     func(scanner.BundleReprotectStatus)
	refreshHeld    func(server string) error
	activeDigest   func() string
	recordHit      func()
	serverPause    time.Duration
	busyRetryDelay time.Duration
}

// handleBundleActivation is the activation observer. It runs the sweep for one
// activation on a tracked goroutine bound to the informational scan context.
//
// STUB (Spec 101 US3 wave 0): no-op until T369.
//
//nolint:unused // registered as the activation observer by T369
func (s *Server) handleBundleActivation(act scanner.BundleActivation) {
	_ = act
}

// runBundleReprotectCatchUp re-runs the sweep at startup when the persisted
// marker (storage.BundleReprotectState) differs from the active bundle digest:
// it covers the construction-time activation, a crash mid-sweep and a binary
// upgrade that changes the embedded corpus.
//
// STUB (Spec 101 US3 wave 0): no-op until T369.
//
//nolint:unused // started next to runBaselineSweep by T369
func (s *Server) runBundleReprotectCatchUp(ctx context.Context) {
	_ = ctx
}

// stopBundleReprotect stops the sweep machinery for Shutdown: it refuses new
// sweeps, cancels the running ones and waits for their goroutines.
//
// STUB (Spec 101 US3 wave 0): no-op until T369.
//
//nolint:unused // called by Shutdown once T369 lands
func (s *Server) stopBundleReprotect() {}

package server

import (
	"context"

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

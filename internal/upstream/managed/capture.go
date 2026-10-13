package managed

import (
	"context"
	"sync/atomic"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// InventoryCapture identifies one upstream tools/list (Spec 117 PR-3, #1565,
// FR-030). The leader of a list flight takes it when it owns the list and
// before the upstream request, and every coalesced waiter of that flight gets
// the same value. The runtime applies tool inventories in capture order: a
// capture older than one already applied for the server writes nothing.
//
// Seq comes from one process-global counter, so it also orders captures across
// a client replacement (remove + re-add, endpoint change). Seq 0 means
// "unordered": a caller with no managed client (fixtures, tests).
type InventoryCapture struct {
	Seq    uint64  // process-global, strictly increasing; 0 = unordered
	Epoch  int64   // the client's connection epoch when the list started
	Client *Client // the client that ran the list
}

// captureSeq is the process-global capture counter.
var captureSeq atomic.Uint64

// nextCaptureSeq returns a fresh capture sequence (never 0).
func nextCaptureSeq() uint64 { return captureSeq.Add(1) }

// newCapture stamps a capture for a list flight that mc is about to run.
func (mc *Client) newCapture() InventoryCapture {
	return InventoryCapture{Seq: nextCaptureSeq(), Epoch: mc.connectionEpoch.Load(), Client: mc}
}

// ListToolsCaptured is ListTools that also returns the capture of the flight
// whose result it returns (the caller's own flight, or the one it joined).
func (mc *Client) ListToolsCaptured(ctx context.Context) ([]*config.ToolMetadata, InventoryCapture, error) {
	return mc.listTools(ctx, false, 0)
}

// ListToolsFreshCaptured is ListToolsFresh that also returns the capture.
func (mc *Client) ListToolsFreshCaptured(ctx context.Context) ([]*config.ToolMetadata, InventoryCapture, error) {
	mc.listToolsMu.Lock()
	entrySeq := mc.listToolsLeaderSeq
	mc.listToolsMu.Unlock()
	return mc.listTools(ctx, true, entrySeq)
}

// Retire marks the client as replaced or removed by the upstream manager
// (FR-036). A retired client is never current again: its captures are
// discarded and WithConnectionEpoch reports it as not current. Retirement is
// an atomic flag, so the manager can set it under its own locks without taking
// any client lock.
func (mc *Client) Retire() { mc.retired.Store(true) }

// Retired reports whether the manager retired the client.
func (mc *Client) Retired() bool { return mc.retired.Load() }

// CaptureIsCurrent reports whether c was taken by this client, the client is
// not retired and its connection epoch has not moved since the list started
// (no reconnect). Lock-free: it reads two atomics, so it can be called under
// any lock (the runtime calls it under a server's tool-approval lock).
func (mc *Client) CaptureIsCurrent(c InventoryCapture) bool {
	return mc != nil && c.Client == mc && c.Seq != 0 && !mc.retired.Load() && mc.connectionEpoch.Load() == c.Epoch
}

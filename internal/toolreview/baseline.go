// Package toolreview holds the value types shared by the tool-review layers
// (storage, runtime and the security scanner) for Spec 117. It is a leaf
// package: it imports only the standard library, so storage (which imports the
// scanner) and the scanner can both depend on it without an import cycle.
package toolreview

import "time"

// Baseline decision sources (Spec 117 FR-020).
const (
	// BaselineSourceDiscovery: a trusted discovery pass established the
	// server's first tool baseline.
	BaselineSourceDiscovery = "discovery"
	// BaselineSourceServerApproval: an operator approved the server through
	// the security (review) approval, with or without tools.
	BaselineSourceServerApproval = "server_approval"
	// BaselineSourceInferred: back-filled from approved/changed records that
	// predate the marker (an older store).
	BaselineSourceInferred = "inferred"
)

// BaselineDecision records that a server's tool baseline has been decided.
// While it exists, a new or replaced tool on that server is never
// auto-approved as a first baseline (Spec 117 FR-020..FR-024, issue #1567).
type BaselineDecision struct {
	DecidedAt time.Time `json:"decided_at"`
	Source    string    `json:"source"`
	By        string    `json:"by,omitempty"`
}

package contracts

import "time"

// AttentionSubject identifies what an AttentionItem is about
// (contracts/rest-api.md#attention, Spec 109 FR-001).
type AttentionSubject struct {
	Type string `json:"type"` // server|tool|client
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AttentionFix describes the one action that resolves an AttentionItem
// (Spec 109 FR-005): a verb the caller may run directly, and a target
// screen/route.
type AttentionFix struct {
	Verb   string `json:"verb"`
	Label  string `json:"label"`
	Target string `json:"target"`
}

// AttentionItem is one row of the needs-attention list
// (contracts/rest-api.md#attention), identical across every surface (Web UI,
// macOS tray/Home, CLI `attention`/`status`/`doctor`). Computed by
// internal/runtime.Compute from in-memory state; this package holds only the
// wire shape, mirroring how contracts.HealthStatus sits beside
// internal/health.CalculateHealth.
type AttentionItem struct {
	ID      string           `json:"id"` // kind:type:subject[:state]
	Kind    string           `json:"kind"`
	Rank    int              `json:"rank"`
	Subject AttentionSubject `json:"subject"`
	Summary string           `json:"summary"`
	Detail  string           `json:"detail,omitempty"`
	Fix     AttentionFix     `json:"fix"`
	Since   time.Time        `json:"since"`
}

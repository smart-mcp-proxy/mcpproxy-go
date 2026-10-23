// Package qapilot is the independent reference model for MCPProxy's
// quarantine and approval policy (the quarantine/approval testing pilot).
//
// The model encodes the policy from the specs and docs listed in
// requirements.go. It computes every expected outcome from its own prior
// state, the action inputs and that policy only: it imports no production
// package and never reads implementation state. Adapters in the production
// packages' tests drive the real implementation with the same actions and
// compare the implementation's observed state against the model's.
//
// Scope (deliberately bounded, see docs/development/quarantine-approval-pilot.md):
// one server, a monotone tool inventory (tools are added or redefined, never
// removed), a fixed trust mode per server, and the operations an operator,
// an agent or a reconnecting upstream can perform.
package qapilot

import (
	"fmt"
	"sort"
)

// TrustMode mirrors the per-server trust_mode policy values (spec 086 FR-008).
type TrustMode string

const (
	TrustAuto   TrustMode = "auto"
	TrustScan   TrustMode = "scan"
	TrustManual TrustMode = "manual"
)

// Verdict is the fixture label of a tool definition's offline TPA scan. The
// model does not run a scanner; a scenario supplies definitions whose verdict
// is known (benign text → clean, a known TPA payload → dangerous). The runtime
// conformance test asserts the fixture labels against the real scanner once,
// in its own check, so a drift in the scanner surfaces as a fixture failure
// rather than as a silently wrong expectation.
type Verdict string

const (
	VerdictClean     Verdict = "clean"
	VerdictDangerous Verdict = "dangerous"
)

// Contract is one tool definition as the policy sees it: an identity (two
// definitions with the same Key hash identically; a different Key is a
// changed contract) and its scan verdict.
type Contract struct {
	Key     string
	Verdict Verdict
}

// Status is the tool-approval status (spec 041: exactly three states).
type Status string

const (
	StatusNone     Status = "" // never discovered: no record
	StatusPending  Status = "pending"
	StatusChanged  Status = "changed"
	StatusApproved Status = "approved"
)

// DispatchClass is the policy's answer to "may a call_tool for this tool be
// dispatched upstream?" (docs/features/tools-preflight.md, spec 098 FR-002,
// spec 105 FR-009). The names match the published preflight reason vocabulary
// so a report reads the same on both sides.
type DispatchClass string

const (
	DispatchReady             DispatchClass = "ready"
	DispatchServerQuarantined DispatchClass = "server_quarantined"
	DispatchServerDisabled    DispatchClass = "server_disabled"
	DispatchDeniedByConfig    DispatchClass = "denied_by_config"
	DispatchBlockedByUser     DispatchClass = "blocked_by_user"
	DispatchChanged           DispatchClass = "changed"
	DispatchPendingApproval   DispatchClass = "pending_approval"
	DispatchServerNotFound    DispatchClass = "server_not_configured"
)

// Tool is the model's view of one tool's approval record.
type Tool struct {
	Status Status
	// Current is the contract the upstream last presented.
	Current Contract
	// Approved is the contract the operator (or an automatic policy) approved.
	// Meaningful only while Status is approved or changed.
	Approved Contract
	// Disabled is the operator's block on the tool (approved + disabled is
	// the "blocked" state, spec 109).
	Disabled bool
	// ApprovedBy records WHO approved: "operator", "auto", "baseline",
	// "scan", "server-approval". It explains a promotion in failure output and
	// is not compared (the implementation uses its own labels).
	ApprovedBy string
}

// Server is the model state for one upstream server.
type Server struct {
	Name string
	// Quarantined is the server-level quarantine flag.
	Quarantined bool
	// Enabled is the server's enabled flag.
	Enabled bool
	// Trust is the server's effective trust mode. Fixed for a scenario.
	Trust TrustMode
	// GlobalQuarantine is the global quarantine_enabled switch (default true).
	GlobalQuarantine bool
	// BaselineDecided is the durable per-server tool baseline marker (spec
	// 117 FR-020..FR-024).
	BaselineDecided bool
	Tools           map[string]*Tool
}

// NewServer returns a model server in the state a freshly configured server
// has: no tool records, no baseline decision.
func NewServer(name string, quarantined, enabled bool, trust TrustMode, globalQuarantine bool) *Server {
	return &Server{
		Name:             name,
		Quarantined:      quarantined,
		Enabled:          enabled,
		Trust:            trust,
		GlobalQuarantine: globalQuarantine,
		Tools:            map[string]*Tool{},
	}
}

// Clone returns a deep copy, used by the shrinker to replay prefixes.
func (s *Server) Clone() *Server {
	c := *s
	c.Tools = make(map[string]*Tool, len(s.Tools))
	for name, t := range s.Tools {
		tc := *t
		c.Tools[name] = &tc
	}
	return &c
}

// gateOn reports whether tool-level quarantine applies to this server: the
// global switch is on and the server has not opted out with trust_mode auto
// (docs/features/security-quarantine.md trust-mode table; spec 086 FR-009).
func (s *Server) gateOn() bool {
	return s.GlobalQuarantine && s.Trust != TrustAuto
}

// HasBaseline is spec 117 FR-021: the baseline marker exists, or any record
// is approved or changed (a changed record proves a prior approval).
func (s *Server) HasBaseline() bool {
	if s.BaselineDecided {
		return true
	}
	for _, t := range s.Tools {
		if t.Status == StatusApproved || t.Status == StatusChanged {
			return true
		}
	}
	return false
}

// Discover applies one upstream tools/list capture (first discovery or a
// reconnect) to the model. Rules, in the order the policy states them:
//
//   - A new tool is approved when the gate is off (auto mode or global
//     quarantine disabled); when this pass is the trusted server's first
//     baseline (gate on, server not quarantined, no baseline yet — spec 117
//     FR-020, MCP-2931); or in scan mode when the definition scans clean
//     (spec 086 FR-013). Otherwise it is pending (spec 041 US1).
//   - A pending tool is promoted by that same first-baseline pass only when
//     its definition is unchanged since it was recorded (MCP-2931 #4), or
//     when the gate is off. Otherwise it stays pending with the live
//     definition recorded. Scan mode never promotes an existing pending
//     record (the scan gate applies to the pass that files the record).
//   - An approved tool whose definition changed becomes changed (the
//     rug-pull guard, spec 041 FR-001) unless the gate is off, in which case
//     the new definition is approved (trust auto: auto_approve_changes), or
//     the server is in scan mode and the new definition scans clean, in
//     which case it is scan-approved (spec 119 FR-004). In manual mode a
//     changed definition is ALWAYS held, whatever its verdict (RT-02).
//   - A changed tool returns to approved when the live definition is the
//     approved one again (description revert, spec 041 US2), when the gate
//     is off, or in scan mode when its current definition scans clean (spec
//     119 FR-006); otherwise it stays changed with the live definition
//     recorded.
//   - The baseline marker is set by a pass that leaves at least one approved
//     or changed record (spec 117 FR-022).
func (s *Server) Discover(inventory map[string]Contract) {
	gate := s.gateOn()
	baselinePass := gate && !s.Quarantined && !s.HasBaseline()
	for _, name := range sortedKeys(inventory) {
		c := inventory[name]
		t := s.Tools[name]
		if t == nil {
			t = &Tool{Current: c}
			s.Tools[name] = t
			switch {
			case !gate:
				t.approve("auto")
			case baselinePass:
				t.approve("baseline")
			case s.Trust == TrustScan && c.Verdict == VerdictClean:
				t.approve("scan")
			default:
				t.Status = StatusPending
			}
			continue
		}
		switch t.Status {
		case StatusPending:
			unchanged := t.Current.Key == c.Key
			t.Current = c
			switch {
			case !gate:
				t.approve("auto")
			case baselinePass && unchanged:
				t.approve("baseline")
			}
		case StatusApproved:
			t.Current = c
			if c.Key != t.Approved.Key {
				switch {
				case !gate:
					t.approve("auto")
				case s.Trust == TrustScan && c.Verdict == VerdictClean:
					t.approve("scan")
				default:
					t.Status = StatusChanged
				}
			}
		case StatusChanged:
			t.Current = c
			switch {
			case c.Key == t.Approved.Key:
				t.approve("revert")
			case !gate:
				t.approve("auto")
			case s.Trust == TrustScan && c.Verdict == VerdictClean:
				t.approve("scan")
			}
		}
	}
	for _, t := range s.Tools {
		if t.Status == StatusApproved || t.Status == StatusChanged {
			s.BaselineDecided = true
			break
		}
	}
}

func (t *Tool) approve(by string) {
	t.Status = StatusApproved
	t.Approved = t.Current
	t.ApprovedBy = by
}

// ApproveTools is the operator's explicit per-tool approval (REST
// /tools/approve, MCP approve_tool): each named tool with a record becomes
// approved for its current definition. Names without a record are ignored,
// as the implementation documents (not an error). Returns the names that
// were promoted.
func (s *Server) ApproveTools(names ...string) []string {
	var promoted []string
	for _, name := range names {
		t := s.Tools[name]
		if t == nil {
			continue
		}
		if t.Status != StatusApproved {
			promoted = append(promoted, name)
		}
		t.approve("operator")
	}
	return promoted
}

// ApproveAll approves every pending and changed record (REST approve_all,
// MCP approve_all_tools).
func (s *Server) ApproveAll() []string {
	var promoted []string
	for _, name := range sortedKeys(s.Tools) {
		t := s.Tools[name]
		if t.Status == StatusPending || t.Status == StatusChanged {
			t.approve("operator")
			promoted = append(promoted, name)
		}
	}
	return promoted
}

// SetQuarantined flips the server-level quarantine flag. Releasing a server
// is baseline trust (docs/features/security-quarantine.md "Approving a
// server"): every pending record is promoted to approved for its current
// definition; changed records are NOT touched. Quarantining again changes no
// tool record.
func (s *Server) SetQuarantined(quarantined bool) {
	s.Quarantined = quarantined
	if quarantined {
		return
	}
	for _, t := range s.Tools {
		if t.Status == StatusPending {
			t.approve("server-approval")
		}
	}
}

// SetEnabled flips the server's enabled flag. Approval state is independent
// of enablement.
func (s *Server) SetEnabled(enabled bool) {
	s.Enabled = enabled
}

// SetToolDisabled is the operator's per-tool block/unblock. A block on a
// pending or changed tool approves it as well (spec 109: block is atomic
// approve+disable); an unblock never approves.
func (s *Server) SetToolDisabled(name string, disabled bool) {
	t := s.Tools[name]
	if t == nil {
		return
	}
	t.Disabled = disabled
	if disabled && t.Status != StatusApproved {
		t.approve("operator-block")
	}
}

// Restart models a process restart: every field above is durable (spec 117
// "what must survive restart"), so the model state is unchanged. The adapter
// closes and reopens the implementation and the comparison after this action
// is what verifies persistence.
func (s *Server) Restart() {}

// Dispatch is the policy's dispatch classification for one tool of this
// server, by the published first-match precedence (internal/preflight is the
// implementation; the policy source is docs/features/tools-preflight.md and
// spec 098 FR-002 / spec 105 FR-009): server quarantined → server disabled →
// denied by config → no record (pending while the gate applies to a
// discovered tool, else ready) → blocked by user → gate off → changed →
// pending → ready.
func (s *Server) Dispatch(name string, configDenied bool) DispatchClass {
	if s.Quarantined {
		return DispatchServerQuarantined
	}
	if !s.Enabled {
		return DispatchServerDisabled
	}
	if configDenied {
		return DispatchDeniedByConfig
	}
	t := s.Tools[name]
	if t == nil || t.Status == StatusNone {
		// The tool is in the inventory the moment it has a record in this
		// model; a name with no record was never discovered.
		return DispatchReady
	}
	if t.Disabled {
		return DispatchBlockedByUser
	}
	if !s.gateOn() {
		return DispatchReady
	}
	switch t.Status {
	case StatusChanged:
		return DispatchChanged
	case StatusPending:
		return DispatchPendingApproval
	}
	return DispatchReady
}

// Snapshot is the comparable projection of a model or implementation state.
// Adapters build one from the implementation; the test compares it with the
// model's. Only policy-visible facts are included.
type Snapshot struct {
	Quarantined bool `json:"quarantined"`
	Enabled     bool `json:"enabled"`
	HasBaseline bool `json:"has_baseline"`
	// BaselineDecided is the durable marker on its own (spec 117 FR-022/23):
	// HasBaseline alone would be satisfied by the approved records and could
	// hide a marker that was never written or was lost across a restart.
	BaselineDecided bool                     `json:"baseline_decided"`
	Tools           map[string]Status        `json:"tools"`
	ToolsDisabled   map[string]bool          `json:"tools_disabled,omitempty"`
	Dispatch        map[string]DispatchClass `json:"dispatch"`
	// Current and Approved are the contract Keys the tool's stored record
	// presents / has approved. Approved is empty unless the status is
	// approved or changed.
	Current  map[string]string `json:"current,omitempty"`
	Approved map[string]string `json:"approved,omitempty"`
}

// Snapshot projects the model state.
func (s *Server) Snapshot() Snapshot {
	snap := Snapshot{
		Quarantined:     s.Quarantined,
		Enabled:         s.Enabled,
		HasBaseline:     s.HasBaseline(),
		BaselineDecided: s.BaselineDecided,
		Tools:           map[string]Status{},
		ToolsDisabled:   map[string]bool{},
		Dispatch:        map[string]DispatchClass{},
		Current:         map[string]string{},
		Approved:        map[string]string{},
	}
	for name, t := range s.Tools {
		snap.Tools[name] = t.Status
		snap.Current[name] = t.Current.Key
		if t.Status == StatusApproved || t.Status == StatusChanged {
			snap.Approved[name] = t.Approved.Key
		}
		if t.Disabled {
			snap.ToolsDisabled[name] = true
		}
		snap.Dispatch[name] = s.Dispatch(name, false)
	}
	if len(snap.ToolsDisabled) == 0 {
		snap.ToolsDisabled = nil
	}
	return snap
}

// Diff returns a human-readable list of differences between the expected
// (model) and actual (implementation) snapshots, empty when they agree.
func Diff(expected, actual Snapshot) []string {
	var out []string
	if expected.Quarantined != actual.Quarantined {
		out = append(out, fmt.Sprintf("server.quarantined: expected %v, actual %v", expected.Quarantined, actual.Quarantined))
	}
	if expected.Enabled != actual.Enabled {
		out = append(out, fmt.Sprintf("server.enabled: expected %v, actual %v", expected.Enabled, actual.Enabled))
	}
	if expected.HasBaseline != actual.HasBaseline {
		out = append(out, fmt.Sprintf("server.has_baseline: expected %v, actual %v", expected.HasBaseline, actual.HasBaseline))
	}
	if expected.BaselineDecided != actual.BaselineDecided {
		out = append(out, fmt.Sprintf("server.baseline_decided (durable marker): expected %v, actual %v", expected.BaselineDecided, actual.BaselineDecided))
	}
	names := map[string]struct{}{}
	for n := range expected.Tools {
		names[n] = struct{}{}
	}
	for n := range actual.Tools {
		names[n] = struct{}{}
	}
	for _, n := range sortedKeys(names) {
		e, eok := expected.Tools[n]
		a, aok := actual.Tools[n]
		switch {
		case !eok:
			out = append(out, fmt.Sprintf("tool %q: unexpected record with status %q", n, a))
		case !aok:
			out = append(out, fmt.Sprintf("tool %q: expected status %q, no record", n, e))
		case e != a:
			out = append(out, fmt.Sprintf("tool %q: expected status %q, actual %q", n, e, a))
		}
		if expected.ToolsDisabled[n] != actual.ToolsDisabled[n] {
			out = append(out, fmt.Sprintf("tool %q: expected disabled=%v, actual %v", n, expected.ToolsDisabled[n], actual.ToolsDisabled[n]))
		}
		if ec, ac := expected.Current[n], actual.Current[n]; ec != ac {
			out = append(out, fmt.Sprintf("tool %q: expected current contract %q, actual %q", n, ec, ac))
		}
		if ea, aa := expected.Approved[n], actual.Approved[n]; ea != aa {
			out = append(out, fmt.Sprintf("tool %q: expected approved contract %q, actual %q", n, ea, aa))
		}
		if ed, ad := expected.Dispatch[n], actual.Dispatch[n]; ed != ad {
			out = append(out, fmt.Sprintf("tool %q: expected dispatch %q, actual %q", n, ed, ad))
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

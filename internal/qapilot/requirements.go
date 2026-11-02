package qapilot

// Requirement is one selected policy rule the pilot checks. Source cites the
// authoritative text (spec FR or doc section) the rule was encoded from, so a
// reviewer can audit the model against the policy without the implementation.
type Requirement struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Sources   []string `json:"sources"`
	Mandatory bool     `json:"mandatory"`
	// Note records an ambiguity or a reading the pilot had to choose.
	Note string `json:"note,omitempty"`
}

// Requirements is the pilot's bounded requirement inventory. Every mandatory
// requirement must have at least one passing check in the report.
var Requirements = []Requirement{
	{ID: "QP-01", Mandatory: true, Title: "A new server is quarantined on add unless global quarantine is off or its trust mode is auto; empty or unknown trust_mode is manual",
		Sources: []string{"specs/086-tpa-scanner-approval/spec.md FR-011", "specs/088-scanner-trust-ui/spec.md FR-002, FR-006", "docs/features/security-quarantine.md 'Trust modes'"}},
	{ID: "QP-02", Mandatory: true, Title: "A quarantined server's tools are never dispatched: call_tool is refused with the quarantine verdict and no upstream call is made",
		Sources: []string{"docs/features/security-quarantine.md 'What quarantine does'", "specs/098 FR-002 (shared classifier precedence)"}},
	{ID: "QP-03", Mandatory: true, Title: "Releasing a server (unquarantine) is baseline trust: pending records become approved; changed records stay changed",
		Sources: []string{"docs/features/security-quarantine.md 'Approving a server'", "specs/117-review-bound-approvals/spec.md A7"}},
	{ID: "QP-04", Mandatory: true, Title: "Security approval without force needs a scan report with no hard-tier finding; force bypasses both; a refused approval leaves the server quarantined",
		Sources: []string{"specs/109-ux-navigation-consistency/spec.md FR-022", "docs/features/security-quarantine.md 'Force approval'"},
		Note:    "Scan freshness is advisory on this path (A-5): only report presence and hard findings gate the unforced approval."},
	{ID: "QP-05", Mandatory: true, Title: "Agent tokens cannot approve, release, enable, patch or otherwise mutate a server or its tools: every denied REST route returns 403 and reaches no controller",
		Sources: []string{"docs/features/agent-tokens.md 'Operations denied to agent tokens'", "specs/105-agent-scope-hardening/spec.md", "issues #877/#878"}},
	{ID: "QP-06", Mandatory: true, Title: "The MCP surface cannot release a server: quarantine_security has no unquarantine operation and upstream_servers patch/update ignores quarantined:false on a quarantined server",
		Sources: []string{"docs/features/security-quarantine.md 'MCP surface'", "docs/features/quarantine-testing.md 'MCP patch cannot unquarantine'"}},
	{ID: "QP-07", Mandatory: true, Title: "A new tool on a quarantined manual-trust server, or one that scans dangerous on a quarantined scan-trust server, is pending and stays pending across reconnects; server quarantine refuses dispatch regardless",
		Sources: []string{"specs/041-quarantine-invariants/spec.md US1, FR-002", "docs/features/tool-quarantine.md 'Decision table'"}},
	{ID: "QP-08", Mandatory: true, Title: "A trusted server's first non-empty discovery is its baseline: every tool approved and the durable baseline decision recorded",
		Sources: []string{"specs/117-review-bound-approvals/spec.md FR-020..FR-022", "docs/features/security-quarantine.md 'Durable tool baseline' (MCP-2931)"}},
	{ID: "QP-09", Mandatory: true, Title: "After the baseline a new tool is pending in manual mode, scan-gated in scan mode (clean → approved, dangerous → pending) and approved in auto mode or with global quarantine off",
		Sources: []string{"specs/086-tpa-scanner-approval/spec.md FR-009, FR-013, FR-014", "docs/features/security-quarantine.md trust-mode table"},
		Note:    "Scan-mode approval of a new tool applies whether or not the server is quarantined; dispatch stays refused by the server-level verdict (reading chosen for S15, see pilot doc)."},
	{ID: "QP-10", Mandatory: true, Title: "A changed definition of an approved tool is held as changed in manual mode whatever its verdict; in scan mode it is approved on a clean verdict and held on a dangerous one; auto mode approves it",
		Sources: []string{"specs/041-quarantine-invariants/spec.md FR-001", "specs/119-scan-default-trust/spec.md FR-004..FR-006, FR-009", "docs/features/security-quarantine.md trust-mode table"},
		Note:    "Spec 119 (2026-10-22) reversed RT-02 for scan mode; manual keeps it. The structural gate in front of the scan verdict is QP-20; hint flips are outside the model (no scenario flips hints)."},
	{ID: "QP-11", Mandatory: true, Title: "A changed tool stays changed across reconnects and returns to approved only when the approved definition returns",
		Sources: []string{"specs/041-quarantine-invariants/spec.md US2, FR-001"}},
	{ID: "QP-12", Mandatory: true, Title: "Explicit operator approval (per tool or approve_all) approves the current definition; a later reversion to an older definition is a change",
		Sources: []string{"docs/features/tool-quarantine.md 'Approving tools'", "specs/041 FR-003"}},
	{ID: "QP-13", Mandatory: true, Title: "changed→approved and pending→approved are allowed only for the permitted transition reasons",
		Sources: []string{"specs/041-quarantine-invariants/spec.md FR-001..FR-003", "docs/features/quarantine-testing.md 'Invariant rules'"},
		Note:    "The docs table predates baseline_trust, auto_approve_changes and scan_approved; the pilot's lists include them per docs/features/security-quarantine.md (A-2)."},
	{ID: "QP-14", Mandatory: true, Title: "Dispatch classification follows the published precedence for every input combination",
		Sources: []string{"docs/features/tools-preflight.md 'Reasons'", "specs/098 FR-002", "specs/105-agent-scope-hardening/spec.md FR-009 (no record under an active gate is pending)"}},
	{ID: "QP-15", Mandatory: true, Title: "Server quarantine, enablement, tool approval records (incl. blocks) and the baseline decision survive a restart",
		Sources: []string{"specs/117-review-bound-approvals/spec.md 'what must survive restart'", "docs/features/security-quarantine.md 'config.db'"}},
	{ID: "QP-16", Mandatory: true, Title: "An approved, enabled tool on an enabled, released server is dispatched exactly once; pending, changed, blocked tools and disabled servers are refused with zero upstream calls",
		Sources: []string{"docs/features/tool-quarantine.md 'Callability'", "specs/105 FR-009"}},
	{ID: "QP-17", Mandatory: true, Title: "A review-bound approval whose expected hash no longer matches is refused (review_out_of_date) and writes nothing",
		Sources: []string{"specs/117-review-bound-approvals/spec.md FR-005..FR-009"}},
	{ID: "QP-18", Mandatory: true, Title: "An approval bound to a server incarnation that no longer exists is refused (server_replaced) and writes nothing",
		Sources: []string{"specs/116-config-commit-consistency/spec.md FR-014, FR-017"}},
	{ID: "QP-19", Mandatory: true, Title: "The fixture contracts carry the scan verdict the model assumes (benign → clean, poisoned → dangerous)",
		Sources: []string{"specs/086-tpa-scanner-approval/spec.md FR-012 (offline synchronous scan)"},
		Note:    "A fixture check, not a policy rule: it keeps the model's Verdict labels honest against the real scanner."},
	{ID: "QP-20", Mandatory: true, Title: "Under scan, a changed definition that widens the approved contract (new required parameter, type change, enum member added or enum dropped, loosened constraint, default/const added or changed, output schema change, any safety-hint change) is held whatever the scan verdict; a narrowing or neutral change with a clean verdict is approved",
		Sources: []string{"specs/120-scan-structural-gate/spec.md FR-002..FR-005, FR-009", "docs/features/security-quarantine.md 'Structural changes are held'"},
		Note:    "The model carries one widening shape (BenignWide: a new defaulted parameter); the per-keyword rules are pinned by TestContractDelta_SharedFixture in internal/runtime."},
}

// RequirementByID looks a requirement up.
func RequirementByID(id string) (Requirement, bool) {
	for _, r := range Requirements {
		if r.ID == id {
			return r, true
		}
	}
	return Requirement{}, false
}

// MandatoryCheckIDs lists every check the report requires beyond the
// scenarios: the decision-table cells and the standalone checks.
func MandatoryCheckIDs() []string {
	ids := []string{
		"fixture/verdicts",
		"admission/table",
		"authz/table",
		"invariant/table",
		"mcp/no-unquarantine-op",
		"mcp/patch-ignores-unquarantine",
		"dispatch/counting-upstream",
		"security-approve/table",
		"review-binding/stale-hash",
		"incarnation/server-replaced",
	}
	for _, c := range ClassifyCells() {
		ids = append(ids, c.ID)
	}
	return ids
}

package qapilot

// Decision tables the pilot checks exhaustively. Each is encoded from the
// policy sources named in requirements.go, never from the implementation.

// AdmissionQuarantined is the add-time quarantine default (spec 086 FR-011,
// spec 088 FR-006): a new server is quarantined unless global quarantine is
// off or its trust mode is auto. An unknown trust mode is manual (spec 088
// FR-002: empty or unrecognised resolves to manual).
func AdmissionQuarantined(globalQuarantine bool, trust TrustMode) bool {
	if !globalQuarantine {
		return false
	}
	return EffectiveTrust(trust) != TrustAuto
}

// EffectiveTrust resolves a raw trust_mode value: auto and scan are
// themselves, everything else (including empty and garbage) is manual.
func EffectiveTrust(raw TrustMode) TrustMode {
	switch raw {
	case TrustAuto, TrustScan:
		return raw
	default:
		return TrustManual
	}
}

// Caller is the authorization class of a request.
type Caller string

const (
	CallerAdmin Caller = "admin" // API key, OS-authenticated socket, tray
	CallerAgent Caller = "agent" // scoped agent token (mcp_agt_)
)

// AgentDeniedOps is the policy list of server/tool-mutating operations an
// agent token may never perform on any surface (docs/features/agent-tokens.md
// "Operations denied to agent tokens"; spec 105; issues #877/#878). The
// strings are the REST route names the pilot drives; they are compared by
// behaviour (403 + no controller call), never by matching the
// implementation's own identifier table.
var AgentDeniedOps = []string{
	"add", "remove", "update", "patch", "enable", "disable", "restart",
	"refresh", "discover_tools", "quarantine", "unquarantine",
	"add_from_registry", "login", "logout", "config_to_secret",
	"approve_tools", "block_tools", "config_write", "scan",
	"security_approve", "security_reject", "secret_write", "diagnostics_fix",
}

// Authorize is the policy's answer for one caller and one operation: an
// admin may do anything; an agent may do nothing on the denied list.
func Authorize(caller Caller, op string) bool {
	if caller == CallerAdmin {
		return true
	}
	for _, denied := range AgentDeniedOps {
		if denied == op {
			return false
		}
	}
	return true
}

// Transition reasons (spec 041 FR-001..FR-003; docs/features/quarantine-testing.md
// "Invariant rules"; docs/features/security-quarantine.md for the three
// reasons added since: baseline_trust (MCP-2931), auto_approve_changes
// (MCP-2931/2930) and scan_approved (spec 086 stage 2)).
var (
	// ChangedToApprovedReasons may clear a changed (rug-pull) record: the
	// operator, proof the content did not actually change, the operator's
	// standing opt-in to auto-approve changes, or — since spec 119 — a clean
	// offline scan under trust_mode scan. Under manual a clean verdict never
	// re-approves a changed contract (RT-02); that is a trust-mode rule the
	// gate applies, not a reason the table refuses.
	ChangedToApprovedReasons = []string{
		"hash_match", "description_revert", "formula_migration",
		"content_match", "description_match", "user_approve",
		"auto_approve_changes", "scan_approved",
	}
	// PendingToApprovedReasons may promote a never-reviewed record.
	PendingToApprovedReasons = []string{
		"user_approve", "auto_approve", "baseline_trust",
		"auto_approve_changes", "scan_approved",
	}
	// AllReasons is every reason the implementation can name, plus one
	// unknown value, so the decision table covers rejection too.
	AllReasons = []string{
		"hash_match", "description_revert", "formula_migration",
		"content_match", "description_match", "user_approve", "auto_approve",
		"baseline_trust", "auto_approve_changes", "scan_approved", "unknown",
	}
)

// TransitionAllowed is the policy's invariant: a transition INTO approved
// from changed or pending needs a permitted reason; every other transition
// (into pending or changed, approved→approved, from no record) is allowed.
func TransitionAllowed(oldStatus, newStatus Status, reason string) bool {
	if newStatus != StatusApproved {
		return true
	}
	switch oldStatus {
	case StatusChanged:
		return contains(ChangedToApprovedReasons, reason)
	case StatusPending:
		return contains(PendingToApprovedReasons, reason)
	}
	return true
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ClassifyCell is one row of the dispatch decision table.
type ClassifyCell struct {
	ID               string
	ServerFound      bool
	Quarantined      bool
	Enabled          bool
	TrustAuto        bool
	GlobalQuarantine bool
	ConfigDenied     bool
	Discovered       bool
	HasRecord        bool
	Status           Status
	Disabled         bool
}

// Expected is the policy classification for the cell (same precedence as
// Server.Dispatch, extended with the "not configured" and "no record" inputs
// the classifier takes).
func (c ClassifyCell) Expected() DispatchClass {
	if !c.ServerFound {
		return DispatchServerNotFound
	}
	if c.Quarantined {
		return DispatchServerQuarantined
	}
	if !c.Enabled {
		return DispatchServerDisabled
	}
	if c.ConfigDenied {
		return DispatchDeniedByConfig
	}
	gate := c.GlobalQuarantine && !c.TrustAuto
	if !c.HasRecord {
		if c.Discovered && gate {
			return DispatchPendingApproval
		}
		return DispatchReady
	}
	if c.Disabled {
		return DispatchBlockedByUser
	}
	if !gate {
		return DispatchReady
	}
	switch c.Status {
	case StatusChanged:
		return DispatchChanged
	case StatusPending:
		return DispatchPendingApproval
	}
	return DispatchReady
}

// ClassifyCells enumerates every combination of the classifier's inputs:
// 2 (found) × 2 × 2 × 2 × 2 × 2 (denied) × 2 (discovered) × (1 no-record +
// 3 statuses × 2 disabled) = 896 cells. The implementation must agree on all
// of them; the ledger records each cell as a check so the report can prove
// the whole table ran.
func ClassifyCells() []ClassifyCell {
	var cells []ClassifyCell
	bools := []bool{false, true}
	for _, found := range bools {
		for _, q := range bools {
			for _, en := range bools {
				for _, auto := range bools {
					for _, global := range bools {
						for _, denied := range bools {
							for _, disc := range bools {
								base := ClassifyCell{ServerFound: found, Quarantined: q, Enabled: en, TrustAuto: auto, GlobalQuarantine: global, ConfigDenied: denied, Discovered: disc}
								c := base
								c.ID = cellID(c)
								cells = append(cells, c)
								for _, st := range []Status{StatusPending, StatusChanged, StatusApproved} {
									for _, dis := range bools {
										c := base
										c.HasRecord, c.Status, c.Disabled = true, st, dis
										c.ID = cellID(c)
										cells = append(cells, c)
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return cells
}

func cellID(c ClassifyCell) string {
	b := func(v bool) byte {
		if v {
			return '1'
		}
		return '0'
	}
	status := "none"
	if c.HasRecord {
		status = string(c.Status)
		if c.Disabled {
			status += "+disabled"
		}
	}
	return "classify/f" + string(b(c.ServerFound)) + "q" + string(b(c.Quarantined)) + "e" + string(b(c.Enabled)) +
		"a" + string(b(c.TrustAuto)) + "g" + string(b(c.GlobalQuarantine)) + "d" + string(b(c.ConfigDenied)) +
		"v" + string(b(c.Discovered)) + "/" + status
}

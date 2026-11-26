package runtime

// admin_auditor.go — Spec 133 plan D-2: the seam through which a committed
// administrative effect reaches the audit stream as an `admin_action` line.
//
// The runtime knows WHAT committed (the action, the target, the changes, from
// the storage transaction's own report); the implementation in
// internal/server knows WHO asked and through which door (the caller derived
// from the trusted request context exactly as for `authz` lines, the ingress
// tag, the request id) and owns the sink. Lines are written synchronously,
// never through the event bus (FR-018), and a failed write never refuses the
// change (FR-017).

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
)

// AdminAuditor writes admin_action lines. A nil auditor means audit is off.
type AdminAuditor interface {
	AdminAction(ctx context.Context, rec AdminRecord)
}

// AdminRecord is one committed effect.
type AdminRecord struct {
	// At is the commit time of the effect.
	At      time.Time
	Action  string
	Target  audit.AdminTarget
	Changes []audit.Change
	// Decider, when set, replaces only caller/trigger/policy (contract rule
	// 3): an effect the proxy decided (policy, admission, startup) or one it
	// observed (a config file edit). Source, origin, request id and client
	// still come from the context when there is a request.
	Decider *Decider
}

// Decider kinds.
const (
	DeciderInternal   = "internal"
	DeciderNone       = "none"
	DeciderCLIOffline = "cli_offline"
)

// Decider is an explicit attribution override.
type Decider struct {
	Kind    string // internal|none|cli_offline
	Trigger string
	Policy  string
}

// adminAuditHolder lets services created before the server installs the sink
// read the current auditor at the time of the effect.
type adminAuditHolder struct {
	v atomic.Pointer[adminAuditorBox]
}

type adminAuditorBox struct{ a AdminAuditor }

func (h *adminAuditHolder) set(a AdminAuditor) {
	if h == nil {
		return
	}
	if a == nil {
		h.v.Store(nil)
		return
	}
	h.v.Store(&adminAuditorBox{a: a})
}

// record hands rec to the installed auditor, if any. Safe on a nil holder.
func (h *adminAuditHolder) record(ctx context.Context, rec AdminRecord) {
	if h == nil {
		return
	}
	box := h.v.Load()
	if box == nil || box.a == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	box.a.AdminAction(ctx, rec)
}

// SetAdminAuditor installs the admin_action writer (internal/server, when it
// receives the audit sink). nil turns it off.
func (r *Runtime) SetAdminAuditor(a AdminAuditor) {
	if r == nil {
		return
	}
	r.adminAudit.set(a)
}

// agentTokenTarget is the audit target of an ownerless or tenant agent token.
func agentTokenTarget(name, ref, ownerUserID string) audit.AdminTarget {
	return audit.AgentTokenTarget(name, ref, ownerUserID)
}

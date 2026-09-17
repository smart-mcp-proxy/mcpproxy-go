// Package audit builds and writes the Spec 107 audit trail: one JSON line
// per pre-dispatch authorization decision (authz), one per completed tool
// dispatch (tool_call), and one per terminal login/logout attempt
// (auth_event). See specs/107-server-edition-sso-hardening/contracts/
// audit-line.schema.json for the binding wire schema and
// audit-line-events.md for the event vocabulary.
//
// This package has no dependency on internal/server or internal/serveredition
// (dependency direction: audit is a leaf consumed by both, never the other
// way around) and depends only on internal/security (StripInternalArgs) plus
// the standard library and gopkg.in/natefinch/lumberjack.v2 (already a module
// dependency via internal/logs).
package audit

import "time"

// Attempt is the immutable per-dispatch-attempt record installed in the
// request context before the first authorization gate runs (data-model.md
// §5). It never holds arguments, responses, error text or `_auth_*` values:
// there is no field through which they could arrive, which is the
// structural half of FR-015's "never raw args/response/error" invariant.
type Attempt struct {
	RequestID          string
	TransportRequestID string
	ParentID           string
	SessionID          string
	WorkSessionID      string
	Server             string // canonical server name (Spec 105 FR-009)
	Tool               string // raw upstream tool name
	Operation          string // read|write|destructive|unknown
	Surface            string // call_tool_read|call_tool_write|call_tool_destructive|direct|code_execution|rest
	Source             string // mcp|api|internal — from the mount point
	Origin             string // local|socket|remote(reserved)
	ClientName         string
	ClientVersion      string
	ClientIP           string
	Profile            string
	ProfilePin         string
	ArgsSHA256         string // RFC 8785 canonical hash over StripInternalArgs(args), pre-masking
	ArgsBytes          int
	StartedAt          time.Time
}

// Caller is the audit line's `caller` object: the identity derived from
// auth.AuthContext plus the credential kind (contracts/audit-line-events.md
// "caller.kind derivation"). Fields not applicable to a given kind must be
// left zero — the per-kind rules are enforced by the line builders, and the
// schema's identity `allOf` blocks are the binding source of truth.
type Caller struct {
	Kind        string // api_key|socket|stdio|anonymous|agent_token|session_user|session_admin|internal
	UserID      string
	UserEmail   string
	EmailHash   string // auth_event only, verified email, no user record yet
	Role        string // admin|user
	Provider    string // google|github|microsoft|oidc
	TokenName   string
	TokenPrefix string
	ProfilePin  string
}

// Client is the audit line's optional `client` object: caller-asserted
// identification, always untrusted.
type Client struct {
	Name    string
	Version string
	IP      string
}

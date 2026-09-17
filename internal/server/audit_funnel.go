package server

// audit_funnel.go — Spec 107 PR-D (T103/T104): the audit line at the two
// activity funnels.
//
// Every dispatch path installs an immutable audit.Attempt on the request
// context before its first authorization gate (installAuditAttempt). The
// two activity funnels — emitActivityPolicyDecision and
// emitActivityToolCallCompleted — plus emitActivityToolCallStarted read it
// back and write the `authz` / `tool_call` lines synchronously through the
// server's audit.Sink (research.md D6: never the lossy event bus, never a
// zap core). The attempt is paired with a small mutable companion
// (auditDispatch) that pins the count invariants of SC-003 structurally:
// exactly one `authz` line per attempt (the first decision wins; a
// post-allow refusal such as ErrConnectionGenerationChanged is a tool_call
// error, never a second authz) and exactly one `tool_call` line per
// `authz allow`.
//
// A nil sink is the personal-edition default: installAuditAttempt returns
// the context unchanged and every funnel is a no-op, so no hashing, no
// allocation and no lock are paid on the hot path (SC-009).

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/jsruntime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

// Audit surfaces (schema `surface` enum for authz/tool_call).
const (
	auditSurfaceDirect        = "direct"
	auditSurfaceCodeExecution = "code_execution"
	auditSurfaceREST          = "rest"
)

// auditDispatch is the per-attempt mutable companion of the immutable
// audit.Attempt: it remembers which lines were already written so the
// funnels can enforce "one authz, one tool_call" without the call sites
// having to know about each other, and carries the typed error a dispatch
// path noted for error_class derivation (the completion funnel only sees
// the prose message, FR-015).
type auditDispatch struct {
	attempt audit.Attempt
	caller  audit.Caller

	mu              sync.Mutex
	authzWritten    bool
	toolCallWritten bool
	errClass        audit.ErrorClass
}

type auditDispatchKeyType struct{}

var auditDispatchKey auditDispatchKeyType

func auditDispatchFromContext(ctx context.Context) *auditDispatch {
	if ctx == nil {
		return nil
	}
	d, _ := ctx.Value(auditDispatchKey).(*auditDispatch)
	return d
}

// auditAttemptSpec is what a dispatch path knows about the call before its
// first gate. Args are hashed here (RFC 8785 over StripInternalArgs(args))
// and never stored.
type auditAttemptSpec struct {
	RequestID     string
	ParentID      string
	SessionID     string
	Server        string
	Tool          string
	Operation     string // read|write|destructive|unknown
	Surface       string // call_tool_*|direct|code_execution|rest
	ClientName    string
	ClientVersion string
	Profile       string
	Args          map[string]interface{}
	// Caller overrides the context-derived caller. The code_execution
	// wrapper captures the script's caller once and hands it to every
	// nested attempt, whose own context is tagged SourceInternal (Spec 093
	// FR-012) and would otherwise derive caller.kind: internal.
	Caller *audit.Caller
}

// installAuditAttempt builds the attempt record for one dispatch and
// installs it on ctx. It is the ONLY constructor of an audit.Attempt in this
// package. With no sink configured it returns ctx unchanged.
func (p *MCPProxyServer) installAuditAttempt(ctx context.Context, spec auditAttemptSpec) context.Context {
	if p == nil || p.auditSink == nil {
		return ctx
	}
	hash, argsBytes, err := audit.HashArgs(spec.Args)
	if err != nil {
		// A map that cannot be canonicalised (a non-JSON value smuggled in
		// through an in-process caller) still gets a line: the hash of the
		// empty object is unambiguous and the activity record keeps the
		// arguments. Logged once per occurrence at DEBUG — never the args.
		p.logger.Debug("audit: arguments not canonicalisable, hashing empty object", zap.Error(err))
		hash, argsBytes, _ = audit.HashArgs(nil)
	}
	server, tool := spec.Server, spec.Tool
	if server == "" {
		server = "unknown"
	}
	if tool == "" {
		tool = "unknown"
	}
	op := spec.Operation
	switch op {
	case contracts.OperationTypeRead, contracts.OperationTypeWrite, contracts.OperationTypeDestructive:
	default:
		op = "unknown"
	}

	var caller audit.Caller
	if spec.Caller != nil {
		caller = *spec.Caller
	} else {
		caller = auditCallerFromContext(ctx)
	}

	var clientIP string
	if meta, ok := reqcontext.GetRequestMeta(ctx); ok {
		clientIP = meta.ClientIP
	}

	// work_session_id (round-1 cross-review finding, PR-D): the attempt is
	// the only place this is stamped, never re-resolved by a gate or the
	// completion path. p.sessionStore is nil-safe for an unresolved or
	// unknown session id (returns "").
	var workSessionID string
	if p != nil && p.sessionStore != nil {
		workSessionID = p.sessionStore.WorkSessionID(spec.SessionID)
	}

	d := &auditDispatch{
		attempt: audit.Attempt{
			RequestID:          spec.RequestID,
			TransportRequestID: auditTransportRequestID(ctx),
			ParentID:           spec.ParentID,
			SessionID:          spec.SessionID,
			WorkSessionID:      workSessionID,
			Server:             server,
			Tool:               tool,
			Operation:          op,
			Surface:            spec.Surface,
			Source:             auditSourceFromContext(ctx),
			Origin:             auditOriginFromContext(ctx),
			ClientName:         spec.ClientName,
			ClientVersion:      spec.ClientVersion,
			ClientIP:           clientIP,
			Profile:            spec.Profile,
			ProfilePin:         caller.ProfilePin,
			ArgsSHA256:         hash,
			ArgsBytes:          argsBytes,
			StartedAt:          time.Now(),
		},
		caller: caller,
	}
	return context.WithValue(ctx, auditDispatchKey, d)
}

// auditTransportRequestID is the X-Request-Id the REST middleware installed
// (REST only; the /mcp handlers carry none — server.go's MCP chain has no
// RequestIDMiddleware).
func auditTransportRequestID(ctx context.Context) string {
	if meta, ok := reqcontext.GetRequestMeta(ctx); ok && meta.Mount == reqcontext.MountAPI {
		return reqcontext.GetRequestID(ctx)
	}
	return ""
}

// auditSourceFromContext derives the line's `source` from the mount point
// (reqcontext.RequestMeta, T050) — never from reqcontext.GetRequestSource,
// which the REST layer rewrites from the caller-controlled X-MCPProxy-Client
// header. The one exception is SourceInternal: it is set by the proxy itself
// (the code_execution bridge, Spec 093 FR-012), not by a header.
func auditSourceFromContext(ctx context.Context) string {
	if reqcontext.GetRequestSource(ctx) == reqcontext.SourceInternal {
		return "internal"
	}
	if meta, ok := reqcontext.GetRequestMeta(ctx); ok && meta.Mount == reqcontext.MountAPI {
		return "api"
	}
	// /mcp* mounts and the native stdio transport (no HTTP mount at all)
	// are both the MCP surface.
	return "mcp"
}

// auditOriginFromContext maps the listener-derived connection source onto
// the schema's `origin`: tcp→local, tray→socket, stdio→local. `remote` is
// reserved (Spec 089 FR-010) and never emitted here.
func auditOriginFromContext(ctx context.Context) string {
	if transport.GetConnectionSource(ctx) == transport.ConnectionSourceTray {
		return "socket"
	}
	return "local"
}

// auditCallerFromContext derives the audit line's caller from the
// AuthContext, its CredentialKind and Anonymous bit, and the connection
// source (contracts/audit-line-events.md "caller.kind derivation").
//
// A proxy-originated request (reqcontext.SourceInternal) is `internal`
// regardless of any AuthContext also present: the sandbox copies the
// caller's context for policy checks, but a sub-call's own line must not
// attribute the proxy's dispatch to that caller (the code_execution
// wrapper passes the script's caller explicitly instead). A context with
// no AuthContext at all is `anonymous` — nil is unprivileged, never
// admin-by-absence (auth.AuthContext.CanRevealSecrets' rule).
func auditCallerFromContext(ctx context.Context) audit.Caller {
	if reqcontext.GetRequestSource(ctx) == reqcontext.SourceInternal {
		return audit.Caller{Kind: "internal"}
	}
	ac := auth.AuthContextFromContext(ctx)
	if ac == nil {
		return audit.Caller{Kind: "anonymous"}
	}
	switch ac.Type {
	case auth.AuthTypeAgent:
		c := audit.Caller{
			Kind:        "agent_token",
			TokenName:   ac.AgentName,
			TokenPrefix: ac.TokenPrefix,
			ProfilePin:  ac.ProfilePin,
		}
		// Owner identity is all-or-none (schema: user_id ⇒ user_email, role,
		// provider). An ownerless token carries none of the four.
		if ac.UserID != "" {
			c.UserID = ac.UserID
			c.UserEmail = ac.Email
			c.Role = ac.Role
			c.Provider = ac.Provider
		}
		return c
	case auth.AuthTypeAdminUser:
		return audit.Caller{
			Kind:      "session_admin",
			UserID:    ac.UserID,
			UserEmail: ac.Email,
			Role:      "admin",
			Provider:  ac.Provider,
		}
	case auth.AuthTypeUser:
		// No dispatch door admits a session_user (FR-002/FR-003); the kind
		// exists for auth_event lines only. Should one ever reach a funnel it
		// is recorded as what it is rather than mislabelled.
		return audit.Caller{
			Kind:      "session_user",
			UserID:    ac.UserID,
			UserEmail: ac.Email,
			Role:      "user",
			Provider:  ac.Provider,
		}
	}
	// AuthTypeAdmin: the impersonal kinds, told apart by the anonymous bit
	// and the connection source.
	if ac.Anonymous {
		return audit.Caller{Kind: "anonymous"}
	}
	switch transport.GetConnectionSource(ctx) {
	case transport.ConnectionSourceTray:
		return audit.Caller{Kind: "socket"}
	case transport.ConnectionSourceStdio:
		return audit.Caller{Kind: "stdio"}
	}
	return audit.Caller{Kind: "api_key"}
}

// auditReasonFromBlockKey maps the closed telemetry.BlockReason* enum a gate
// declared onto the authz `reason` vocabulary (identical members minus the
// two post-dispatch keys, which are tool_call reasons) and reports whether
// the refusal was non-disclosing to the caller (Spec 105 FR-010 shapes:
// scope refusals reveal nothing about whether the server exists).
func auditReasonFromBlockKey(reasonKey string) (reason string, disclosed bool) {
	switch reasonKey {
	case telemetry.BlockReasonTokenScope, telemetry.BlockReasonProfileScope:
		return reasonKey, false
	case telemetry.BlockReasonIntentInvalid, telemetry.BlockReasonIntentRejected,
		telemetry.BlockReasonTokenPermission, telemetry.BlockReasonServerQuarantined,
		telemetry.BlockReasonToolPendingApproval, telemetry.BlockReasonToolChanged,
		telemetry.BlockReasonToolNotCallable:
		return reasonKey, true
	default:
		return telemetry.BlockReasonOther, true
	}
}

// isPostDispatchBlockKey reports whether a "blocked" policy decision is one
// of the two post-dispatch output blocks, which are `tool_call
// outcome:blocked` lines and never an authz line (FR-043(j)).
func isPostDispatchBlockKey(reasonKey string) bool {
	return reasonKey == telemetry.BlockReasonOutputSanitisation || reasonKey == telemetry.BlockReasonOutputSchema
}

// auditWrite serialises and writes one line; write failures are the sink's
// business (counter + rate-limited log) and never surface to the caller.
func (p *MCPProxyServer) auditWrite(line audit.Line, err error) {
	if err != nil {
		p.logger.Warn("audit: line rejected by builder", zap.Error(err))
		return
	}
	raw, err := line.JSON()
	if err != nil {
		p.logger.Warn("audit: line not serialisable", zap.Error(err))
		return
	}
	_ = p.auditSink.Write(raw)
}

// auditAuthz writes the attempt's ONE authz line. decision is allow|deny;
// reasonKey is the gate's telemetry.BlockReason* (ignored for allow). A
// second call for the same attempt is a no-op.
func (p *MCPProxyServer) auditAuthz(ctx context.Context, decision, reasonKey string) {
	d := auditDispatchFromContext(ctx)
	if d == nil || p.auditSink == nil {
		return
	}
	d.mu.Lock()
	if d.authzWritten {
		d.mu.Unlock()
		return
	}
	d.authzWritten = true
	d.mu.Unlock()

	in := audit.AuthzInput{
		Ts:       time.Now(),
		Attempt:  d.attempt,
		Caller:   d.caller,
		Decision: decision,
		Reason:   "none",
	}
	if decision == "deny" {
		reason, disclosed := auditReasonFromBlockKey(reasonKey)
		in.Reason = reason
		in.Disclosed = &disclosed
	}
	p.auditWrite(audit.NewAuthz(in))
}

// auditToolCall writes the attempt's ONE tool_call line, writing the paired
// `authz allow` first if no decision was recorded yet (every completion is
// preceded by a Started emission on the dispatch paths, so this is a
// defensive pairing, not the normal route). A second call for the same
// attempt is a no-op.
func (p *MCPProxyServer) auditToolCall(ctx context.Context, outcome, reason string, errClass audit.ErrorClass, durationMs int64, requestBytes, responseBytes *int) {
	d := auditDispatchFromContext(ctx)
	if d == nil || p.auditSink == nil {
		return
	}
	p.auditAuthz(ctx, "allow", "")

	d.mu.Lock()
	if d.toolCallWritten {
		d.mu.Unlock()
		return
	}
	d.toolCallWritten = true
	noted := d.errClass
	d.mu.Unlock()

	if durationMs < 0 {
		durationMs = 0
	}
	in := audit.ToolCallInput{
		Ts:            time.Now(),
		Attempt:       d.attempt,
		Caller:        d.caller,
		Outcome:       outcome,
		Reason:        reason,
		DurationMs:    int(durationMs),
		RequestBytes:  requestBytes,
		ResponseBytes: responseBytes,
	}
	if outcome == "error" {
		switch {
		case errClass != "":
			in.ErrorClass = string(errClass)
		case noted != "":
			in.ErrorClass = string(noted)
		default:
			// The upstream answered (a well-formed isError result or a
			// transport error the path did not classify): an upstream error.
			in.ErrorClass = string(audit.ErrorClassUpstreamError)
		}
	}
	p.auditWrite(audit.NewToolCall(in))
}

// auditNoteError records the typed error a dispatch path is about to report
// through emitActivityToolCallCompleted, so the completion funnel — which
// receives only the prose message — can derive the bounded error_class
// through audit.ErrorClassOf. nil-safe; a no-op without an attempt.
func auditNoteError(ctx context.Context, err error) {
	auditNoteErrorClass(ctx, audit.ErrorClassOf(err))
}

// auditNoteErrorClass is auditNoteError for paths that know the class
// without holding a typed error (a server the proxy has no client for is
// upstream_unavailable; a rejected argument set is validation).
func auditNoteErrorClass(ctx context.Context, class audit.ErrorClass) {
	d := auditDispatchFromContext(ctx)
	if d == nil {
		return
	}
	d.mu.Lock()
	d.errClass = class
	d.mu.Unlock()
}

// auditSetOperation corrects the attempt's `operation` once the target
// tool's actual annotation-derived tier is known (round-2 cross-review
// finding, PR-D): installAuditAttempt runs before the identity gate that
// resolves the tool's annotations, so it can only stamp the CALLER-chosen
// door (contracts.ToolVariantToOperationType[toolVariant]) — for a scoped
// caller that is explicitly NOT what gets authorized (mcp.go:
// "the variant is the CALLER's choice ... Authorize against the TARGET
// tool's annotation-derived tier"), so a call_tool_read against a write
// tool would otherwise record operation:"read" on both the authz and
// tool_call lines despite being authorized, and refused, against write. A
// no-op without an attempt (nil sink / already-dispatched line: this is
// always called before the first line of an attempt is written).
func auditSetOperation(ctx context.Context, op string) {
	d := auditDispatchFromContext(ctx)
	if d == nil || op == "" {
		return
	}
	d.mu.Lock()
	d.attempt.Operation = op
	d.mu.Unlock()
}

// auditToolCallShed writes the tool_call line for a concurrency-limiter shed
// (`outcome:rejected`, reason limiter_queue_full|limiter_queue_timeout). The
// shed happens inside the managed client after every gate (research.md D6),
// so it is the tool_call half of the pair, never a second authz. The
// dispatch paths do not emit a completion record for a shed (the limiter's
// own seam wrote the activity row), so this is called explicitly there.
func (p *MCPProxyServer) auditToolCallShed(ctx context.Context, limitErr *limiter.LimitError, durationMs int64) {
	if limitErr == nil {
		return
	}
	reason := "limiter_queue_full"
	if limitErr.Reason == limiter.ReasonQueueTimeout {
		reason = "limiter_queue_timeout"
	}
	p.auditToolCall(ctx, "rejected", reason, "", durationMs, nil, nil)
}

// auditDurationMs is the elapsed time since the attempt was installed —
// used by the post-dispatch block path, which has no dispatch duration of
// its own in hand at the funnel.
func auditDurationMs(ctx context.Context) int64 {
	d := auditDispatchFromContext(ctx)
	if d == nil || d.attempt.StartedAt.IsZero() {
		return 0
	}
	return time.Since(d.attempt.StartedAt).Milliseconds()
}

// ---------------------------------------------------------------------------
// Nested (code_execution) refusals — T104
// ---------------------------------------------------------------------------

// nestedAuthzObserver is the jsruntime.AuthzObserver the code_execution
// wrapper installs: every scope/permission refusal jsruntime's
// checkDispatchGates decides — which never reaches the bridge, so no
// completion emitter ever sees it — becomes one `authz deny` line carrying
// the script's caller, surface code_execution and the wrapper's request id
// as parent_id. It installs a fresh attempt per report, so the dedup the
// funnels enforce per attempt applies per refusal.
type nestedAuthzObserver struct {
	proxy         *MCPProxyServer
	parentCtx     context.Context
	caller        audit.Caller
	sessionID     string
	clientName    string
	clientVersion string
	profile       string
}

func (o *nestedAuthzObserver) ObserveAuthzGate(report jsruntime.AuthzGateReport) {
	if o == nil || o.proxy == nil || o.proxy.auditSink == nil || !report.Denied {
		return
	}
	reasonKey := telemetry.BlockReasonTokenScope
	switch report.Code {
	case jsruntime.ErrorCodeServerNotAllowed:
		// The sandbox's allow-list is the intersection of the token's scope
		// and the active profile (applyProfileScopeToExecution); a profile
		// that narrowed it is the profile's refusal.
		if o.profile != "" {
			reasonKey = telemetry.BlockReasonProfileScope
		}
	case jsruntime.ErrorCodeAccessDenied:
		reasonKey = telemetry.BlockReasonTokenScope
	case jsruntime.ErrorCodePermissionDenied:
		reasonKey = telemetry.BlockReasonTokenPermission
		if report.RequiredPerm == jsruntime.PermissionTierUnresolved {
			// An identity the lookup could not resolve: the refusal is about
			// the tool, not the caller's grant (same bucket as
			// handleCallToolVariant's unresolved-identity refusal).
			reasonKey = telemetry.BlockReasonToolNotCallable
		}
	}
	operation := report.RequiredPerm
	if operation == jsruntime.PermissionTierUnresolved {
		operation = ""
	}
	caller := o.caller
	ctx := o.parentCtx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = reqcontext.WithRequestSource(ctx, reqcontext.SourceInternal)
	ctx = o.proxy.installAuditAttempt(ctx, auditAttemptSpec{
		RequestID:     mintCorrelationID(report.ServerName, report.ToolName),
		ParentID:      report.ParentID,
		SessionID:     o.sessionID,
		Server:        report.ServerName,
		Tool:          report.ToolName,
		Operation:     operation,
		Surface:       auditSurfaceCodeExecution,
		ClientName:    o.clientName,
		ClientVersion: o.clientVersion,
		Profile:       o.profile,
		Args:          report.Arguments,
		Caller:        &caller,
	})
	o.proxy.auditAuthz(ctx, "deny", reasonKey)
}

package server

// admin_auditor.go — Spec 133: the sink-backed runtime.AdminAuditor. It
// turns one committed administrative effect (runtime.AdminRecord) into one
// `admin_action` line and writes it synchronously through the audit sink,
// never through the event bus (FR-018). A failed build or write never
// refuses the change (FR-017): the builder rejection is counted
// (audit.BuildFailures) and logged, the write failure is the sink's.
//
// Attribution (contract §Caller, rule 3):
//   - caller: the record's Decider when set; otherwise the request's
//     AuthContext through the same credential mapping as authz lines,
//     without the dispatch-only SourceInternal shortcut. An admin record
//     with neither is a bug: it is counted and not written, never
//     attributed to `anonymous` by default.
//   - source/origin/client: the ingress tag (reqcontext.RequestMeta, the
//     listener's connection source), never a header-derived actor.
//   - request_id: the id already on the context (the one the activity row
//     carries); never on a none or cli_offline line.

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

type sinkAdminAuditor struct {
	sink   audit.Sink
	logger *zap.Logger
}

// adminClientKey carries the MCP clientInfo captured when an admin tool call
// was admitted, so a session evicted before the commit does not lose it.
type adminClientKey struct{}

type adminClient struct{ name, version string }

func withAdminClient(ctx context.Context, name, version string) context.Context {
	if name == "" && version == "" {
		return ctx
	}
	return context.WithValue(ctx, adminClientKey{}, adminClient{name: name, version: version})
}

// adminProfileKey carries the active profile an admin tool call was admitted
// under, captured at the ingress before the mutation (a later set_profile on
// the same session must not re-attribute it).
type adminProfileKey struct{}

func withAdminProfile(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, adminProfileKey{}, name)
}

func adminProfileFromContext(ctx context.Context) string {
	s, _ := ctx.Value(adminProfileKey{}).(string)
	return s
}

// newSinkAdminAuditor returns nil when there is no sink (audit off).
func newSinkAdminAuditor(sink audit.Sink, logger *zap.Logger) *sinkAdminAuditor {
	if sink == nil {
		return nil
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &sinkAdminAuditor{sink: sink, logger: logger}
}

// adminLineInput derives the request-side keys of an admin_action line.
// ok=false means the record has no attributable caller (a bug).
func adminLineInput(ctx context.Context, rec runtime.AdminRecord) (audit.AdminActionInput, bool) {
	in := audit.AdminActionInput{
		Ts:      rec.At,
		Action:  rec.Action,
		Target:  rec.Target,
		Changes: rec.Changes,
		Origin:  auditOriginFromContext(ctx),
	}
	if in.Ts.IsZero() {
		in.Ts = time.Now()
	}
	meta, hasMeta := reqcontext.GetRequestMeta(ctx)
	stdio := transport.GetConnectionSource(ctx) == transport.ConnectionSourceStdio
	hasRequest := hasMeta || stdio
	switch {
	case hasMeta && meta.Mount == reqcontext.MountAPI:
		in.Source = "api"
		in.ClientIP = meta.ClientIP
		in.ClientName = meta.ClientName
		in.ClientSurface = meta.Surface
	case hasMeta || stdio:
		// /mcp* mounts and the native stdio transport are the MCP surface.
		in.Source = "mcp"
		in.ClientIP = meta.ClientIP
		in.ClientSurface = "mcp"
	default:
		in.Source = "internal"
	}

	if d := rec.Decider; d != nil {
		in.Caller = audit.Caller{Kind: d.Kind}
		in.Trigger, in.Policy = d.Trigger, d.Policy
		switch d.Kind {
		case runtime.DeciderNone, runtime.DeciderCLIOffline:
			// An observed edit or an offline command is not caused by a
			// request: no request metadata at all.
			in.Source, in.Origin = "internal", "local"
			in.ClientIP, in.ClientName, in.ClientSurface = "", "", ""
			return in, true
		}
		if hasRequest {
			in.RequestID = reqcontext.GetRequestID(ctx)
		}
		return in, true
	}

	ac := auth.AuthContextFromContext(ctx)
	if ac == nil {
		return in, false
	}
	in.Caller = auditCallerFromAuthContext(ctx, ac)
	in.RequestID = reqcontext.GetRequestID(ctx)
	return in, true
}

func (a *sinkAdminAuditor) AdminAction(ctx context.Context, rec runtime.AdminRecord) {
	in, ok := adminLineInput(ctx, rec)
	if ok && in.Source == "mcp" && in.ClientName == "" {
		if ci, has := ctx.Value(adminClientKey{}).(adminClient); has {
			in.ClientName, in.ClientVersion = ci.name, ci.version
		}
	}
	if ok && rec.Decider == nil {
		in.Profile = adminProfileFromContext(ctx)
	}
	if !ok {
		audit.NoteBuildFailure()
		a.logger.Warn("audit: admin_action record has no attributable caller; not written",
			zap.String("action", rec.Action))
		return
	}
	line, err := audit.NewAdminAction(in)
	if err != nil {
		audit.NoteBuildFailure()
		a.logger.Warn("audit: admin_action line rejected by builder", zap.String("action", rec.Action), zap.Error(err))
		return
	}
	raw, err := line.JSON()
	if err != nil {
		audit.NoteBuildFailure()
		a.logger.Warn("audit: admin_action line not serialisable", zap.String("action", rec.Action), zap.Error(err))
		return
	}
	_ = a.sink.Write(raw)
}

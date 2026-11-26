package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

func tokenRecord() runtime.AdminRecord {
	return runtime.AdminRecord{
		At: time.Now(), Action: audit.ActionTokenDelete,
		Target: audit.AgentTokenTarget("ci", auth.NewAuditRef(auth.AuditRefKindAgentToken), ""),
	}
}

func TestAdminLineInput_Attribution(t *testing.T) {
	admin := auth.WithAuthContext(context.Background(), auth.AdminContext())

	// CLI over the tray socket: caller socket, origin socket, source api.
	ctx := transport.TagConnectionContext(admin, transport.ConnectionSourceTray)
	ctx = reqcontext.WithRequestMeta(ctx, reqcontext.RequestMeta{Mount: reqcontext.MountAPI, Surface: "cli", ClientName: "cli/1"})
	ctx = reqcontext.WithRequestID(ctx, "rid-1")
	in, ok := adminLineInput(ctx, tokenRecord())
	require.True(t, ok)
	require.Equal(t, "socket", in.Caller.Kind)
	require.Equal(t, "socket", in.Origin)
	require.Equal(t, "api", in.Source)
	require.Equal(t, "cli", in.ClientSurface)
	require.Equal(t, "rid-1", in.RequestID)

	// Native stdio: caller stdio, source mcp, surface mcp.
	ctx = transport.TagConnectionContext(admin, transport.ConnectionSourceStdio)
	in, ok = adminLineInput(ctx, tokenRecord())
	require.True(t, ok)
	require.Equal(t, "stdio", in.Caller.Kind)
	require.Equal(t, "mcp", in.Source)
	require.Equal(t, "mcp", in.ClientSurface)

	// A REST built-in dispatch keeps the REST surface even though the
	// dispatch path marks the context SourceInternal for authz lines.
	ctx = reqcontext.WithRequestMeta(admin, reqcontext.RequestMeta{Mount: reqcontext.MountAPI, Surface: "web"})
	ctx = reqcontext.WithRequestSource(ctx, reqcontext.SourceInternal)
	in, ok = adminLineInput(ctx, tokenRecord())
	require.True(t, ok)
	require.Equal(t, "api_key", in.Caller.Kind, "admin lines never take the dispatch-only internal shortcut")
	require.Equal(t, "web", in.ClientSurface)

	// A decided effect inside a request keeps the request's metadata.
	rec := tokenRecord()
	rec.Decider = &runtime.Decider{Kind: runtime.DeciderInternal, Trigger: audit.TriggerPolicy, Policy: audit.PolicyRotationReconcile}
	ctx = reqcontext.WithRequestID(reqcontext.WithRequestMeta(admin, reqcontext.RequestMeta{Mount: reqcontext.MountAPI}), "rid-2")
	in, ok = adminLineInput(ctx, rec)
	require.True(t, ok)
	require.Equal(t, "internal", in.Caller.Kind)
	require.Equal(t, "rid-2", in.RequestID)
	require.Equal(t, "api", in.Source)

	// An observed edit never carries request metadata.
	rec.Decider = &runtime.Decider{Kind: runtime.DeciderNone, Trigger: audit.TriggerConfigFile}
	in, ok = adminLineInput(ctx, rec)
	require.True(t, ok)
	require.Empty(t, in.RequestID)
	require.Equal(t, "internal", in.Source)
	_, err := audit.NewAdminAction(in)
	require.NoError(t, err)

	// No AuthContext and no decider: a bug, never "anonymous".
	_, ok = adminLineInput(context.Background(), tokenRecord())
	require.False(t, ok)
}

func TestSinkAdminAuditor_NoCallerIsCountedNotWritten(t *testing.T) {
	sink := &recordingAuditSink{}
	a := newSinkAdminAuditor(sink, nil)
	before := audit.BuildFailures()
	a.AdminAction(context.Background(), tokenRecord())
	require.Empty(t, sink.snapshot())
	require.Equal(t, before+1, audit.BuildFailures())
	require.Nil(t, newSinkAdminAuditor(nil, nil), "no sink, no auditor")
}

func TestAdminProfile_CapturedAtIngress(t *testing.T) {
	ctx := withAdminProfile(auth.WithAuthContext(context.Background(), auth.AdminContext()), "admin-ops")
	ctx = transport.TagConnectionContext(ctx, transport.ConnectionSourceStdio)
	ctx = reqcontext.WithRequestID(ctx, "rid")
	sink := &recordingAuditSink{}
	newSinkAdminAuditor(sink, nil).AdminAction(ctx, tokenRecord())
	lines := sink.snapshot()
	require.Len(t, lines, 1)
	require.Contains(t, string(lines[0]), `"profile":"admin-ops"`)
}

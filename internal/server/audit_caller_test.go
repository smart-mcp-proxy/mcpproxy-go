package server

// audit_caller_test.go: T101 (Spec 107 PR-D). Pins the caller.kind/origin
// derivation table of contracts/audit-line-events.md "caller.kind
// derivation (FR-013)" — one case per caller kind the dispatch doors admit
// (authz/tool_call never see caller.kind:session_user; that kind is
// auth_event-only, FR-002/FR-003).
//
// auditCallerFromContext(ctx) is added by T103 (internal/server); this file
// is compile-red until T099 (internal/audit.Caller — already implemented)
// AND T103 land. transport.ConnectionSourceStdio is also new in T103.
//
// No production code lives here.

import (
	"context"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// TestAuditCallerFromContext_DerivationTable exercises every caller.kind the
// schema's identity allOf blocks recognise on a dispatch door (authz /
// tool_call), per contracts/audit-line-events.md.
func TestAuditCallerFromContext_DerivationTable(t *testing.T) {
	tests := []struct {
		name string
		ctx  func() context.Context
		want audit.Caller
	}{
		{
			// AuthContext.Type==admin, CredentialKind==api_key, connection
			// source defaults to tcp (no tag) -> caller.kind: api_key.
			name: "api_key",
			ctx: func() context.Context {
				ac := auth.AdminContext()
				ac.CredentialKind = auth.CredentialKindAPIKey
				return auth.WithAuthContext(context.Background(), ac)
			},
			want: audit.Caller{Kind: "api_key"},
		},
		{
			// Type==admin, source==tray -> caller.kind: socket. The tray
			// Unix-socket/named-pipe door bypasses the API key (OS-level
			// auth) but is still admin type with CredentialKindSocket.
			name: "socket_tray",
			ctx: func() context.Context {
				ac := auth.AdminContext()
				ac.CredentialKind = auth.CredentialKindSocket
				ctx := auth.WithAuthContext(context.Background(), ac)
				return transport.TagConnectionContext(ctx, transport.ConnectionSourceTray)
			},
			want: audit.Caller{Kind: "socket"},
		},
		{
			// Type==admin, source==stdio -> caller.kind: stdio. The native
			// stdio transport installs auth.AdminContext() with no listener
			// (server.go:1039); without the ConnectionSourceStdio tag,
			// GetConnectionSource defaults to tcp and the line would
			// misreport api_key — this is the case T103's stdioAuthContext
			// tag exists to fix.
			name: "stdio",
			ctx: func() context.Context {
				ac := auth.AdminContext()
				ctx := auth.WithAuthContext(context.Background(), ac)
				return transport.TagConnectionContext(ctx, transport.ConnectionSourceStdio)
			},
			want: audit.Caller{Kind: "stdio"},
		},
		{
			// Type==admin && Anonymous -> caller.kind: anonymous. No
			// identity fields survive: the anonymous bit exists precisely
			// because the admin type here is back-compat, not proof of
			// identity (issue #1148).
			name: "anonymous",
			ctx: func() context.Context {
				return auth.WithAuthContext(context.Background(), auth.AnonymousContext())
			},
			want: audit.Caller{Kind: "anonymous"},
		},
		{
			// Type==agent, owned (UserID present from the single owner
			// resolution, agent_token.go:133-152) -> all-or-none owner
			// identity: user_id, user_email, role, provider all present
			// alongside token_name/token_prefix.
			name: "agent_token_owned",
			ctx: func() context.Context {
				tok := &auth.AgentToken{
					Name:          "ci-bot",
					TokenPrefix:   "mcp_agt_abcd",
					UserID:        "usr_123",
					OwnerEmail:    "owner@example.com",
					OwnerProvider: "google",
					OwnerRole:     "user",
				}
				return auth.WithAuthContext(context.Background(), tok.AuthContext())
			},
			want: audit.Caller{
				Kind:        "agent_token",
				TokenName:   "ci-bot",
				TokenPrefix: "mcp_agt_abcd",
				UserID:      "usr_123",
				UserEmail:   "owner@example.com",
				Role:        "user",
				Provider:    "google",
			},
		},
		{
			// Type==agent, ownerless (personal edition / no owner
			// resolution): UserID empty, so user_email/role/provider must
			// ALSO be empty (all-or-none) even though token_name/prefix are
			// always required for agent_token.
			name: "agent_token_ownerless",
			ctx: func() context.Context {
				tok := &auth.AgentToken{
					Name:        "local-agent",
					TokenPrefix: "mcp_agt_efgh",
				}
				return auth.WithAuthContext(context.Background(), tok.AuthContext())
			},
			want: audit.Caller{
				Kind:        "agent_token",
				TokenName:   "local-agent",
				TokenPrefix: "mcp_agt_efgh",
			},
		},
		{
			// Type==admin_user, CredentialKind==cookie -> caller.kind:
			// session_admin, role: admin, user_id present.
			name: "session_admin_cookie",
			ctx: func() context.Context {
				ac := auth.AdminUserContext("usr_admin", "admin@example.com", "Admin", "google")
				ac.CredentialKind = auth.CredentialKindCookie
				return auth.WithAuthContext(context.Background(), ac)
			},
			want: audit.Caller{
				Kind:      "session_admin",
				UserID:    "usr_admin",
				UserEmail: "admin@example.com",
				Role:      "admin",
				Provider:  "google",
			},
		},
		{
			// Type==admin_user, CredentialKind==bearer_jwt -> same
			// caller.kind: session_admin (the schema's derivation keys off
			// CredentialKind ∈ {cookie, bearer_jwt}, not the credential
			// used for THIS particular request being the cookie alone).
			name: "session_admin_bearer_jwt",
			ctx: func() context.Context {
				ac := auth.AdminUserContext("usr_admin2", "admin2@example.com", "Admin Two", "github")
				ac.CredentialKind = auth.CredentialKindBearerJWT
				return auth.WithAuthContext(context.Background(), ac)
			},
			want: audit.Caller{
				Kind:      "session_admin",
				UserID:    "usr_admin2",
				UserEmail: "admin2@example.com",
				Role:      "admin",
				Provider:  "github",
			},
		},
		{
			// proxy-originated (nested code_execution sub-call dispatch,
			// reqcontext.SourceInternal) -> caller.kind: internal,
			// regardless of any AuthContext also present on the ctx (the
			// sandbox copies the caller's AuthContext for policy checks,
			// mcp_code_execution.go:249-256, but the audit line for the
			// sub-call itself must not attribute it to that caller).
			name: "internal",
			ctx: func() context.Context {
				ac := auth.AdminContext()
				ctx := auth.WithAuthContext(context.Background(), ac)
				return reqcontext.WithRequestSource(ctx, reqcontext.SourceInternal)
			},
			want: audit.Caller{Kind: "internal"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := auditCallerFromContext(tt.ctx())
			if got != tt.want {
				t.Fatalf("auditCallerFromContext() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestAuditCallerFromContext_NilAuthContext covers a ctx with no AuthContext
// installed at all (a test double, an in-process caller that never went
// through auth middleware) — it must not panic and must not fabricate an
// identity; the least-privileged reading is caller.kind: anonymous, mirroring
// AuthContext.CanRevealSecrets' own "nil is unprivileged, not admin-by-
// absence" rule (auth/context.go).
func TestAuditCallerFromContext_NilAuthContext(t *testing.T) {
	got := auditCallerFromContext(context.Background())
	want := audit.Caller{Kind: "anonymous"}
	if got != want {
		t.Fatalf("auditCallerFromContext(no AuthContext) = %+v, want %+v", got, want)
	}
}

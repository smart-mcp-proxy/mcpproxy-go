//go:build server

package broker

import (
	"context"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
)

// Audit vocabulary for the per-user OAuth connect flow (spec 074 T10, FR-028).
// These strings are the stable, secret-free attribution values recorded on
// every connect operation. Spec 107 (FR-031) retired the acquire / refresh /
// inject actions and the token_exchange / entra_obo methods together with the
// never-wired credential resolver that was their only emitter: the connect
// flow is the one operation the broker performs.
const (
	// AuditMethodConnect is the per-user OAuth connect-flow acquisition method.
	AuditMethodConnect = "connect"

	// AuditActionConnect is the per-user OAuth connect-flow consent/callback.
	AuditActionConnect = "connect"

	// AuditOutcomeSuccess marks a successful operation.
	AuditOutcomeSuccess = "success"
	// AuditOutcomeFailure marks a failed operation; Reason explains why.
	AuditOutcomeFailure = "failure"
)

// AuditEvent is the secret-free record of a single per-user credential-brokering
// operation. It deliberately carries NO token, refresh-token, client-secret, or
// any other credential material (FR-029): there is no field able to hold one, so
// auditing can never leak a secret. Reason is a coarse, secret-free explanation
// drawn from the broker's sentinel errors (which are themselves secret-free).
type AuditEvent struct {
	// UserID is the user the credential is brokered for (attribution; FR-028).
	UserID string
	// ServerName is the brokered upstream's configured name.
	ServerName string
	// Method is the acquisition method: connect.
	Method string
	// Action is the operation: connect.
	Action string
	// Outcome is success | failure.
	Outcome string
	// Reason is a secret-free explanation, set on failure (empty on success).
	Reason string
	// RequestID correlates the operation with the originating HTTP request.
	RequestID string
}

// AuditSink receives broker audit events and persists them to the activity log.
// Implementations MUST NOT block the caller's request path (use async writes) and
// MUST tolerate a nil/zero event gracefully.
type AuditSink interface {
	RecordBrokerEvent(ctx context.Context, ev AuditEvent)
}

// nopAuditSink discards every event. It is the default when no sink is wired, so
// broker code can always call the sink unconditionally.
type nopAuditSink struct{}

func (nopAuditSink) RecordBrokerEvent(context.Context, AuditEvent) {}

// auditRequestID extracts the correlatable request id from ctx, if present.
func auditRequestID(ctx context.Context) string {
	return reqcontext.GetRequestID(ctx)
}

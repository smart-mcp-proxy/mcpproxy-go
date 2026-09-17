//go:build server

package auth

// auth_event.go: the Spec 107 PR-D `auth_event` emitter (T107). It adapts
// the handler's typed LoginResult (T044) into internal/audit's
// AuthEventInput and writes exactly one line per terminal login attempt and
// one per logout through the ONE audit.Sink the dispatch funnels write
// through (serveredition.Dependencies.AuditSink, contracts/audit-line-
// events.md). Identity is stage-dependent, never reason-dependent
// (auditCallerFor): LoginResult.UserID set ⇒ the store was reached and a
// record exists (session_user|session_admin + user_id); otherwise
// LoginResult.EmailHash set ⇒ a verified email is known and the store was
// not yet consulted (anonymous + email_hash); neither ⇒ anonymous.

import (
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
)

// NewAuditEmitter returns a LoginResultObserver that writes one `auth_event`
// line per call to sink. Install it directly on OAuthHandler.
// LoginResultObserver (setup.go); nil sink is a no-op writer, so
// audit_log:off costs nothing on the login/logout hot path. logger may be
// nil (a build/marshal/write failure is then silently dropped, matching
// audit.Sink's own "never block the caller" contract).
func NewAuditEmitter(sink audit.Sink, logger *zap.SugaredLogger) func(LoginResult) {
	return func(res LoginResult) {
		if sink == nil {
			return
		}
		line, err := audit.NewAuthEvent(audit.AuthEventInput{
			Ts:        time.Now(),
			RequestID: res.RequestID,
			// Login/logout are REST-only, always over the listener (never
			// the tray socket or stdio) — contracts/audit-line-events.md's
			// auth_event fixtures fix origin:local, source:api.
			Origin:  "local",
			Source:  "api",
			Surface: res.Surface,
			Reason:  string(res.Reason),
			Caller:  auditCallerFor(res),
			Flags:   auditFlagsFor(res.Flags),
		})
		if err != nil {
			if logger != nil {
				logger.Errorw("audit: failed to build auth_event line", "error", err, "request_id", res.RequestID, "reason", string(res.Reason))
			}
			return
		}
		b, err := line.JSON()
		if err != nil {
			if logger != nil {
				logger.Errorw("audit: failed to marshal auth_event line", "error", err, "request_id", res.RequestID)
			}
			return
		}
		if werr := sink.Write(b); werr != nil && logger != nil {
			// The sink's own always-on WriteFailures() counter already
			// recorded this; the rate-limited failure logger it was built
			// with (T109) covers operator visibility. This warning is best
			// effort, request-scoped context only.
			logger.Warnw("audit: auth_event write failed", "error", werr, "request_id", res.RequestID)
		}
	}
}

// auditCallerFor derives the auth_event `caller` object from a LoginResult.
func auditCallerFor(res LoginResult) audit.Caller {
	if res.UserID != "" {
		role := res.Role
		kind := "session_user"
		switch role {
		case "admin":
			kind = "session_admin"
		default:
			role = "user"
		}
		return audit.Caller{Kind: kind, UserID: res.UserID, Role: role, Provider: res.Provider}
	}
	if res.EmailHash != "" {
		return audit.Caller{Kind: "anonymous", EmailHash: res.EmailHash}
	}
	return audit.Caller{Kind: "anonymous"}
}

func auditFlagsFor(flags []LoginFlag) []string {
	if len(flags) == 0 {
		return nil
	}
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = string(f)
	}
	return out
}

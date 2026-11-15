package reqcontext

import (
	"context"
	"unicode"
	"unicode/utf8"
)

// RunNameHeader carries a caller-chosen label for one run of work (Spec 124
// FR-003). It is a label, never a credential: it only splits the caller's own
// activity into named work sessions, and it is never forwarded upstream.
const RunNameHeader = "X-MCPProxy-Run"

// MaxRunNameLen is the longest accepted run name, in characters.
const MaxRunNameLen = 128

const (
	runHeaderKey     ContextKey = "run_header"
	clientSurfaceKey ContextKey = "client_surface"
	attemptIDKey     ContextKey = "attempt_id"
)

// ValidRunName reports whether s is an acceptable run name: 1 to MaxRunNameLen
// printable characters. Control characters, invalid UTF-8 and the empty string
// are refused.
func ValidRunName(s string) bool {
	if s == "" || !utf8.ValidString(s) {
		return false
	}
	n := 0
	for _, r := range s {
		n++
		if n > MaxRunNameLen || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// WithRunHeader stores the raw X-MCPProxy-Run header value. It is stored
// unvalidated so the reader can tell "absent" from "present but invalid" and
// log the latter (FR-004). An empty value stores nothing.
func WithRunHeader(ctx context.Context, raw string) context.Context {
	if raw == "" {
		return ctx
	}
	return context.WithValue(ctx, runHeaderKey, raw)
}

// RunHeader returns the raw header value and whether one was sent.
func RunHeader(ctx context.Context) (string, bool) {
	raw, ok := ctx.Value(runHeaderKey).(string)
	return raw, ok && raw != ""
}

// WithClientSurface records which first-party surface made a REST call
// ("cli", "webui", "tray") or "rest" for anything else. It names the client of
// a call that has no MCP clientInfo (Spec 124 FR-002).
func WithClientSurface(ctx context.Context, surface string) context.Context {
	if surface == "" {
		return ctx
	}
	return context.WithValue(ctx, clientSurfaceKey, surface)
}

// ClientSurface returns the surface recorded by WithClientSurface, or "".
func ClientSurface(ctx context.Context) string {
	s, _ := ctx.Value(clientSurfaceKey).(string)
	return s
}

// WithAttemptID records the request id a dispatch path minted for one tool
// call attempt. Every activity row of that attempt carries it, including a
// limiter rejection, which would otherwise fall back to the transport request
// id that several attempts of one HTTP request share (Spec 124 D2).
func WithAttemptID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, attemptIDKey, id)
}

// AttemptID returns the id recorded by WithAttemptID, or "".
func AttemptID(ctx context.Context) string {
	id, _ := ctx.Value(attemptIDKey).(string)
	return id
}

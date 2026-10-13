//go:build server

package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// MCP-3207: exercise the server-edition tool-call telemetry label VALUES that
// editionToolCallAttributes() injects (MCP-32). The QA pass (OBS-05) code-verified
// the path but never drove ≥2 authenticated users to confirm the emitted user_id /
// profile values are distinct and correct.
//
// IMPORTANT design note (provenance: internal/server/observability_edition_server.go:13-17):
// user_id and profile are OTLP SPAN attributes, NOT Prometheus metric labels. The
// tool-call counter mcpproxy_tool_calls_total carries only {server,tool,status} to
// keep metric cardinality bounded. Therefore "exercise the label values" is faithfully
// and deterministically tested at the span layer here; the metric-layer negative
// control (no per-user labels) lives in
// internal/observability/metrics_test.go::TestToolCallMetric_NoPerUserCardinalityLabels,
// and the personal-edition negative control lives in observability_edition_test.go.

// attrValue returns the string value of the named attribute, or "" plus a bool
// indicating presence.
func attrValue(attrs []attribute.KeyValue, key string) (string, bool) {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value.AsString(), true
		}
	}
	return "", false
}

// TestEditionToolCallAttributes_TwoUsersDistinctValues verifies that two distinct
// authenticated users on the same non-default profile yield distinct, correct
// user_id values and the same correct profile value.
func TestEditionToolCallAttributes_TwoUsersDistinctValues(t *testing.T) {
	const profileSlug = "team-acme"

	// User A: regular OAuth user.
	ctxA := auth.WithAuthContext(context.Background(),
		auth.UserContext("01HZUSERAAAAAAAAAAAAAAAAA", "alice@example.com", "Alice", "google"))
	// User B: OAuth admin user — IsUser() must also be true for admin_user.
	ctxB := auth.WithAuthContext(context.Background(),
		auth.AdminUserContext("01HZUSERBBBBBBBBBBBBBBBBB", "bob@example.com", "Bob", "github"))

	attrsA := editionToolCallAttributes(ctxA, profileSlug)
	attrsB := editionToolCallAttributes(ctxB, profileSlug)

	uidA, okA := attrValue(attrsA, "user_id")
	require.True(t, okA, "user A span must carry user_id")
	assert.Equal(t, "01HZUSERAAAAAAAAAAAAAAAAA", uidA)

	uidB, okB := attrValue(attrsB, "user_id")
	require.True(t, okB, "user B (admin_user) span must carry user_id")
	assert.Equal(t, "01HZUSERBBBBBBBBBBBBBBBBB", uidB)

	assert.NotEqual(t, uidA, uidB, "the two callers must produce distinct user_id label values")

	profA, okPA := attrValue(attrsA, "profile")
	require.True(t, okPA)
	assert.Equal(t, profileSlug, profA)
	profB, okPB := attrValue(attrsB, "profile")
	require.True(t, okPB)
	assert.Equal(t, profileSlug, profB)
}

// TestEditionToolCallAttributes_AdminAPIKey_NoUserID verifies that a non-OAuth
// caller (personal-style API-key admin) does NOT get a user_id label even in the
// server edition, while a profile, if present, is still attached.
func TestEditionToolCallAttributes_AdminAPIKey_NoUserID(t *testing.T) {
	ctx := auth.WithAuthContext(context.Background(), auth.AdminContext())

	attrs := editionToolCallAttributes(ctx, "team-acme")

	_, hasUID := attrValue(attrs, "user_id")
	assert.False(t, hasUID, "API-key admin (non-user) must not carry a user_id label")

	prof, hasProf := attrValue(attrs, "profile")
	require.True(t, hasProf, "profile label is independent of user identity")
	assert.Equal(t, "team-acme", prof)
}

// TestEditionToolCallAttributes_NoAuthContext verifies a missing auth context
// yields no user_id (defensive: avoids panics / empty-string labels).
func TestEditionToolCallAttributes_NoAuthContext(t *testing.T) {
	attrs := editionToolCallAttributes(context.Background(), "")
	assert.Empty(t, attrs, "no auth context and no profile => no edition attributes")

	attrsProfileOnly := editionToolCallAttributes(context.Background(), "default")
	uid, hasUID := attrValue(attrsProfileOnly, "user_id")
	assert.False(t, hasUID, "no auth context must not emit a user_id")
	assert.Empty(t, uid)
}

// TestEditionToolCallAttributes_EmptyUserIDOmitted verifies a user context whose
// UserID is empty does not emit an empty-valued user_id label (cardinality / noise).
func TestEditionToolCallAttributes_EmptyUserIDOmitted(t *testing.T) {
	ctx := auth.WithAuthContext(context.Background(),
		auth.UserContext("", "noid@example.com", "NoID", "google"))
	attrs := editionToolCallAttributes(ctx, "team-acme")
	_, hasUID := attrValue(attrs, "user_id")
	assert.False(t, hasUID, "empty user_id must be omitted, not emitted as user_id=\"\"")
}

// TestToolCallSpan_CarriesDistinctUserAndProfileValues drives the attributes onto
// real, emitted OTLP spans for two users and asserts each recorded span carries the
// matching caller's user_id and the active profile. This is the closest faithful
// "exercise the values on emitted telemetry" check that stays deterministic (an
// in-memory span recorder, no live OTLP collector).
func TestToolCallSpan_CarriesDistinctUserAndProfileValues(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	tracer := tp.Tracer("mcp-3207-test")

	const profileSlug = "team-acme"
	callers := []struct {
		name   string
		ctx    context.Context
		userID string
	}{
		{
			name:   "alice",
			ctx:    auth.WithAuthContext(context.Background(), auth.UserContext("01HZUSERAAAAAAAAAAAAAAAAA", "alice@example.com", "Alice", "google")),
			userID: "01HZUSERAAAAAAAAAAAAAAAAA",
		},
		{
			name:   "bob",
			ctx:    auth.WithAuthContext(context.Background(), auth.AdminUserContext("01HZUSERBBBBBBBBBBBBBBBBB", "bob@example.com", "Bob", "github")),
			userID: "01HZUSERBBBBBBBBBBBBBBBBB",
		},
	}

	// Mirror the production emission path (startToolCallSpan): start a tool.call
	// span, then attach edition attributes from the caller's auth context.
	for _, c := range callers {
		_, span := tracer.Start(c.ctx, "tool.call")
		span.SetAttributes(editionToolCallAttributes(c.ctx, profileSlug)...)
		span.End()
	}

	ended := sr.Ended()
	require.Len(t, ended, len(callers), "one emitted span per caller")

	seen := map[string]string{} // user_id -> profile
	for _, s := range ended {
		uid, hasUID := attrValue(s.Attributes(), "user_id")
		require.True(t, hasUID, "emitted span missing user_id")
		prof, hasProf := attrValue(s.Attributes(), "profile")
		require.True(t, hasProf, "emitted span missing profile")
		seen[uid] = prof
	}

	require.Len(t, seen, len(callers), "each caller must emit a distinct user_id on its span")
	for _, c := range callers {
		prof, ok := seen[c.userID]
		require.Truef(t, ok, "no emitted span carried user_id=%s", c.userID)
		assert.Equal(t, profileSlug, prof, "profile value must match the active profile for user %s", c.name)
	}
}

package oauth

import (
	"strings"
	"testing"
)

// SEC-01 gap fix (PR #1350, live-verification follow-up).
//
// LogSafeRequestPath, LogSafeQueryString and LogSafeRequestURL mask a
// credential by NAME (`apikey=<value>`) or by VENDOR SHAPE (`ghp_…`,
// `sk-…`). The admin API key mcpproxy auto-generates
// (config.generateAPIKey) is neither: it is a bare 64-character lowercase
// hex string with no enclosing name and no vendor prefix. Worse, hex has a
// 4-bit-per-symbol ceiling (16 possible runes), so even a perfectly random
// hex string's Shannon entropy tops out at 4.0 — below the detector's 4.5
// "possible secret" threshold (internal/security/entropy.go) — so the
// value-shaped detector can never catch it either, no matter how it is
// tuned. A live instance reproduced the key landing verbatim in
// ~/.mcpproxy/logs/main.log as a bare PATH SEGMENT
// (`GET /api/v1/status/<key>`).
//
// These renderers now accept the caller's currently-configured secret(s) and
// redact every EXACT occurrence before any name- or shape-based rule runs.
// Exact match is the only rule that cannot be defeated by inventing a new
// shape, and it is also the only one that will not fire on a same-shaped
// value that happens NOT to be the live secret — see the
// "*_UnrelatedHexPathSegmentsSurvive" tests below, which pin that a SHA-256
// tool hash or a ULID-shaped activity id sitting right next to the real key
// in the same path is left untouched.
const sec01AdminKey = "ccc01b2ee10ad391e6a083b675023bba7124a8c8c94203b9cc4f2683831df1ee"

// A tool hash / activity id: same shape (hex) and comparable length to the
// admin key, but NOT the configured secret. Chosen to be exactly 64 hex
// chars, the shape a real SHA-256 digest takes, so it also exercises the
// "same length as the key" edge the naive fix (mask every 64-hex segment)
// would get wrong.
const sec01UnrelatedHex = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85"

func TestLogSafeRequestPath_ExactSecretRedactsBarePathSegment(t *testing.T) {
	path := "/api/v1/status/" + sec01AdminKey

	got := LogSafeRequestPath(path, sec01AdminKey)
	if strings.Contains(got, sec01AdminKey) {
		t.Fatalf("admin key survived in the path field: %q", got)
	}
}

func TestLogSafeRequestPath_UnrelatedHexPathSegmentsSurvive(t *testing.T) {
	// The false-positive story: a tool-hash / activity-id path segment right
	// next to the redacted key must not be swept up too.
	path := "/api/v1/activity/" + sec01UnrelatedHex + "/tool/" + sec01AdminKey

	got := LogSafeRequestPath(path, sec01AdminKey)
	if strings.Contains(got, sec01AdminKey) {
		t.Fatalf("admin key survived in the path field: %q", got)
	}
	if !strings.Contains(got, sec01UnrelatedHex) {
		t.Fatalf("unrelated 64-hex path segment (activity id / tool hash) was over-redacted: %q", got)
	}
}

func TestLogSafeQueryString_ExactSecretRedactsBareValue(t *testing.T) {
	got := LogSafeQueryString(sec01AdminKey, sec01AdminKey)
	if strings.Contains(got, sec01AdminKey) {
		t.Fatalf("admin key survived in the query field: %q", got)
	}
}

func TestLogSafeQueryString_UnrelatedHexValueSurvives(t *testing.T) {
	got := LogSafeQueryString("request_id="+sec01UnrelatedHex, sec01AdminKey)
	if !strings.Contains(got, sec01UnrelatedHex) {
		t.Fatalf("unrelated 64-hex query value (request id) was over-redacted: %q", got)
	}
}

func TestLogSafeRequestURL_ExactSecretRedactsPathAndFragment(t *testing.T) {
	for _, referer := range []string{
		"http://127.0.0.1:8080/ui/" + sec01AdminKey,
		"http://127.0.0.1:8080/ui/#/servers/" + sec01AdminKey,
	} {
		got := LogSafeRequestURL(referer, sec01AdminKey)
		if strings.Contains(got, sec01AdminKey) {
			t.Fatalf("admin key survived in the referer field: %q -> %q", referer, got)
		}
	}
}

// No known secret configured (the empty-string case an unconfigured or
// testing scenario produces, see httpapi.Server.currentAdminAPIKey) must be a
// no-op: the exact-match pass never runs on an empty needle.
func TestLogSafeRequestPath_EmptyKnownSecretIsNoop(t *testing.T) {
	path := "/api/v1/status/" + sec01UnrelatedHex

	got := LogSafeRequestPath(path, "")
	if got != LogSafeRequestPath(path) {
		t.Fatalf("an empty known secret changed the rendering: %q vs %q", got, LogSafeRequestPath(path))
	}
}

// Backward compatibility: every existing single-argument call site (internal
// recursive use inside this file, and every caller before SEC-01's follow-up)
// must keep compiling and behaving exactly as before.
func TestLogSafeRequestPath_NoKnownSecretsArgStillCompiles(t *testing.T) {
	const path = "/api/v1/servers"
	if got := LogSafeRequestPath(path); got != path {
		t.Fatalf("benign path was rewritten: got %q", got)
	}
}

// Unlike the admin key, an mcp_agt_ agent token IS vendor-shaped (see
// agentTokenPattern in internal/security/patterns/tokens.go), so it is
// already caught by the value-shaped detector (MaskDetectedSecrets) with NO
// known-secret value threaded through — exercised here at the same three
// entry points the admin key needed exact-match plumbing for.
func TestLogSafeRequestPath_AgentTokenCoveredByShapeRule(t *testing.T) {
	const agentToken = "mcp_agt_" + "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4"

	if got := LogSafeRequestPath("/api/v1/status/" + agentToken); strings.Contains(got, agentToken) {
		t.Fatalf("agent token survived in the path field: %q", got)
	}
	if got := LogSafeQueryString("opaque=" + agentToken); strings.Contains(got, agentToken) {
		t.Fatalf("agent token survived in the query field: %q", got)
	}
	if got := LogSafeRequestURL("http://127.0.0.1:8080/ui/" + agentToken); strings.Contains(got, agentToken) {
		t.Fatalf("agent token survived in the referer field: %q", got)
	}
}

package transport

import "testing"

// Spec 074 T7 (FR-016/FR-017): the per-user resolved credential is injected into
// the configured outbound header, REPLACING any inbound/configured auth header.
// The inbound gateway/IdP token must never be forwarded.

func TestBrokeredAuth_HeaderValue(t *testing.T) {
	cases := []struct {
		name   string
		format string
		token  string
		want   string
	}{
		{name: "default bearer", format: "Bearer {token}", token: "u1-tok", want: "Bearer u1-tok"},
		{name: "raw token", format: "{token}", token: "abc", want: "abc"},
		{name: "custom prefix", format: "token {token}", token: "xyz", want: "token xyz"},
		{name: "no placeholder", format: "static-value", token: "ignored", want: "static-value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &BrokeredAuth{Header: "Authorization", Format: tc.format, Token: tc.token}
			if got := b.HeaderValue(); got != tc.want {
				t.Fatalf("HeaderValue() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEffectiveHeaders_InjectsBrokeredCredential(t *testing.T) {
	base := map[string]string{"X-Trace": "on"}
	b := &BrokeredAuth{Header: "Authorization", Format: "Bearer {token}", Token: "user-A-token"}

	got := EffectiveHeaders(base, b)

	if got["Authorization"] != "Bearer user-A-token" {
		t.Fatalf("outbound Authorization = %q, want %q", got["Authorization"], "Bearer user-A-token")
	}
	if got["X-Trace"] != "on" {
		t.Fatalf("non-auth header should be preserved, got %q", got["X-Trace"])
	}
}

// FR-017: the inbound gateway/IdP token configured on the server must be
// REPLACED, never merged or forwarded — even if cased differently.
func TestEffectiveHeaders_ReplacesInboundAuthHeader(t *testing.T) {
	base := map[string]string{
		"authorization": "Bearer INBOUND-GATEWAY-TOKEN", // lowercase, different casing
		"X-Other":       "keep",
	}
	b := &BrokeredAuth{Header: "Authorization", Format: "Bearer {token}", Token: "per-user-token"}

	got := EffectiveHeaders(base, b)

	// Exactly one auth header, carrying the per-user token, never the inbound one.
	authCount := 0
	for k, v := range got {
		if equalFoldHeader(k, "Authorization") {
			authCount++
			if v != "Bearer per-user-token" {
				t.Fatalf("auth header = %q, want per-user token, must not forward inbound", v)
			}
		}
		if v == "Bearer INBOUND-GATEWAY-TOKEN" {
			t.Fatalf("inbound gateway token was forwarded on outbound header %q (FR-017 violation)", k)
		}
	}
	if authCount != 1 {
		t.Fatalf("expected exactly 1 auth header after replacement, got %d", authCount)
	}
	if got["X-Other"] != "keep" {
		t.Fatalf("unrelated header dropped: %q", got["X-Other"])
	}
}

func TestEffectiveHeaders_NilBrokeredReturnsBaseCopy(t *testing.T) {
	base := map[string]string{"Authorization": "Bearer static"}
	got := EffectiveHeaders(base, nil)
	if got["Authorization"] != "Bearer static" {
		t.Fatalf("nil broker must leave base headers intact, got %q", got["Authorization"])
	}
	// Must be a copy, not the same map (callers must not mutate the server config).
	got["Authorization"] = "mutated"
	if base["Authorization"] != "Bearer static" {
		t.Fatalf("EffectiveHeaders must not alias/mutate the base map")
	}
}

func TestEffectiveHeaders_InjectsIntoEmptyBase(t *testing.T) {
	b := &BrokeredAuth{Header: "Authorization", Format: "Bearer {token}", Token: "t"}
	got := EffectiveHeaders(nil, b)
	if got["Authorization"] != "Bearer t" {
		t.Fatalf("brokered auth must inject even with no base headers, got %q", got["Authorization"])
	}
}

// equalFoldHeader is a test helper mirroring the case-insensitive header match.
func equalFoldHeader(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

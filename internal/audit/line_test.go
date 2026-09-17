// Package audit_test exercises the audit line builders (internal/audit) against
// the binding wire schema (specs/107-server-edition-sso-hardening/contracts/audit-line.schema.json).
//
// T097 (Spec 107 PR-D): this file, together with schema_test.go, is
// [compile-red until T099] — internal/audit does not exist yet, so nothing
// here compiles. That is the expected red: the paired implementation task
// (T099) introduces internal/audit/{attempt.go,canonical.go,line.go,sink.go}
// and every symbol referenced below, after which these tests must pass
// unchanged (plan.md "Failing tests first").
//
// No production code lives in this file. The three event-specific
// constructors (NewAuthz, NewToolCall, NewAuthEvent) are expected to take
// typed input structs that simply have no field through which a forbidden
// key (e.g. `outcome` on an authz line, raw argument/response/error text on
// any line) could arrive — that is a structural, compile-time guarantee and
// is not re-tested at runtime here; schema_test.go proves the resulting
// *producer* key set is exactly what the strict (additionalProperties:false)
// variant of the schema allows.
package audit_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
)

// ---------------------------------------------------------------------------
// Fixed test inputs
// ---------------------------------------------------------------------------

var fixedTS = time.Date(2026, 9, 17, 10, 0, 0, 1, time.UTC)

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

func baseAttempt() audit.Attempt {
	return audit.Attempt{
		RequestID: "1757930400000000001-jira-create_issue-7",
		SessionID: "s-1",
		Server:    "jira",
		Tool:      "create_issue",
		Operation: "write",
		Surface:   "call_tool_write",
		Source:    "mcp",
		Origin:    "local",
		StartedAt: fixedTS,
	}
}

func agentCaller() audit.Caller {
	return audit.Caller{
		Kind:        "agent_token",
		UserID:      "01J000000000000000000000",
		UserEmail:   "alice@example.com",
		Role:        "user",
		Provider:    "oidc",
		TokenName:   "t1",
		TokenPrefix: "mcp_agt_ab12",
	}
}

// argsHash mirrors what a caller of the builder must have already computed
// (Spec 107 FR-015: SHA-256 over the RFC 8785 canonical serialisation of
// security.StripInternalArgs(args)). T096 (canonical_test.go) proves the
// canonicalisation itself; here we only need a stable, schema-shaped value.
func argsHash(canonical string) (sum string, n int) {
	h := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(h[:]), len(canonical)
}

// ---------------------------------------------------------------------------
// authz
// ---------------------------------------------------------------------------

func TestNewAuthz_AllowValidatesAgainstSchema(t *testing.T) {
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n

	line, err := audit.NewAuthz(audit.AuthzInput{
		Ts:       fixedTS,
		Attempt:  att,
		Caller:   agentCaller(),
		Decision: "allow",
		Reason:   "none",
	})
	if err != nil {
		t.Fatalf("NewAuthz: %v", err)
	}
	validateAgainstPublishedSchema(t, line)

	obj := decodeLine(t, line)
	if obj["event"] != "authz" {
		t.Fatalf("event = %v, want authz", obj["event"])
	}
	if _, ok := obj["outcome"]; ok {
		t.Fatalf("authz line must never carry outcome: %v", obj)
	}
}

func TestNewAuthz_DenyRequiresDisclosedAndNonNoneReason(t *testing.T) {
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.Server, att.Tool = "prod-db", "query"
	att.ArgsSHA256, att.ArgsBytes = sum, n

	line, err := audit.NewAuthz(audit.AuthzInput{
		Ts:        fixedTS,
		Attempt:   att,
		Caller:    agentCaller(),
		Decision:  "deny",
		Reason:    "token_scope",
		Disclosed: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("NewAuthz: %v", err)
	}
	validateAgainstPublishedSchema(t, line)

	obj := decodeLine(t, line)
	if obj["decision"] != "deny" || obj["reason"] == "none" {
		t.Fatalf("deny line must carry a non-none reason: %v", obj)
	}
	if obj["disclosed"] != false {
		t.Fatalf("non-disclosing deny must carry disclosed:false: %v", obj)
	}

	// A deny with no Disclosed pointer set must be rejected by the builder
	// itself (or produce a line the schema rejects) — the schema requires
	// `disclosed` whenever decision:deny.
	_, err = audit.NewAuthz(audit.AuthzInput{
		Ts:       fixedTS,
		Attempt:  att,
		Caller:   agentCaller(),
		Decision: "deny",
		Reason:   "token_scope",
		// Disclosed intentionally omitted.
	})
	if err == nil {
		t.Fatalf("NewAuthz: expected error for deny without Disclosed")
	}
}

func TestNewAuthz_AllowMustCarryReasonNone(t *testing.T) {
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n

	_, err := audit.NewAuthz(audit.AuthzInput{
		Ts:       fixedTS,
		Attempt:  att,
		Caller:   agentCaller(),
		Decision: "allow",
		Reason:   "token_scope", // wrong: allow must pair with reason:none
	})
	if err == nil {
		t.Fatalf("NewAuthz: expected error for allow with non-none reason")
	}
}

// ---------------------------------------------------------------------------
// tool_call
// ---------------------------------------------------------------------------

func TestNewToolCall_SuccessValidatesAgainstSchema(t *testing.T) {
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n

	line, err := audit.NewToolCall(audit.ToolCallInput{
		Ts:            fixedTS,
		Attempt:       att,
		Caller:        agentCaller(),
		Outcome:       "success",
		DurationMs:    248,
		RequestBytes:  intPtr(2),
		ResponseBytes: intPtr(512),
	})
	if err != nil {
		t.Fatalf("NewToolCall: %v", err)
	}
	validateAgainstPublishedSchema(t, line)

	obj := decodeLine(t, line)
	for _, forbidden := range []string{"decision", "disclosed", "flags"} {
		if _, ok := obj[forbidden]; ok {
			t.Fatalf("tool_call line must never carry %q: %v", forbidden, obj)
		}
	}
}

func TestNewToolCall_ErrorRequiresErrorClass(t *testing.T) {
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n

	line, err := audit.NewToolCall(audit.ToolCallInput{
		Ts:         fixedTS,
		Attempt:    att,
		Caller:     agentCaller(),
		Outcome:    "error",
		ErrorClass: "upstream_timeout",
		DurationMs: 30000,
	})
	if err != nil {
		t.Fatalf("NewToolCall: %v", err)
	}
	validateAgainstPublishedSchema(t, line)

	_, err = audit.NewToolCall(audit.ToolCallInput{
		Ts:         fixedTS,
		Attempt:    att,
		Caller:     agentCaller(),
		Outcome:    "error",
		DurationMs: 30000,
		// ErrorClass intentionally omitted — must be rejected.
	})
	if err == nil {
		t.Fatalf("NewToolCall: expected error for outcome:error without ErrorClass")
	}

	// error_class is forbidden on every non-error outcome.
	_, err = audit.NewToolCall(audit.ToolCallInput{
		Ts:         fixedTS,
		Attempt:    att,
		Caller:     agentCaller(),
		Outcome:    "success",
		ErrorClass: "upstream_timeout",
		DurationMs: 1,
	})
	if err == nil {
		t.Fatalf("NewToolCall: expected error for ErrorClass set on a success outcome")
	}
}

func TestNewToolCall_BlockedAndRejectedReasons(t *testing.T) {
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n

	cases := []struct {
		outcome, reason string
	}{
		{"blocked", "output_sanitisation"},
		{"blocked", "output_schema"},
		{"rejected", "limiter_queue_full"},
		{"rejected", "limiter_queue_timeout"},
	}
	for _, tc := range cases {
		line, err := audit.NewToolCall(audit.ToolCallInput{
			Ts:         fixedTS,
			Attempt:    att,
			Caller:     agentCaller(),
			Outcome:    tc.outcome,
			Reason:     tc.reason,
			DurationMs: 1,
		})
		if err != nil {
			t.Fatalf("NewToolCall(%s/%s): %v", tc.outcome, tc.reason, err)
		}
		validateAgainstPublishedSchema(t, line)
	}

	// reason is forbidden on success/error.
	_, err := audit.NewToolCall(audit.ToolCallInput{
		Ts:         fixedTS,
		Attempt:    att,
		Caller:     agentCaller(),
		Outcome:    "success",
		Reason:     "output_sanitisation",
		DurationMs: 1,
	})
	if err == nil {
		t.Fatalf("NewToolCall: expected error for Reason set on a success outcome")
	}
}

// ---------------------------------------------------------------------------
// auth_event
// ---------------------------------------------------------------------------

func TestNewAuthEvent_LoginOkValidatesAgainstSchema(t *testing.T) {
	line, err := audit.NewAuthEvent(audit.AuthEventInput{
		Ts:        fixedTS,
		RequestID: "req-4f2a",
		Origin:    "local",
		Source:    "api",
		Surface:   "login",
		Reason:    "ok",
		Caller: audit.Caller{
			Kind:     "session_user",
			UserID:   "01J000000000000000000000",
			Role:     "user",
			Provider: "oidc",
		},
	})
	if err != nil {
		t.Fatalf("NewAuthEvent: %v", err)
	}
	validateAgainstPublishedSchema(t, line)
}

func TestNewAuthEvent_PreIdentityRefusalCarriesNoIdentity(t *testing.T) {
	line, err := audit.NewAuthEvent(audit.AuthEventInput{
		Ts:        fixedTS,
		RequestID: "req-9c01",
		Origin:    "local",
		Source:    "api",
		Surface:   "login",
		Reason:    "nonce_mismatch",
		Caller:    audit.Caller{Kind: "anonymous"},
	})
	if err != nil {
		t.Fatalf("NewAuthEvent: %v", err)
	}
	validateAgainstPublishedSchema(t, line)

	obj := decodeLine(t, line)
	caller, _ := obj["caller"].(map[string]interface{})
	if _, ok := caller["user_id"]; ok {
		t.Fatalf("pre-identity refusal must not carry user_id: %v", caller)
	}
	if _, ok := caller["email_hash"]; ok {
		t.Fatalf("pre-identity refusal must not carry email_hash: %v", caller)
	}
}

func TestNewAuthEvent_ProviderErrorFromUserinfoCarriesEmailHashNeverUserID(t *testing.T) {
	line, err := audit.NewAuthEvent(audit.AuthEventInput{
		Ts:        fixedTS,
		RequestID: "req-b7d2",
		Origin:    "local",
		Source:    "api",
		Surface:   "login",
		Reason:    "provider_error",
		Caller: audit.Caller{
			Kind:      "anonymous",
			EmailHash: strings.Repeat("b", 64),
		},
	})
	if err != nil {
		t.Fatalf("NewAuthEvent: %v", err)
	}
	validateAgainstPublishedSchema(t, line)

	obj := decodeLine(t, line)
	caller, _ := obj["caller"].(map[string]interface{})
	if _, ok := caller["user_id"]; ok {
		t.Fatalf("provider_error must never carry user_id: %v", caller)
	}
}

func TestNewAuthEvent_LogoutSurfaceReasonPairing(t *testing.T) {
	// surface:logout <=> reason:logout, both directions.
	_, err := audit.NewAuthEvent(audit.AuthEventInput{
		Ts:        fixedTS,
		RequestID: "req-1",
		Origin:    "local",
		Source:    "api",
		Surface:   "login",
		Reason:    "logout",
		Caller:    audit.Caller{Kind: "session_user", UserID: "u1", Role: "user"},
	})
	if err == nil {
		t.Fatalf("NewAuthEvent: expected error for surface:login with reason:logout")
	}

	_, err = audit.NewAuthEvent(audit.AuthEventInput{
		Ts:        fixedTS,
		RequestID: "req-2",
		Origin:    "local",
		Source:    "api",
		Surface:   "logout",
		Reason:    "ok",
		Caller:    audit.Caller{Kind: "session_user", UserID: "u1", Role: "user"},
	})
	if err == nil {
		t.Fatalf("NewAuthEvent: expected error for surface:logout with reason:ok")
	}
}

// ---------------------------------------------------------------------------
// Redaction — nine sentinels (contracts/audit-line-events.md "Redaction (FR-015)")
// ---------------------------------------------------------------------------
//
// Four of the nine sentinel locations (arguments, response, error text, a
// caller-supplied `_auth_user_email`) are structurally unrepresentable: no
// Attempt/AuthzInput/ToolCallInput/AuthEventInput field exists through which
// raw argument, response or error text — or an `_auth_*` map member — could
// reach a line. That is proven by the type signatures above compiling at
// all (there is no such parameter to pass a sentinel into), not by a
// separate runtime assertion. The remaining five sentinel locations are
// caller/operator-controlled strings the line *does* carry, and are
// exercised below: they must be masked (fixed-prefix patterns only, never
// the generic high-entropy rule — see internal/logs sanitizer.go:104) by
// the per-field pass at build time, so that the resulting line is already
// byte-absent of the sentinel and the schema is validated *after*
// sanitisation.

const akiaSentinel = "AKIAQUICKSTART7SENTINEL0"
const ghpSentinel = "ghp_1234567890abcdef1234567890abcdef1234"

func TestRedaction_ClientNameSentinelMasked(t *testing.T) {
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n
	att.ClientName = "evil-client " + akiaSentinel

	line, err := audit.NewAuthz(audit.AuthzInput{
		Ts: fixedTS, Attempt: att, Caller: agentCaller(),
		Decision: "allow", Reason: "none",
	})
	if err != nil {
		t.Fatalf("NewAuthz: %v", err)
	}
	assertSentinelAbsent(t, line, akiaSentinel)
	validateAgainstPublishedSchema(t, line)
}

// TestRedaction_ClientNameLengthCapped is a round-2 cross-review regression
// (PR-D): an unbounded, entirely caller-asserted MCP `initialize` clientInfo
// value must not reach the sink unbounded — it can otherwise exceed the
// rotating-file writer's per-record limit and cause the required authz line
// to be silently dropped, or force unbounded audit-log disk growth.
func TestRedaction_ClientNameLengthCapped(t *testing.T) {
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n
	att.ClientName = strings.Repeat("x", 10000)

	line, err := audit.NewAuthz(audit.AuthzInput{
		Ts: fixedTS, Attempt: att, Caller: agentCaller(),
		Decision: "allow", Reason: "none",
	})
	if err != nil {
		t.Fatalf("NewAuthz: %v", err)
	}
	raw, err := line.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if len(raw) > 2000 {
		t.Fatalf("line with a 10000-rune client.name serialised to %d bytes — length cap not applied", len(raw))
	}
	validateAgainstPublishedSchema(t, line)
}

func TestRedaction_TokenNameSentinelMasked(t *testing.T) {
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n
	caller := agentCaller()
	caller.TokenName = "token-" + akiaSentinel

	line, err := audit.NewAuthz(audit.AuthzInput{
		Ts: fixedTS, Attempt: att, Caller: caller,
		Decision: "allow", Reason: "none",
	})
	if err != nil {
		t.Fatalf("NewAuthz: %v", err)
	}
	assertSentinelAbsent(t, line, akiaSentinel)
	validateAgainstPublishedSchema(t, line)
}

// TestRedaction_OpenAIProjectKeySentinelMasked is a round-3 cross-review
// regression (PR-D): the generic `sk-` pattern required 16+ alphanumeric
// characters immediately after the prefix, so a current-format OpenAI
// project/service-account/admin key (`sk-proj-...`, `sk-svcacct-...`,
// `sk-admin-...`), which inserts a hyphen-delimited segment before the
// random suffix, fell through unmasked when placed in a
// caller/operator-controlled field (client.name here).
func TestRedaction_OpenAIProjectKeySentinelMasked(t *testing.T) {
	const openAIProjSentinel = "sk-proj-QUICKSTART7SENTINEL0abcdefghijklmnopqrstuvwxyz"
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n
	att.ClientName = "evil-client " + openAIProjSentinel

	line, err := audit.NewAuthz(audit.AuthzInput{
		Ts: fixedTS, Attempt: att, Caller: agentCaller(),
		Decision: "allow", Reason: "none",
	})
	if err != nil {
		t.Fatalf("NewAuthz: %v", err)
	}
	assertSentinelAbsent(t, line, openAIProjSentinel)
	validateAgainstPublishedSchema(t, line)
}

func TestRedaction_ProfileSentinelMasked(t *testing.T) {
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n
	att.Profile = "profile-" + akiaSentinel

	line, err := audit.NewAuthz(audit.AuthzInput{
		Ts: fixedTS, Attempt: att, Caller: agentCaller(),
		Decision: "allow", Reason: "none",
	})
	if err != nil {
		t.Fatalf("NewAuthz: %v", err)
	}
	assertSentinelAbsent(t, line, akiaSentinel)
	validateAgainstPublishedSchema(t, line)
}

// A caller-supplied server:tool pair on a REFUSED dispatch is the one case
// FR-016 admits recording verbatim unless it is itself credential-shaped —
// here it is, so it must still be masked.
func TestRedaction_RefusedDispatchServerToolSentinelMasked(t *testing.T) {
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n
	att.Server = akiaSentinel + ":" + ghpSentinel
	att.Tool = akiaSentinel

	line, err := audit.NewAuthz(audit.AuthzInput{
		Ts: fixedTS, Attempt: att, Caller: agentCaller(),
		Decision: "deny", Reason: "tool_not_callable", Disclosed: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("NewAuthz: %v", err)
	}
	assertSentinelAbsent(t, line, akiaSentinel)
	assertSentinelAbsent(t, line, ghpSentinel)
	validateAgainstPublishedSchema(t, line)
}

// A configured (non-credential-shaped) server name on an allowed dispatch
// must survive verbatim — masking must be conditional on the value looking
// like a credential, not a blanket wipe of server/tool.
func TestRedaction_ConfiguredServerNameSurvivesVerbatim(t *testing.T) {
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n
	att.Server = "jira-prod-01" // 40-char-ish alphanumeric, not credential-shaped per spec fixture intent

	line, err := audit.NewAuthz(audit.AuthzInput{
		Ts: fixedTS, Attempt: att, Caller: agentCaller(),
		Decision: "allow", Reason: "none",
	})
	if err != nil {
		t.Fatalf("NewAuthz: %v", err)
	}
	obj := decodeLine(t, line)
	if obj["server"] != "jira-prod-01" {
		t.Fatalf("configured server name must survive verbatim, got %v", obj["server"])
	}
}

// TestRedaction_ArgsSHA256AndEmailHashSurviveVerbatim proves the
// defence-in-depth whole-line pass excludes the generic high-entropy rule:
// a 64-hex-char args_sha256/email_hash must never be masked, or the line
// would fail schema validation after the pass.
func TestRedaction_ArgsSHA256AndEmailHashSurviveVerbatim(t *testing.T) {
	sum, n := argsHash(`{"a":1}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n

	line, err := audit.NewAuthz(audit.AuthzInput{
		Ts: fixedTS, Attempt: att, Caller: agentCaller(),
		Decision: "allow", Reason: "none",
	})
	if err != nil {
		t.Fatalf("NewAuthz: %v", err)
	}
	obj := decodeLine(t, line)
	if obj["args_sha256"] != sum {
		t.Fatalf("args_sha256 must survive the whole-line sanitizer pass byte-identical: got %v want %s", obj["args_sha256"], sum)
	}
	validateAgainstPublishedSchema(t, line)
}

// TestSanitizerWholeLinePass_IsIdentityOnBuilderOutput asserts the
// defence-in-depth whole-line pass (audit.SanitizeLine, per-field masking
// having already run at build time) is a no-op on every well-formed
// constructor output — because every caller/operator-controlled field was
// already sanitised per field, the whole-line pass can only fire on a
// builder bug, which is what SanitizeLine's hit-reporting is for.
func TestSanitizerWholeLinePass_IsIdentityOnBuilderOutput(t *testing.T) {
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n

	fixtures := []audit.Line{}
	authzLine, err := audit.NewAuthz(audit.AuthzInput{Ts: fixedTS, Attempt: att, Caller: agentCaller(), Decision: "allow", Reason: "none"})
	if err != nil {
		t.Fatalf("NewAuthz: %v", err)
	}
	fixtures = append(fixtures, authzLine)

	toolCallLine, err := audit.NewToolCall(audit.ToolCallInput{Ts: fixedTS, Attempt: att, Caller: agentCaller(), Outcome: "success", DurationMs: 1})
	if err != nil {
		t.Fatalf("NewToolCall: %v", err)
	}
	fixtures = append(fixtures, toolCallLine)

	authEventLine, err := audit.NewAuthEvent(audit.AuthEventInput{
		Ts: fixedTS, RequestID: "req-1", Origin: "local", Source: "api", Surface: "login", Reason: "ok",
		Caller: audit.Caller{Kind: "session_user", UserID: "u1", Role: "user"},
	})
	if err != nil {
		t.Fatalf("NewAuthEvent: %v", err)
	}
	fixtures = append(fixtures, authEventLine)

	for i, line := range fixtures {
		raw, err := line.JSON()
		if err != nil {
			t.Fatalf("fixture %d: JSON(): %v", i, err)
		}
		sanitised, hit := audit.SanitizeLine(raw)
		if hit {
			t.Fatalf("fixture %d: whole-line sanitizer pass must not fire on well-formed builder output", i)
		}
		if !bytes.Equal(sanitised, raw) {
			t.Fatalf("fixture %d: whole-line sanitizer pass must be the identity on builder output:\n got: %s\nwant: %s", i, sanitised, raw)
		}
	}

	// Simulated builder bug: a fixed-prefix credential leaked past the
	// per-field pass. The whole-line pass must catch it and report a hit.
	authzRaw, err := authzLine.JSON()
	if err != nil {
		t.Fatalf("authzLine.JSON(): %v", err)
	}
	buggy := bytes.Replace(authzRaw, []byte(`"jira"`), []byte(`"`+akiaSentinel+`"`), 1)
	sanitised, hit := audit.SanitizeLine(buggy)
	if !hit {
		t.Fatalf("whole-line sanitizer pass must report a hit on a planted credential")
	}
	if bytes.Contains(sanitised, []byte(akiaSentinel)) {
		t.Fatalf("whole-line sanitizer pass must mask a planted credential: %s", sanitised)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func decodeLine(t *testing.T, line audit.Line) map[string]interface{} {
	t.Helper()
	raw, err := line.JSON()
	if err != nil {
		t.Fatalf("line.JSON(): %v", err)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("json.Unmarshal(line): %v\nraw: %s", err, raw)
	}
	return obj
}

func assertSentinelAbsent(t *testing.T, line audit.Line, sentinel string) {
	t.Helper()
	raw, err := line.JSON()
	if err != nil {
		t.Fatalf("line.JSON(): %v", err)
	}
	if bytes.Contains(raw, []byte(sentinel)) {
		t.Fatalf("sentinel %q must be byte-absent from the line, got: %s", sentinel, raw)
	}
}

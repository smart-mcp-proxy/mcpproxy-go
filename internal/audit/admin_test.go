// Spec 133 PR-1a: the `admin_action` builder (internal/audit/admin.go)
// against the binding wire schema and the line contract
// (specs/133-admin-action-audit/contracts/admin-action-line.md).
package audit_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
)

func tokenRotateInput() audit.AdminActionInput {
	return audit.AdminActionInput{
		Ts:            fixedTS,
		RequestID:     "01JD5QREQ",
		Origin:        "socket",
		Source:        "api",
		Caller:        audit.Caller{Kind: "socket"},
		ClientName:    "cli/0.77.0",
		ClientSurface: "cli",
		Action:        audit.ActionTokenRotate,
		Target:        audit.AgentTokenTarget("ci-bot", "tok_7q2m9x4kab", ""),
		Changes: []audit.Change{
			audit.Literal(audit.FieldTokenPrefix, audit.TokenPrefix("mcp_agt_a1b2"), audit.TokenPrefix("mcp_agt_9f8e")),
		},
	}
}

func mustAdmin(t *testing.T, in audit.AdminActionInput) (audit.Line, map[string]interface{}) {
	t.Helper()
	line, err := audit.NewAdminAction(in)
	if err != nil {
		t.Fatalf("NewAdminAction: %v", err)
	}
	validateAgainstPublishedSchema(t, line)
	validateProducerKeys(t, line)
	return line, decodeLine(t, line)
}

func TestNewAdminAction_TokenRotateMatchesContractExample(t *testing.T) {
	_, obj := mustAdmin(t, tokenRotateInput())
	want := `{"action":"token.rotate","caller":{"kind":"socket"},"changes":[{"after":"mcp_agt_9f8e","before":"mcp_agt_a1b2","field":"/token_prefix"}],"client":{"name":"cli/0.77.0","surface":"cli"},"event":"admin_action","origin":"socket","outcome":"success","request_id":"01JD5QREQ","schema_version":1,"source":"api","target":{"kind":"agent_token","name":"ci-bot","ref":"tok_7q2m9x4kab"},"ts":"2026-09-17T10:00:00.000000001Z"}`
	got, _ := json.Marshal(obj)
	if string(got) != want {
		t.Fatalf("line mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestNewAdminAction_RejectsUnknownAction(t *testing.T) {
	in := tokenRotateInput()
	in.Action = "token.explode"
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("unknown action must be rejected")
	}
}

func TestNewAdminAction_TriggerIffBackgroundKinds(t *testing.T) {
	in := tokenRotateInput()
	in.Trigger = audit.TriggerStartup
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("trigger on a request caller must be rejected")
	}

	in = tokenRotateInput()
	in.Caller = audit.Caller{Kind: "internal"}
	in.Origin, in.Source = "local", "internal"
	in.RequestID = ""
	in.ClientName, in.ClientSurface = "", ""
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("internal caller without a trigger must be rejected")
	}
	in.Trigger = audit.TriggerPolicy
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("trigger:policy without a policy must be rejected")
	}
	in.Policy = audit.PolicyRotationReconcile
	_, obj := mustAdmin(t, in)
	if _, ok := obj["request_id"]; ok {
		t.Fatal("background line must omit request_id, never \"\"")
	}
	in.Trigger = audit.TriggerStartup
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("policy without trigger:policy must be rejected")
	}
}

func TestNewAdminAction_NoneAndCLIOfflineNeverCarryRequestID(t *testing.T) {
	for _, kind := range []string{"none", "cli_offline"} {
		in := tokenRotateInput()
		in.Caller = audit.Caller{Kind: kind}
		in.Origin, in.Source = "local", "internal"
		if kind == "none" {
			in.Trigger = audit.TriggerConfigFile
		}
		if _, err := audit.NewAdminAction(in); err == nil {
			t.Fatalf("%s with a request_id must be rejected", kind)
		}
		in.RequestID = ""
		in.ClientName, in.ClientSurface = "", ""
		mustAdmin(t, in)
	}
	in := tokenRotateInput()
	in.Caller = audit.Caller{Kind: "cli_offline"}
	in.RequestID = ""
	in.Trigger = audit.TriggerPolicy
	in.Policy = audit.PolicyRotationReconcile
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("cli_offline with a trigger must be rejected")
	}
}

func TestNewAdminAction_BackgroundCallerCarriesNoIdentity(t *testing.T) {
	in := tokenRotateInput()
	in.Caller = audit.Caller{Kind: "internal", UserID: "u1"}
	in.Trigger, in.Policy = audit.TriggerPolicy, audit.PolicyRotationReconcile
	in.RequestID = ""
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("identity on a background caller must be rejected")
	}
}

func TestNewAdminAction_CredentialShapedRequestIDIsMasked(t *testing.T) {
	in := tokenRotateInput()
	in.RequestID = "github_pat_11ABCDEFGHIJKLMNOPQRSTUV_0123456789"
	before := audit.FieldMaskHits()
	line, obj := mustAdmin(t, in)
	if obj["request_id_masked"] != true {
		t.Fatalf("masked request_id must set request_id_masked: %v", obj)
	}
	assertSentinelAbsent(t, line, "github_pat_11ABCDEFGHIJKLMNOPQRSTUV_0123456789")
	if audit.FieldMaskHits() <= before {
		t.Fatal("a per-field mask hit must be counted")
	}
}

func TestNewAdminAction_LongValidRequestIDKeptWhole(t *testing.T) {
	in := tokenRotateInput()
	in.RequestID = strings.Repeat("a", 256)
	_, obj := mustAdmin(t, in)
	if obj["request_id"] != in.RequestID {
		t.Fatal("a 256-character request id must not be capped")
	}
	if _, ok := obj["request_id_masked"]; ok {
		t.Fatal("request_id_masked must be absent when nothing was masked")
	}
}

func TestNewAdminAction_ProxyTokenShapesMasked(t *testing.T) {
	for _, secret := range []string{
		"mcp_agt_0123456789abcdef0123",
		"mcp_cli_0123456789abcdef0123",
		"mcp_oat_0123456789abcdef0123",
		"mcp_ort_0123456789abcdef0123",
	} {
		in := tokenRotateInput()
		in.Target = audit.AgentTokenTarget("x-"+secret, "tok_7q2m9x4kab", "")
		line, _ := mustAdmin(t, in)
		assertSentinelAbsent(t, line, secret)
	}
	// A display prefix (12 characters) never matches.
	_, obj := mustAdmin(t, tokenRotateInput())
	ch := obj["changes"].([]interface{})[0].(map[string]interface{})
	if ch["before"] != "mcp_agt_a1b2" {
		t.Fatalf("display prefix must survive verbatim: %v", ch)
	}
}

func TestNewAdminAction_PrivateKeyLabelWithheld(t *testing.T) {
	pem := "-----BEGIN RSA PRIVATE KEY-----MIIEsecretbody"
	in := tokenRotateInput()
	in.Target = audit.AgentTokenTarget(pem, "tok_7q2m9x4kab", "")
	in.ClientName = pem
	line, obj := mustAdmin(t, in)
	assertSentinelAbsent(t, line, "MIIEsecretbody")
	assertSentinelAbsent(t, line, "PRIVATE KEY")
	tgt := obj["target"].(map[string]interface{})
	if tgt["name_withheld"] != true || tgt["name"] != nil {
		t.Fatalf("a private-key name must become name_withheld: %v", tgt)
	}
	if c, ok := obj["client"].(map[string]interface{}); ok && c["name"] != nil {
		t.Fatalf("a private-key client.name must be omitted: %v", c)
	}
}

func TestNewAdminAction_LabelCappedAt64AfterMasking(t *testing.T) {
	// A credential that straddles the 64-character cap is masked before the cut.
	name := strings.Repeat("n", 50) + "ghp_0123456789abcdefghij"
	in := tokenRotateInput()
	in.Target = audit.AgentTokenTarget(name, "tok_7q2m9x4kab", "")
	line, obj := mustAdmin(t, in)
	assertSentinelAbsent(t, line, "ghp_0123456789")
	got := obj["target"].(map[string]interface{})["name"].(string)
	if n := len([]rune(got)); n > 64 {
		t.Fatalf("target.name must be capped at 64 runes, got %d", n)
	}
}

func TestLiteral_TypeMismatchIsWithheld(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.Literal(audit.FieldTokenPrefix, audit.Bool(true), audit.Bool(false))}
	_, obj := mustAdmin(t, in)
	ch := obj["changes"].([]interface{})[0].(map[string]interface{})
	if ch["withheld"] != "value" || ch["before"] != nil || ch["after"] != nil {
		t.Fatalf("a literal whose type does not match the classifier must be withheld: %v", ch)
	}
}

func TestLiteral_InvalidTokenPrefixIsWithheld(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.Literal(audit.FieldTokenPrefix, nil, audit.TokenPrefix("not a prefix at all, way too long"))}
	_, obj := mustAdmin(t, in)
	ch := obj["changes"].([]interface{})[0].(map[string]interface{})
	if ch["withheld"] != "value" {
		t.Fatalf("an out-of-set prefix must be withheld: %v", ch)
	}
}

func TestClassifier_SecretAndUnknownPaths(t *testing.T) {
	cases := map[string]string{
		"/env/GITHUB_TOKEN":      "secret",
		"/headers/Authorization": "secret",
		"/oauth/client_secret":   "secret",
		"/api_key":               "secret",
		"/args":                  "value",
		"/something_new":         "value",
	}
	for path, want := range cases {
		if got := audit.ClassifyField(audit.ParsePointer(path)); got != want {
			t.Errorf("ClassifyField(%s) = %q, want %q", path, got, want)
		}
	}
	if got := audit.ClassifyField(audit.FieldTokenPrefix); got != "literal" {
		t.Errorf("token_prefix must be literal, got %q", got)
	}
}

func TestChange_PointerEscapes(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.Withheld(audit.Path("env", "a.b/c~d"), audit.WithheldSecret)}
	_, obj := mustAdmin(t, in)
	ch := obj["changes"].([]interface{})[0].(map[string]interface{})
	if ch["field"] != "/env/a.b~1c~0d" {
		t.Fatalf("JSON pointer escaping: got %v", ch["field"])
	}
}

func TestNewAdminAction_ChangesSortedAndCapped(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = nil
	for i := 0; i < 200; i++ {
		in.Changes = append(in.Changes, audit.Withheld(audit.Path("env", strings.Repeat("k", 40)+string(rune('a'+i%26))+strings.Repeat("z", i%7)), audit.WithheldSecret))
	}
	line, obj := mustAdmin(t, in)
	raw, _ := line.JSON()
	if len(raw) > audit.MaxAdminLineBytes {
		t.Fatalf("line exceeds the 16 KiB budget: %d", len(raw))
	}
	if obj["changes_truncated"] != true {
		t.Fatal("changes_truncated must be set when changes were capped")
	}
	if int(obj["changes_count"].(float64)) != 200 {
		t.Fatalf("changes_count must report the full set: %v", obj["changes_count"])
	}
	if n := len(obj["changes"].([]interface{})); n > 128 {
		t.Fatalf("changes must be capped at 128, got %d", n)
	}
}

func TestNewAdminAction_SessionEmailOmittedWhenInvalid(t *testing.T) {
	in := tokenRotateInput()
	in.Origin = "local"
	in.Caller = audit.Caller{Kind: "session_admin", UserID: "u1", UserEmail: "not-an-email", Role: "admin", Provider: "oidc"}
	_, obj := mustAdmin(t, in)
	c := obj["caller"].(map[string]interface{})
	if _, ok := c["user_email"]; ok {
		t.Fatalf("invalid email must be omitted: %v", c)
	}
	if c["user_id"] != "u1" {
		t.Fatal("user_id must stay")
	}
}

func TestNewAdminAction_SanitizerIsIdentity(t *testing.T) {
	line, _ := mustAdmin(t, tokenRotateInput())
	raw, _ := line.JSON()
	out, hit := audit.SanitizeLine(raw)
	if hit || !bytes.Equal(out, raw) {
		t.Fatalf("whole-line sanitizer must be the identity on admin builder output")
	}
}

func TestNewAdminAction_TimestampLiteral(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenCreate
	exp := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	in.Changes = []audit.Change{
		audit.Literal(audit.FieldTokenPrefix, nil, audit.TokenPrefix("mcp_agt_9f8e")),
		audit.Literal(audit.FieldExpiresAt, nil, audit.Timestamp(exp)),
	}
	_, obj := mustAdmin(t, in)
	chs := obj["changes"].([]interface{})
	if chs[0].(map[string]interface{})["field"] != "/expires_at" {
		t.Fatalf("changes must be sorted by field: %v", chs)
	}
	if chs[0].(map[string]interface{})["after"] != "2027-01-02T03:04:05.000000000Z" {
		t.Fatalf("timestamp literal: %v", chs[0])
	}
}

func TestNewAdminAction_RequiresTheLifecyclePrefixes(t *testing.T) {
	in := tokenRotateInput()
	in.Changes = []audit.Change{audit.Literal(audit.FieldTokenPrefix, nil, audit.TokenPrefix("mcp_agt_9f8e"))}
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("token.rotate without the before prefix must be rejected")
	}
	in.Changes = nil
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("token.rotate without changes must be rejected")
	}
}

func TestNewAdminAction_RequestCallerRequiresRequestID(t *testing.T) {
	in := tokenRotateInput()
	in.RequestID = ""
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("a socket caller without request_id must be rejected")
	}
}

func TestChange_PointerSegmentRules(t *testing.T) {
	render := func(c audit.Change) map[string]interface{} {
		in := tokenRotateInput()
		in.Action = audit.ActionTokenDelete
		in.Changes = []audit.Change{c}
		line, obj := mustAdmin(t, in)
		assertSentinelAbsent(t, line, "MIIEsecret")
		return obj["changes"].([]interface{})[0].(map[string]interface{})
	}
	pem := render(audit.Withheld(audit.Path("env", "-----BEGIN PRIVATE KEY-----MIIEsecret"), audit.WithheldSecret))
	if pem["field"] != "/env" || pem["field_masked"] != true {
		t.Fatalf("a private-key segment collapses to its fixed parent: %v", pem)
	}
	opaque := render(audit.Withheld(audit.Path("server_edition", "private-key-name"), audit.WithheldValue))
	if opaque["field"] != "/server_edition" || opaque["field_masked"] != true {
		t.Fatalf("below an opaque block the change is reported at the parent: %v", opaque)
	}
	// A credential straddling the 64-character segment cap is masked first.
	straddle := render(audit.Withheld(audit.Path("env", strings.Repeat("k", 50)+"ghp_0123456789abcdefghij"), audit.WithheldSecret))
	if strings.Contains(straddle["field"].(string), "ghp_01") {
		t.Fatalf("mask before cap: %v", straddle)
	}
}

func TestChange_DynamicSegmentOnlyWhereAllowed(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.Withheld(audit.Path("args", "positional-password"), audit.WithheldValue)}
	line, obj := mustAdmin(t, in)
	assertSentinelAbsent(t, line, "positional-password")
	ch := obj["changes"].([]interface{})[0].(map[string]interface{})
	if ch["field"] != "/args" || ch["field_masked"] != true {
		t.Fatalf("a segment below a non-map root collapses to the root: %v", ch)
	}
}

func TestChange_UnicodePointerWithinCapsKeepsItsKey(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	key := strings.Repeat("\U0001F600", 64)
	in.Changes = []audit.Change{audit.Withheld(audit.Path("env", key), audit.WithheldSecret)}
	_, obj := mustAdmin(t, in)
	ch := obj["changes"].([]interface{})[0].(map[string]interface{})
	if ch["field"] != "/env/"+key {
		t.Fatalf("a 69-character pointer is within the 256-character cap: %v", ch["field"])
	}
}

func TestNewAdminAction_CapCannotDropTheRequiredPrefix(t *testing.T) {
	in := tokenRotateInput()
	for i := 0; i < 130; i++ {
		in.Changes = append(in.Changes, audit.Withheld(audit.Path("env", "K"+strings.Repeat("a", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26))), audit.WithheldSecret))
	}
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("a line whose caps drop the required /token_prefix entry must be rejected")
	}
}

func TestMask_ProxyTokenAfterPATIsFullyMasked(t *testing.T) {
	in := tokenRotateInput()
	in.RequestID = "github_pat_11ABCDEFGHIJKLMNOPQRSTUVmcp_agt_secret-tail-0123456789"
	line, _ := mustAdmin(t, in)
	assertSentinelAbsent(t, line, "secret-tail-0123456789")
}

func TestTarget_RefMustBeStable(t *testing.T) {
	in := tokenRotateInput()
	in.Target = audit.AgentTokenTarget("ci", "tok_AKIAABCDEFGH", "")
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("an upper-case ref (which the sanitizer could rewrite) must be rejected")
	}
}

func TestChange_OAuthPathStopsAtTheFixedField(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.Withheld(audit.Path("oauth", "client_secret", "the-secret-value"), audit.WithheldSecret)}
	line, obj := mustAdmin(t, in)
	assertSentinelAbsent(t, line, "the-secret-value")
	if f := obj["changes"].([]interface{})[0].(map[string]interface{})["field"]; f != "/oauth/client_secret" {
		t.Fatalf("got %v", f)
	}
}

func TestChange_OAuthUnknownChildCollapses(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.Withheld(audit.Path("oauth", "private-customer-hostname"), audit.WithheldSecret)}
	line, obj := mustAdmin(t, in)
	assertSentinelAbsent(t, line, "private-customer-hostname")
	if f := obj["changes"].([]interface{})[0].(map[string]interface{})["field"]; f != "/oauth" {
		t.Fatalf("got %v", f)
	}
}

func TestChange_EmptyPathIsARejectionNotAPanic(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.Withheld(audit.ParsePointer(""), audit.WithheldValue)}
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("an empty field path must be rejected")
	}
}

func TestNewAdminAction_PrefixSidesAreExact(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenCreate
	in.Changes = []audit.Change{audit.Literal(audit.FieldTokenPrefix, audit.TokenPrefix("mcp_agt_a1b2"), audit.TokenPrefix("mcp_agt_9f8e"))}
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("token.create has no before prefix")
	}
	in.Action = audit.ActionTokenRevoke
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("token.revoke has no after prefix")
	}
}

func TestChange_UnknownRootIsNeverSpelled(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.Withheld(audit.Path("private-customer-hostname"), audit.WithheldValue)}
	line, obj := mustAdmin(t, in)
	assertSentinelAbsent(t, line, "private-customer-hostname")
	if f := obj["changes"].([]interface{})[0].(map[string]interface{})["field"]; f != "" {
		t.Fatalf("got %v", f)
	}
}

func TestLiteral_ValueFixedAtConstruction(t *testing.T) {
	before := audit.TokenPrefix("mcp_agt_a1b2")
	after := audit.TokenPrefix("mcp_agt_9f8e")
	c := audit.Literal(audit.FieldTokenPrefix, &before, &after)
	before, after = "not a prefix", "not a prefix"
	in := tokenRotateInput()
	in.Changes = []audit.Change{c}
	_, obj := mustAdmin(t, in)
	ch := obj["changes"].([]interface{})[0].(map[string]interface{})
	if ch["before"] != "mcp_agt_a1b2" || ch["after"] != "mcp_agt_9f8e" {
		t.Fatalf("a value mutated after Literal returned must not change the line: %v", ch)
	}
}

func TestNewAdminAction_NoRequestMeansInternalLocal(t *testing.T) {
	in := tokenRotateInput()
	in.Caller = audit.Caller{Kind: "cli_offline"}
	in.RequestID = ""
	in.ClientName, in.ClientSurface = "", ""
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("no request with source api/origin socket must be rejected")
	}
}

func TestMask_JWTAfterProxyTokenIsFullyMasked(t *testing.T) {
	in := tokenRotateInput()
	in.Target = audit.AgentTokenTarget("mcp_agt_0123456789abcdefeyJhbGciOi.eyJzdWIiOiJ4In0.c2lnbmF0dXJlLXRhaWw", "tok_7q2m9x4kab", "")
	line, _ := mustAdmin(t, in)
	assertSentinelAbsent(t, line, "eyJzdWIiOiJ4In0")
	assertSentinelAbsent(t, line, "c2lnbmF0dXJlLXRhaWw")
}

func TestNewAdminAction_RequestLineIsNeverSourceInternal(t *testing.T) {
	in := tokenRotateInput()
	in.Source = "internal"
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("a line with a request_id cannot be source internal")
	}
}

func TestChange_RateLimitAndAllServersLiterals(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenCreate
	in.Changes = []audit.Change{
		audit.Literal(audit.FieldTokenPrefix, nil, audit.TokenPrefix("mcp_agt_9f8e")),
		audit.Literal(audit.Path("rate_limit", "per_minute"), nil, audit.RateLimitValue(0)),
		audit.Literal(audit.Path("rate_limit", "burst"), nil, audit.RateLimitValue(1000000)),
		audit.AllServersGranted(audit.Path("allowed_servers")),
	}
	_, obj := mustAdmin(t, in)
	byField := map[string]map[string]interface{}{}
	for _, c := range obj["changes"].([]interface{}) {
		m := c.(map[string]interface{})
		byField[m["field"].(string)] = m
	}
	if byField["/rate_limit/per_minute"]["after"] != float64(0) {
		t.Fatalf("per_minute 0 is a literal: %v", byField["/rate_limit/per_minute"])
	}
	if byField["/rate_limit/burst"]["withheld"] != "value" {
		t.Fatalf("an out-of-bounds integer is withheld: %v", byField["/rate_limit/burst"])
	}
	if a := byField["/allowed_servers"]["added"].([]interface{}); len(a) != 1 || a[0] != "*" {
		t.Fatalf("the * grant is shown: %v", byField["/allowed_servers"])
	}
}

func TestAllServersGranted_OnlyOnTheAllowList(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.AllServersGranted(audit.Path("env", "GITHUB_TOKEN"))}
	_, obj := mustAdmin(t, in)
	if ch := obj["changes"].([]interface{})[0].(map[string]interface{}); ch["withheld"] != "secret" {
		t.Fatalf("a secret path is withheld, never a list: %v", ch)
	}
}

func TestMask_ClassicTokenRunningIntoFineGrainedPAT(t *testing.T) {
	in := tokenRotateInput()
	in.RequestID = "ghp_0123456789abcdefghijgithub_pat_11ABCDEFGHIJKLMNOPQRSTUV_secrettail0123456789"
	line, _ := mustAdmin(t, in)
	assertSentinelAbsent(t, line, "secrettail0123456789")
}

func TestLiteral_FlattenedSegmentIsNotLiteral(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.Literal(audit.Path("rate_limit/per_minute"), nil, audit.RateLimitValue(5))}
	_, obj := mustAdmin(t, in)
	if ch := obj["changes"].([]interface{})[0].(map[string]interface{}); ch["withheld"] != "value" {
		t.Fatalf("a segment containing '/' is never literal: %v", ch)
	}
}

func TestAllServersGrantedAmong_ShowsTheWildcardAndCountsTheRest(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.AllServersGrantedAmong(audit.Path("allowed_servers"), 2)}
	_, obj := mustAdmin(t, in)
	ch := obj["changes"].([]interface{})[0].(map[string]interface{})
	if a := ch["added"].([]interface{}); len(a) != 1 || a[0] != "*" || ch["added_count"] != float64(2) || ch["truncated"] != true {
		t.Fatalf("got %v", ch)
	}
}

func TestNewAdminAction_PEMRequestIDIsRejected(t *testing.T) {
	in := tokenRotateInput()
	in.RequestID = "-----BEGIN PRIVATE KEY-----MIIEbody"
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("a private-key request id is a rejected record")
	}
}

func TestNewAdminAction_OnePrefixEntryOnly(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenCreate
	in.Changes = []audit.Change{
		audit.Literal(audit.FieldTokenPrefix, nil, audit.TokenPrefix("mcp_agt_9f8e")),
		audit.Literal(audit.FieldTokenPrefix, audit.TokenPrefix("mcp_agt_a1b2"), nil),
	}
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("conflicting /token_prefix entries must be rejected")
	}
}

func TestMaskAllSpans_BoundedOnRepeatedPrefixes(t *testing.T) {
	s := strings.Repeat("mcp_agt_", 20000)
	start := time.Now()
	in := tokenRotateInput()
	in.ClientName = s
	_, _ = audit.NewAdminAction(in)
	if time.Since(start) > 2*time.Second {
		t.Fatal("masking a label of repeated prefixes must stay bounded")
	}
}

func TestNewAdminAction_CollapsedPathCountsAsAPrefixEntry(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenCreate
	in.Changes = []audit.Change{
		audit.Literal(audit.FieldTokenPrefix, nil, audit.TokenPrefix("mcp_agt_9f8e")),
		audit.Withheld(audit.Path("token_prefix", "extra"), audit.WithheldValue),
	}
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("a path that renders as /token_prefix is a second prefix entry")
	}
}

func TestTimestamp_OutOfRangeYearIsWithheld(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.Literal(audit.FieldExpiresAt, nil, audit.Timestamp(time.Date(9999, 12, 31, 23, 30, 0, 0, time.FixedZone("x", -3600))))}
	_, obj := mustAdmin(t, in)
	if ch := obj["changes"].([]interface{})[0].(map[string]interface{}); ch["withheld"] != "value" {
		t.Fatalf("a five-digit UTC year is outside the wire layout: %v", ch)
	}
}

func TestNewAdminAction_ClientIPMustBeAnIP(t *testing.T) {
	in := tokenRotateInput()
	in.ClientIP = "@/tmp/-----BEGIN PRIVATE KEY-----MIIEbody"
	line, obj := mustAdmin(t, in)
	assertSentinelAbsent(t, line, "MIIEbody")
	if c := obj["client"].(map[string]interface{}); c["ip"] != nil {
		t.Fatalf("a non-IP peer label is omitted: %v", c)
	}
	in.ClientIP = "10.0.0.7"
	_, obj = mustAdmin(t, in)
	if obj["client"].(map[string]interface{})["ip"] != "10.0.0.7" {
		t.Fatal("an IP is kept")
	}
}

// The schema cannot compare changes_count with the number of entries; the
// builder guarantees it exceeds them whenever changes_truncated is set.
func TestNewAdminAction_ChangesCountExceedsKeptEntries(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	for i := 0; i < 140; i++ {
		in.Changes = append(in.Changes, audit.Withheld(audit.Path("env", "K"+strings.Repeat("a", i%7)+string(rune('a'+i%26))+string(rune('a'+i/26))), audit.WithheldSecret))
	}
	_, obj := mustAdmin(t, in)
	if obj["changes_truncated"] != true || int(obj["changes_count"].(float64)) <= len(obj["changes"].([]interface{})) {
		t.Fatalf("changes_count must exceed the kept entries when truncated: %v / %d", obj["changes_count"], len(obj["changes"].([]interface{})))
	}
}

func TestChange_ServerAndProfilePathsAreBounded(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{
		audit.Withheld(audit.Path("mcpServers", "ci", "args", "positional-password"), audit.WithheldValue),
		audit.Withheld(audit.Path("profiles", "dev", "nested", "free-text"), audit.WithheldValue),
	}
	line, obj := mustAdmin(t, in)
	assertSentinelAbsent(t, line, "positional-password")
	assertSentinelAbsent(t, line, "free-text")
	got := map[string]bool{}
	for _, c := range obj["changes"].([]interface{}) {
		got[c.(map[string]interface{})["field"].(string)] = true
	}
	if !got["/mcpServers"] || !got["/profiles/dev"] {
		t.Fatalf("got %v", got)
	}
}

func TestNewAdminAction_ProviderAndRoleAreClosed(t *testing.T) {
	in := tokenRotateInput()
	in.Origin = "local"
	in.Caller = audit.Caller{Kind: "session_admin", UserID: "u1", Role: "admin", Provider: "saml"}
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("a provider outside the closed set is rejected")
	}
}

func TestLiteral_TypedNilIsAnAbsentSide(t *testing.T) {
	var unset *audit.Timestamp
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.Literal(audit.FieldExpiresAt, unset, audit.Timestamp(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)))}
	_, obj := mustAdmin(t, in)
	ch := obj["changes"].([]interface{})[0].(map[string]interface{})
	if _, has := ch["before"]; has || ch["after"] == nil {
		t.Fatalf("a typed nil side is absent: %v", ch)
	}
}

func TestRenderLabel_ControlCharactersReplaced(t *testing.T) {
	in := tokenRotateInput()
	in.ClientName = "\x0eyJabc.eyJdef.signature"
	line, obj := mustAdmin(t, in)
	raw, _ := line.JSON()
	if out, hit := audit.SanitizeLine(raw); hit || string(out) != string(raw) {
		t.Fatalf("the sanitizer must not rewrite a label across a JSON escape: %s", raw)
	}
	if n := obj["client"].(map[string]interface{})["name"].(string); strings.ContainsRune(n, 0x0e) {
		t.Fatalf("hex-escaped control characters are replaced: %q", n)
	}
	in.ClientName = "build\trunner"
	_, obj = mustAdmin(t, in)
	if n := obj["client"].(map[string]interface{})["name"]; n != "build\trunner" {
		t.Fatalf("a tab in a chosen label is kept: %q", n)
	}
}

func TestNewAdminAction_TimestampYearBound(t *testing.T) {
	in := tokenRotateInput()
	in.Ts = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("a five-digit year is rejected")
	}
}

func TestRenderLabel_BearerWithATabIsMasked(t *testing.T) {
	in := tokenRotateInput()
	in.ClientName = "Bearer\tSecretExample123"
	line, _ := mustAdmin(t, in)
	assertSentinelAbsent(t, line, "SecretExample123")
}

func TestAdminLine_NoHTMLEscapeNextToALabel(t *testing.T) {
	in := tokenRotateInput()
	in.ClientName = ">yJabc.eyJdef.signature"
	line, obj := mustAdmin(t, in)
	raw, _ := line.JSON()
	if out, hit := audit.SanitizeLine(raw); hit || string(out) != string(raw) {
		t.Fatalf("no escape may join a label into a credential shape: %s", raw)
	}
	if obj["client"].(map[string]interface{})["name"] != ">yJabc.eyJdef.signature" {
		t.Fatalf("got %v", obj["client"])
	}
}

func TestChange_PointerSegmentHexEscapedControl(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.Withheld(audit.Path("env", "\x0eyJabc.eyJdef.signature"), audit.WithheldSecret)}
	line, obj := mustAdmin(t, in)
	raw, _ := line.JSON()
	if out, hit := audit.SanitizeLine(raw); hit || string(out) != string(raw) {
		t.Fatalf("no escape may join a pointer segment into a credential shape: %s", raw)
	}
	if obj["changes"].([]interface{})[0].(map[string]interface{})["field_masked"] != true {
		t.Fatal("a replaced character marks the pointer field_masked")
	}
}

func TestNewAdminAction_NoneTakesConfigFileOnly(t *testing.T) {
	in := tokenRotateInput()
	in.Caller = audit.Caller{Kind: "none"}
	in.RequestID, in.ClientName, in.ClientSurface = "", "", ""
	in.Origin, in.Source = "local", "internal"
	in.Trigger, in.Policy = audit.TriggerPolicy, audit.PolicyRotationReconcile
	if _, err := audit.NewAdminAction(in); err == nil {
		t.Fatal("a policy decision is internal, never none")
	}
}

func TestChange_CredentialAcrossSegmentsCollapses(t *testing.T) {
	in := tokenRotateInput()
	in.Action = audit.ActionTokenDelete
	in.Changes = []audit.Change{audit.Withheld(audit.Path("profiles", "Bearer ", "description"), audit.WithheldValue)}
	line, obj := mustAdmin(t, in)
	raw, _ := line.JSON()
	if out, hit := audit.SanitizeLine(raw); hit || string(out) != string(raw) {
		t.Fatalf("the sanitizer must not rewrite a pointer: %s", raw)
	}
	ch := obj["changes"].([]interface{})[0].(map[string]interface{})
	if ch["field"] != "/profiles" || ch["field_masked"] != true {
		t.Fatalf("got %v", ch)
	}
}

func TestRenderLabel_CertificateHeaderIsNotAPrivateKey(t *testing.T) {
	in := tokenRotateInput()
	in.ClientName = "-----BEGIN CERTIFICATE----- (PRIVATE KEY stored separately)"
	_, obj := mustAdmin(t, in)
	if obj["client"].(map[string]interface{})["name"] == nil {
		t.Fatal("a certificate header is not a private-key block")
	}
}

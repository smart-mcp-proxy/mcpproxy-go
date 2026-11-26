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
		Target:        audit.AgentTokenTarget("ci-bot", "tok_7Q2M9X4KAB", ""),
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
	want := `{"action":"token.rotate","caller":{"kind":"socket"},"changes":[{"after":"mcp_agt_9f8e","before":"mcp_agt_a1b2","field":"/token_prefix"}],"client":{"name":"cli/0.77.0","surface":"cli"},"event":"admin_action","origin":"socket","outcome":"success","request_id":"01JD5QREQ","schema_version":1,"source":"api","target":{"kind":"agent_token","name":"ci-bot","ref":"tok_7Q2M9X4KAB"},"ts":"2026-09-17T10:00:00.000000001Z"}`
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
		in.Target = audit.AgentTokenTarget("x-"+secret, "tok_7Q2M9X4KAB", "")
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
	in.Target = audit.AgentTokenTarget(pem, "tok_7Q2M9X4KAB", "")
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
	in.Target = audit.AgentTokenTarget(name, "tok_7Q2M9X4KAB", "")
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

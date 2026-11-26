// T097 (Spec 107 PR-D): schema conformance for internal/audit.
//
// [compile-red until T099] — see the package doc comment in line_test.go.
//
// This file loads the binding wire schema (contracts/audit-line.schema.json)
// once, validates every builder-produced fixture against it, validates the
// schema's own `examples` array, derives a *producer-strict* variant
// (additionalProperties flipped to false at every object level, in memory —
// the published file itself stays consumer-tolerant per its own
// `description`) to prove the exact key set each constructor emits, runs the
// per-event/per-caller-kind negative fixtures enumerated in tasks.md T097,
// and asserts the schema/doc copy stay in identity lockstep.
package audit_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
)

const (
	contractSchemaPath  = "../../specs/107-server-edition-sso-hardening/contracts/audit-line.schema.json"
	publishedSchemaPath = "../../docs/schemas/audit-line-v1.schema.json"
)

// ---------------------------------------------------------------------------
// schema loading
// ---------------------------------------------------------------------------

func loadSchemaDoc(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshalling %s: %v", path, err)
	}
	return doc
}

func compileSchemaFromDoc(t *testing.T, url string, doc map[string]interface{}) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, doc); err != nil {
		t.Fatalf("AddResource(%s): %v", url, err)
	}
	sch, err := c.Compile(url)
	if err != nil {
		t.Fatalf("Compile(%s): %v", url, err)
	}
	return sch
}

var permissiveSchema *jsonschema.Schema

func publishedSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	if permissiveSchema != nil {
		return permissiveSchema
	}
	doc := loadSchemaDoc(t, contractSchemaPath)
	permissiveSchema = compileSchemaFromDoc(t, "mem://audit-line-permissive.json", doc)
	return permissiveSchema
}

func validateAgainstPublishedSchema(t *testing.T, line audit.Line) {
	t.Helper()
	raw, err := line.JSON()
	if err != nil {
		t.Fatalf("line.JSON(): %v", err)
	}
	validateBytesAgainstSchema(t, publishedSchema(t), raw)
}

func validateBytesAgainstSchema(t *testing.T, sch *jsonschema.Schema, raw []byte) {
	t.Helper()
	var inst interface{}
	if err := json.Unmarshal(raw, &inst); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, raw)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("schema validation failed: %v\nline: %s", err, raw)
	}
}

func mustFail(t *testing.T, sch *jsonschema.Schema, obj map[string]interface{}, label string) {
	t.Helper()
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("%s: json.Marshal: %v", label, err)
	}
	var inst interface{}
	if err := json.Unmarshal(raw, &inst); err != nil {
		t.Fatalf("%s: json.Unmarshal: %v", label, err)
	}
	if err := sch.Validate(inst); err == nil {
		t.Fatalf("%s: expected schema validation to reject fixture, but it passed: %s", label, raw)
	}
}

// ---------------------------------------------------------------------------
// examples
// ---------------------------------------------------------------------------

func TestSchemaExamplesValidate(t *testing.T) {
	doc := loadSchemaDoc(t, contractSchemaPath)
	examples, ok := doc["examples"].([]interface{})
	if !ok || len(examples) == 0 {
		t.Fatalf("contracts/audit-line.schema.json: expected a non-empty top-level examples array")
	}
	sch := publishedSchema(t)
	for i, ex := range examples {
		if err := sch.Validate(ex); err != nil {
			t.Fatalf("examples[%d] failed to validate: %v\n%v", i, err, ex)
		}
	}
}

// ---------------------------------------------------------------------------
// producer-strict key set
// ---------------------------------------------------------------------------

func TestProducerStrictSchema_BuilderOutputHasExactKeySet(t *testing.T) {
	sum, n := argsHash(`{}`)
	att := baseAttempt()
	att.ArgsSHA256, att.ArgsBytes = sum, n

	authzLine, err := audit.NewAuthz(audit.AuthzInput{Ts: fixedTS, Attempt: att, Caller: agentCaller(), Decision: "allow", Reason: "none"})
	if err != nil {
		t.Fatalf("NewAuthz: %v", err)
	}
	toolCallLine, err := audit.NewToolCall(audit.ToolCallInput{Ts: fixedTS, Attempt: att, Caller: agentCaller(), Outcome: "success", DurationMs: 1})
	if err != nil {
		t.Fatalf("NewToolCall: %v", err)
	}
	authEventLine, err := audit.NewAuthEvent(audit.AuthEventInput{
		Ts: fixedTS, RequestID: "req-1", Origin: "local", Source: "api", Surface: "login", Reason: "ok",
		Caller: audit.Caller{Kind: "session_user", UserID: "u1", Role: "user"},
	})
	if err != nil {
		t.Fatalf("NewAuthEvent: %v", err)
	}
	adminLine, err := audit.NewAdminAction(tokenRotateInput())
	if err != nil {
		t.Fatalf("NewAdminAction: %v", err)
	}

	for name, line := range map[string]audit.Line{
		"authz":        authzLine,
		"tool_call":    toolCallLine,
		"auth_event":   authEventLine,
		"admin_action": adminLine,
	} {
		t.Run(name, func(t *testing.T) {
			validateAgainstPublishedSchema(t, line)
			validateProducerKeys(t, line)
		})
	}
}

// producerKeys is the exact key set each event kind may emit (Spec 133 plan
// "Schema": event-specific exact-key checks replace the recursive
// additionalProperties strictifier, which closed partial if/then schemas and
// could not express per-event exclusivity). The published schema stays
// consumer-tolerant; this is the producer property.
var (
	commonKeys   = []string{"schema_version", "ts", "event", "request_id", "origin", "source", "caller", "client", "profile"}
	dispatchKeys = []string{"transport_request_id", "parent_id", "session_id", "work_session_id", "surface", "server", "tool", "operation", "args_sha256", "args_bytes"}

	producerTopKeys = map[string][]string{
		"authz":        append(append([]string{"decision", "reason", "disclosed"}, commonKeys...), dispatchKeys...),
		"tool_call":    append(append([]string{"outcome", "reason", "error_class", "duration_ms", "request_bytes", "response_bytes"}, commonKeys...), dispatchKeys...),
		"auth_event":   append([]string{"surface", "reason", "flags"}, commonKeys...),
		"admin_action": append([]string{"action", "target", "outcome", "changes", "changes_truncated", "changes_count", "request_id_masked", "trigger", "policy"}, commonKeys...),
	}
	legacyCallerKeys   = []string{"kind", "user_id", "user_email", "email_hash", "role", "provider", "token_name", "token_prefix", "profile_pin"}
	producerCallerKeys = map[string][]string{
		"authz":        legacyCallerKeys,
		"tool_call":    legacyCallerKeys,
		"auth_event":   legacyCallerKeys,
		"admin_action": {"kind", "user_id", "user_email", "role", "provider", "token_name", "token_prefix", "profile_pin", "token_name_withheld", "ref"},
	}
	producerClientKeys = map[string][]string{
		"authz":        {"name", "version", "ip"},
		"tool_call":    {"name", "version", "ip"},
		"auth_event":   {"name", "version", "ip"},
		"admin_action": {"name", "version", "ip", "surface"},
	}
	adminTargetKeys = []string{"kind", "name", "name_withheld", "ref", "owner_user_id", "client_id", "client_id_withheld", "client_ref",
		"grant_id", "authorization_id", "server", "server_withheld", "server_ref", "count", "names", "truncated", "names_withheld",
		"source", "authority", "digest_prefix"}
	adminChangeKeys = []string{"field", "before", "after", "withheld", "field_masked", "segment_ref", "added", "removed",
		"added_count", "removed_count", "unresolved", "truncated"}
)

func keyViolations(where string, obj map[string]interface{}, allowed []string) []string {
	set := map[string]bool{}
	for _, k := range allowed {
		set[k] = true
	}
	var out []string
	for k := range obj {
		if !set[k] {
			out = append(out, where+"."+k)
		}
	}
	return out
}

// producerKeyViolations returns every key the producer may not emit on the
// line's event kind.
func producerKeyViolations(obj map[string]interface{}) []string {
	event, _ := obj["event"].(string)
	top, ok := producerTopKeys[event]
	if !ok {
		return []string{"event:" + event}
	}
	out := keyViolations("line", obj, top)
	if c, ok := obj["caller"].(map[string]interface{}); ok {
		out = append(out, keyViolations("caller", c, producerCallerKeys[event])...)
	}
	if c, ok := obj["client"].(map[string]interface{}); ok {
		out = append(out, keyViolations("client", c, producerClientKeys[event])...)
	}
	if event == "admin_action" {
		if tg, ok := obj["target"].(map[string]interface{}); ok {
			out = append(out, keyViolations("target", tg, adminTargetKeys)...)
		}
		if chs, ok := obj["changes"].([]interface{}); ok {
			for _, ch := range chs {
				if m, ok := ch.(map[string]interface{}); ok {
					out = append(out, keyViolations("changes[]", m, adminChangeKeys)...)
				}
			}
		}
	}
	return out
}

func validateProducerKeys(t *testing.T, line audit.Line) {
	t.Helper()
	obj := decodeLine(t, line)
	if v := producerKeyViolations(obj); len(v) > 0 {
		raw, _ := line.JSON()
		t.Fatalf("producer emitted keys outside its event's set: %v\nline: %s", v, raw)
	}
}

// TestLegacyLinesStillValidate pins FR-002/FR-003: every pre-133 example, and
// an old line carrying shipper extension keys that collide with the new
// admin_action keys (target, changes, action, client.surface), validates
// under the widened published schema unchanged; the producer-strict check
// rejects the extension keys (it was never meant for consumers).
func TestLegacyLinesStillValidate(t *testing.T) {
	raw, err := os.ReadFile("testdata/legacy_pre133_lines.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	sch := publishedSchema(t)
	lines := splitLines(raw)
	if len(lines) < 7 {
		t.Fatalf("expected the six pre-133 examples and one extension-key line, got %d", len(lines))
	}
	for i, l := range lines {
		if len(l) == 0 {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal(l, &obj); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if err := sch.Validate(obj); err != nil {
			t.Fatalf("legacy line %d no longer validates: %v\n%s", i, err, l)
		}
		_, hasExt := obj["target"]
		if v := producerKeyViolations(obj); hasExt != (len(v) > 0) {
			t.Fatalf("legacy line %d: producer-strict violations %v (extension keys present: %v)", i, v, hasExt)
		}
	}
}

func baseAdminFixture(overrides map[string]interface{}) map[string]interface{} {
	fx := map[string]interface{}{
		"schema_version": 1, "ts": "2026-09-17T10:00:00.000000001Z", "event": "admin_action",
		"request_id": "req-1", "origin": "local", "source": "api", "caller": map[string]interface{}{"kind": "api_key"},
		"action": "token.revoke", "outcome": "success",
		"target": map[string]interface{}{"kind": "agent_token", "name": "ci", "ref": "tok_ABCDEFGH"},
	}
	for k, v := range overrides {
		if v == nil {
			delete(fx, k)
			continue
		}
		fx[k] = v
	}
	return fx
}

func TestAdminActionSchema_PositiveAndNegative(t *testing.T) {
	sch := publishedSchema(t)
	ok := []map[string]interface{}{
		baseAdminFixture(nil),
		baseAdminFixture(map[string]interface{}{"request_id": nil, "source": "internal", "caller": map[string]interface{}{"kind": "internal"}, "trigger": "policy", "policy": "rotation_reconcile"}),
		baseAdminFixture(map[string]interface{}{"request_id": nil, "caller": map[string]interface{}{"kind": "cli_offline"}}),
		baseAdminFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "agent_token", "token_prefix": "mcp_agt_ab12", "token_name_withheld": true, "ref": "cli_ABCDEFGH"}}),
		baseAdminFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "session_user", "user_id": "u1", "role": "user", "provider": "oidc"}}),
		baseAdminFixture(map[string]interface{}{"target": map[string]interface{}{"kind": "agent_token", "name_withheld": true, "ref": "tok_ABCDEFGH"}}),
	}
	for i, fx := range ok {
		if err := sch.Validate(roundTrip(t, fx)); err != nil {
			t.Fatalf("positive fixture %d rejected: %v", i, err)
		}
	}
	bad := map[string]map[string]interface{}{
		"unknown action":                 baseAdminFixture(map[string]interface{}{"action": "token.explode"}),
		"outcome error":                  baseAdminFixture(map[string]interface{}{"outcome": "error"}),
		"missing target":                 baseAdminFixture(map[string]interface{}{"target": nil}),
		"forbidden server":               baseAdminFixture(map[string]interface{}{"server": "jira"}),
		"forbidden surface":              baseAdminFixture(map[string]interface{}{"surface": "rest"}),
		"forbidden transport id":         baseAdminFixture(map[string]interface{}{"transport_request_id": "x"}),
		"trigger on api_key":             baseAdminFixture(map[string]interface{}{"trigger": "startup"}),
		"internal without trigger":       baseAdminFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "internal"}}),
		"policy without trigger":         baseAdminFixture(map[string]interface{}{"policy": "scan_clean"}),
		"trigger policy no policy":       baseAdminFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "internal"}, "trigger": "policy"}),
		"none with request_id":           baseAdminFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "none"}, "trigger": "config_file"}),
		"cli_offline with request":       baseAdminFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "cli_offline"}}),
		"name and name_withheld":         baseAdminFixture(map[string]interface{}{"target": map[string]interface{}{"kind": "agent_token", "name": "a", "name_withheld": true, "ref": "tok_ABCDEFGH"}}),
		"agent target without ref":       baseAdminFixture(map[string]interface{}{"target": map[string]interface{}{"kind": "agent_token", "name": "a"}}),
		"malformed ref":                  baseAdminFixture(map[string]interface{}{"target": map[string]interface{}{"kind": "agent_token", "name": "a", "ref": "ci-bot"}}),
		"identity on internal":           baseAdminFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "internal", "user_id": "u1"}, "trigger": "startup", "request_id": nil}),
		"agent without prefix":           baseAdminFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "agent_token", "token_name": "t"}}),
		"agent both shapes":              baseAdminFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "agent_token", "token_prefix": "mcp_agt_ab12", "token_name": "t", "token_name_withheld": true, "ref": "cli_ABCDEFGH"}}),
		"user_id without provider":       baseAdminFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "session_admin", "user_id": "u1", "role": "admin"}}),
		"masked flag without id":         baseAdminFixture(map[string]interface{}{"request_id": nil, "request_id_masked": true, "caller": map[string]interface{}{"kind": "cli_offline"}}),
		"changes_count alone":            baseAdminFixture(map[string]interface{}{"changes_count": 3}),
		"change with value and withheld": baseAdminFixture(map[string]interface{}{"changes": []interface{}{map[string]interface{}{"field": "/enabled", "before": true, "withheld": "value"}}}),
		"change with object value":       baseAdminFixture(map[string]interface{}{"changes": []interface{}{map[string]interface{}{"field": "/args", "after": map[string]interface{}{"x": 1}}}}),
		"change field not a pointer":     baseAdminFixture(map[string]interface{}{"changes": []interface{}{map[string]interface{}{"field": "enabled", "withheld": "value"}}}),
		"bad client surface":             baseAdminFixture(map[string]interface{}{"client": map[string]interface{}{"surface": "tray"}}),
		"secret target with name":        baseAdminFixture(map[string]interface{}{"target": map[string]interface{}{"kind": "secret", "name": "GITHUB_TOKEN", "name_withheld": true, "ref": "sec_ABCDEFGH"}}),
		"none on authz":                  baseAuthzFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "none"}}),
	}
	for name, fx := range bad {
		t.Run(name, func(t *testing.T) { mustFail(t, sch, fx, name) })
	}
}

func roundTrip(t *testing.T, v interface{}) interface{} {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestAdminActionVocabularyMatchesSchema pins the builder's closed action
// list to the schema's action enum.
func TestAdminActionVocabularyMatchesSchema(t *testing.T) {
	doc := loadSchemaDoc(t, contractSchemaPath)
	var enum []interface{}
	for _, b := range doc["allOf"].([]interface{}) {
		m := b.(map[string]interface{})
		cond, _ := m["if"].(map[string]interface{})
		props, _ := cond["properties"].(map[string]interface{})
		ev, _ := props["event"].(map[string]interface{})
		if ev["const"] == "admin_action" {
			enum = m["then"].(map[string]interface{})["properties"].(map[string]interface{})["action"].(map[string]interface{})["enum"].([]interface{})
		}
	}
	got := audit.AdminActionSorted()
	if len(enum) != len(got) {
		t.Fatalf("schema has %d actions, builder %d", len(enum), len(got))
	}
	for i, a := range enum {
		if a.(string) != got[i] {
			t.Fatalf("action %d: schema %q, builder %q (keep both sorted and identical)", i, a, got[i])
		}
	}
}

// ---------------------------------------------------------------------------
// negative identity fixtures (tasks.md T097)
// ---------------------------------------------------------------------------

func baseAuthzFixture(overrides map[string]interface{}) map[string]interface{} {
	fx := map[string]interface{}{
		"schema_version": 1,
		"ts":             "2026-09-17T10:00:00.000000001Z",
		"event":          "authz",
		"request_id":     "req-1",
		"origin":         "local",
		"source":         "mcp",
		"surface":        "call_tool_write",
		"server":         "jira",
		"tool":           "create_issue",
		"operation":      "write",
		"decision":       "allow",
		"reason":         "none",
		"args_sha256":    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"args_bytes":     2,
		"caller":         map[string]interface{}{"kind": "api_key"},
	}
	for k, v := range overrides {
		fx[k] = v
	}
	return fx
}

func baseAuthEventFixture(caller map[string]interface{}, reason string) map[string]interface{} {
	return map[string]interface{}{
		"schema_version": 1,
		"ts":             "2026-09-17T10:00:00.000000001Z",
		"event":          "auth_event",
		"request_id":     "req-1",
		"origin":         "local",
		"source":         "api",
		"surface":        "login",
		"reason":         reason,
		"caller":         caller,
	}
}

func TestNegativeFixtures_CallerIdentityRules(t *testing.T) {
	sch := publishedSchema(t)

	cases := []struct {
		name string
		obj  map[string]interface{}
	}{
		{"session_user without user_id", baseAuthEventFixture(map[string]interface{}{"kind": "session_user", "role": "user"}, "ok")},
		{"session_user with wrong role", baseAuthEventFixture(map[string]interface{}{"kind": "session_user", "user_id": "u1", "role": "admin"}, "ok")},
		{"session_admin without user_id", baseAuthEventFixture(map[string]interface{}{"kind": "session_admin", "role": "admin"}, "ok")},
		{"session_admin with wrong role", baseAuthEventFixture(map[string]interface{}{"kind": "session_admin", "user_id": "u1", "role": "user"}, "ok")},
		{"anonymous with user_id", baseAuthEventFixture(map[string]interface{}{"kind": "anonymous", "user_id": "u1"}, "nonce_mismatch")},
		{"anonymous with provider", baseAuthEventFixture(map[string]interface{}{"kind": "anonymous", "provider": "google"}, "nonce_mismatch")},
		{"agent_token without token_name/token_prefix", baseAuthzFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "agent_token"}})},
		{"owned agent_token missing user_email/role/provider", baseAuthzFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "agent_token", "token_name": "t", "token_prefix": "mcp_agt_ab12", "user_id": "u1"}})},
		{"ownerless agent_token carrying role", baseAuthzFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "agent_token", "token_name": "t", "token_prefix": "mcp_agt_ab12", "role": "user"}})},
		{"ownerless agent_token carrying provider", baseAuthzFixture(map[string]interface{}{"caller": map[string]interface{}{"kind": "agent_token", "token_name": "t", "token_prefix": "mcp_agt_ab12", "provider": "google"}})},
		{"email_hash beside user_id", baseAuthEventFixture(map[string]interface{}{"kind": "session_user", "user_id": "u1", "role": "user", "email_hash": stringOfLen("a", 64)}, "ok")},
		{"auth_event with user_email", baseAuthEventFixture(map[string]interface{}{"kind": "session_user", "user_id": "u1", "role": "user", "user_email": "alice@example.com"}, "ok")},
		{"surface login with reason logout", baseAuthEventFixture(map[string]interface{}{"kind": "session_user", "user_id": "u1", "role": "user"}, "logout")},
		{"surface logout with reason ok", map[string]interface{}{
			"schema_version": 1, "ts": "2026-09-17T10:00:00.000000001Z", "event": "auth_event",
			"request_id": "req-1", "origin": "local", "source": "api", "surface": "logout", "reason": "ok",
			"caller": map[string]interface{}{"kind": "session_user", "user_id": "u1", "role": "user"},
		}},
		{"reason:ok with anonymous caller", baseAuthEventFixture(map[string]interface{}{"kind": "anonymous"}, "ok")},
		{"reason:ok without user_id", baseAuthEventFixture(map[string]interface{}{"kind": "session_user", "role": "user"}, "ok")},
		{"reason:logout without user_id", baseAuthEventFixture(map[string]interface{}{"kind": "session_user", "role": "user"}, "logout")},
		{"reason:subject_mismatch with anonymous caller", baseAuthEventFixture(map[string]interface{}{"kind": "anonymous"}, "subject_mismatch")},
		{"reason:user_disabled without user_id", baseAuthEventFixture(map[string]interface{}{"kind": "session_admin", "role": "admin"}, "user_disabled")},
		{"domain_not_allowed without email_hash", baseAuthEventFixture(map[string]interface{}{"kind": "anonymous"}, "domain_not_allowed")},
		{"domain_not_allowed with session caller", baseAuthEventFixture(map[string]interface{}{"kind": "session_user", "user_id": "u1", "role": "user"}, "domain_not_allowed")},
		{"userinfo_subject_mismatch without email_hash", baseAuthEventFixture(map[string]interface{}{"kind": "anonymous"}, "userinfo_subject_mismatch")},
		{"provider_error with user_id", baseAuthEventFixture(map[string]interface{}{"kind": "session_user", "user_id": "u1", "role": "user"}, "provider_error")},
		{"pre-identity reason with email_hash", baseAuthEventFixture(map[string]interface{}{"kind": "anonymous", "email_hash": stringOfLen("c", 64)}, "nonce_mismatch")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustFail(t, sch, tc.obj, tc.name)
		})
	}
}

func stringOfLen(ch string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ch[0]
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// schema-identity sync
// ---------------------------------------------------------------------------

func TestSchemaIdentitySync(t *testing.T) {
	contract := loadSchemaDoc(t, contractSchemaPath)

	version, ok := contract["properties"].(map[string]interface{})["schema_version"].(map[string]interface{})["const"].(float64)
	if !ok {
		t.Fatalf("contract schema: properties.schema_version.const is not a number")
	}
	n := int(version)

	id, _ := contract["$id"].(string)
	wantIDSuffix := "audit-line-v" + itoa(n) + ".json"
	if !hasSuffix(id, wantIDSuffix) {
		t.Fatalf("$id %q does not encode schema_version %d (want suffix %q)", id, n, wantIDSuffix)
	}

	title, _ := contract["title"].(string)
	wantTitleSuffix := "schema_version " + itoa(n) + ")"
	if !hasSuffix(title, wantTitleSuffix) {
		t.Fatalf("title %q does not encode schema_version %d (want suffix %q)", title, n, wantTitleSuffix)
	}

	wantPublishedPath := "../../docs/schemas/audit-line-v" + itoa(n) + ".schema.json"
	if wantPublishedPath != publishedSchemaPath {
		t.Fatalf("publishedSchemaPath %q does not match schema_version-derived path %q", publishedSchemaPath, wantPublishedPath)
	}

	published, err := os.ReadFile(publishedSchemaPath)
	if err != nil {
		t.Fatalf("reading published schema %s: %v (T099 must copy the contract to docs/schemas/)", publishedSchemaPath, err)
	}
	contractRaw, err := os.ReadFile(contractSchemaPath)
	if err != nil {
		t.Fatalf("reading contract schema %s: %v", contractSchemaPath, err)
	}
	if string(published) != string(contractRaw) {
		t.Fatalf("docs/schemas/audit-line-v%d.schema.json must be byte-identical to the contract copy", n)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// ---------------------------------------------------------------------------
// external JSONL gate (quickstart §6 / T116)
// ---------------------------------------------------------------------------

// TestExternalJSONLValidates lets the quickstart / T116 real-instance gate
// validate an actual sink file without npx/ajv-cli:
//
//	MCPPROXY_AUDIT_JSONL=<file> go test ./internal/audit -run TestExternalJSONLValidates
func TestExternalJSONLValidates(t *testing.T) {
	path := os.Getenv("MCPPROXY_AUDIT_JSONL")
	if path == "" {
		t.Skip("MCPPROXY_AUDIT_JSONL not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	sch := publishedSchema(t)
	lines := splitLines(raw)
	if len(lines) == 0 {
		t.Fatalf("%s: no lines to validate", path)
	}
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		validateBytesAgainstSchema(t, sch, line)
	}
}

func splitLines(raw []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range raw {
		if b == '\n' {
			out = append(out, raw[start:i])
			start = i + 1
		}
	}
	if start < len(raw) {
		out = append(out, raw[start:])
	}
	return out
}

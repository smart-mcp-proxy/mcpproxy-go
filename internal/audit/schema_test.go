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

// strictifyAdditionalProperties walks every JSON-Schema "object"-shaped node
// reachable from the document (properties/items/allOf/anyOf/oneOf/if/then/
// else) and sets additionalProperties:false wherever the node declares
// "properties" but no explicit additionalProperties — deriving the
// producer-strict variant referenced by T097 without touching the published
// (consumer-tolerant) file on disk.
func strictifyAdditionalProperties(node interface{}) interface{} {
	switch v := node.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for k, val := range v {
			out[k] = strictifyAdditionalProperties(val)
		}
		if _, hasProps := out["properties"]; hasProps {
			out["additionalProperties"] = false
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, val := range v {
			out[i] = strictifyAdditionalProperties(val)
		}
		return out
	default:
		return node
	}
}

func producerStrictSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	doc := loadSchemaDoc(t, contractSchemaPath)
	strict := strictifyAdditionalProperties(doc).(map[string]interface{})
	return compileSchemaFromDoc(t, "mem://audit-line-strict.json", strict)
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
	strict := producerStrictSchema(t)

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

	for name, line := range map[string]audit.Line{
		"authz":      authzLine,
		"tool_call":  toolCallLine,
		"auth_event": authEventLine,
	} {
		raw, err := line.JSON()
		if err != nil {
			t.Fatalf("%s: line.JSON(): %v", name, err)
		}
		validateBytesAgainstSchema(t, strict, raw)
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

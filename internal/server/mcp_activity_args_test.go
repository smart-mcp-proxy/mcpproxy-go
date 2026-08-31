package server

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Issue #1146: the activity log used to record only {operation, name} for
// upstream_servers / quarantine_security, dropping every mutation payload
// field. The whole point of an audit trail is to say WHAT changed.

const (
	testEnvSecret     = "sk-live-abcdef1234567890"
	testBearerSecret  = "ghp_realtokenAAAAAAAAAAAAAAAAAAAAAA"
	testClientSecret  = "cs-supersecret-9876543210"
	testURLTokenValue = "leakmeleakmeleakme"
)

func upstreamPatchRequest() mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation":      "patch",
			"name":           "github",
			"url":            "https://example.test/mcp?token=" + testURLTokenValue,
			"protocol":       "streamable-http",
			"command":        "npx",
			"enabled":        true,
			"trust_mode":     "manual",
			"init_timeout":   "30s",
			"args_json":      `["-y","@modelcontextprotocol/server-github"]`,
			"env_json":       `{"API_KEY":"` + testEnvSecret + `","LOG_LEVEL":"debug"}`,
			"headers_json":   `{"Authorization":"Bearer ` + testBearerSecret + `","Accept":"application/json"}`,
			"isolation_json": `{"enabled":true,"image":"python:3.12","memory_limit":"1g"}`,
			"oauth_json":     `{"client_id":"public-client","client_secret":"` + testClientSecret + `","scopes":["read"]}`,
		},
	}}
}

// (a) Every supplied parameter must survive into the activity arguments.
func TestActivityArgs_UpstreamPatch_RecordsMutatedFieldNames(t *testing.T) {
	req := upstreamPatchRequest()
	args := activityArgsFromRequest(req)
	require.NotNil(t, args)

	for key := range req.GetArguments() {
		assert.Containsf(t, args, key,
			"parameter %q was dropped from the activity record (issue #1146)", key)
	}
}

// (b) A secret-bearing value must be PRESENT but REDACTED — never absent, never
// in the clear. The operator has to see THAT API_KEY changed.
func TestActivityArgs_SecretsArePresentButRedacted(t *testing.T) {
	args := activityArgsFromRequest(upstreamPatchRequest())

	env, ok := args["env_json"].(map[string]interface{})
	require.Truef(t, ok, "env_json must be recorded as a parsed map, got %T", args["env_json"])

	apiKey, ok := env["API_KEY"].(string)
	require.True(t, ok, "the API_KEY name must survive so the operator sees which var changed")
	assert.NotEmpty(t, apiKey)
	assert.NotEqual(t, testEnvSecret, apiKey, "the raw secret must not be recorded")
	assert.True(t, strings.HasPrefix(apiKey, "••••"), "expected a masked value, got %q", apiKey)

	assert.Equal(t, "debug", env["LOG_LEVEL"], "ordinary env values must stay readable")
}

// (b') A single net that catches any field the walker forgets, present or
// future: no raw secret may survive anywhere in the recorded arguments.
func TestActivityArgs_NoSecretSurvivesAnywhere(t *testing.T) {
	args := activityArgsFromRequest(upstreamPatchRequest())
	encoded, err := json.Marshal(args)
	require.NoError(t, err)
	rendered := string(encoded)

	for _, secret := range []string{testEnvSecret, testBearerSecret, testClientSecret, testURLTokenValue} {
		assert.NotContainsf(t, rendered, secret,
			"a raw secret leaked into the activity arguments: %s", rendered)
	}
}

// (b”) Header names and URL structure stay readable; only the credential is
// masked.
func TestActivityArgs_HeadersAndURLRedacted(t *testing.T) {
	args := activityArgsFromRequest(upstreamPatchRequest())

	headers, ok := args["headers_json"].(map[string]interface{})
	require.Truef(t, ok, "headers_json must be recorded as a parsed map, got %T", args["headers_json"])
	authz, ok := headers["Authorization"].(string)
	require.True(t, ok, "the Authorization header NAME must survive")
	assert.NotContains(t, authz, testBearerSecret)
	assert.Equal(t, "application/json", headers["Accept"], "ordinary headers stay readable")

	rawURL, ok := args["url"].(string)
	require.True(t, ok)
	assert.Contains(t, rawURL, "https://example.test/mcp", "scheme/host/path must stay readable")
	assert.NotContains(t, rawURL, testURLTokenValue)
}

// (b”') ${keyring:...} references are labels, not secrets — masking them would
// destroy the very information the operator needs.
func TestActivityArgs_KeyringReferencePassesThrough(t *testing.T) {
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation": "patch",
			"name":      "github",
			"env_json":  `{"API_KEY":"${keyring:gh}"}`,
		},
	}}
	args := activityArgsFromRequest(req)
	env, ok := args["env_json"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "${keyring:gh}", env["API_KEY"])
}

// (b””) A malformed *_json parameter must still be recorded (scrubbed and
// capped), not silently dropped — a rejected mutation is audit-relevant too.
func TestActivityArgs_MalformedJSONParamIsScrubbedNotDropped(t *testing.T) {
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation": "patch",
			"name":      "github",
			"env_json":  `{not json`,
		},
	}}
	args := activityArgsFromRequest(req)
	require.Contains(t, args, "env_json")
	assert.Equal(t, `{not json`, args["env_json"])
}

// Over-masking is the safe direction, but the log must stay readable: ordinary
// configuration fields must not be turned into bullets.
func TestActivityArgs_OrdinaryFieldsStayReadable(t *testing.T) {
	args := activityArgsFromRequest(upstreamPatchRequest())

	assert.Equal(t, "patch", args["operation"])
	assert.Equal(t, "github", args["name"])
	assert.Equal(t, "npx", args["command"])
	assert.Equal(t, "streamable-http", args["protocol"])
	assert.Equal(t, true, args["enabled"])

	isolation, ok := args["isolation_json"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "python:3.12", isolation["image"])
	assert.Equal(t, true, isolation["enabled"])

	argsList, ok := args["args_json"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, []interface{}{"-y", "@modelcontextprotocol/server-github"}, argsList)
}

// A key literally named api_key pins the structure-aware walk: the naive
// marshal → RedactSensitiveData → unmarshal shortcut rewrites across the JSON
// closing quote and yields invalid JSON.
func TestActivityArgs_LowercaseSecretKeyStaysStructured(t *testing.T) {
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation": "patch",
			"name":      "github",
			"env_json":  `{"api_key":"` + testEnvSecret + `"}`,
		},
	}}
	args := activityArgsFromRequest(req)
	env, ok := args["env_json"].(map[string]interface{})
	require.Truef(t, ok, "env_json must stay a structured map, got %T", args["env_json"])
	require.Contains(t, env, "api_key")
	assert.NotContains(t, env["api_key"], testEnvSecret)
}

// A very long value must be capped so one mutation cannot bloat the store.
func TestActivityArgs_LongValuesAreCapped(t *testing.T) {
	long := strings.Repeat("é", activityArgValueLimit)
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation": "patch",
			"name":      "github",
			"command":   long,
		},
	}}
	args := activityArgsFromRequest(req)
	got, ok := args["command"].(string)
	require.True(t, ok)
	assert.LessOrEqual(t, len(got), activityArgValueLimit+len(activityErrorMessageEllipsis))
	assert.True(t, len(got) < len(long))
}

// (c) The resolved server name must reach the activity record so the Server
// column renders and `--server` filtering matches.
func TestActivityTargetServer_ResolvedFromNameParam(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"upstream patch", map[string]interface{}{"operation": "patch", "name": "github"}, "github"},
		{"quarantine approve", map[string]interface{}{"operation": "approve_tool", "name": "github", "tool_name": "x"}, "github"},
		{"list has no target", map[string]interface{}{"operation": "list"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: tc.args}}
			assert.Equal(t, tc.want, activityTargetServer(req))
		})
	}
}

// (c”) add_from_registry resolves its server name only in the response, so
// lift it from there rather than leaving the row unattributed.
func TestActivityTargetServer_AddFromRegistryLiftsResolvedName(t *testing.T) {
	assert.Equal(t, "pulse-weather",
		serverNameFromRegistryResult(`{"success":true,"server":{"name":"pulse-weather"}}`))
	assert.Empty(t, serverNameFromRegistryResult(`{"success":false,"message":"nope"}`))
	assert.Empty(t, serverNameFromRegistryResult("not json at all"))
	assert.Empty(t, serverNameFromRegistryResult(""))
}

// (c') Guard against a new emit site reintroducing the "-" Server column: no
// call inside these two handlers may hardcode the targetServer argument.
func TestActivityEmitsNeverHardcodeEmptyTargetServer(t *testing.T) {
	const targetServerArgIndex = 1
	guardedFuncs := map[string]bool{
		"handleUpstreamServers":    true,
		"handleQuarantineSecurity": true,
	}

	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	var offenders []string

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		require.NoErrorf(t, err, "parsing %s", name)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !guardedFuncs[fn.Name.Name] {
				continue
			}
			ast.Inspect(fn, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "emitActivityInternalToolCall" {
					return true
				}
				if len(call.Args) <= targetServerArgIndex {
					return true
				}
				if lit, ok := call.Args[targetServerArgIndex].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					offenders = append(offenders,
						fset.Position(lit.Pos()).String()+" (in "+fn.Name.Name+")")
				}
				return true
			})
		}
	}

	require.Emptyf(t, offenders,
		"these emit sites hardcode the target server, so the Activity Log Server "+
			"column renders '-' and --server filtering misses the row (issue "+
			"#1146); pass the resolved server name instead:\n%s",
		strings.Join(offenders, "\n"))
}

// (diff) The resolved before/after diff must keep field PATHS verbatim (that is
// the audit signal) while masking every value.
func TestRedactedConfigDiff_KeepsPathsMasksValues(t *testing.T) {
	diff := config.NewConfigDiff()
	diff.Modified["env"] = config.FieldChange{
		Path: "env",
		From: map[string]string{"API_KEY": "old-" + testEnvSecret},
		To:   map[string]string{"API_KEY": testEnvSecret},
	}
	diff.Modified["oauth"] = config.FieldChange{
		Path: "oauth",
		From: nil,
		To:   &config.OAuthConfig{ClientID: "public-client", ClientSecret: testClientSecret},
	}
	diff.Modified["url"] = config.FieldChange{
		Path: "url",
		From: "https://old.test/mcp",
		To:   "https://new.test/mcp",
	}
	diff.Removed = []string{"env.OLD_KEY"}

	redacted := redactedConfigDiff(diff)
	require.NotNil(t, redacted)

	modified, ok := redacted["modified"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, modified, "env")
	assert.Contains(t, modified, "oauth")
	assert.Contains(t, modified, "url")

	encoded, err := json.Marshal(redacted)
	require.NoError(t, err)
	rendered := string(encoded)
	assert.Contains(t, rendered, "env.OLD_KEY", "removed field paths are key names, keep them")
	assert.Contains(t, rendered, "https://new.test/mcp", "ordinary values stay readable")
	assert.NotContains(t, rendered, testEnvSecret)
	assert.NotContains(t, rendered, testClientSecret)

	assert.Nil(t, redactedConfigDiff(nil))
	assert.Nil(t, redactedConfigDiff(config.NewConfigDiff()))
}

// Numbers must render as numbers, not as 1.048576e+06.
func TestRedactedConfigDiff_NumbersRenderSanely(t *testing.T) {
	diff := config.NewConfigDiff()
	diff.Modified["isolation.memory_bytes"] = config.FieldChange{
		Path: "isolation.memory_bytes",
		From: 0,
		To:   1048576,
	}
	encoded, err := json.Marshal(redactedConfigDiff(diff))
	require.NoError(t, err)
	assert.Contains(t, string(encoded), "1048576")
	assert.NotContains(t, string(encoded), "e+06")
}

// (regression, Finding A) The patch response echoed the whole before/after
// config diff in the clear — env values, Authorization headers and
// oauth.client_secret — straight back to the calling agent AND into the
// persisted activity `response` field.
func TestPatchResponse_DoesNotEchoSecrets(t *testing.T) {
	proxy, _ := newStoredScriptProxy(t)

	seed := &config.ServerConfig{
		Name:     "secretful",
		URL:      "https://old.test/mcp",
		Protocol: "streamable-http",
		Enabled:  false,
		Env:      map[string]string{"API_KEY": "old-" + testEnvSecret},
		Headers:  map[string]string{"Authorization": "Bearer " + testBearerSecret},
	}
	_, err := proxy.storage.AddUpstream(seed)
	require.NoError(t, err)

	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation": "patch",
			"name":      "secretful",
			"env_json":  `{"API_KEY":"` + testEnvSecret + `"}`,
		},
	}}

	result, _, err := proxy.handlePatchUpstream(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)
	text := resultText(t, result)

	assert.NotContains(t, text, testEnvSecret, "the patch response echoed a secret in the clear")
	assert.NotContains(t, text, "old-"+testEnvSecret, "the patch response echoed the previous secret")
	assert.NotContains(t, text, testBearerSecret)

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &payload))
	changes, ok := payload["changes"].(map[string]interface{})
	require.Truef(t, ok, "the diff must still be reported, got %s", text)
	modified, ok := changes["modified"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, modified, "env", "the operator must still see WHICH field changed")
}

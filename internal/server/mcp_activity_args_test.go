package server

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
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

// (b””) A malformed *_json parameter must still be RECORDED — a rejected
// mutation is audit-relevant too — but not recorded verbatim: review round 3
// showed the old heuristic string scrubbing is regex-shaped and misses a
// payload that is merely malformed, so the placeholder reports that the
// parameter was sent, and its size, and withholds the bytes.
func TestActivityArgs_MalformedJSONParamIsRecordedAsPlaceholder(t *testing.T) {
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
	assert.Equal(t, "<unparsable json omitted: 9 bytes>", args["env_json"])
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
			operation, _ := tc.args["operation"].(string)
			assert.Equal(t, tc.want, activityTargetServer(req, operation))
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

// (5) MINOR/contract — masking the `changes` diff is agent-observable: an agent
// that reads modified[*].from/.to back to confirm what it wrote must still get
// the TRUTH for anything that is not a secret. Only secret-bearing values may
// mask. (That the description now says so is pinned by
// assertUpstreamServersDelta in mcp_menu_surface_test.go.)
func TestPatchResponse_NonSecretValuesRoundTripTruthfully(t *testing.T) {
	proxy, _ := newStoredScriptProxy(t)

	seed := &config.ServerConfig{
		Name:     "plain",
		URL:      "https://old.test/mcp",
		Protocol: "streamable-http",
		Enabled:  false,
	}
	_, err := proxy.storage.AddUpstream(seed)
	require.NoError(t, err)

	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation": "patch",
			"name":      "plain",
			"url":       "https://new.test/mcp",
			"enabled":   true,
		},
	}}

	result, _, err := proxy.handlePatchUpstream(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &payload))
	changes, ok := payload["changes"].(map[string]interface{})
	require.Truef(t, ok, "the diff must be reported, got %v", payload)
	modified, ok := changes["modified"].(map[string]interface{})
	require.True(t, ok)

	urlChange, ok := modified["url"].(map[string]interface{})
	require.Truef(t, ok, "url must appear in the diff, got %v", modified)
	assert.Equal(t, "https://old.test/mcp", urlChange["from"],
		"a non-secret value must round-trip exactly — an agent verifies what it wrote from this")
	assert.Equal(t, "https://new.test/mcp", urlChange["to"])

	if enabledChange, ok := modified["enabled"].(map[string]interface{}); ok {
		assert.Equal(t, false, enabledChange["from"])
		assert.Equal(t, true, enabledChange["to"])
	}
}

// ---------------------------------------------------------------------------
// Cross-model review of #1146 (round 1). Findings 1-4, each pinned here.
// ---------------------------------------------------------------------------

const (
	testArgvSecret       = "sk-live-1234567890abcdef"
	testArgvInlineSecret = "ghp_inlineAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

// (1) MAJOR — a credential passed as an argv TOKEN has no key to be judged by,
// so the env-var rule could not see it and recorded it verbatim. Passing a
// secret as `--api-key <value>` is one of the commonest MCP server config
// shapes, so this was a live leak on the exact path #1146 set out to seal.
func TestActivityArgs_ArgvSecretsAreMasked(t *testing.T) {
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation": "add",
			"name":      "x",
			"command":   "uvx",
			"args_json": `["mcp-foo","--api-key","` + testArgvSecret + `","--token=` + testArgvInlineSecret + `"]`,
		},
	}}

	argv, ok := activityArgsFromRequest(req)["args_json"].([]interface{})
	require.Truef(t, ok, "args_json must stay a list, got %T", activityArgsFromRequest(req)["args_json"])
	require.Len(t, argv, 4)

	assert.Equal(t, "mcp-foo", argv[0], "a package name is not a secret and must stay readable")
	assert.Equal(t, "--api-key", argv[1], "the flag NAMES the credential — keep it, that is the audit signal")
	assert.NotContains(t, argv[2], testArgvSecret, "the argv credential was recorded in the clear")
	assert.Contains(t, argv[2], "••••", "the credential must be present but masked, not dropped")
	assert.NotContains(t, argv[3], testArgvInlineSecret, "the inline --flag=secret form leaked")
	assert.Contains(t, argv[3], "--token=", "the inline flag name must survive the mask")
}

// (1') …and the same token reaching the record through the resolved config
// diff (`args` is a FieldChange whose From/To are []string).
func TestRedactedConfigDiff_ArgvSecretsAreMasked(t *testing.T) {
	diff := config.NewConfigDiff()
	diff.Modified["args"] = config.FieldChange{
		Path: "args",
		From: []string{"mcp-foo"},
		To:   []string{"mcp-foo", "--api-key", testArgvSecret},
	}

	encoded, err := json.Marshal(redactedConfigDiff(diff))
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), testArgvSecret,
		"the config diff recorded an argv credential in the clear")
	assert.Contains(t, string(encoded), "--api-key",
		"the flag name must survive so the operator knows WHICH credential moved")
}

// (1”) Over-masking would make the record useless. Ordinary argv tokens —
// package names, subcommands, paths, ports — must round-trip verbatim.
func TestActivityArgs_OrdinaryArgvStaysReadable(t *testing.T) {
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation": "add",
			"name":      "x",
			"args_json": `["-y","@modelcontextprotocol/server-github","--port","8080","--transport=stdio","/srv/data"]`,
		},
	}}
	argv, ok := activityArgsFromRequest(req)["args_json"].([]interface{})
	require.True(t, ok)
	assert.Equal(t,
		[]interface{}{"-y", "@modelcontextprotocol/server-github", "--port", "8080", "--transport=stdio", "/srv/data"},
		argv)
}

// (4) MINOR/security — `_auth_*` are keys MCPProxy injects into a call for its
// own use, and internal/runtime copies `_auth_user_id` / `_auth_user_email`
// straight onto ActivityRecord.UserID/UserEmail. Neither of these two handlers
// calls injectAuthMetadata, so anything under that prefix here can ONLY have
// come from the caller: capturing the whole request handed an agent a
// caller-controlled identity stamp on the audit row for a privileged mutation.
// The old {operation,name} allowlist dropped them by accident; drop them on
// purpose.
func TestActivityArgs_DropsCallerSuppliedAuthMetadata(t *testing.T) {
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation":        "remove",
			"name":             "github",
			"_auth_user_id":    "01HIMPERSONATED",
			"_auth_user_email": "victim@example.test",
			"_auth_auth_type":  "oauth",
		},
	}}
	args := activityArgsFromRequest(req)
	require.NotNil(t, args)

	for key := range args {
		assert.NotContainsf(t, key, "_auth_",
			"caller-supplied %q became a forged identity stamp on the audit row", key)
	}
	assert.Equal(t, "remove", args["operation"], "the real parameters must still be recorded")
	assert.Equal(t, "github", args["name"])
}

// (2) MINOR — `name` is agent-supplied and unvalidated. The inventory
// operations do not take the request at all, so attributing their activity row
// to whatever `name` the caller passed lets an agent stamp a row onto a server
// it never touched (and, through /api/v1/activity/summary, onto that server's
// traffic totals).
func TestActivityTargetServer_UnattributedForInventoryOperations(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		want      string
	}{
		{"upstream list ignores name", "list", ""},
		{"quarantine list ignores name", "list_quarantined", ""},
		{"unparsed operation", "", ""},
		{"unknown operation", "definitely-not-an-operation", ""},
		{"patch acts on name", "patch", "github"},
		{"remove acts on name", "remove", "github"},
		{"approve_tool acts on name", "approve_tool", "github"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := mcp.CallToolRequest{Params: mcp.CallToolParams{
				Arguments: map[string]interface{}{"operation": tc.operation, "name": "github"},
			}}
			assert.Equal(t, tc.want, activityTargetServer(req, tc.operation))
		})
	}
}

// (2') Rot guard, both directions: activityAttributedOps must be EXACTLY the
// operations whose switch arm is handed the request. A new mutating operation
// that forgets to register is unattributed — the issue-#1146 "-" Server column,
// back again; an inventory operation that wrongly appears is the misattribution
// this finding was about.
func TestActivityAttributedOps_MatchTheOperationSwitches(t *testing.T) {
	consts := parseServerStringConsts(t)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(".", "mcp.go"), nil, 0)
	require.NoError(t, err)

	guarded := map[string]bool{"handleUpstreamServers": true, "handleQuarantineSecurity": true}
	withRequest := map[string]bool{}
	withoutRequest := map[string]bool{}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !guarded[fn.Name.Name] {
			continue
		}
		ast.Inspect(fn, func(node ast.Node) bool {
			clause, isClause := node.(*ast.CaseClause)
			if !isClause || len(clause.List) == 0 || !caseBodyCallsHandler(clause) {
				return true
			}
			target := withoutRequest
			if caseBodyPassesRequest(clause) {
				target = withRequest
			}
			for _, expr := range clause.List {
				if op, resolved := resolveCaseString(expr, consts); resolved {
					target[op] = true
				}
			}
			return true
		})
	}

	require.NotEmpty(t, withRequest, "the AST walk found no operation arms — the guard would be vacuous")
	require.NotEmpty(t, withoutRequest, "the AST walk found no inventory arms — the guard would be vacuous")

	assert.Equal(t, sortedKeys(withRequest), sortedKeys(activityAttributedOps),
		"activityAttributedOps must list exactly the operations whose handler receives the request")

	for op := range withoutRequest {
		assert.Falsef(t, activityAttributedOps[op],
			"operation %q never receives the request, so the caller's `name` was not acted on", op)
	}
}

// parseServerStringConsts collects the package's `const x = "literal"` values
// so a case clause written as an identifier (operationList) can be resolved.
func parseServerStringConsts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		require.NoErrorf(t, err, "parsing %s", name)
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				unquoted, err := strconv.Unquote(lit.Value)
				if err == nil {
					out[vs.Names[0].Name] = unquoted
				}
			}
		}
	}
	require.NotEmpty(t, out)
	return out
}

func resolveCaseString(expr ast.Expr, consts map[string]string) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		unquoted, err := strconv.Unquote(e.Value)
		return unquoted, err == nil
	case *ast.Ident:
		v, ok := consts[e.Name]
		return v, ok
	}
	return "", false
}

// caseBodyCallsHandler reports whether the clause dispatches to a p.handleX
// method — i.e. it is one of the operation switch's arms rather than an
// unrelated switch (status strings, etc.).
func caseBodyCallsHandler(clause *ast.CaseClause) bool {
	found := false
	for _, stmt := range clause.Body {
		ast.Inspect(stmt, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && strings.HasPrefix(sel.Sel.Name, "handle") {
				found = true
			}
			return true
		})
	}
	return found
}

func caseBodyPassesRequest(clause *ast.CaseClause) bool {
	found := false
	for _, stmt := range clause.Body {
		ast.Inspect(stmt, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if ok && ident.Name == "request" {
				found = true
			}
			return true
		})
	}
	return found
}

package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #1146, review round 3. Every case here is a place where the record was
// decided by a KEY NAME alone — the flag before an argv value, the header name,
// the config field — and a credential whose name gave nothing away walked
// straight into BBolt, the SSE payload and `mcpproxy activity list`.

// argvCredentialShapes are credentials the value-shaped detector recognises on
// sight. The FLAG names are deliberately innocuous: none of them carries a
// marker from oauth.sensitiveEnvMarkers, so the key-name rule sees nothing.
var argvCredentialShapes = []struct {
	name   string
	flag   string
	secret string
}{
	{"github token", "--endpoint", "ghp_1234567890abcdefghij1234567890abcdef"},
	{"aws access key id", "--endpoint", "AKIAIOSFODNN7EXAMPLE"},
	{"slack bot token", "--session", "xoxb-123456789012-1234567890123-AbCdEfGh"},
	{"openai project key", "--cookie", "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"},
}

// (1) MAJOR — redactActivityArgv had two masking rules and only the
// space-separated one consulted the value-shaped detector. `--endpoint=ghp_…`
// was routed through the key-name rule alone, matched nothing, and was stored
// in the clear. Both argv spellings must reach the same verdict.
func TestActivityArgs_ArgvInlineFlagValuesConsultTheValueDetector(t *testing.T) {
	for _, shape := range argvCredentialShapes {
		t.Run(shape.name, func(t *testing.T) {
			inline := activityArgvFor(t, `["srv","`+shape.flag+`=`+shape.secret+`"]`)
			require.Len(t, inline, 2)
			assert.NotContainsf(t, inline[1], shape.secret,
				"inline %s=VALUE recorded the credential in the clear", shape.flag)
			assert.Contains(t, inline[1], shape.flag+"=",
				"the flag name is the audit signal and must survive the mask")

			spaced := activityArgvFor(t, `["srv","`+shape.flag+`","`+shape.secret+`"]`)
			require.Len(t, spaced, 3)
			assert.NotContainsf(t, spaced[2], shape.secret,
				"space-separated %s VALUE recorded the credential in the clear", shape.flag)
		})
	}
}

// (1') A positional credential — no flag at all — must be caught too.
func TestActivityArgs_PositionalArgvCredentialIsMasked(t *testing.T) {
	argv := activityArgvFor(t, `["srv","AKIAIOSFODNN7EXAMPLE"]`)
	require.Len(t, argv, 2)
	assert.NotContains(t, argv[1], "AKIAIOSFODNN7EXAMPLE")
}

func activityArgvFor(t *testing.T, argsJSON string) []interface{} {
	t.Helper()
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation": "add",
			"name":      "x",
			"command":   "npx",
			"args_json": argsJSON,
		},
	}}
	argv, ok := activityArgsFromRequest(req)["args_json"].([]interface{})
	require.Truef(t, ok, "args_json must stay a list, got %T", activityArgsFromRequest(req)["args_json"])
	return argv
}

// (2) Custom credential headers. oauth's header matcher recognises only whole
// delimiter-separated segments, so `X-AuthToken` / `X-SecretValue` fell through
// and an opaque value — one no value-shaped detector can recognise either —
// was recorded verbatim.
func TestActivityArgs_CustomCredentialHeaderNamesAreMasked(t *testing.T) {
	const (
		authTokenSecret = "hdr-secret-98765"
		secretValue     = "hdr-value-13579"
	)
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation":    "patch",
			"name":         "github",
			"headers_json": `{"X-AuthToken":"` + authTokenSecret + `","X-SecretValue":"` + secretValue + `","X-Session-Id":"` + testEnvSecret + `"}`,
		},
	}}

	headers, ok := activityArgsFromRequest(req)["headers_json"].(map[string]interface{})
	require.Truef(t, ok, "headers_json must stay a map, got %T", activityArgsFromRequest(req)["headers_json"])

	require.Contains(t, headers, "X-AuthToken", "the header NAME is the audit signal and must survive")
	assert.NotContains(t, headers["X-AuthToken"], authTokenSecret, "X-AuthToken value recorded in the clear")
	assert.NotContains(t, headers["X-SecretValue"], secretValue, "X-SecretValue value recorded in the clear")
	assert.NotContains(t, headers["X-Session-Id"], testEnvSecret, "X-Session-Id value recorded in the clear")
}

// (2') …and the same widening must not start masking ordinary headers, which
// is what makes the record worth reading.
func TestActivityArgs_OrdinaryHeadersStayReadable(t *testing.T) {
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation":    "patch",
			"name":         "github",
			"headers_json": `{"X-Author-ID":"42","X-Monkey-ID":"capuchin","Accept":"application/json"}`,
		},
	}}
	headers, ok := activityArgsFromRequest(req)["headers_json"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "42", headers["X-Author-ID"])
	assert.Equal(t, "capuchin", headers["X-Monkey-ID"])
	assert.Equal(t, "application/json", headers["Accept"])
}

// (3) The `add` operation emitted a SECOND activity row (config_change) built
// from the raw request values, so a URL credential survived there even though
// the internal-tool row masked it.
func TestRedactActivityConfigValues_MasksURLCredential(t *testing.T) {
	values := redactActivityConfigValues(map[string]interface{}{
		"protocol": "streamable-http",
		"enabled":  true,
		"url":      "https://host.test/mcp?token=" + testURLTokenValue,
		"command":  "npx",
	})

	encoded, err := json.Marshal(values)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), testURLTokenValue,
		"the config_change row recorded the URL credential in the clear")
	assert.Contains(t, values["url"], "https://host.test/mcp", "the endpoint must stay readable")
	assert.Equal(t, "npx", values["command"])
	assert.Equal(t, true, values["enabled"])
}

// (4) The mask itself must not be a fingerprint. `••••90 (22 chars)` persisted
// across BBolt, SSE and the CLI hands a reader the exact length and the last
// two bytes of every credential; on the audit surfaces the mask carries no
// length and no trailing bytes.
func TestActivityArgs_MaskCarriesNoLengthOrTail(t *testing.T) {
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation":    "patch",
			"name":         "github",
			"env_json":     `{"API_KEY":"` + testEnvSecret + `"}`,
			"headers_json": `{"Authorization":"` + testBearerSecret + `"}`,
			"url":          "https://host.test/mcp?token=" + testURLTokenValue,
		},
	}}
	args := activityArgsFromRequest(req)

	env, ok := args["env_json"].(map[string]interface{})
	require.True(t, ok)
	masked, ok := env["API_KEY"].(string)
	require.True(t, ok)
	assert.NotContains(t, masked, "chars", "the mask must not carry the credential length")
	assert.NotContains(t, masked, testEnvSecret[len(testEnvSecret)-2:],
		"the mask must not carry the credential's trailing bytes")

	headers, ok := args["headers_json"].(map[string]interface{})
	require.True(t, ok)
	authz, ok := headers["Authorization"].(string)
	require.True(t, ok)
	assert.NotContains(t, authz, "chars")
	assert.NotContains(t, authz, testBearerSecret[len(testBearerSecret)-2:])

	rawURL, ok := args["url"].(string)
	require.True(t, ok)
	assert.NotContains(t, rawURL, "chars")
	assert.NotContains(t, rawURL, testURLTokenValue[len(testURLTokenValue)-2:])
}

// (5) A *_json argument that does not parse used to fall back to heuristic
// string scrubbing — which is regex-shaped and misses `{"API_KEY" "sk-live-…"}`
// entirely. A payload we could not understand must fail CLOSED.
func TestActivityArgs_UnparsableJSONFailsClosed(t *testing.T) {
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation": "patch",
			"name":      "github",
			"env_json":  `{"API_KEY" "` + testEnvSecret + `"}`,
		},
	}}
	args := activityArgsFromRequest(req)

	require.Contains(t, args, "env_json", "a rejected mutation is still audit-relevant — record THAT it happened")
	recorded, ok := args["env_json"].(string)
	require.True(t, ok)
	assert.NotContains(t, recorded, testEnvSecret, "unparsable JSON was stored in the clear")
	assert.Contains(t, recorded, "unparsable", "the placeholder must say why the payload is missing")
}

// (5') The literal `null` removal marker parses fine and carries nothing; it
// must stay legible rather than being replaced by the fail-closed placeholder.
func TestActivityArgs_NullJSONRemovalMarkerSurvives(t *testing.T) {
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation": "patch",
			"name":      "github",
			"env_json":  `null`,
		},
	}}
	args := activityArgsFromRequest(req)
	recorded, ok := args["env_json"].(string)
	require.True(t, ok)
	assert.Equal(t, "null", strings.TrimSpace(recorded))
}

// A credential-bearing connection string is masked whole once the value-shaped
// detector is in the chain, rather than kept structurally readable with only
// its password masked. That is a deliberate trade: the FIELD NAME is the audit
// signal, over-masking is the safe direction, and the detector's four-rune
// prefix still says which kind of URL it was. Pinned so it is a choice rather
// than a surprise. An ordinary URL with no embedded credential stays readable.
func TestActivityArgs_ConnectionStringsMaskWholeButPlainURLsStayReadable(t *testing.T) {
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "upstream_servers",
		Arguments: map[string]interface{}{
			"operation": "patch",
			"name":      "github",
			"env_json":  `{"DATABASE_URL":"postgres://u:p4ssw0rd@db.test/app","SERVICE_URL":"https://host.test/health"}`,
		},
	}}
	env, ok := activityArgsFromRequest(req)["env_json"].(map[string]interface{})
	require.True(t, ok)

	require.Contains(t, env, "DATABASE_URL", "the field name is the audit signal and must survive")
	assert.NotContains(t, env["DATABASE_URL"], "p4ssw0rd")
	assert.Equal(t, "https://host.test/health", env["SERVICE_URL"],
		"a URL with no credential in it must stay readable")
}

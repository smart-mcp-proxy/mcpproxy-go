package configimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The native rule must not take over any other client's file.
func TestDetectFixturesUnchangedByNativeFormat(t *testing.T) {
	for file, want := range map[string]ConfigFormat{
		"claude_desktop.json": FormatClaudeDesktop,
		"claude_code.json":    FormatClaudeCode,
		"cursor.json":         FormatCursor,
		"gemini.json":         FormatGemini,
	} {
		t.Run(file, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("testdata", file))
			require.NoError(t, err)
			got, err := DetectFormat(content)
			require.NoError(t, err)
			assert.Equal(t, want, got.Format)
		})
	}
}

func TestDetectNativeFormat(t *testing.T) {
	native := map[string]string{
		"oauth client_id":     `{"url":"https://x/mcp","oauth":{"client_id":"c"}}`,
		"oauth client_secret": `{"url":"https://x/mcp","oauth":{"client_secret":"s"}}`,
		"oauth redirect_uri":  `{"url":"https://x/mcp","oauth":{"redirect_uri":"http://127.0.0.1:8765/cb"}}`,
		"protocol":            `{"url":"https://x/mcp","protocol":"http"}`,
		"working_dir":         `{"command":"node","working_dir":"/srv"}`,
		"isolation":           `{"command":"node","isolation":{"enabled":false}}`,
	}
	for name, server := range native {
		t.Run(name, func(t *testing.T) {
			got, err := DetectFormat([]byte(`{"mcpServers":{"a":{"command":"echo"},"b":` + server + `}}`))
			require.NoError(t, err)
			assert.Equal(t, FormatMCPProxy, got.Format)
			assert.Equal(t, "high", got.Confidence)
		})
	}

	foreign := map[string]string{
		"cursor auth":       `{"type":"streamable-http","url":"https://x/mcp","auth":{"CLIENT_ID":"c"}}`,
		"claude code oauth": `{"type":"http","url":"https://x/mcp","oauth":{"clientId":"c","callbackPort":1}}`,
		"gemini oauth":      `{"httpUrl":"https://x/mcp","oauth":{"enabled":true,"clientId":"c","redirectUri":"http://localhost:3000/cb"}}`,
		"plain url":         `{"url":"https://x/mcp"}`,
	}
	for name, server := range foreign {
		t.Run(name, func(t *testing.T) {
			got, err := DetectFormat([]byte(`{"mcpServers":{"b":` + server + `}}`))
			require.NoError(t, err)
			assert.NotEqual(t, FormatMCPProxy, got.Format)
		})
	}
}

func TestImportNativeFormat(t *testing.T) {
	dir := t.TempDir()
	dirJSON, _ := json.Marshal(dir)
	content := []byte(`{"mcpServers":{
		"local": {"command":"node","args":["s.js"],"working_dir":` + string(dirJSON) + `},
		"remote": {
			"url":"https://ha.example.com/api/mcp",
			"protocol":"http",
			"trust_mode":"auto",
			"enabled":false,
			"oauth":{"client_id":"http://127.0.0.1:8765/","client_secret":"shh","redirect_uri":"http://127.0.0.1:8765/oauth/callback","scopes":["a","b"]}
		}
	}}`)
	result, err := Import(content, nil)
	require.NoError(t, err)
	assert.Equal(t, FormatMCPProxy, result.Format)
	require.Empty(t, result.Failed)
	require.Len(t, result.Imported, 2)

	byName := map[string]*ImportedServer{}
	for _, s := range result.Imported {
		byName[s.Server.Name] = s
	}
	local := byName["local"].Server
	assert.Equal(t, "stdio", local.Protocol)
	assert.Equal(t, dir, local.WorkingDir)
	assert.Nil(t, local.OAuth)

	remote := byName["remote"]
	require.NotNil(t, remote.Server.OAuth)
	assert.Equal(t, "http://127.0.0.1:8765/", remote.Server.OAuth.ClientID)
	assert.Equal(t, "shh", remote.Server.OAuth.ClientSecret)
	assert.Equal(t, "http://127.0.0.1:8765/oauth/callback", remote.Server.OAuth.RedirectURI)
	assert.Equal(t, []string{"a", "b"}, remote.Server.OAuth.Scopes)
	assert.Equal(t, "http", remote.Server.Protocol)
	assert.True(t, remote.Server.Quarantined, "imports stay quarantined")
	assert.Empty(t, remote.Server.TrustMode, "a file must not pick its own review tier")
	assert.ElementsMatch(t, []string{"enabled", "trust_mode"}, remote.FieldsSkipped)
	assert.Contains(t, strings.Join(remote.Warnings, "\n"), "trust_mode")
}

// Same rejections as add-json, and the whole file fails rather than half of it.
func TestImportNativeFormatRefusesWhatAddJSONRefuses(t *testing.T) {
	for name, server := range map[string]string{
		"unknown oauth key":   `{"url":"https://x/mcp","oauth":{"client_id":"c","clientSecret":"s"}}`,
		"bad redirect_uri":    `{"url":"https://x/mcp","oauth":{"redirect_uri":"https://example.com/cb"}}`,
		"no url or command":   `{"protocol":"http"}`,
		"protocol/type clash": `{"url":"https://x/mcp","protocol":"sse","type":"http"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Import([]byte(`{"mcpServers":{"ok":{"command":"echo","protocol":"stdio"},"bad":`+server+`}}`), nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), `server "bad"`)
		})
	}
}

// Every snake_case JSON key of config.OAuthConfig marks a file as mcpproxy's
// own, so a server whose oauth block holds only one of them (for example the
// Spec 135 skip_issuer_validation opt-out) is not handed to another dialect's
// parser, which would drop the block.
func TestNativeOAuthKeys_CoverEveryOAuthConfigField(t *testing.T) {
	known := map[string]bool{}
	for _, k := range nativeOAuthKeys {
		known[k] = true
	}
	typ := reflect.TypeOf(config.OAuthConfig{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" || name == "scopes" { // scopes is shared with other dialects
			continue
		}
		if !known[name] {
			t.Errorf("oauth.%s is not in nativeOAuthKeys", name)
		}
	}
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/codescripts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// newStoredScriptProxy builds a code-execution-enabled proxy whose config-file
// authority is an explicit path (the Spec 097 construction-time authority), and
// returns it with the scripts directory that authority implies.
func newStoredScriptProxy(t *testing.T, opts ...MCPProxyOption) (*MCPProxyServer, string) {
	t.Helper()

	tmpDir := t.TempDir()
	logger := zap.NewNop()

	sm, err := storage.NewManager(tmpDir, logger.Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { sm.Close() })

	idx, err := index.NewManager(tmpDir, logger)
	require.NoError(t, err)
	t.Cleanup(func() { idx.Close() })

	cfg := config.DefaultConfig()
	cfg.DataDir = tmpDir
	cfg.EnableCodeExecution = true
	cfg.CodeExecutionPoolSize = 1

	um := upstream.NewManager(logger, cfg, sm.GetBoltDB(), secret.NewResolver(), sm)

	cm, err := cache.NewManager(sm.GetDB(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { cm.Close() })

	tr := truncate.NewTruncator(cfg.ToolResponseLimit)

	if len(opts) == 0 {
		opts = []MCPProxyOption{WithConfigFilePath(filepath.Join(tmpDir, "mcp_config.json"))}
	}
	proxy := NewMCPProxyServer(sm, idx, um, cm, func() *truncate.Truncator { return tr }, logger, nil, false, cfg, nil, opts...)
	t.Cleanup(func() { proxy.Close() })

	scriptsDir := filepath.Join(tmpDir, codescripts.DirName)
	require.NoError(t, os.MkdirAll(scriptsDir, 0o755))
	return proxy, scriptsDir
}

func writeStoredScript(t *testing.T, scriptsDir, filename, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(scriptsDir, filename), []byte(content), 0o644))
}

// callCodeExecution runs the code_execution handler and returns the result.
func callCodeExecution(t *testing.T, proxy *MCPProxyServer, args map[string]interface{}) *mcp.CallToolResult {
	t.Helper()
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "code_execution", Arguments: args}}
	result, err := proxy.handleCodeExecution(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, result.Content)
	text, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok, "expected text content, got %T", result.Content[0])
	return text.Text
}

// TestScriptsDirAuthority (T002) pins where the scripts directory comes from:
// the config FILE path handed in at construction, with config.GetConfigPath on
// the data dir only as the documented last-resort fallback.
func TestScriptsDirAuthority(t *testing.T) {
	t.Run("explicit construction-time path wins", func(t *testing.T) {
		explicit := filepath.Join(t.TempDir(), "elsewhere", "mcp_config.json")
		proxy, _ := newStoredScriptProxy(t, WithConfigFilePath(explicit))
		assert.Equal(t, codescripts.DirFor(explicit), proxy.scriptsDir())
	})

	t.Run("falls back to the data-dir config path when nothing was provided", func(t *testing.T) {
		proxy, _ := newStoredScriptProxy(t, MCPProxyOption(func(*MCPProxyServer) {}))
		want := codescripts.DirFor(config.GetConfigPath(proxy.config.DataDir))
		assert.Equal(t, want, proxy.scriptsDir())
	})
}

// TestCodeExecution_ScriptXORCode (T003) pins FR-002: exactly one of code or
// script, both violations explained rather than silently preferred.
func TestCodeExecution_ScriptXORCode(t *testing.T) {
	proxy, scriptsDir := newStoredScriptProxy(t)
	writeStoredScript(t, scriptsDir, "double.js", "({result: input.value * 2})")

	t.Run("both rejected", func(t *testing.T) {
		result := callCodeExecution(t, proxy, map[string]interface{}{
			"code":   "({result: 1})",
			"script": "double",
		})
		require.True(t, result.IsError, "supplying both code and script must fail")
		assert.Contains(t, resultText(t, result), "exactly one")
	})

	t.Run("neither rejected", func(t *testing.T) {
		result := callCodeExecution(t, proxy, map[string]interface{}{
			"input": map[string]interface{}{},
		})
		require.True(t, result.IsError, "supplying neither code nor script must fail")
		assert.Contains(t, resultText(t, result), "exactly one")
	})

	t.Run("empty strings count as absent", func(t *testing.T) {
		result := callCodeExecution(t, proxy, map[string]interface{}{"code": "", "script": ""})
		require.True(t, result.IsError)
		assert.Contains(t, resultText(t, result), "exactly one")
	})

	t.Run("non-string script is rejected", func(t *testing.T) {
		result := callCodeExecution(t, proxy, map[string]interface{}{"script": 42})
		require.True(t, result.IsError)
		assert.Contains(t, resultText(t, result), "script")
	})
}

// TestCodeExecution_StoredScriptMatchesInline (T003 / SC-002) executes the same
// source both ways and compares the results byte for byte.
func TestCodeExecution_StoredScriptMatchesInline(t *testing.T) {
	proxy, scriptsDir := newStoredScriptProxy(t)
	const source = "({result: input.value * 2, kind: 'stored'})"
	writeStoredScript(t, scriptsDir, "double.js", source)

	input := map[string]interface{}{"value": 21}
	stored := resultText(t, callCodeExecution(t, proxy, map[string]interface{}{
		"script": "double",
		"input":  input,
	}))
	inline := resultText(t, callCodeExecution(t, proxy, map[string]interface{}{
		"code":  source,
		"input": input,
	}))

	assert.Equal(t, inline, stored, "a stored script must execute identically to the same source inline")
	assert.Contains(t, stored, `"result":42`)
}

// TestCodeExecution_StoredScriptMatchesInlineUnderEnforcement is the parity
// case with teeth (SC-002 / FR-005). Comparing a self-contained arithmetic
// script proves the source arrives intact and nothing more; what the shared
// path actually risks is ORDERING — the resolver writes options.Language part
// way through a fixed language→resolve→options→scope sequence, so a stored
// script could silently execute under different option and scope enforcement
// than the same source inline. This runs both branches through call_tool()
// under a caller-set timeout and tool-call budget, an allowed_servers
// restriction and a deny-all profile scope, and requires the answers — and the
// records they leave behind — to agree.
func TestCodeExecution_StoredScriptMatchesInlineUnderEnforcement(t *testing.T) {
	// Two calls: one to a server the options exclude, one to a server they
	// allow. Neither needs a real upstream — the allow-list is consulted before
	// any connection — and the second call also proves the budget was not spent
	// refusing the first.
	const source = `var denied = call_tool('deploy-srv', 'ship', {});
var allowed = call_tool('research-srv', 'search', {});
({
  denied: denied.ok ? 'RAN' : denied.error.code,
  allowed: allowed.ok ? 'RAN' : allowed.error.code
})`

	options := map[string]interface{}{
		"timeout_ms":      7500,
		"max_tool_calls":  3,
		"allowed_servers": []interface{}{"research-srv"},
	}

	// Each branch gets its own proxy so the record it leaves behind is the only
	// one in storage, and so neither can observe the other's execution.
	run := func(t *testing.T, ctx context.Context, args map[string]interface{}) (text string, rec *storage.ToolCallRecord) {
		t.Helper()
		proxy, scriptsDir := newStoredScriptProxy(t)
		writeStoredScript(t, scriptsDir, "enforced.js", source)

		request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "code_execution", Arguments: args}}
		result, err := proxy.handleCodeExecution(ctx, request)
		require.NoError(t, err)
		require.NotNil(t, result)

		records, err := proxy.storage.GetServerToolCalls("code_execution", 10)
		require.NoError(t, err)
		require.NotEmpty(t, records, "the parent code_execution call must be recorded")
		return resultText(t, result), records[0]
	}

	scenarios := []struct {
		name string
		ctx  func() context.Context
	}{
		{
			name: "allowed_servers restriction",
			ctx:  context.Background,
		},
		{
			name: "deny-all profile scope on top",
			ctx: func() context.Context {
				return profile.WithProfileScope(context.Background(), profile.NewProfileScope("locked", nil))
			},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			storedText, storedRec := run(t, sc.ctx(), map[string]interface{}{
				"script":  "enforced",
				"input":   map[string]interface{}{},
				"options": options,
			})
			inlineText, inlineRec := run(t, sc.ctx(), map[string]interface{}{
				"code":    source,
				"input":   map[string]interface{}{},
				"options": options,
			})

			assert.Equal(t, inlineText, storedText,
				"a stored script must be enforced exactly like the same source inline")
			assert.Contains(t, storedText, "SERVER_NOT_ALLOWED",
				"the excluded server must be refused inside the stored script too")

			// The records agree on everything except the one field a stored
			// script is meant to add.
			assert.Equal(t, source, storedRec.Arguments["code"], "the record keeps the executed source")
			assert.Equal(t, "enforced", storedRec.Arguments["script"])
			assert.NotContains(t, inlineRec.Arguments, "script")
			for _, key := range []string{"code", "input", "language"} {
				assert.Equal(t, inlineRec.Arguments[key], storedRec.Arguments[key],
					"records must agree on %q", key)
			}
			assert.Equal(t, inlineRec.Error, storedRec.Error)
			assert.Equal(t, inlineRec.ExecutionType, storedRec.ExecutionType)
		})
	}
}

// TestCodeExecution_StoredTypeScript pins that the extension derives the
// language, so a .ts stored script transpiles exactly like inline TypeScript.
func TestCodeExecution_StoredTypeScript(t *testing.T) {
	proxy, scriptsDir := newStoredScriptProxy(t)
	writeStoredScript(t, scriptsDir, "typed.ts", "const factor: number = 3; ({result: (input.value as number) * factor})")

	text := resultText(t, callCodeExecution(t, proxy, map[string]interface{}{
		"script": "typed",
		"input":  map[string]interface{}{"value": 4},
	}))
	assert.Contains(t, text, `"result":12`)
}

// TestCodeExecution_ScriptLanguageContradiction: the extension is
// authoritative, an explicit contradicting language is an error rather than a
// silently-ignored parameter.
func TestCodeExecution_ScriptLanguageContradiction(t *testing.T) {
	proxy, scriptsDir := newStoredScriptProxy(t)
	writeStoredScript(t, scriptsDir, "typed.ts", "const x: number = 1; ({x})")

	result := callCodeExecution(t, proxy, map[string]interface{}{
		"script":   "typed",
		"language": "javascript",
	})
	require.True(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "typescript")

	// The agreeing language is accepted.
	ok := callCodeExecution(t, proxy, map[string]interface{}{
		"script":   "typed",
		"language": "typescript",
	})
	assert.False(t, ok.IsError, "an agreeing language must not be rejected: %s", resultText(t, ok))
}

// TestCodeExecution_ScriptNotFoundListsAvailable pins FR-004: the not-found
// error IS the MCP discovery mechanism.
func TestCodeExecution_ScriptNotFoundListsAvailable(t *testing.T) {
	proxy, scriptsDir := newStoredScriptProxy(t)
	writeStoredScript(t, scriptsDir, "alpha.js", "1")
	writeStoredScript(t, scriptsDir, "beta.ts", "1")

	result := callCodeExecution(t, proxy, map[string]interface{}{"script": "gamma"})
	require.True(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "gamma")
	assert.Contains(t, text, "alpha")
	assert.Contains(t, text, "beta")

	t.Run("an invalid name never reaches the filesystem", func(t *testing.T) {
		result := callCodeExecution(t, proxy, map[string]interface{}{"script": "../../etc/passwd"})
		require.True(t, result.IsError)
		assert.Contains(t, resultText(t, result), "invalid script name")
	})
}

// TestCodeExecution_RecordsCarryScriptAndSource pins FR-005 / research R6:
// history keeps the executed SOURCE as code (Spec 024 parity) and additionally
// names the script.
func TestCodeExecution_RecordsCarryScriptAndSource(t *testing.T) {
	proxy, scriptsDir := newStoredScriptProxy(t)
	const source = "({result: 7})"
	writeStoredScript(t, scriptsDir, "seven.js", source)

	result := callCodeExecution(t, proxy, map[string]interface{}{"script": "seven"})
	require.False(t, result.IsError, resultText(t, result))

	records, err := proxy.storage.GetServerToolCalls("code_execution", 10)
	require.NoError(t, err)
	require.NotEmpty(t, records, "the parent code_execution call must be recorded")

	rec := records[0]
	assert.Equal(t, source, rec.Arguments["code"], "records keep the resolved source as code (Spec 024 parity)")
	assert.Equal(t, "seven", rec.Arguments["script"], "records additionally name the stored script")
	assert.Equal(t, "javascript", rec.Arguments["language"])

	t.Run("inline calls carry no script key", func(t *testing.T) {
		result := callCodeExecution(t, proxy, map[string]interface{}{"code": "({result: 8})"})
		require.False(t, result.IsError, resultText(t, result))
		records, err := proxy.storage.GetServerToolCalls("code_execution", 10)
		require.NoError(t, err)
		require.NotEmpty(t, records)
		assert.NotContains(t, records[0].Arguments, "script")
	})
}

// TestCodeExecRecordArguments pins that history arguments and the activity
// payload are built by ONE helper, so the two can never disagree about what a
// stored-script execution ran.
func TestCodeExecRecordArguments(t *testing.T) {
	input := map[string]interface{}{"a": 1}

	stored := codeExecRecordArguments("src", "name", "javascript", input)
	assert.Equal(t, map[string]interface{}{
		"code":     "src",
		"input":    input,
		"language": "javascript",
		"script":   "name",
	}, stored)

	inline := codeExecRecordArguments("src", "", "typescript", input)
	assert.Equal(t, map[string]interface{}{
		"code":     "src",
		"input":    input,
		"language": "typescript",
	}, inline)
}

// TestCodeExecution_StoredScriptFreshness (T003 support / FR-009): an atomic
// replacement is executed by the very next invocation, no restart.
func TestCodeExecution_StoredScriptFreshness(t *testing.T) {
	proxy, scriptsDir := newStoredScriptProxy(t)
	writeStoredScript(t, scriptsDir, "hot.js", "({result: 1})")

	first := resultText(t, callCodeExecution(t, proxy, map[string]interface{}{"script": "hot"}))
	assert.Contains(t, first, `"result":1`)

	staging := filepath.Join(t.TempDir(), "hot.js")
	require.NoError(t, os.WriteFile(staging, []byte("({result: 2})"), 0o644))
	require.NoError(t, os.Rename(staging, filepath.Join(scriptsDir, "hot.js")))

	second := resultText(t, callCodeExecution(t, proxy, map[string]interface{}{"script": "hot"}))
	assert.Contains(t, second, `"result":2`)
}

// --- T005: the three registration sites ---

// codeExecutionSchemas returns the code_execution tool schema from every
// surface that registers it.
func codeExecutionSchemas(t *testing.T, proxy *MCPProxyServer) map[string]map[string]interface{} {
	t.Helper()
	schemas := map[string]map[string]interface{}{}

	if st, ok := proxy.server.ListTools()["code_execution"]; ok {
		schemas["default_server"] = toolAsMap(t, st.Tool)
	}
	for _, st := range proxy.buildCodeExecModeTools() {
		if st.Tool.Name == "code_execution" {
			schemas["code_execution_mode"] = toolAsMap(t, st.Tool)
		}
	}
	for _, st := range proxy.buildCallToolModeTools() {
		if st.Tool.Name == "code_execution" {
			schemas["call_tool_mode"] = toolAsMap(t, st.Tool)
		}
	}
	return schemas
}

func toolAsMap(t *testing.T, tool mcp.Tool) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(tool)
	require.NoError(t, err)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

func requiredParams(tool map[string]interface{}) []string {
	schema, _ := tool["inputSchema"].(map[string]interface{})
	raw, _ := schema["required"].([]interface{})
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// TestCodeExecutionRegistrations_ScriptParam (T005) asserts every LIVE
// registration advertises the optional script parameter from the one shared
// description, and that code is no longer schema-required (the XOR rule cannot
// be expressed in JSON Schema, so the handler enforces it).
func TestCodeExecutionRegistrations_ScriptParam(t *testing.T) {
	proxy, _ := newStoredScriptProxy(t)
	schemas := codeExecutionSchemas(t, proxy)
	require.Len(t, schemas, 3, "code_execution must be registered on all three surfaces: %v", schemas)

	for surface, tool := range schemas {
		props := schemaProps(tool)
		require.NotNil(t, props, "surface %s: code_execution lost its inputSchema", surface)

		script, ok := props["script"].(map[string]interface{})
		require.True(t, ok, "surface %s: code_execution must expose the script parameter", surface)
		assert.Equal(t, codeExecutionScriptDescription, script["description"],
			"surface %s: script description must come from the shared constant", surface)

		assert.NotContains(t, requiredParams(tool), "code",
			"surface %s: code must not be schema-required — the handler enforces the XOR", surface)

		desc, _ := tool["description"].(string)
		assert.Contains(t, desc, "script",
			"surface %s: the tool description must document stored scripts (FR-008)", surface)
	}
}

// TestCodeExecutionDisabledStub_AcceptsScript (T005): a script call must reach
// the disabled handler and get the disabled explanation — so the stub takes the
// parameter, but keeps ONLY its disabled description (no discovery prose).
func TestCodeExecutionDisabledStub_AcceptsScript(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	proxy.config.EnableCodeExecution = false

	tools := proxy.buildCodeExecutionTool()
	require.Len(t, tools, 1)
	stub := toolAsMap(t, tools[0].Tool)

	desc, _ := stub["description"].(string)
	assert.Contains(t, desc, "disabled")
	assert.NotContains(t, desc, codeExecutionScriptDescription,
		"the disabled stub keeps only its disabled description")
	assert.False(t, strings.Contains(desc, "call_tools"),
		"the disabled stub must not advertise the executable contract")

	props := schemaProps(stub)
	require.NotNil(t, props)
	_, hasScript := props["script"]
	assert.True(t, hasScript, "the stub must accept script so those calls reach the disabled handler")
	assert.NotContains(t, requiredParams(stub), "code",
		"the stub must not require code, or a script-only call is rejected by schema instead of explained")

	result, err := tools[0].Handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "code_execution", Arguments: map[string]interface{}{"script": "anything"}},
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "disabled")
}

// TestCodeExecution_DisabledGateCoversEveryDispatch pins enable_code_execution
// as a FEATURE switch rather than a tool-registration detail. The MCP surfaces
// gated it by omitting the tool (or serving a disabled stub), but every
// non-MCP caller — REST /api/v1/code/exec, REST /api/v1/tools/call, the tray —
// reaches the handler through CallToolDirect, which routes code_execution
// straight through. With the flag off an API-key holder could still run inline
// code and, since Spec 097, read and execute a server-side stored script. The
// gate belongs on the handler, where every surface passes.
func TestCodeExecution_DisabledGateCoversEveryDispatch(t *testing.T) {
	proxy, scriptsDir := newStoredScriptProxy(t)
	writeStoredScript(t, scriptsDir, "sentinel.js", "({result: 'executed'})")
	proxy.config.EnableCodeExecution = false

	call := func(t *testing.T, args map[string]interface{}) error {
		t.Helper()
		_, err := proxy.CallToolDirect(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "code_execution", Arguments: args},
		})
		require.Error(t, err, "a disabled feature must not answer with a result")
		return err
	}

	t.Run("a stored script is neither resolved nor executed", func(t *testing.T) {
		err := call(t, map[string]interface{}{"script": "sentinel"})
		assert.Contains(t, err.Error(), "disabled")
		assert.NotContains(t, err.Error(), "executed", "the script must never run")
	})

	t.Run("a missing script name is not answered with the discovery listing", func(t *testing.T) {
		err := call(t, map[string]interface{}{"script": "nope"})
		assert.Contains(t, err.Error(), "disabled")
		assert.NotContains(t, err.Error(), "sentinel",
			"a disabled feature must not enumerate the scripts directory")
	})

	t.Run("inline code is refused too", func(t *testing.T) {
		err := call(t, map[string]interface{}{"code": "({result: 'executed'})"})
		assert.Contains(t, err.Error(), "disabled")
		assert.NotContains(t, err.Error(), "executed")
	})

	t.Run("the wording matches the disabled stub", func(t *testing.T) {
		stub := proxy.buildCodeExecutionTool()
		require.Len(t, stub, 1)
		result, err := stub[0].Handler(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "code_execution", Arguments: map[string]interface{}{"script": "sentinel"}},
		})
		require.NoError(t, err)
		require.True(t, result.IsError)
		assert.Equal(t, resultText(t, result), call(t, map[string]interface{}{"script": "sentinel"}).Error(),
			"every surface must explain a disabled feature the same way")
	})

	t.Run("re-enabling takes effect without reconstruction", func(t *testing.T) {
		proxy.config.EnableCodeExecution = true
		t.Cleanup(func() { proxy.config.EnableCodeExecution = false })

		result := callCodeExecution(t, proxy, map[string]interface{}{"script": "sentinel"})
		require.False(t, result.IsError, resultText(t, result))
		assert.Contains(t, resultText(t, result), "executed")
	})
}

// TestCodeExecution_RefusalsKeepTheirTypeThroughDispatch is the seam that lets
// the REST surface answer a stored-script rejection with a 4xx. The MCP
// contract makes the handler return an isError RESULT, and CallToolDirect
// flattens that to a plain error — so without a typed channel the HTTP layer
// has nothing but prose to classify by, and every caller mistake arrives as a
// retryable 500. Each refusal must therefore stay reachable through
// errors.As/errors.Is while keeping its agent-readable message.
func TestCodeExecution_RefusalsKeepTheirTypeThroughDispatch(t *testing.T) {
	proxy, scriptsDir := newStoredScriptProxy(t)
	writeStoredScript(t, scriptsDir, "alpha.js", "({result: 1})")
	writeStoredScript(t, scriptsDir, "dup.js", "1")
	writeStoredScript(t, scriptsDir, "dup.ts", "1")
	writeStoredScript(t, scriptsDir, "blank.js", "")
	writeStoredScript(t, scriptsDir, "typed.ts", "const x: number = 1; ({x})")

	dispatch := func(t *testing.T, args map[string]interface{}) error {
		t.Helper()
		_, err := proxy.CallToolDirect(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "code_execution", Arguments: args},
		})
		require.Error(t, err)
		return err
	}

	t.Run("not found", func(t *testing.T) {
		err := dispatch(t, map[string]interface{}{"script": "nope"})
		var notFound *codescripts.NotFoundError
		require.True(t, errors.As(err, &notFound), "want *NotFoundError, got %T: %v", err, err)
		assert.Contains(t, err.Error(), "alpha", "the discovery listing must survive alongside the type")
	})

	t.Run("invalid name", func(t *testing.T) {
		err := dispatch(t, map[string]interface{}{"script": "../../etc/passwd"})
		var invalidName *codescripts.InvalidNameError
		assert.True(t, errors.As(err, &invalidName), "want *InvalidNameError, got %T: %v", err, err)
	})

	t.Run("ambiguous", func(t *testing.T) {
		err := dispatch(t, map[string]interface{}{"script": "dup"})
		var ambiguous *codescripts.AmbiguousError
		assert.True(t, errors.As(err, &ambiguous), "want *AmbiguousError, got %T: %v", err, err)
	})

	t.Run("present but unusable", func(t *testing.T) {
		err := dispatch(t, map[string]interface{}{"script": "blank"})
		var invalid *codescripts.InvalidError
		require.True(t, errors.As(err, &invalid), "want *InvalidError, got %T: %v", err, err)
		assert.Equal(t, codescripts.ReasonEmpty, invalid.Reason)
	})

	t.Run("language contradicts the extension", func(t *testing.T) {
		err := dispatch(t, map[string]interface{}{"script": "typed", "language": "javascript"})
		var mismatch *codescripts.LanguageMismatchError
		assert.True(t, errors.As(err, &mismatch), "want *LanguageMismatchError, got %T: %v", err, err)
	})

	t.Run("feature disabled", func(t *testing.T) {
		proxy.config.EnableCodeExecution = false
		t.Cleanup(func() { proxy.config.EnableCodeExecution = true })

		err := dispatch(t, map[string]interface{}{"script": "alpha"})
		assert.True(t, errors.Is(err, config.ErrCodeExecutionDisabled), "want the disabled sentinel, got %T: %v", err, err)
		assert.Equal(t, config.CodeExecutionDisabledMessage, err.Error())
	})

	t.Run("an execution fault keeps no refusal type", func(t *testing.T) {
		// A script that runs and throws is not a refusal: it comes back as a
		// normal result envelope, so the REST surface still answers 200 with
		// ok:false rather than reclassifying it as a caller mistake.
		writeStoredScript(t, scriptsDir, "boom.js", "throw new Error('kaboom')")
		result, err := proxy.CallToolDirect(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "code_execution", Arguments: map[string]interface{}{"script": "boom"}},
		})
		require.NoError(t, err, "a thrown error is reported inside the result, not as a dispatch failure")
		require.NotNil(t, result)
	})
}

// TestCodeExecution_EndToEndFreshness (T011 / FR-009) pins the whole
// invocation path against a directory that changes underneath it: an atomic
// replacement is executed by the very next call, and a script added or removed
// after startup is reflected in the very next listing. Nothing caches a script,
// so nothing needs invalidating — and no restart is ever required.
func TestCodeExecution_EndToEndFreshness(t *testing.T) {
	proxy, scriptsDir := newStoredScriptProxy(t)
	writeStoredScript(t, scriptsDir, "report.js", "({result: 'v1'})")

	// Version 1 executes.
	first := resultText(t, callCodeExecution(t, proxy, map[string]interface{}{"script": "report"}))
	assert.Contains(t, first, `"result":"v1"`)

	// Replace atomically (write elsewhere, rename over) — the editor-safe way.
	staging := filepath.Join(t.TempDir(), "report.js")
	require.NoError(t, os.WriteFile(staging, []byte("({result: 'v2'})"), 0o644))
	require.NoError(t, os.Rename(staging, filepath.Join(scriptsDir, "report.js")))

	second := resultText(t, callCodeExecution(t, proxy, map[string]interface{}{"script": "report"}))
	assert.Contains(t, second, `"result":"v2"`, "an atomically replaced script must run on the very next invocation")

	// A script added after the server started is invocable immediately and
	// shows up in the listing the discovery surfaces read.
	writeStoredScript(t, scriptsDir, "fresh.js", "({result: 'new'})")
	added := resultText(t, callCodeExecution(t, proxy, map[string]interface{}{"script": "fresh"}))
	assert.Contains(t, added, `"result":"new"`)

	entries, err := codescripts.List(proxy.scriptsDir())
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	assert.Equal(t, []string{"fresh", "report"}, names, "a newly added script must appear in the next listing")

	// Removing it takes effect just as immediately: the next invocation fails
	// with the discovery error, and the listing no longer names it.
	require.NoError(t, os.Remove(filepath.Join(scriptsDir, "fresh.js")))
	gone := callCodeExecution(t, proxy, map[string]interface{}{"script": "fresh"})
	require.True(t, gone.IsError, "a removed script must stop being invocable at once")
	assert.Contains(t, resultText(t, gone), "not found")

	entries, err = codescripts.List(proxy.scriptsDir())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "report", entries[0].Name)
}

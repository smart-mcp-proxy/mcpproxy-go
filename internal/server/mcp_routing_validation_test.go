package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 102 US3 (T059–T064) — self-healing direct calls.
//
// A deferred listing advertises `{"type":"object"}`, so an agent guesses its
// arguments from a compact signature. When the signature was lossy the guess
// can be wrong, and the cost of being wrong is what decides whether deferral is
// worth it: an opaque upstream error costs an unbounded debugging loop, while a
// pre-dispatch rejection carrying the FULL schema costs exactly one retry.
//
// Validation is therefore deliberately mode-INDEPENDENT: a full-mode client
// that guessed wrong gets the same help, and the schema it validates against is
// always the stored upstream one, never the placeholder the listing advertised.

const validationParamsJSON = `{"type":"object","properties":{"path":{"type":"string"},"depth":{"type":"integer"}},"required":["path"]}`

func validationEntry() *directCatalogEntry {
	return &directCatalogEntry{
		DisplayName: "fs__read",
		ServerName:  "fs",
		ToolName:    "read",
		Description: "Read a path",
		ParamsJSON:  validationParamsJSON,
		Hash:        "h-fs-read",
		Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
	}
}

// newDirectValidationProxy makes the tool CALLABLE, so what these tests observe
// is the validator and not the callability gate that runs just before it.
func newDirectValidationProxy(t *testing.T) *MCPProxyServer {
	t.Helper()
	p := createTestMCPProxyServer(t)
	require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{Name: "fs", Enabled: true}))
	require.NoError(t, p.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "fs", ToolName: "read", Status: storage.ToolApprovalStatusApproved,
	}))
	return p
}

// callDirect dispatches one direct-mode call through the handler under test.
func callDirect(t *testing.T, p *MCPProxyServer, entry *directCatalogEntry, args map[string]interface{}) *mcp.CallToolResult {
	t.Helper()
	return callDirectAs(t, p, context.Background(), entry, args)
}

func callDirectAs(t *testing.T, p *MCPProxyServer, ctx context.Context, entry *directCatalogEntry, args map[string]interface{}) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = entry.DisplayName
	req.Params.Arguments = args

	result, err := p.makeDirectModeHandler(entry)(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

// invalidParamsBody decodes the self-healing error body.
func invalidParamsBody(t *testing.T, result *mcp.CallToolResult) map[string]interface{} {
	t.Helper()
	require.True(t, result.IsError, "a validation failure must be an error result")
	require.NotEmpty(t, result.Content)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &body),
		"the error body must be the structured invalid_params contract, not prose")
	return body
}

// T059: a missing required argument is rejected before dispatch, with the full
// schema and a hint — in BOTH serialization modes.
func TestDirectValidation_MissingRequiredArgIsSelfHealing(t *testing.T) {
	for _, mode := range []string{config.DirectToolResponseModeFull, config.DirectToolResponseModeDeferred} {
		t.Run(mode, func(t *testing.T) {
			p := newDirectValidationProxy(t)
			p.config.DirectToolResponseMode = mode
			entry := validationEntry()

			result := callDirect(t, p, entry, map[string]interface{}{"depth": 2})
			body := invalidParamsBody(t, result)

			assert.Equal(t, "invalid_params", body["error_type"])
			assert.Equal(t, entry.DisplayName, body["tool"],
				"the error names the tool by the id the caller actually used")
			assert.Contains(t, body["error"], "path", "the detail must name the offending property")
			assert.Contains(t, body["hint"], "describe_tool", "the hint must name the recovery step")

			// The FULL stored schema, not the advertised placeholder — this is
			// what caps the cost of a lossy signature at one retry.
			schema, ok := body["input_schema"].(map[string]interface{})
			require.True(t, ok, "the error must embed the input schema")
			props, ok := schema["properties"].(map[string]interface{})
			require.True(t, ok)
			assert.Contains(t, props, "path")
			assert.Contains(t, props, "depth")
			assert.Equal(t, []interface{}{"path"}, schema["required"])
		})
	}
}

// T060: validation reads the STORED upstream schema, never the
// `{"type":"object"}` placeholder the deferred listing advertises. A permissive
// placeholder would accept everything and validate nothing.
func TestDirectValidation_ValidatesStoredSchemaNotThePlaceholder(t *testing.T) {
	p := newDirectValidationProxy(t)
	p.config.DirectToolResponseMode = config.DirectToolResponseModeDeferred
	entry := validationEntry()

	// Exactly what the deferred listing advertised for this tool.
	rendered := renderDeferredDirectTool(entry, "[fs] Read a path")
	raw, err := json.Marshal(rendered)
	require.NoError(t, err)
	var wire map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &wire))
	require.JSONEq(t, `{"type":"object"}`, string(wire["inputSchema"]),
		"the fixture must actually advertise the placeholder, or this proves nothing")

	// Arguments that the ADVERTISED schema accepts and the STORED one rejects.
	result := callDirect(t, p, entry, map[string]interface{}{"depth": "not-an-integer"})
	body := invalidParamsBody(t, result)
	assert.Contains(t, body["error"], "depth",
		"the stored schema's type constraint must be enforced even though the listing advertised none")
}

// Valid arguments are not rejected, and nothing is serialized on the happy path.
func TestDirectValidation_ValidArgsReachDispatch(t *testing.T) {
	p := newDirectValidationProxy(t)
	result := callDirect(t, p, validationEntry(), map[string]interface{}{"path": "/tmp", "depth": 2})

	require.True(t, result.IsError, "the fixture has no upstream, so dispatch fails downstream")
	text := result.Content[0].(mcp.TextContent).Text
	assert.NotContains(t, text, "invalid_params",
		"valid arguments must reach dispatch, not be rejected by the validator")
	assert.NotContains(t, text, "input_schema",
		"no schema is ever serialized on the happy path (SC-006)")
}

// T061: fail-open. An uncompilable stored schema dispatches exactly as a
// schemaless proxy would, and is counted rather than silently swallowed.
func TestDirectValidation_UncompilableSchemaFailsOpen(t *testing.T) {
	p := newDirectValidationProxy(t)
	entry := validationEntry()
	entry.ParamsJSON = `{"type":"object","properties":{"path":{"type":` // truncated: unparseable

	before := p.inputValidator.SkippedCount()
	result := callDirect(t, p, entry, map[string]interface{}{"anything": true})

	text := result.Content[0].(mcp.TextContent).Text
	assert.NotContains(t, text, "invalid_params",
		"an uncompilable schema must never block a call a schemaless proxy would have allowed (FR-013b)")
	assert.Greater(t, p.inputValidator.SkippedCount(), before,
		"a fail-open dispatch must be counted, not silent")
}

// A tool with no stored schema at all is not a "skip" — there is nothing to
// validate, and nothing to count.
func TestDirectValidation_NoStoredSchemaIsNotCountedAsSkipped(t *testing.T) {
	p := newDirectValidationProxy(t)
	entry := validationEntry()
	entry.ParamsJSON = ""

	before := p.inputValidator.SkippedCount()
	callDirect(t, p, entry, map[string]interface{}{"anything": true})
	assert.Equal(t, before, p.inputValidator.SkippedCount())
}

// T062: non-argument failures keep their current shapes and attach NO schema.
// An agent must never read "your upstream is down" as "your arguments are
// wrong" — that is an unbounded retry loop against a schema that was fine.
func TestDirectValidation_NonArgumentFailuresAttachNoSchema(t *testing.T) {
	p := newDirectValidationProxy(t)

	// Valid args, but there is no upstream client: the failure is transport.
	result := callDirect(t, p, validationEntry(), map[string]interface{}{"path": "/tmp"})

	require.True(t, result.IsError)
	text := result.Content[0].(mcp.TextContent).Text
	assert.NotContains(t, text, "input_schema")
	assert.NotContains(t, text, "invalid_params")
	assert.Contains(t, text, "Error calling fs:read", "the existing transport-error shape is unchanged")
}

// toolCallProbe collects the activity.tool_call.* events the runtime publishes.
type toolCallProbe struct {
	mu     sync.Mutex
	events []internalRuntime.Event
}

func watchToolCalls(t *testing.T, rt *internalRuntime.Runtime) *toolCallProbe {
	t.Helper()
	probe := &toolCallProbe{}
	ch := rt.SubscribeEvents()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for evt := range ch {
			switch evt.Type {
			case internalRuntime.EventTypeActivityToolCallStarted,
				internalRuntime.EventTypeActivityToolCallCompleted:
				probe.mu.Lock()
				probe.events = append(probe.events, evt)
				probe.mu.Unlock()
			}
		}
	}()
	t.Cleanup(func() {
		rt.UnsubscribeEvents(ch)
		<-done
	})
	return probe
}

func (p *toolCallProbe) await(t *testing.T, n int) []internalRuntime.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		p.mu.Lock()
		got := append([]internalRuntime.Event(nil), p.events...)
		p.mu.Unlock()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d tool-call events, saw %d: %+v", n, len(got), got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// T063: a rejected call is VISIBLE in the availability funnel. Validation that
// rejects silently is the observability blind spot issue #969 established this
// handler must not have — the call never happened, as far as any operator
// looking at the funnel could tell.
func TestDirectValidation_RejectionEmitsTheStartedCompletedPair(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "fs", Enabled: true}})
	require.NoError(t, rt.StorageManager().SaveUpstreamServer(&config.ServerConfig{Name: "fs", Enabled: true}))
	require.NoError(t, rt.StorageManager().SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "fs", ToolName: "read", Status: storage.ToolApprovalStatusApproved,
	}))
	probe := watchToolCalls(t, rt)

	result := callDirect(t, proxy, validationEntry(), map[string]interface{}{"depth": 2})
	require.True(t, result.IsError)

	events := probe.await(t, 2)
	require.Len(t, events, 2, "exactly one started and one completed, not a bare rejection")

	assert.Equal(t, internalRuntime.EventTypeActivityToolCallStarted, events[0].Type)
	assert.Equal(t, internalRuntime.EventTypeActivityToolCallCompleted, events[1].Type)

	completed := events[1].Payload
	assert.Equal(t, "error", completed["status"],
		"a rejected call is an error in the funnel, not a success and not an absence")
	assert.Equal(t, "fs", completed["server_name"])
	assert.Equal(t, "read", completed["tool_name"])
	assert.Contains(t, completed["error_message"], "invalid arguments",
		"the funnel record must say WHY, or a rejected call is indistinguishable from a transport failure")

	// Both halves must correlate, or an operator cannot join them.
	assert.Equal(t, events[0].Payload["request_id"], completed["request_id"])
	assert.NotEmpty(t, completed["request_id"])
}

// The unconditional started event stays on the DISPATCH path: a call blocked
// earlier (callability) must not gain a started/completed pair it never had.
func TestDirectValidation_CallabilityBlockStillEmitsNoToolCallPair(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "fs", Enabled: false}})
	probe := watchToolCalls(t, rt)

	result := callDirect(t, proxy, validationEntry(), map[string]interface{}{"path": "/tmp"})
	require.True(t, result.IsError)

	time.Sleep(200 * time.Millisecond)
	probe.mu.Lock()
	defer probe.mu.Unlock()
	assert.Empty(t, probe.events,
		"a callability block is a policy decision, not a tool call — its funnel record is elsewhere")
}

// The validator sees the CALLER's arguments, not the enriched ones. A schema
// with additionalProperties:false would otherwise reject the auth metadata
// injectAuthMetadata adds — turning the proxy's own bookkeeping into the
// agent's bug, on a call whose arguments were correct.
func TestDirectValidation_EnrichedArgsAreNotValidated(t *testing.T) {
	p := newDirectValidationProxy(t)
	entry := validationEntry()
	entry.ParamsJSON = `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`

	// An auth context is REQUIRED here: injectAuthMetadata is a no-op without
	// one, so a context-free call would exercise args == enrichedArgs and the
	// assertion below would hold for the wrong reason.
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "probe",
		TokenPrefix:    "mcp_agt_x",
		AllowedServers: []string{"fs"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})
	require.NotEqual(t,
		map[string]interface{}{"path": "/tmp"},
		injectAuthMetadata(ctx, map[string]interface{}{"path": "/tmp"}),
		"the fixture context must actually enrich, or this test proves nothing")

	result := callDirectAs(t, p, ctx, entry, map[string]interface{}{"path": "/tmp"})

	text := result.Content[0].(mcp.TextContent).Text
	require.Contains(t, text, "Error calling fs:read",
		"the call must reach dispatch, or the assertion below passes for the wrong reason")
	assert.NotContains(t, text, "invalid_params",
		"a strict schema must not reject arguments the caller never sent")
}

package jsruntime

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestCallToolResultExposesJSONTagNames verifies that call_tool() results are
// visible to scripts under their wire (JSON tag) names, not Go field names, and
// that Go methods do not leak onto the result object.
func TestCallToolResultExposesJSONTagNames(t *testing.T) {
	caller := newMockToolCaller()
	caller.results["s:t"] = &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: `{"hello":"world"}`},
		},
	}

	code := `
		const r = call_tool("s", "t", {});
		if (!r.ok) throw new Error("call_tool failed: " + JSON.stringify(r.error));
		({
			hello: JSON.parse(r.result.content[0].text).hello,
			pascal: typeof r.result.Content,
			method: typeof r.result.MarshalJSON
		})
	`

	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap, ok := result.Value.(map[string]interface{})
	if !ok {
		t.Fatalf("expected result to be a map, got %T", result.Value)
	}

	if resultMap["hello"] != "world" {
		t.Errorf("expected hello='world', got %v", resultMap["hello"])
	}
	if resultMap["pascal"] != "undefined" {
		t.Errorf("expected Go field name Content to be absent, got typeof=%v", resultMap["pascal"])
	}
	if resultMap["method"] != "undefined" {
		t.Errorf("expected Go method MarshalJSON to be absent, got typeof=%v", resultMap["method"])
	}
}

// TestCallToolResultMatchesWireShape verifies the shape scripts see is
// byte-for-byte the shape the tool result marshals to on the wire, including
// the custom MarshalJSON semantics (isError omitted when false,
// structuredContent present).
func TestCallToolResultMatchesWireShape(t *testing.T) {
	raw := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: `{"count":3}`},
		},
		StructuredContent: map[string]interface{}{"count": 3},
		IsError:           false,
	}

	caller := newMockToolCaller()
	caller.results["s:t"] = raw

	// Object.assign copies the properties the script can actually see, so the
	// comparison is against the sandbox-visible shape, not the wrapped Go value.
	code := `
		const r = call_tool("s", "t", {});
		if (!r.ok) throw new Error("call_tool failed: " + JSON.stringify(r.error));
		Object.assign({}, r.result)
	`

	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	gotBytes, err := json.Marshal(result.Value)
	if err != nil {
		t.Fatalf("failed to marshal script result: %v", err)
	}
	wantBytes, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("failed to marshal upstream result: %v", err)
	}

	got := unmarshalObject(t, gotBytes)
	want := unmarshalObject(t, wantBytes)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("script sees a different shape than the wire\n got: %s\nwant: %s", gotBytes, wantBytes)
	}
	if _, hasIsError := got["isError"]; hasIsError {
		t.Errorf("expected isError to be omitted when false, got: %s", gotBytes)
	}
	if _, hasStructured := got["structuredContent"]; !hasStructured {
		t.Errorf("expected structuredContent to be present, got: %s", gotBytes)
	}
}

// TestCallToolResultNotSerializable verifies that an upstream result which
// cannot be JSON-encoded surfaces as a call_tool error rather than leaking a
// live Go value into the sandbox.
func TestCallToolResultNotSerializable(t *testing.T) {
	caller := newMockToolCaller()
	caller.results["s:t"] = map[string]interface{}{"ch": make(chan int)}

	code := `
		const r = call_tool("s", "t", {});
		({ ok: r.ok, code: r.error && r.error.code })
	`

	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["ok"] != false {
		t.Errorf("expected call_tool ok=false, got %v", resultMap["ok"])
	}
	if resultMap["code"] != string(ErrorCodeSerializationError) {
		t.Errorf("expected code=%s, got %v", ErrorCodeSerializationError, resultMap["code"])
	}
}

// TestCallToolPlainMapResultUnchanged guards the existing behaviour for callers
// that already return plain maps.
func TestCallToolPlainMapResultUnchanged(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		const r = call_tool("s", "t", {});
		({ ok: r.ok, success: r.result.success })
	`

	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["ok"] != true {
		t.Errorf("expected call_tool ok=true, got %v", resultMap["ok"])
	}
	if resultMap["success"] != true {
		t.Errorf("expected result.success=true, got %v", resultMap["success"])
	}
}

func unmarshalObject(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("failed to unmarshal %s: %v", data, err)
	}
	return out
}

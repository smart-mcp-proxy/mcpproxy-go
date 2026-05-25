package server

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/outputvalidation"
)

// objectSchema requires an integer field "id".
const objectSchema = `{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}`

func newTestValidator() *outputvalidation.Validator {
	return outputvalidation.New(5<<20, 64, nil)
}

func resultWithStructured(structured any) *mcp.CallToolResult {
	return &mcp.CallToolResult{StructuredContent: structured}
}

func TestEvaluateOutputValidation_NilValidator_NoOp(t *testing.T) {
	d := evaluateOutputValidation(nil, "s:t", objectSchema, true, true, resultWithStructured(map[string]any{"id": "nope"}))
	if d.decision != "" || d.block {
		t.Fatalf("nil validator must be a no-op, got %+v", d)
	}
}

func TestEvaluateOutputValidation_EmptySchema_NoOp(t *testing.T) {
	d := evaluateOutputValidation(newTestValidator(), "s:t", "", true, true, resultWithStructured(map[string]any{"id": "nope"}))
	if d.decision != "" {
		t.Fatalf("empty schema must be a no-op, got %+v", d)
	}
}

func TestEvaluateOutputValidation_ErrorResult_Skipped(t *testing.T) {
	res := &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{"id": "nope"}}
	d := evaluateOutputValidation(newTestValidator(), "s:t", objectSchema, true, true, res)
	if d.decision != "" {
		t.Fatalf("IsError result must be skipped (FR-A10), got %+v", d)
	}
}

func TestEvaluateOutputValidation_Conforming_Pass(t *testing.T) {
	d := evaluateOutputValidation(newTestValidator(), "s:t", objectSchema, true, true, resultWithStructured(map[string]any{"id": 42}))
	if d.decision != "" || d.block {
		t.Fatalf("conforming output must pass, got %+v", d)
	}
}

func TestEvaluateOutputValidation_Violation_StrictBlocks(t *testing.T) {
	d := evaluateOutputValidation(newTestValidator(), "s:t", objectSchema, true, false, resultWithStructured(map[string]any{"id": "not-an-int"}))
	if d.decision != "blocked" || !d.block {
		t.Fatalf("strict violation must block, got %+v", d)
	}
	if d.reason == "" {
		t.Fatal("blocked decision must carry a reason")
	}
}

func TestEvaluateOutputValidation_Violation_WarnForwards(t *testing.T) {
	d := evaluateOutputValidation(newTestValidator(), "s:t", objectSchema, false, false, resultWithStructured(map[string]any{"id": "not-an-int"}))
	if d.decision != "warning" || d.block {
		t.Fatalf("warn violation must forward + tag, got %+v", d)
	}
	if d.reason == "" {
		t.Fatal("warning decision must carry a reason")
	}
}

func TestEvaluateOutputValidation_MissingStructured_WarnNoOp(t *testing.T) {
	// ContextForge #4042 trap: declared schema, text-only response (nil structured).
	d := evaluateOutputValidation(newTestValidator(), "s:t", objectSchema, false, false, resultWithStructured(nil))
	if d.decision != "" {
		t.Fatalf("warn mode must not fail on missing structured content (FR-A8), got %+v", d)
	}
}

func TestEvaluateOutputValidation_MissingStructured_StrictAllow_NoOp(t *testing.T) {
	d := evaluateOutputValidation(newTestValidator(), "s:t", objectSchema, true, false, resultWithStructured(nil))
	if d.decision != "" {
		t.Fatalf("strict+allow must forward missing structured content, got %+v", d)
	}
}

func TestEvaluateOutputValidation_MissingStructured_StrictBlock_Blocks(t *testing.T) {
	d := evaluateOutputValidation(newTestValidator(), "s:t", objectSchema, true, true, resultWithStructured(nil))
	if d.decision != "blocked" || !d.block {
		t.Fatalf("strict+block must block missing structured content, got %+v", d)
	}
}

func TestEvaluateOutputValidation_UncompilableSchema_DegradesToPass(t *testing.T) {
	// {"type": 123} is not a valid JSON Schema; FR-A9 says degrade to no-op, never block.
	d := evaluateOutputValidation(newTestValidator(), "s:t", `{"type": 123}`, true, true, resultWithStructured(map[string]any{"id": "anything"}))
	if d.decision != "" || d.block {
		t.Fatalf("uncompilable schema must degrade to pass (FR-A9), got %+v", d)
	}
}

func TestEvaluateOutputValidation_GuardBreach_StrictBlocks(t *testing.T) {
	// Tiny byte guard so any payload trips it; proves a guard breach is a violation.
	v := outputvalidation.New(8, 64, nil)
	d := evaluateOutputValidation(v, "s:t", objectSchema, true, false, resultWithStructured(map[string]any{"id": 42, "padding": "this easily exceeds eight bytes"}))
	if d.decision != "blocked" || !d.block {
		t.Fatalf("guard breach in strict must block (US2), got %+v", d)
	}
}

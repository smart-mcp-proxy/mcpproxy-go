package outputvalidation

import (
	"strings"
	"testing"

	"go.uber.org/zap/zaptest"
)

// alwaysFailSchema is a schema that will always produce a violation for any object.
// Used to prove that when a guard trips, schema validation is NOT reached.
const alwaysFailSchema = `{"not": {}}`

func TestGuard_MaxBytes_Exceeded(t *testing.T) {
	// Create a payload large enough to exceed 10 bytes limit
	payload := map[string]any{"data": strings.Repeat("x", 100)}
	v := New(10, 0, zaptest.NewLogger(t))

	// Even though alwaysFailSchema would cause a violation, the guard should fire first
	verdict := v.Validate("srv:tool", alwaysFailSchema, payload)
	if !verdict.IsViolation() {
		t.Fatal("expected violation due to max_bytes guard")
	}
	if verdict.GuardHit != "max_bytes" {
		t.Fatalf("expected GuardHit=max_bytes, got %q", verdict.GuardHit)
	}
	if verdict.Reason == "" {
		t.Fatal("expected non-empty Reason on guard hit")
	}
}

func TestGuard_MaxBytes_NotSchemaViolation(t *testing.T) {
	// This test specifically verifies guard fires BEFORE schema validation.
	// We use alwaysFailSchema + a passing payload that's too large.
	// The guard intercepts first — outcome is GuardHit, not a schema error.
	payload := map[string]any{"data": strings.Repeat("a", 200)}
	v := New(10, 0, zaptest.NewLogger(t))
	verdict := v.Validate("srv:tool", alwaysFailSchema, payload)
	if verdict.GuardHit != "max_bytes" {
		t.Fatalf("schema validation was not short-circuited by byte guard; GuardHit=%q, Reason=%q", verdict.GuardHit, verdict.Reason)
	}
}

func TestGuard_MaxDepth_Exceeded(t *testing.T) {
	// Build a deeply nested structure: depth 5 object nesting
	// maxDepth=3 means depth 4+ is rejected
	nested := buildNestedObject(5)
	v := New(0, 3, zaptest.NewLogger(t))
	verdict := v.Validate("srv:tool", alwaysFailSchema, nested)
	if !verdict.IsViolation() {
		t.Fatal("expected violation due to max_depth guard")
	}
	if verdict.GuardHit != "max_depth" {
		t.Fatalf("expected GuardHit=max_depth, got %q", verdict.GuardHit)
	}
}

func TestGuard_MaxDepth_Array_Exceeded(t *testing.T) {
	// Deeply nested arrays
	nested := buildNestedArray(5)
	v := New(0, 3, zaptest.NewLogger(t))
	verdict := v.Validate("srv:tool", alwaysFailSchema, nested)
	if verdict.GuardHit != "max_depth" {
		t.Fatalf("expected GuardHit=max_depth for deeply nested array, got %q", verdict.GuardHit)
	}
}

func TestGuard_WithinBoth_Pass(t *testing.T) {
	// Small, shallow payload — both guards pass, schema validation runs.
	// Conforming payload + valid schema → OutcomePass.
	schema := `{"type":"object"}`
	payload := map[string]any{"name": "hello"}
	v := New(1024, 10, zaptest.NewLogger(t))
	verdict := v.Validate("srv:tool", schema, payload)
	if verdict.IsViolation() {
		t.Fatalf("expected Pass for payload within guards, got Violate: GuardHit=%q Reason=%q", verdict.GuardHit, verdict.Reason)
	}
}

func TestGuard_MaxBytesZero_Disabled(t *testing.T) {
	// maxBytes <= 0 disables the byte guard
	payload := map[string]any{"data": strings.Repeat("x", 10_000)}
	v := New(0, 0, zaptest.NewLogger(t)) // both guards disabled
	schema := `{"type":"object"}`
	verdict := v.Validate("srv:tool", schema, payload)
	if verdict.GuardHit == "max_bytes" {
		t.Fatal("max_bytes guard should be disabled when maxBytes <= 0")
	}
}

func TestGuard_MaxDepthZero_Disabled(t *testing.T) {
	// maxDepth <= 0 disables depth guard
	nested := buildNestedObject(50)
	v := New(0, 0, zaptest.NewLogger(t)) // both guards disabled
	schema := `{"type":"object"}`
	verdict := v.Validate("srv:tool", schema, nested)
	if verdict.GuardHit == "max_depth" {
		t.Fatal("max_depth guard should be disabled when maxDepth <= 0")
	}
}

func TestGuard_MaxBytesNegative_Disabled(t *testing.T) {
	payload := map[string]any{"data": strings.Repeat("x", 10_000)}
	v := New(-1, -1, zaptest.NewLogger(t))
	schema := `{"type":"object"}`
	verdict := v.Validate("srv:tool", schema, payload)
	if verdict.GuardHit != "" {
		t.Fatalf("guards should be disabled when maxBytes/maxDepth < 0, got GuardHit=%q", verdict.GuardHit)
	}
}

func TestGuard_MaxDepth_ObjectCountedCorrectly(t *testing.T) {
	// depth=1: {"a": 1}      (object is depth 1, value is depth 2)
	// maxDepth=2 should allow it
	payload := map[string]any{"a": 1}
	v := New(0, 2, zaptest.NewLogger(t))
	schema := `{"type":"object"}`
	verdict := v.Validate("srv:tool", schema, payload)
	if verdict.GuardHit == "max_depth" {
		t.Fatalf("depth 2 payload should not trip maxDepth=2 guard")
	}
}

// buildNestedObject creates an n-level deep nested object: {"child": {"child": ...}}
func buildNestedObject(depth int) any {
	if depth <= 0 {
		return "leaf"
	}
	return map[string]any{"child": buildNestedObject(depth - 1)}
}

// buildNestedArray creates an n-level deep nested array: [[[ ... ]]]
func buildNestedArray(depth int) any {
	if depth <= 0 {
		return "leaf"
	}
	return []any{buildNestedArray(depth - 1)}
}

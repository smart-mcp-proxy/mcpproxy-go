package outputvalidation

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func TestValidate_EmptySchema_Pass(t *testing.T) {
	v := New(0, 0, nil)
	verdict := v.Validate("srv:tool", "", map[string]any{"id": 1})
	if verdict.IsViolation() {
		t.Fatalf("expected Pass for empty schema, got Violate: %s", verdict.Reason)
	}
	if verdict.Outcome != OutcomePass {
		t.Fatalf("expected OutcomePass, got %d", verdict.Outcome)
	}
}

func TestValidate_NilStructured_Pass(t *testing.T) {
	schema := `{"type":"object","required":["id"],"properties":{"id":{"type":"integer"}}}`
	v := New(0, 0, nil)
	verdict := v.Validate("srv:tool", schema, nil)
	if verdict.IsViolation() {
		t.Fatalf("expected Pass for nil structured, got Violate: %s", verdict.Reason)
	}
}

func TestValidate_ConformingStructured_Pass(t *testing.T) {
	schema := `{"type":"object","required":["id"],"properties":{"id":{"type":"integer"}}}`
	v := New(0, 0, zaptest.NewLogger(t))
	verdict := v.Validate("srv:tool", schema, map[string]any{"id": 42})
	if verdict.IsViolation() {
		t.Fatalf("expected Pass for conforming payload, got Violate: %s", verdict.Reason)
	}
}

func TestValidate_ViolatingStructured_Violate(t *testing.T) {
	// schema requires "id" to be integer; we pass a string — should violate
	schema := `{"type":"object","required":["id"],"properties":{"id":{"type":"integer"}}}`
	v := New(0, 0, zaptest.NewLogger(t))
	verdict := v.Validate("srv:tool", schema, map[string]any{"id": "not-an-integer"})
	if !verdict.IsViolation() {
		t.Fatalf("expected Violate for non-conforming payload, got Pass")
	}
	if verdict.Reason == "" {
		t.Fatal("expected non-empty Reason on violation")
	}
	if verdict.GuardHit != "" {
		t.Fatalf("expected no GuardHit on schema violation, got %q", verdict.GuardHit)
	}
}

func TestValidate_UncompilableSchema_Pass(t *testing.T) {
	// {"type": 123} — type must be a string or array, not a number; invalid per spec
	schema := `{"type": 123}`
	v := New(0, 0, zaptest.NewLogger(t))
	verdict := v.Validate("srv:bad", schema, map[string]any{"id": 1})
	if verdict.IsViolation() {
		t.Fatalf("expected Pass (FR-A9) for uncompilable schema, got Violate: %s", verdict.Reason)
	}
}

func TestValidate_CacheReuse(t *testing.T) {
	schema := `{"type":"object","required":["id"],"properties":{"id":{"type":"integer"}}}`
	v := New(0, 0, zaptest.NewLogger(t))

	// reset counter
	compileCount.Store(0)

	v.Validate("srv:cached", schema, map[string]any{"id": 1})
	v.Validate("srv:cached", schema, map[string]any{"id": 2})
	v.Validate("srv:cached", schema, map[string]any{"id": 3})

	count := compileCount.Load()
	if count != 1 {
		t.Fatalf("expected schema to be compiled exactly once, got %d compilations", count)
	}
}

func TestValidate_NilLogger_NoPanic(t *testing.T) {
	schema := `{"type":"object"}`
	// Must not panic when logger is nil
	v := New(0, 0, nil)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked with nil logger: %v", r)
		}
	}()
	v.Validate("srv:tool", schema, map[string]any{})
}

func TestValidate_DifferentToolKeys_CompiledSeparately(t *testing.T) {
	schema := `{"type":"object"}`
	v := New(0, 0, zaptest.NewLogger(t))

	compileCount.Store(0)
	v.Validate("srv:tool1", schema, map[string]any{})
	v.Validate("srv:tool2", schema, map[string]any{})

	// Same schemaJSON but different toolKeys — two separate cache entries.
	// Each must compile once.
	count := compileCount.Load()
	if count != 2 {
		t.Fatalf("expected 2 compilations for 2 different tool keys, got %d", count)
	}
}

func TestValidate_UncompilableSchema_WarnOnce(t *testing.T) {
	// Sentinel ensures that subsequent calls for the same uncompilable schema
	// do NOT try to compile again (and do NOT log more warnings).
	schema := `{"type": 123}`
	v := New(0, 0, zap.NewNop())

	compileCount.Store(0)
	v.Validate("srv:badonce", schema, map[string]any{"x": 1})
	v.Validate("srv:badonce", schema, map[string]any{"x": 2})
	v.Validate("srv:badonce", schema, map[string]any{"x": 3})

	count := compileCount.Load()
	if count != 1 {
		t.Fatalf("expected exactly 1 compile attempt for uncompilable schema (sentinel), got %d", count)
	}
}

func TestVerdictIsViolation(t *testing.T) {
	pass := Verdict{Outcome: OutcomePass}
	if pass.IsViolation() {
		t.Fatal("OutcomePass should not be a violation")
	}
	viol := Verdict{Outcome: OutcomeViolate, Reason: "oops"}
	if !viol.IsViolation() {
		t.Fatal("OutcomeViolate should be a violation")
	}
}

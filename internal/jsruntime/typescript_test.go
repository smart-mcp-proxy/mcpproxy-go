package jsruntime

import (
	"strings"
	"testing"
)

func TestTranspileTypeScript_BasicTypeAnnotation(t *testing.T) {
	code := `const x: number = 42; x;`
	result, jsErr := TranspileTypeScript(code)
	if jsErr != nil {
		t.Fatalf("expected no error, got: %v", jsErr)
	}
	// The output should not contain ": number"
	if strings.Contains(result, ": number") {
		t.Errorf("expected type annotation to be stripped, got: %s", result)
	}
	// The output should still contain the value assignment
	if !strings.Contains(result, "42") {
		t.Errorf("expected output to contain '42', got: %s", result)
	}
}

func TestTranspileTypeScript_InterfaceRemoved(t *testing.T) {
	code := `
interface User {
	name: string;
	age: number;
}
const user: User = { name: "Alice", age: 30 };
user;
`
	result, jsErr := TranspileTypeScript(code)
	if jsErr != nil {
		t.Fatalf("expected no error, got: %v", jsErr)
	}
	if strings.Contains(result, "interface") {
		t.Errorf("expected interface to be removed, got: %s", result)
	}
	if !strings.Contains(result, "Alice") {
		t.Errorf("expected output to contain 'Alice', got: %s", result)
	}
}

func TestTranspileTypeScript_GenericsRemoved(t *testing.T) {
	code := `
function identity<T>(arg: T): T {
	return arg;
}
const result = identity<number>(42);
result;
`
	result, jsErr := TranspileTypeScript(code)
	if jsErr != nil {
		t.Fatalf("expected no error, got: %v", jsErr)
	}
	// Generics should be stripped
	if strings.Contains(result, "<T>") {
		t.Errorf("expected generics to be stripped, got: %s", result)
	}
	if !strings.Contains(result, "42") {
		t.Errorf("expected output to contain '42', got: %s", result)
	}
}

func TestTranspileTypeScript_EnumProducesJS(t *testing.T) {
	code := `
enum Direction {
	Up = "UP",
	Down = "DOWN",
	Left = "LEFT",
	Right = "RIGHT"
}
const d: Direction = Direction.Up;
d;
`
	result, jsErr := TranspileTypeScript(code)
	if jsErr != nil {
		t.Fatalf("expected no error, got: %v", jsErr)
	}
	// Enum should produce JavaScript output (not just be removed)
	if !strings.Contains(result, "UP") {
		t.Errorf("expected enum values in output, got: %s", result)
	}
	// Type annotation should be stripped
	if strings.Contains(result, ": Direction") {
		t.Errorf("expected type annotation stripped, got: %s", result)
	}
}

func TestTranspileTypeScript_NamespaceProducesJS(t *testing.T) {
	code := `
namespace MyNamespace {
	export const value = 42;
}
MyNamespace.value;
`
	result, jsErr := TranspileTypeScript(code)
	if jsErr != nil {
		t.Fatalf("expected no error, got: %v", jsErr)
	}
	if !strings.Contains(result, "42") {
		t.Errorf("expected namespace value in output, got: %s", result)
	}
}

func TestTranspileTypeScript_PlainJavaScriptPassthrough(t *testing.T) {
	code := `var x = 42; x;`
	result, jsErr := TranspileTypeScript(code)
	if jsErr != nil {
		t.Fatalf("expected no error, got: %v", jsErr)
	}
	if !strings.Contains(result, "42") {
		t.Errorf("expected output to contain '42', got: %s", result)
	}
}

func TestTranspileTypeScript_InvalidCode(t *testing.T) {
	code := `const x: number = ;` // invalid: missing value
	_, jsErr := TranspileTypeScript(code)
	if jsErr == nil {
		t.Fatal("expected transpilation error, got nil")
	}
	if jsErr.Code != ErrorCodeTranspileError {
		t.Errorf("expected error code TRANSPILE_ERROR, got %s", jsErr.Code)
	}
	// Error should mention line information
	if !strings.Contains(jsErr.Message, "line") {
		t.Errorf("expected error to contain line info, got: %s", jsErr.Message)
	}
}

func TestTranspileTypeScript_EmptyCode(t *testing.T) {
	code := ``
	result, jsErr := TranspileTypeScript(code)
	if jsErr != nil {
		t.Fatalf("expected no error for empty code, got: %v", jsErr)
	}
	// Empty input should produce empty or whitespace-only output
	if strings.TrimSpace(result) != "" {
		t.Errorf("expected empty output, got: %q", result)
	}
}

func TestTranspileTypeScript_TypeAlias(t *testing.T) {
	code := `
type StringOrNumber = string | number;
const val: StringOrNumber = "hello";
val;
`
	result, jsErr := TranspileTypeScript(code)
	if jsErr != nil {
		t.Fatalf("expected no error, got: %v", jsErr)
	}
	if strings.Contains(result, "StringOrNumber") {
		t.Errorf("expected type alias to be removed, got: %s", result)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected output to contain 'hello', got: %s", result)
	}
}

func TestTranspileTypeScript_AsExpression(t *testing.T) {
	code := `const x = (42 as number); x;`
	result, jsErr := TranspileTypeScript(code)
	if jsErr != nil {
		t.Fatalf("expected no error, got: %v", jsErr)
	}
	if strings.Contains(result, " as ") {
		t.Errorf("expected 'as' expression to be stripped, got: %s", result)
	}
	if !strings.Contains(result, "42") {
		t.Errorf("expected output to contain '42', got: %s", result)
	}
}

func TestValidateLanguage(t *testing.T) {
	tests := []struct {
		name     string
		language string
		wantErr  bool
	}{
		{"empty defaults to javascript", "", false},
		{"javascript is valid", "javascript", false},
		{"typescript is valid", "typescript", false},
		{"python is invalid", "python", true},
		{"TypeScript (wrong case) is invalid", "TypeScript", true},
		{"js is invalid", "js", true},
		{"ts is invalid", "ts", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLanguage(tt.language)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for language %q, got nil", tt.language)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("expected no error for language %q, got: %v", tt.language, err)
			}
			if tt.wantErr && err != nil && err.Code != ErrorCodeInvalidLanguage {
				t.Errorf("expected error code INVALID_LANGUAGE, got %s", err.Code)
			}
		})
	}
}

func BenchmarkTranspileTypeScript(b *testing.B) {
	// Generate a ~10KB TypeScript code sample
	var code strings.Builder
	code.WriteString("interface Config { host: string; port: number; debug: boolean; }\n")
	code.WriteString("type Result<T> = { ok: true; value: T } | { ok: false; error: string };\n")
	for i := 0; i < 200; i++ {
		code.WriteString("const val")
		code.WriteString(strings.Repeat("x", 3))
		code.WriteString(": number = ")
		code.WriteString("42;\n")
	}
	code.WriteString("({ result: 42 });\n")
	codeStr := code.String()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, jsErr := TranspileTypeScript(codeStr)
		if jsErr != nil {
			b.Fatalf("transpilation failed: %v", jsErr)
		}
	}
}

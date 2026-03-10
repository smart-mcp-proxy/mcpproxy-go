# Research: TypeScript Code Execution Support

**Feature**: 033-typescript-code-execution
**Date**: 2026-03-10

## R1: esbuild Go API for TypeScript Transpilation

**Decision**: Use `github.com/evanw/esbuild` Go API with `api.Transform()` for TypeScript-to-JavaScript transpilation.

**Rationale**:
- esbuild's Go API is a native Go library (no external process spawning needed)
- `api.Transform()` performs in-memory string-to-string transformation, ideal for this use case
- Transpilation is extremely fast: typically <1ms for typical code sizes
- Supports all TypeScript syntax: type annotations, interfaces, enums, generics, namespaces
- The `api.LoaderTS` loader strips types without type-checking (exactly what we need)
- Zero runtime dependencies - compiles into the Go binary

**Alternatives considered**:
- `swc` (Rust-based): Requires CGO or external binary, adds build complexity
- `tsc` (TypeScript compiler): Requires Node.js runtime, 100x+ slower
- Manual regex type stripping: Fragile, won't handle enums/namespaces correctly
- `babel`: Requires Node.js runtime

**Key API usage**:
```go
import "github.com/evanw/esbuild/pkg/api"

result := api.Transform(code, api.TransformOptions{
    Loader: api.LoaderTS,
    Target: api.ES2015,  // ES5 target not available; ES2015 is closest compatible
})
```

## R2: esbuild Target Compatibility with goja

**Decision**: Use `api.ESNext` as the esbuild target, since goja supports ES5.1+ and esbuild's type-stripping doesn't downlevel JavaScript features regardless of target.

**Rationale**:
- esbuild's `Loader: api.LoaderTS` mode primarily strips type annotations
- The `Target` option controls JavaScript syntax downleveling (e.g., arrow functions, template literals)
- Since goja already supports ES5.1+ including many ES6+ features, using ESNext avoids unnecessary transformations
- TypeScript enums and namespaces produce JavaScript regardless of target, and the output is compatible with goja
- If specific ES6+ features cause goja issues, they would already be a problem with plain JavaScript execution

**Alternatives considered**:
- `api.ES5`: Not available in esbuild (minimum is ES2015)
- `api.ES2015`: Would add unnecessary downleveling; goja handles most ES2015+ features

## R3: Error Handling for Transpilation Failures

**Decision**: Add a new `ErrorCodeTranspileError` to the existing error code enum. Transpilation errors include source location (line, column) from esbuild's error messages.

**Rationale**:
- esbuild provides structured error messages with file/line/column information
- Reusing `ErrorCodeSyntaxError` would conflate TypeScript type errors with JavaScript syntax errors
- A dedicated error code allows clients to distinguish transpilation failures from runtime errors
- Error messages should reference the original TypeScript source location, not the transpiled output

## R4: Language Parameter Design

**Decision**: Add `language` as a top-level string parameter (not nested in `options`) to the code_execution tool schema, REST API request body, and CLI flags.

**Rationale**:
- The language determines how the code is processed before execution - it's a fundamental property of the request, not an execution option
- Top-level placement matches the prominence of the `code` parameter (they're directly related)
- Simple string enum (`"javascript"`, `"typescript"`) is easy to validate and extend later
- Default value `"javascript"` ensures full backward compatibility

**Alternatives considered**:
- Nested in `options` object: Less visible, semantically wrong (it's not an execution option)
- Auto-detection from code content: Unreliable, opaque to users, hard to debug
- Separate tool (`code_execution_ts`): Duplicates tool logic, harder to maintain

## R5: Performance Validation Approach

**Decision**: Validate the <5ms transpilation target using benchmark tests and log transpilation duration for production observability.

**Rationale**:
- esbuild benchmarks show sub-millisecond performance for typical code sizes
- Adding a Go benchmark test (`BenchmarkTranspile`) provides reproducible evidence
- Logging transpilation duration alongside execution duration gives operators visibility
- No separate timeout needed - transpilation time counts toward the existing execution timeout

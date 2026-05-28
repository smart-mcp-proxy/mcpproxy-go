// Package outputvalidation provides a pure, self-contained validator that checks
// a tool's structured output against its declared JSON Schema (draft 2020-12).
//
// Contract:
//   - Imports only stdlib, go.uber.org/zap, and github.com/santhosh-tekuri/jsonschema/v6.
//   - MUST NOT import internal/server, internal/config, internal/storage, or mcp-go.
//   - Safe for concurrent use (sync.Map cache, no shared mutable state).
//   - Never blocks on the proxy's inability to compile a schema (FR-A9).
//   - Never mutates the structured value passed to Validate.
package outputvalidation

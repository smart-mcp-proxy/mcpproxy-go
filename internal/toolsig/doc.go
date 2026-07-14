// Package toolsig compiles deterministic compact tool signatures (Spec 085,
// Compact Router) from a tool's JSON Schema (ToolMetadata.ParamsJSON) and its
// description.
//
// The normative grammar — type abbreviations, the required (*) and lossy (~)
// markers, atom quoting, parameter ordering, the (~) unparseable-schema
// fallback, and the first-sentence terminator rules — lives in
// specs/085-compact-router/contracts/signature-grammar.md. Worked examples
// E1–E11 in that contract are pinned byte-for-byte by this package's tests.
//
// IMPORT RULE: this is a LEAF package. It MUST NOT import internal/server (or
// anything that pulls the HTTP/MCP server surface): the spec-083 bench arms
// (bench/arms) must be able to import the same grammar (FR-019) without
// dragging in the server, and internal/server itself imports this package.
// Allowed dependencies: stdlib only.
package toolsig

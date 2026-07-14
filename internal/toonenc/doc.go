// Package toonenc implements the adaptive TOON encoder for tool-call result
// text blocks (spec 084). It decides, per block, whether to replace a JSON
// rendering with a smaller TOON encoding (marker + decode hint + body) or to
// pass the block through byte-identically.
//
// Layering rule: this package depends ONLY on the standard library and
// github.com/toon-format/toon-go. It is imported by both internal/server
// (production seam) and bench/arms (spec-083 profiler, FR-012), so it must
// never import internal/server, internal/config, or any other mcpproxy
// package — that is what keeps "the profiler exercises the exact production
// code path" literally true without dragging the server into the bench build.
//
// All exported functions are pure and deterministic (FR-011): identical input
// produces an identical decision and identical bytes. Observability (logging,
// metrics) for encoder failures is the caller's responsibility (FR-006).
package toonenc

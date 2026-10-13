// Package detect implements the deterministic, offline MCP tool-scanner v2
// (Spec 076).
//
// Contract (see specs/076-deterministic-tool-scanner/contracts/detect-engine.md):
//
//   - Offline: this package performs NO I/O. It imports no networking
//     (net, net/http), no process execution (os/exec), no filesystem access
//     (os), and no HTTP/Docker client. Detection runs purely over in-memory
//     tool definitions supplied by the caller. The offline guarantee is
//     enforced by the standing import-guard test (imports_test.go) and backs
//     FR-001.
//
//   - Deterministic: identical input (a RegistryView) yields byte-identical
//     output, including finding and signal ordering. No maps are iterated for
//     output ordering; no clocks or randomness are consulted.
//
//   - Total: every registered Check.Inspect call is run under recover(). A
//     check that panics or errors is isolated, counted in Coverage, and never
//     aborts the scan. A degraded scan still returns its other findings, the
//     same way the existing scanner surfaces scanners_failed.
//
// The engine aggregates per-tool Signals into the existing
// internal/security/scanner.ScanFinding type (now additively carrying
// Confidence and Signals), so all CLI/REST/MCP entry points keep their shapes.
package detect

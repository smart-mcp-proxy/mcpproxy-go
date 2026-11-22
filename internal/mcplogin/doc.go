// Package mcplogin holds the pure protocol logic for opt-in browser login
// (OAuth 2.1 + PKCE) of MCP clients against the local mcpproxy (Spec 132).
//
// Scope: protocol rules only. No HTTP wiring, no storage and no new external
// dependencies. From this repository it may import only internal/auth,
// internal/clientidentity and internal/upstream/limiter.
package mcplogin

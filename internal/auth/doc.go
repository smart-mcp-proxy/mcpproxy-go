// Package auth provides agent token authentication for MCPProxy.
//
// It implements token generation, validation, HMAC-based hashing,
// and context propagation for distinguishing admin vs agent access.
// Agent tokens use the "mcp_agt_" prefix and are secured with
// HMAC-SHA256 hashing before storage.
package auth

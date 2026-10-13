// Package headerfwd implements Spec 112 client header forwarding: capturing an
// allowlisted subset of the inbound MCP client HTTP headers into an immutable,
// value-redacting Snapshot, filtering it per upstream server, and exposing the
// result to the streamable-HTTP transport header func.
//
// Two private context keys gate forwarding (FR-009). Key A carries the edge
// snapshot captured from the inbound request. Key B carries the per-server
// outbound set and is set only by core.Client.CallTool; the header func reads
// key B only, so connect/list/refresh paths can never forward.
//
// Forwarded values are unverified client assertions (FR-022). This package
// never exposes values through formatting, JSON or logging.
package headerfwd

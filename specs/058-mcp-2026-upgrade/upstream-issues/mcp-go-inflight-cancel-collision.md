# mcp-go: modern requests share the inflight cancellation key `":<id>"`

**Target**: https://github.com/mark3labs/mcp-go · **Status**: DRAFT — not filed.
`gh issue create` on 2026-10-20 was refused: "Interactions on this repository have
been restricted to collaborators only." File it when the restriction lifts
(GitHub interaction limits expire after at most six months), or hand it to a
collaborator. mcpproxy's mitigation is `modernCancellationGuard`
(`internal/server/mcp.go`); `TestCancelIsolation_LibraryCollisionStillPresent`
skips once the library no longer collides, which is the signal to retire both.

**Suggested title**: Modern (2026-07-28) requests share the inflight cancellation
key `":<id>"`: one client can cancel another's in-flight request

---

## Summary

Under protocol version `2026-07-28` every request is served by an ephemeral session whose `SessionID()` is `""`. `inflightKey` (server/server.go) keys in-flight cancellations as `"<sessionID>:<requestID>"`, and `HandleMessage` registers every request under that key unconditionally (server/request_handler.go). So **all modern clients share the key space `":<id>"`**: two clients that both use JSON-RPC id `1` (the common case for a fresh client) collide, and a `notifications/cancelled` posted by one **cancels the other client's in-flight request**.

The notification is handled in `handleNotification` before any hook runs (`if baseMessage.ID == nil { s.handleNotification(...) }`), so an embedding server cannot intercept it through `Hooks`.

Affects v1.0.0; `inflightKey` is unchanged on `main` (v1.3.0).

## Reproduction

Against an **unpinned** `NewStreamableHTTPServer(srv)` (default options, so `2026-07-28` is served) with one tool whose handler blocks until released or `ctx.Done()`:

1. Client A — modern request (`Mcp-Protocol-Version: 2026-07-28`, `_meta` with `io.modelcontextprotocol/protocolVersion` and `clientCapabilities`) — POSTs `tools/call` for the blocking tool with `"id": 1`. The handler starts.
2. Client B — a different HTTP client, also modern — POSTs `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`.
3. Client A's handler context is cancelled; its call returns `context canceled` instead of the tool result.

Observed with mcp-go v1.0.0. A Go test reproducing it (and skipping once the library no longer collides) is `TestCancelIsolation_LibraryCollisionStillPresent` in smart-mcp-proxy/mcpproxy-go, `internal/server/cancel_isolation_test.go`.

## Expected

A cancellation notification must only ever cancel a request from the same client. In the legacy era the session id guarantees that; in the modern era nothing does.

## Possible fixes

- Key in-flight cancels for an ephemeral (`SessionID() == ""`) session by a per-request nonce rather than `":<id>"`, so a separately posted notification can never match another client's request. Since a modern notification cannot carry that nonce, this effectively means a separately posted `notifications/cancelled` cancels nothing in the modern era — which matches the stateless protocol: the client abandons a call by closing the connection, and the transport cancels the handler's context.
- Equivalently: in `handlePost`, do not forward `notifications/cancelled` to `HandleMessage` when `era.modern` (acknowledge with 202 as today).

Either way, the collision across clients should not be possible by default.

## Context

Found while preparing mcpproxy to serve `2026-07-28` (smart-mcp-proxy/mcpproxy-go spec 058, risk R1). mcpproxy currently mitigates it with an HTTP middleware that drops cancellation notifications carried by a modern POST; happy to send a PR for whichever fix you prefer.

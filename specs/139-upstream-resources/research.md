# Research: Upstream Resources

## R1. Address form

- **Decision**: `mcpproxy://upstream/<pct-encoded-server>/<original-uri-verbatim>`.
- **Rationale**: One fixed prefix keeps routing a string operation; the server segment is injective and contains no RFC 6570 expression characters, so the same prefix works for templates; the original URI is kept byte for byte, so no information is lost and the upstream gets exactly what it published. A URI whose path holds another URI is valid RFC 3986 (`:` and `/` are allowed in path segments after the first).
- **Alternatives**: (a) `<server>:<uri>` like tool names — not a valid URI when the server name is not a valid scheme, and collides with real schemes. (b) Percent-encode the whole original URI into one segment — breaks templates (`{` would be encoded, expansion no longer composes). (c) Leave URIs unchanged and pick the owner by lookup — two servers that list the same URI collide (US1-2) and templates cannot be routed.

## R2. Where the per-caller filter runs

- **Decision**: SDK after-list hooks filtering the result in place, plus a gate in every handler.
- **Rationale**: mcp-go v1.6.0 has `WithPromptFilter`/`WithToolFilter` but no resource filter. The after-list hooks receive the result pointer before `createResponse`, and mcpproxy sets no pagination limit, so the filtered result is the whole list. The handler-side gate is the read half, the same split prompts use (filter + `authorizeAggregatedPromptServer`).
- **Alternatives**: (a) an HTTP interceptor answering `resources/*` like Spec 125's native skills — duplicates header/era/probe machinery (`nativeProbeGate`), and must be repeated per routing mode; (b) session-scoped resources (`SessionWithResources`) — per-connection lists, which Spec 058 FR-012 forbids, and the session wrapper does not implement it.

## R3. Refusal indistinguishable from not-found

- **Decision**: a handler refusal returns `server.NewHandlerError` with the SDK's own not-found code and message.
- **Rationale**: a plain handler error becomes `-32603` with a different message, which would tell a scoped caller that the address exists.

## R4. Approval baseline for resources

- **Decision**: none in this spec; metadata scan + content through output policy.
- **Rationale**: resource lists change by design (files come and go); a hold-until-approved baseline would make most file and database servers unusable. Content is untrusted upstream output, the same class as a tool response, which is not approval-gated either. Skill files, which are instructions by intent, keep Spec 125's gate (FR-017).

## R5. Response bound

- **Decision**: 8 MiB of upstream JSON per read, enforced by `transport.WithResponseLimit` on HTTP transports and a post-read size check on all transports.
- **Rationale**: `tool_response_limit` is a char limit for tool text with a `read_cache` paging path; resources have no paging path in MCP, and blobs must not be truncated. 8 MiB of JSON is ~6 MiB of binary, enough for documents and images, small enough to bound memory.

## R6. Activity accounting

- **Decision**: new type `resource_read`, not an attempt for Spec 124 counters (same class as `prompt_get`).
- **Rationale**: work-session failure facts are about tool attempts; counting reads would change every session's numbers. A later spec can promote it.

## R7. Upstream protocol

- **Decision**: use the 2025-11-25 era methods upstream (`resources/list`, `resources/templates/list`, `resources/read`, and in PR-3 `resources/subscribe`).
- **Rationale**: Spec 058 pins the upstream era; mcp-go's client exposes `ListResourcesByPage`, `ListResourceTemplatesByPage`, `ReadResource`, `Subscribe`, `Unsubscribe`.

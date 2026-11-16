# Research: Skills over MCP

All items are resolved; none is left as NEEDS CLARIFICATION.

## R1. Does an upstream announce skills under mcpproxy's current handshake?

**Decision**: Yes for the server checked; keep the handshake unchanged.
**Evidence** (live, 2026-11-16, `https://huggingface.co/mcp`, raw JSON-RPC over streamable HTTP): `initialize` with `protocolVersion: "2025-11-25"` and `capabilities: {}` returned
`{"tools":{"listChanged":false},"resources":{"listChanged":false,"subscribe":false},"extensions":{"io.modelcontextprotocol/skills":{"directoryRead":true}}}`.
Advertising the extension in the client capabilities, or asking for `2026-07-28`, returned the same capabilities and the same negotiated version (`2025-11-25`).
**Alternatives considered**: advertise the extension always (changes every upstream handshake, Spec 058 FR-027); lift the protocol pin (out of scope).

## R2. Wire shapes actually returned

**Decision**: decode what was observed, tolerate what the SEP allows.
- `skills/list` → `{"skills":[{"uri":"skill://hf-cli/SKILL.md","frontmatter":{"name":"hf-cli","description":"…"},"resources":[{"uri":"skill://hf-cli/SKILL.md","digest":"sha256:a601…","size":34197}]}]}`; 25 entries, no `nextCursor`, no `ttlMs`.
- `skills/get {uri}` → `{"skill":{…same entry…}}`.
- `resources/read {uri}` → one `text/markdown` content item; `sha256` of the UTF-8 bytes and the byte length equal the listed digest and size.
- `resources/list` also lists the `SKILL.md` files, without digests.
- The SEP also allows `resources: "dynamic"`, pagination, nested skill paths and non-`skill://` schemes.

## R3. How to send a method mcp-go does not know

**Decision**: `client.GetTransport().SendRequest(ctx, transport.JSONRPCRequest{…})` with a string id.
**Rationale**: implemented uniformly by the streamable-HTTP, SSE, stdio and in-process transports. The client's own ids are integers from an unexported counter, and stdio/SSE match responses by id in a pending map, so a caller-chosen integer could be answered to the wrong waiter; a string id (`mcpproxy-skills-<n>`) cannot collide.
**Caveat**: a raw request bypasses `Client.sendRequest`'s per-request metadata for protocol `2026-07-28`; harmless while the upstream protocol is pinned.
**Alternatives considered**: a second mcp-go client (second session, second auth); forking mcp-go (maintenance).

## R4. Where the server's capabilities are kept

**Decision**: read `core.Client.GetServerInfo().Capabilities.Extensions` (the `InitializeResult` is already stored; `Extensions map[string]any` exists on `mcp.ServerCapabilities` in v1.0.0). Nothing reads it today.

## R5. Which existing machinery to mirror

**Decision**: tool approvals for the binding and locking (`ApproveToolsReviewedAt`, `WithServerApprovalLockAt`, incarnation check, `StaleToolReviewError`, `ServerReplacedError`); prompts for the upstream call shape (`core/prompts.go`, `managed/prompts.go`); inventory captures for ordering (`managed.InventoryCapture`, `CaptureIsCurrent`).
**Not mirrored**: the prompt gate's decision logic. It treats `scan` like `manual` after the baseline, emits no activity for approvals and has no get-time check. FR-013's table, FR-016 and FR-015 differ on all three.

## R6. Storage and the purge set

**Decision**: two bbolt buckets keyed `server:…`, added by hand to `initBuckets`, `purgeServerTx`'s prefix slice, `Manager.PurgeSetCounts` and `storage/purgetest`. There is no bucket registry; the test oracle is `purgetest.Seed`/`Counts`.
**Alternatives considered**: content files on disk (not transactional with the purge); see plan D4.

## R7. Scanning long text

**Decision**: chunk each text file to at most 48 KiB on line boundaries and pass each chunk as a synthetic tool description to `scanner.ScanToolMetadataVerdict`.
**Rationale**: the scanner reads a field up to `MaxFieldBytes = 64 KiB` (`internal/security/detect/fields.go`) and reports truncation as "analysis incomplete"; the largest `SKILL.md` observed is 34 KiB, so most files are one chunk. `coverageOK == false` must be treated as not clean (the rule every existing caller follows).
**Correction of a project note**: there is no 4 KiB scan cap; the 4 KiB limits found are a bundle sidecar size and one check's line exemption.

## R8. Config wiring

**Decision**: follow `expose_prompts` point for point (list in plan D5). `enable_skills` is hot-reloaded, unlike `enable_prompts`, which is restart-only.

## R9. Test fixture

**Decision**: a raw JSON-RPC `httptest` server in `internal/skills/skillstest`, in the method-switch style already used by `internal/upstream/core/protocol_pin_test.go`, answering `initialize`, `skills/list`, `skills/get`, `resources/read` and `tools/list`, with knobs for: pagination, a wrong digest, a wrong size, a frontmatter mismatch, a dynamic skill, an over-limit skill, a binary file, mutation between calls, and a failing listing.
**Rationale**: mcp-go's server answers unknown methods with METHOD_NOT_FOUND and has no hook to add one, so `cmd/mcpfixture` cannot serve skills without an HTTP wrapper.

## R10. Search

**Decision**: a memory-only Bleve index for approved skills (plan D12).

## R11. Client support (why tools come first)

VS Code's "Skills from MCP servers" is experimental, off by default (`chat.agentHost.mcpSkills.enabled`) and needs the Copilot harness; the extension's implementation table lists VS Code as planned. Claude Code, Cursor, Codex and Copilot CLI do not consume the extension. Sources: the extension repository's implementations table and the VS Code agent-skills documentation, read 2026-11-16.

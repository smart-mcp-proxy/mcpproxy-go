# Implementation Plan: Skills over MCP

**Branch**: `125-skills-over-mcp` | **Date**: 2026-11-16 | **Spec**: [spec.md](spec.md) | **Issue**: #1895

## Summary

mcpproxy reads the skills that upstream servers expose through `io.modelcontextprotocol/skills` (SEP-2640) into its own store, verifies every file against the server's listing, and puts each skill behind an approval that is bound to its content. Agents are served only from the approved copy: through three search-first built-in tools on the aggregated surfaces, and later through the native extension on the single-server endpoint.

A new pure leaf package `internal/skills` owns the wire types, validation, digest and frontmatter checks and the content hash. The upstream layers get a `skills.go` each (core: raw `skills/list` and `skills/get` plus `ReadResource`; managed: session wrapper and capture stamp; manager: per-server access). The runtime owns the catalog and the approval state machine, under the existing per-server approval lock and incarnation check. Storage gains two buckets that join the purge set.

Five PRs, in the spec's delivery order:

| PR | Contents | Agent-facing? | Size |
|---|---|---|---|
| PR-1 | D1-D7: `internal/skills`, upstream reads, storage, config switches, runtime catalog (every skill `pending` or `unusable`), admin-only REST reads, `mcpproxy skills list/show/refresh`, raw JSON-RPC fixture, docs page | no | L |
| PR-2 | D8-D10: approval state machine (FR-013 table), scan of text files, review-bound approve / block / unblock on REST and CLI, activity rows, attention and review queue entries | no | L |
| PR-3 | D11-D12: `retrieve_skills`, `load_skill`, `read_skill_file`, in-memory search index, scope, activity, sanitisation, offsets | **yes** | M |
| PR-4 | D13: Web UI (Skills tab on the server page, review queue rows, diff view), Playwright sweep | yes | M |
| PR-5 | D14: native extension on `/mcp/s/<server>` | yes | M |

`enable_skills` stays default-off through all five. PR-1 and PR-2 can be released without any agent-visible change; nothing agent-facing merges before PR-2.

## Technical Context

**Language/Version**: Go 1.26; TypeScript 5.9 / Vue 3.5 (PR-4 only).
**Primary Dependencies**: `github.com/mark3labs/mcp-go` v1.0.0 (already pinned), `gopkg.in/yaml.v3` (already a direct dependency, for `SKILL.md` frontmatter), `github.com/blevesearch/bleve/v2` (already used; memory-only index for skills). **No new dependencies.**
**Storage**: bbolt `config.db`, two new buckets: `skill_approvals` and `skill_files`. Both are keyed `server:…` and join `purgeServerTx`. No migration: the buckets start empty.
**Testing**: `go test -race ./internal/skills/... ./internal/upstream/... ./internal/storage/... ./internal/runtime/... ./internal/httpapi/... ./internal/server/... ./cmd/mcpproxy/...` (server package with the CI `-skip` list); a raw JSON-RPC `httptest` fixture upstream (no automated test touches a public server); `./scripts/test-api-e2e.sh`; Playwright sweep for PR-4.
**Target Platform**: both editions; Linux, macOS, Windows (pure Go).
**Project Type**: single Go module with an embedded Vue frontend.
**Performance Goals**: SC-001 (25 skills visible within 30 s of connect); skill search under 100 ms for 1,000 catalog entries (Constitution I); refresh never blocks tool discovery or a request path.
**Constraints**: feature off = zero skill requests upstream and an unchanged tool list (SC-005); no skill text to an agent except from the approved copy (SC-002); per-server bounds on skills, files and stored bytes.
**Scale/Scope**: tens of skills per server typical (25 observed), bounded at 200 skills and 64 MiB of stored text per server.

## Decisions (with alternatives rejected)

### D1. A pure leaf package `internal/skills`

Types and rules that every layer needs, importing only the standard library, `yaml.v3` and `mcp-go/mcp`:

- `Entry{URI string; Frontmatter map[string]any; Files []FileRef; Dynamic bool}` and `FileRef{URI, Digest string; Size int64}`, decoded from `skills/list` and `skills/get`. `resources` is decoded as `json.RawMessage` first: an array becomes `Files`, the string `"dynamic"` sets `Dynamic`, anything else is an error for that entry only.
- `ParseSkillURI(uri) (root, name string, err)`: the address must end in `/SKILL.md`, the last path segment before it is the skill name, and it must equal `frontmatter.name`. `RelPath(root, fileURI)` returns the file's path inside the skill and rejects another scheme or host, a query or a fragment, percent-encoding that decodes to a separator or a dot segment, an escaping `..`, an absolute path, an empty segment and control characters. Separators inside a relative path are valid. Two files of one skill with the same relative path make the skill unusable.
- `VerifyFile(ref, content []byte) error`: byte length equals `Size`, `sha256` equals `Digest` (only `sha256:<64 hex>` is accepted; another algorithm makes the skill unusable with the reason).
- `ParseFrontmatter(skillMD []byte) (map[string]any, error)` (leading `---` block, `yaml.v3`, bounded at 64 KiB) and `FrontmatterEqual(listed, parsed)` after normalising both through JSON.
- `ContentHash(entry) string`: `sk1-` + sha256 over an unambiguous encoding: the skill URI, then each file sorted by URI as `(URI, relative path, digest, size)`, then the canonical JSON of the frontmatter, every string written with a length prefix so no two different inputs share an encoding. The exact file address is part of the binding (FR-009: address, digest, size). This is the approval binding and the reviewer fingerprint (`expected_hashes`, FR-011). The prefix versions the schema, as `rv1-` does for tools.
- Limits as constants: `MaxFilesPerSkill = 512`, `MaxSkillBytes = 16 << 20` (the extension's), `MaxSkillsPerServer = 200`, `MaxListPages = 50`, `MaxListResponseBytes = 4 << 20` per page, `MaxStoredTextPerServer = 64 << 20`, `MaxReadPerRefresh = 256 << 20` (all bytes read from one server in one refresh, text and binary), `MaxFrontmatterBytes = 64 << 10`, `MaxScanBytesPerSkill = 1 << 20` (2 MiB in the first draft; lowered in PR-2 after measuring the scanner, see D9).

Rejected:
- **Types in `internal/upstream/core`**: storage, runtime, httpapi and the fixture would all import the upstream client for a struct.
- **Reusing `mcp.Resource`**: it has no digest or size, and the listing is not a resource list.

### D2. Upstream reads: raw JSON-RPC for the two skill methods, `ReadResource` for files

`internal/upstream/core/skills.go`, copying the shape of `core/prompts.go`:

- `SupportsSkills() bool`: `serverInfo.Capabilities.Extensions["io.modelcontextprotocol/skills"]` is present **and** `Capabilities.Resources != nil` (the extension requires both).
- `ListSkills(ctx) ([]skills.Entry, error)`: returns `(nil, nil)` when not supported or when `exposeSkills` is false, so no request is sent (FR-001). Otherwise it takes `beginRequest(ctx, "skills/list")`, holds `sseRequestMu` on SSE, and pages with `GetTransport().SendRequest` until `nextCursor` is empty or a cap is hit (a hit cap is returned as a typed `*skills.TruncatedError` next to the partial list, never swallowed: FR-004).
- `GetSkill(ctx, uri)` for the single-entry read (used by an explicit refresh of one skill).
- `ReadSkillFile(ctx, uri) (content []byte, isText bool, mime string, err error)`: `client.ReadResource`; exactly one content item whose URI equals the request; `TextResourceContents` gives the UTF-8 bytes, `BlobResourceContents` is base64-decoded and is not text. Content that claims to be text but is not valid UTF-8 is treated as binary.
- Request ids for the raw calls are strings, `mcpproxy-skills-<n>` from an atomic counter. The mcp-go client numbers its own requests with integers from 1, and stdio and SSE match responses by id, so an integer id of ours could collide.

`exposeSkills atomic.Pointer[bool]` sits next to `exposePrompts` and is pushed from the managed layer on config change.

**Response size.** A listed size is a claim; the transport would otherwise buffer whatever the server sends before `VerifyFile` can object. Three bounds, all applied before content is accepted:

1. A file whose listed size exceeds what is left of the skill, server or refresh budget (D6) is not requested at all.
2. On the HTTP transports every skill request carries a per-request byte limit in its context (`transport.WithResponseLimit(ctx, n)`), enforced by one more link in mcpproxy's existing round-tripper chain (`internal/transport`, next to the logging and header-forwarding links): the response body is wrapped in a reader that fails the request once more than `n` bytes arrive. For a file, `n` is six times the listed size plus 64 KiB of envelope: a JSON string can spend up to six bytes on one source byte (`\u00XX`), which is more than base64 needs, so no response that matches its listing is ever refused by this limit. For a listing page `n` is `MaxListResponseBytes`. An over-limit response is an error for that skill or that refresh; it is never truncated and used. Requests without the context value are untouched, so tool calls behave as today.
3. On stdio and SSE the response arrives on a stream shared with every other message, so it cannot be cut per request without closing the connection. There the exposure is the one that already exists for any tool result from that server; the decoded length is checked against the listed size before hashing, and the refresh timeout (D6) bounds a slow sender. This limit of the plan is recorded under Risks.

The live check (spec, "Verified background") showed the extension announced under the handshake mcpproxy already sends, so `connection_lifecycle.go` and the Spec 058 pin are untouched.

Rejected:
- **Advertising the extension in `ClientCapabilities`**: not needed by the server checked, and it changes the handshake of every upstream, which Spec 058 FR-027 forbids without its own change. Kept as a follow-up if a server turns up that requires it.
- **`resources/list` filtered by `skill://`**: works on the server checked, but carries no digests or sizes, so nothing could be verified or bound.
- **Waiting for an mcp-go method**: none exists or is announced.

### D3. Managed and manager layers

`managed/skills.go`: `ListSkillsCaptured(ctx) ([]skills.Entry, InventoryCapture, error)` and `ReadSkillFile`, both through `withSession` (re-init gap, 404 session loss) exactly as `ListPrompts`. The capture is stamped before the request, as for tools.

`upstream/manager_skills.go`: `ListServerSkills(ctx, server)` and `ReadServerSkillFile(ctx, server, uri)`. They refuse a disabled or quarantined server and do **not** call `tryReconnectOnUse`: a background read must not wake a sleeping server. There is no aggregated manager call; the runtime refreshes server by server.

### D4. Storage: two buckets, both in the purge set

- `skill_approvals`, key `server + ":" + skillURI`, value `SkillApprovalRecord` (see data-model.md).
- `skill_files`, key `server + ":" + sha256hex`, value the raw bytes of one **text** file. Content-addressed, so the approved and the current copy share a row when equal, and identical files across skills of one server are stored once.

Binary files are read once to verify their digest and are not stored (they are not served in this version); the record keeps `{relpath, digest, size, text: false}`.

Both buckets are added to `initBuckets`, to the prefix slice in `purgeServerTx` (purge.go:94), to `Manager.PurgeSetCounts`, and to `internal/storage/purgetest` (label, `Seed`, `Counts`). Unreferenced `skill_files` rows of a server are collected at the end of each applied refresh, inside the same transaction that writes the records.

Rejected:
- **Files on disk under `<datadir>/skills/`**: larger values are cheaper there, but removal would no longer be one bbolt transaction with the rest of the purge set (Spec 116 PR2), and a crash between the two leaves either orphan files or records without content.
- **Storing only digests and reading upstream at load time**: the agent would receive bytes nobody reviewed if the server changed between review and load, which is the hole FR-008b closes.
- **One record holding the file bodies**: every state change would rewrite megabytes.

### D5. Config: `enable_skills` and `expose_skills`

- `Config.EnableSkills bool` (`enable_skills`, default false), hot-reloaded: the detector in `runtime/config_hotreload.go` gets one more field, and turning it on triggers a skills refresh for every connected server; turning it off drops the catalog from memory, unregisters the tools (PR-3) and keeps storage.
- `ServerConfig.ExposeSkills *bool` (`expose_skills`, tri-state, omitempty), wired at every point `expose_prompts` is: `config.go`, `config/merge.go` (patch with diff entry, copy), `contracts/types.go` and `converters.go`, the runtime state view and REST maps (`runtime.go`), add and update request parsing (`httpapi/server.go`, `server/server.go`), the `upstream_servers` tool schema and parsing (`server/mcp.go`), `oauth/serverfields.go` (`MaskDecisionNotSecret`), `cmd/generate-types` and `frontend/src/types/contracts.ts`, `docs/configuration.md`, `make swagger`, and the MCP tools-list goldens under `internal/server/testdata/`.
- No environment variable and no CLI flag in this spec.
- **Registering the tools at runtime (PR-3).** Change detection alone registers nothing. One function, `MCPProxyServer.RefreshSkillsAvailability()`, is called from the config-reload listener next to `RefreshCodeExecutionAvailability` and once at start. It reads the live config and makes every surface agree: the call-tool and code-execution surface builders include the three tools when the switch is on and are rebuilt; the direct surface is rebuilt on a skills toggle (today it is rebuilt only on serialization drift) with the tools added through `withDirectBuiltins`; and on the default registry (`p.server`, which also serves stdio) exactly these three tools are added or deleted in place. A test per surface asserts the tool list before, after turning on, and after turning off.

### D6. The runtime catalog and its refresh

`internal/runtime/skills_catalog.go`. One refresh of one server:

1. Skip unless `enable_skills`, the server is enabled, not quarantined, connected, `expose_skills` is not false and `SupportsSkills()`.
2. Under the approval lock, delete this server's `skill_files` rows that no record references (what an earlier refresh staged and never applied), so leftovers cannot accumulate across stale or timed-out refreshes, and take the starting budget from the rows that remain. Then `ListSkillsCaptured`. Entries beyond `MaxSkillsPerServer` are not processed and the listing is recorded as truncated.
3. Work through the entries one at a time, so at most one skill's files are in memory:
   - Validate the entry (D1). A dynamic or invalid entry becomes `unusable` and nothing is fetched for it.
   - Before each file: if the bytes are already stored under that digest for this server, load them and run `VerifyFile` against **this** listing's reference (size and digest); a stored row that fails is treated as missing and fetched again. Otherwise reserve the file's listed size against three budgets: the skill's 16 MiB, the refresh's `MaxReadPerRefresh`, and the server's `MaxStoredTextPerServer`. Whether a file is text is only known after it is read, so every file not already stored reserves storage budget as if it were text; the reservation is released when the verified content turns out to be binary. The storage budget starts from the distinct rows left after step 2 (everything current and approved copies of **all** the server's records retain, listed or not) and includes this refresh's reservations and staged rows. A file whose reservation does not fit makes the skill `unusable` with `server_bound_exceeded` and stops its fetching.
   - Fetch, check the decoded length against the listed size, `VerifyFile`, classify text or binary.
   - Stage the skill's verified text blobs: one short storage transaction under `WithServerApprovalLockAt(server, incarnation, …)` writing only `skill_files` rows. No network call is ever made while the approval lock is held. A blob no record references yet is harmless and is collected by step 5 of this or a later refresh.
   - Any failure makes that one skill `unusable` with a reason; the others proceed.
4. Apply the records under `WithServerApprovalLockAt`, only if `CaptureIsCurrent(capture)` and the capture's `Seq` is newer than the last skills capture applied for this server. An older, retired or reconnected capture writes no record (same rule as tool lists, Spec 117 PR-3); its staged blobs are left for collection.
5. In that one transaction: upsert records, mark records the listing no longer contains as `listed: false` (never delete: FR-007), then delete `skill_files` rows of this server that no record's current or approved copy references, computed inside the transaction from the records as just written.
6. Publish `skills.changed` on the event bus (SSE) when anything changed.

Triggers: `onServerConnectedDiscovery` (connect and reconnect), the periodic discovery sweep, `enable_skills` or `expose_skills` changing, and an explicit refresh (REST and CLI). Each server's refresh is single-flight and runs on the runtime's lifetime context with its own timeout (`skillsRefreshTimeout`, 2 minutes, and 30 seconds per request); it never runs inside the tool discovery call and a failure only sets `skills_error` on the server's status (FR-006).

In PR-1 step 4 writes every usable skill as `pending`; the state machine arrives in PR-2.

**One availability predicate.** `Runtime.SkillAvailable(server, record) bool` is the only place that answers "may an agent have this skill": `enable_skills` is on, the server exists in the live config, is enabled, not quarantined and its `expose_skills` is not false, and the record is approved, not blocked and listed. Search (D12), the agent tools (D11), the scoped REST answers (D7) and the native surface (D14) all call it at request time with the live config, so excluding a server or turning the feature off withdraws its skills at once, with no refresh needed and with stored approvals kept (FR-026, FR-027). A config change to either switch also rebuilds the search index.

Rejected:
- **Refreshing inside tool discovery**: a slow file read would delay tool indexing, and a skills failure would look like a discovery failure.
- **Consuming `resources/list_changed`**: the server checked announces `listChanged: false`; reconnect, the sweep and an explicit refresh cover FR-028. Left for later.

### D7. Reads for people (PR-1), administrator only

`internal/httpapi/skills.go`, all behind `requireAdminRead` until PR-3 adds the scoped answers (FR-019a: a scoped credential sees approved skills only, and there are none approved before PR-2):

- `GET /api/v1/servers/{id}/skills` lists records.
- `GET /api/v1/servers/{id}/skills/content?skill=<path>&file=<relpath>&copy=current|approved&offset=&limit=` returns one text file.
- `POST /api/v1/servers/{id}/skills/refresh`.

**FR-019a on the paths that already exist.** Held skills will show up in places that today filter only by server reach: the review queue and per-server review (`internal/httpapi/review.go`), the attention list (`attention.go`) and SSE (`sse_scope.go`). From PR-2, when the first such entry exists, each of them treats a skill entry that is not available as administrator-only: a scoped caller's review and attention answers contain no such entry and its counts are recomputed without them, and a `skills.changed` event reaches a scoped caller only when the set of skills available to it changed, carrying only that count. One helper, `skillVisibleTo(ctx, server, record)` (administrator: any record of a visible server; scoped: `SkillAvailable` and server in reach), is used by every one of these paths and by the handlers below, and a test walks every REST and SSE read door with a scoped credential and a held skill and asserts the skill appears nowhere.

The skill is a query parameter because a skill path may contain `/`. CLI: `mcpproxy skills list [--server]`, `skills show <server>:<skill> [--file]`, `skills refresh <server>`, with `-o json|yaml`. Contracts: [contracts/rest-api.md](contracts/rest-api.md).

### D8. The approval state machine (PR-2)

`internal/runtime/skills_quarantine.go`, one pure function `decideSkill(prev *record, cur observed, mode TrustMode, quarantineOn bool, scan scanResult) decision`, table-tested against the FR-013 table row by row:

| Trust mode | New | Changed |
|---|---|---|
| `manual` | pending | changed (held) |
| `scan` | approved if scan clean and complete, else pending | changed (held), whatever the scan says |
| `auto`, or `quarantine_enabled: false` | approved | approved |

`blocked` is a separate boolean that no transition clears (FR-008). A dynamic skill is always `unusable` (FR-014). A record whose content hash equals its approved hash is restored to approved (the "changed back" case). An illegal promotion is refused by an invariant check, as `enforcePromptInvariant` does.

Note for the implementer: the prompt gate treats `scan` like `manual` after the baseline. Skills deliberately do not copy that: follow the table.

### D9. Scanning skill text (PR-2)

Each text file goes through `scanner.ScanToolMetadataVerdict` as synthetic `config.ToolMetadata{Name: "<skill>/<relpath>#<n>", Description: chunk}`, with `peerTools` nil. The scanner reads a description up to `MaxFieldBytes` (64 KiB), so a file is cut into chunks of at most 48 KiB: at the last newline inside the window when there is one, otherwise at the last UTF-8 boundary, with an overlap of at most 1 KiB and never more than half a chunk, so every chunk advances. A skill's text is scanned up to `MaxScanBytesPerSkill`; text beyond it is not scanned and the scan is recorded as incomplete. The skill's scan is clean only if every chunk of every text file returns no finding and `coverageOK`; anything else is "not clean", and `coverageOK == false`, a non-empty partial, or unscanned text is "analysis incomplete", which is stored as evidence and never lets `scan` mode approve. Findings are stored on the record against the file and chunk offset, capped like tool hold evidence.

**As built (PR-2).** Measured on the 25 skills of the public server: the scanner needs about one second for 48 KiB of text (it runs every signature of the bundle over it), 0.05 MB/s. Scanning inside the refresh's two-minute upstream timeout would not finish on a larger server and would lose every scan with the refresh. So:

- The scan runs after all reads of a refresh, outside the upstream timeout and outside any lock, reading each skill's text back from the staged `skill_files` rows (one skill in memory at a time).
- One refresh starts new scans for 45 seconds (the first skill is always scanned). Skills never scanned come before retries of degraded scans. While a refresh gave some skill its first scan and never-scanned skills remain, a follow-up refresh runs after a 10-second pause; the chain ends when every skill has been scanned once.
- The scan of one skill stops after two minutes and is recorded as `time_limit`; like `size_cap` it is not repeated for the same content. `coverage` (degraded scanner) is retried by later refreshes; `interrupted` (shutdown) is not stored.
- The per-skill cap is 1 MiB of text.
- A scan record carries the content hash it was made of (`Scan.Hash`); a scan of other content counts as no scan.

Rejected: **raising the scanner's field cap** (affects every tool scan and the eval gates), and **scanning only `SKILL.md`** (a script next to it is the more likely place for the payload).

### D10. Approve, block, unblock (PR-2)

`ApproveSkillsReviewedAt(server, skills []string, expected map[string]string, by string, expectedIncarnation int64)`, built like `ApproveToolsReviewedAt`: capture incarnation, take the lock, check incarnation, compare each skill's current `ContentHash` with `expected`, refuse the whole request with a stale-review error (409 `review_out_of_date`) or `server_replaced` (409) and write nothing; otherwise set approved hash, approved file set, actor and time. `BlockSkill` / `UnblockSkill` set the mark. All three are administrator-only on REST and CLI and emit a `skill_quarantine_change` activity row through a `…Ctx` emitter that carries the actor. There is no approval over MCP in this spec.

**As built (PR-2).** Block and unblock are one function, `SetSkillsBlockedAt(…, blocked bool, …)`. An expected hash is required for every named skill (a missing one is reported as `unreviewed`). A person's decision writes records only (`storage.UpdateSkillApprovals`) and collects no `skill_files` rows, because a refresh that is reading at the same moment has staged rows no record references yet; the rows a decision leaves unreferenced are collected by the next refresh. A background trigger (configuration change, connect, sweep) that finds a refresh running marks the server for one more refresh instead of joining one that may already have decided. Activity actions: `approved`, `blocked`, `unblocked` (a person), `auto_approved`, `held_changed`, `restored`, `unusable` (a refresh). `skill_quarantine_change` rows and events are administrator-only on every activity read door (FR-019a).

### D11. Agent tools (PR-3)

`internal/server/mcp_skills.go`: `retrieve_skills(query, limit)`, `load_skill(id, offset, limit)`, `read_skill_file(id, path, offset, limit)`. The id is `server:skill-path`.

- Registered only while `enable_skills` is on, on the surfaces that register `retrieve_tools`, and on the direct surface through `withDirectBuiltins` and `builtinDirectToolNames` (so an upstream tool can never take the name). Not on `/mcp/s/<server>` and not exposed to the code-execution sandbox (FR-023b).
- Scope: the handler resolves the caller's server reach with the same chain the tool handlers use and filters the catalog before search; a skill outside reach, or for which `SkillAvailable` is false for any reason (not approved, blocked, unusable, not listed, server excluded, disabled or quarantined), returns the one `skill not found` result (FR-023). `retrieve_skills` reports `total` as the number of matches among the skills available to the caller, never a wider count.
- Content comes from `skill_files` by the approved digest, re-hashed before it is returned (FR-015); a mismatch marks the skill unusable and returns an error.
- `load_skill` prepends the provenance header and appends the file manifest; offsets and lengths are byte positions in the approved copy, cut on a UTF-8 boundary, and a partial answer ends with `next_offset` (FR-022a). Output then passes the existing sanitisation and sensitive-data detection; the tool response is sized to stay under the response limit so the generic truncator never cuts it.
- Annotated read-only; activity type `skill_access` with `action` = search, load or read.

**As built (PR-3).** Decided during implementation and review; each is recorded in the contracts too.

- `load_skill` and `read_skill_file` take an optional `version` (the approved hash the header names); a continuation with an older one is refused, so parts of two approved versions are never joined.
- The output sanitisation policy is applied to the whole file before paging (`internal/server/mcp_skills_output.go`: `servedSkillText`, remembered per server, file digest and policy), so a secret cannot be fetched in two halves; offsets are positions in the text as served. Under a blocking policy the file is scanned in overlapping windows that follow the detector's own payload limit; a file over 4 MiB is withheld under a policy that blocks or redacts. Names, descriptions and file names pass the same policy (`skillWordCleaner`); a skill whose path or address the policy would change is left out of listings. Skills build a detector themselves when a policy is switched on after start.
- `tool_response_limit` does not shrink a part.
- A stored copy that fails verification answers the one `skill not found`; the reason is in the activity row and on the record. The damage is confirmed under the approval lock against the file that failed, so a read that raced a new approval withdraws nothing.
- `skill_access` rows and their SSE event are administrator-only (a row names the id its caller asked for). Scoped REST reads write them too (source `api`).
- The scoped REST door reads through `Server.ListAvailableSkills` / `Server.AvailableSkillFile`, which apply the same policy as the tools.
- Records of skills a listing did not name are bounded to 200 per server (`PruneSkillApprovalsNotIn`), blocked ones kept.

### D12. Skill search index (PR-3)

A memory-only Bleve index (`bleve.NewMemOnly`) in `internal/skills/index`, documents `{id, server, name, description}` for the skills for which `SkillAvailable` is true, rebuilt on `skills.changed` and on any change of `enable_skills`, a server's `expose_skills`, enabled or quarantine state. Because the index is rebuilt asynchronously, every hit is checked again with `SkillAvailable` and the caller's reach before it is returned. It uses the analyzer the tool index uses, so ranking behaves like `retrieve_tools`.

**As built (PR-3).** One index per reach (the sorted set of servers a caller can reach that may serve skills), at most 16 kept: a full-text score depends on how common a word is in the whole index, so one index for everybody would let skills outside a caller's reach move the order of its results. An index is rebuilt lazily when a generation counter moved (every skills change and every configuration change). The index only proposes: every match (up to 1,000 per search) is checked against its record by key before it is returned or counted.

Rejected: **a `kind` field in the tool index** (changes the on-disk mapping and its staleness check, and a skills bug could then damage tool search), and **a hand-written scorer** (a second ranking behaviour to maintain).

### D13. Web UI (PR-4)

A Skills tab on `ServerDetail.vue` (list with state chips, file list, text viewer, diff of approved against current, findings, approve / block), skill rows in the review queue and the Home attention list, `skills.changed` SSE. New terminology values go through `internal/contracts/terminology.go` and its fixtures.

### D14. Native surface (PR-5)

On `/mcp/s/<server>` only: `WithExtensions` and `WithResourceCapabilities` on that surface's server, a resource template for the skill scheme serving the approved copy byte for byte, and an interceptor for `skills/list` and `skills/get`. mcp-go's dispatcher has no hook for unknown methods, so the interceptor reads the JSON-RPC method from the request body. It sits inside `serveSingleServer`, after scope admission and inside `singleServerSessionGuard`, immediately in front of the transport. Those two guards are not everything a tool call passes: the session guard accepts a request without a session id, and the protocol-version and session checks proper live inside the transport, which the interceptor answers in place of. The interceptor therefore applies the same checks itself before answering, through the functions the transport path uses (extracted and shared, not re-written): the method must be POST, the protocol-version header must be one the endpoint supports, and the session id must be present and belong to a live session of this endpoint and this caller. Parity tests send each extension method with a missing session id, a foreign one and an unsupported protocol version, and assert the same refusal a `tools/call` gets. It is not placed with the existing `notifications/cancelled` peek: that one runs in the auth middleware, before the single-server guards, and only for modern-protocol requests, while the extension methods must be handled for every protocol version the endpoint accepts. Listings and reads use `SkillAvailable` and serve the approved copy byte for byte; sensitive-data detection runs on file content and records findings without altering bytes (FR-029a). All three native operations, `skills/list`, `skills/get` and the resource read, write a `skill_access` activity row with the caller and the outcome, including not-found answers; they do not pass through the tool-handler activity path, so each is instrumented explicitly.

**Following the switch.** Single-server instances are cached for the life of the process, and capabilities are fixed when an instance is built. `RefreshSkillsAvailability` (D5) therefore also compares, per cached single-server instance, whether it was built with the native surface against whether it should have it now (`enable_skills` on and the server not excluded), and retires the endpoints that differ. Retiring is three steps, because the MCP instance, its HTTP transport and its session ids are held separately today: (1) remove the instance from the single-server cache; (2) remove the transport that `Server.singleServerHTTP` keeps for that instance and shut it down, which closes its open streams, so no transport is left behind per toggle; (3) advance a per-endpoint generation that is part of the session id for that endpoint and is checked by both `singleServerSessionGuard` and the interceptor, so an id issued before the toggle is refused the way an unknown session is and the client initializes again. Single-server session ids are otherwise stateless and bound only to the server name, so without step 3 an old id would keep working against the rebuilt instance. The next request builds a fresh instance and transport with the right capabilities.

Getting an endpoint and retiring it share one small per-endpoint lifecycle lock, because today the instance and its transport are obtained in two separate steps and a request could otherwise pick up the old instance, be overtaken by a retirement, and then create a new transport for the retired instance that nothing would ever remove. Under the lock a request obtains the instance, its transport and the current generation together as one value; retirement, under the same lock, detaches instance and transport and advances the generation in one step. The lock is released before the request is served and before the detached transport is shut down, so it is never held across an HTTP stream or a shutdown. A request that obtained an endpoint just before its retirement runs to completion on the detached transport, which is then closed. For that to be safe, nothing a request needs may be looked up from the cache after acquisition: today `singleServerNameFor` recovers an instance's upstream name from the cache, and an in-flight `initialize` on a detached instance would lose it and fall back to the aggregate instructions. The upstream name is therefore bound to the instance when it is built (carried by its instruction callback and the request context) and `singleServerNameFor` reads that binding, never the live cache. A regression test pauses an `initialize` after acquisition, retires the endpoint, resumes, and asserts the answer carries only that upstream's instructions. A regression test races acquisition against retirement and asserts no transport outlives its instance.

A test drives one endpoint off → on → off and checks at each step: `initialize`, the listing, a file read, that a session id from the previous step is refused, that a stream opened in the previous step is closed, and that the number of retained transports does not grow.

## Constitution Check

| Principle | Status | Note |
|---|---|---|
| I. Performance at Scale | PASS | Refresh is background, single-flight per server and bounded; search is a small memory index. |
| II. Actor-Based Concurrency | PASS with a noted reuse | Writes go through the existing per-server approval lock and incarnation check (the established pattern for approval records); no new lock type. |
| III. Configuration-Driven | PASS | Two config fields, hot-reloaded, documented defaults. |
| IV. Security by Default | PASS | Default off; held until approved; content-bound; served from the approved copy; administrator-only review. |
| V. TDD | PASS | Every task starts from a failing test; the fixture and the table tests come first. |
| VI. Documentation Hygiene | PASS | `docs/features/skills.md`, configuration, REST and CLI references, CLAUDE.md line by hand. |

No violations; the Complexity Tracking table is not needed.

## Project Structure

### Documentation (this feature)

```text
specs/125-skills-over-mcp/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── upstream-wire.md
│   ├── rest-api.md
│   └── mcp-tools.md
└── tasks.md            # /speckit.tasks
```

### Source Code (repository root)

```text
internal/skills/                      # new leaf: types, validation, digest, frontmatter, hash, limits
internal/skills/index/                # PR-3: memory-only search index
internal/skills/skillstest/           # raw JSON-RPC httptest fixture upstream
internal/upstream/core/skills.go      # SupportsSkills, ListSkills, GetSkill, ReadSkillFile
internal/upstream/managed/skills.go   # session wrapper + capture
internal/upstream/manager_skills.go   # per-server access
internal/storage/                     # models.go, bbolt.go, manager.go, purge.go, purgetest/
internal/config/                      # config.go, merge.go
internal/runtime/skills_catalog.go    # refresh, apply, events
internal/runtime/skills_quarantine.go # PR-2: state machine, scan, approve/block
internal/httpapi/skills.go            # REST
internal/server/mcp_skills.go         # PR-3: agent tools
internal/server/skills_native.go      # PR-5
cmd/mcpproxy/skills_cmd.go            # CLI
frontend/src/                         # PR-4
docs/features/skills.md
```

**Structure Decision**: one Go module, feature code in a new leaf package plus one file per existing layer, following how prompts and tool approvals are laid out today.

## Risks

- **A server that announces skills only to a client that advertises the extension.** Not seen on the server checked. Symptom: `SupportsSkills()` false on a server known to have skills. Mitigation: documented, follow-up to advertise the extension (its own Spec 058 change).
- **Raw requests skip mcp-go's per-request metadata for protocol 2026-07-28.** Irrelevant while the upstream protocol is pinned to 2025-11-25; the lift of that pin must revisit `core/skills.go` (noted in the file).
- **A hostile stdio or SSE server sending an oversized response.** Not bounded per request by this plan (D2); the same exposure exists today for any tool result from such a server. The HTTP transports are bounded.
- **bbolt growth from stored text.** Bounded at 64 MiB per server and collected on refresh; `db compact` reclaims space.
- **Scanner false positives on long instruction text.** The scanner was tuned on tool descriptions; a skill that tells an agent to run commands may be flagged. In `scan` mode that only means "held for a person", which is the safe side; measured in PR-2 on the 25 skills of the server checked before defaults are discussed.
- **`skills/list` answers that omit entries intermittently** would flip skills to "not listed" and back. FR-007 keeps approvals; a refresh that lists a skill again restores it without review.

# Feature Specification: Upstream Resources

**Feature Branch**: `139-upstream-resources`
**Created**: 2026-11-28
**Status**: Draft
**Input**: Issue #1969 "Expose upstream resources to the client". mcpproxy offers clients only `tools` and `prompts`. Upstream resources and resource templates are not listed or readable, and `notifications/resources/*` from upstream servers are dropped. Aggregate upstream resources and templates (namespaced by server, as tools are), proxy `resources/read`, and forward list changes, subscriptions and `resources/updated`.

**Related**: Spec 100 (aggregated upstream prompts and their baseline, the closest precedent), Spec 125 (Skills over MCP; it already answers `resources/read` for skill addresses on `/mcp/s/<server>`), Spec 104/105/118 (agent-token reach, pin boundary), Spec 108 (profiles), Spec 058 (upstream protocol pin and list-must-not-vary-per-connection), Spec 124 (activity attempts and work sessions), tracker #532 (MCP 2026-07-28 upgrade: `resources/subscribe` becomes `subscriptions/listen`; missing-resource code `-32002` becomes `-32602`).

---

## What a resource is, and the rules that shape this spec

An MCP resource is a piece of data an upstream server makes readable by address (a URI): a file, a database row, a document. A server lists concrete resources (`resources/list`), lists address patterns for resources it can produce on demand (`resources/templates/list`, RFC 6570 URI templates), serves the content of one address (`resources/read`, text or base64 blob), and can tell a client that its list changed or that one resource it subscribed to changed.

Three rules shape this spec:

1. **Resource content is untrusted upstream output, like a tool response.** It ends up in the model's context. Everything that protects tool responses on the way out (output policy, sensitive-data detection, size bounds, activity record) protects resource content the same way. Resource metadata (name, title, description) is text the client may show to the model, so it is scanned like prompt metadata.
2. **A resource is reachable exactly when its server is reachable.** A caller sees and reads a server's resources only if the same caller may reach that server's tools: same quarantine, enabled, agent-token and profile rules, applied on every request, not only on the list.
3. **Skills keep their own gate.** Spec 125 delivers skill files only after their exact content was approved. A skill file is also an upstream resource, so the plain resource path must never become a way around that approval.

## Verified background (code, 2026-11-28)

- `internal/server/mcp.go` builds the client-facing MCP servers with the tools and (optionally) prompts capabilities only. No routing mode advertises resources, except `/mcp/s/<server>` with Spec 125's native skills surface, which advertises `resources` with neither `subscribe` nor `listChanged` and answers `resources/read` for skill addresses itself (`internal/server/skills_native.go`); any other read on that endpoint falls through to the SDK, which has no resources and answers "not found".
- `internal/upstream/core/connection_lifecycle.go` `dispatchNotification` ignores `notifications/resources/list_changed` and `notifications/resources/updated`.
- The upstream client reads resources today only for skills (`internal/upstream/core/skills.go`, with a per-request response limit).
- The MCP SDK in use (mark3labs/mcp-go v1.6.0) can register resources and templates on a server instance, dispatches reads to the registered resource or the first matching template, sends `notifications/resources/list_changed` to every session when the registered set changes, and runs after-list hooks that receive the result before it is sent. It has no per-caller resource filter and no built-in record of which session subscribed to which address.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Read an upstream server's resources from my client (Priority: P1)

A developer has a documentation server and a database server behind mcpproxy. Their MCP client (Claude Desktop, VS Code, Inspector) shows an "attach resource" picker. With the feature on, the picker lists the resources of both servers, each clearly marked with the server it comes from, and attaching one returns the content the server serves. Resource templates of both servers appear too, and a client that fills a template gets the resource the upstream server produces for that address.

**Why this priority**: This is the feature. Without listing and reading, nothing else in this spec has value.

**Independent Test**: Connect two fixture servers that each list two resources and one template, with one resource URI identical on both servers. With the feature on, `resources/list` on `/mcp` returns four entries with four distinct addresses, `resources/templates/list` returns two templates, and reading each address returns exactly what the owning server serves. With the feature off, the client is offered no resources capability and both methods answer "method not found", exactly as today.

**Acceptance Scenarios**:

1. **Given** the feature is on and two connected servers list resources, **When** a client lists resources on `/mcp`, **Then** every resource of every eligible server appears once, under an address that names its server, with its name, title, description, MIME type, size and annotations as the server sent them.
2. **Given** two servers list the same URI, **When** a client lists resources, **Then** both appear under distinct addresses, and reading each returns the content of its own server.
3. **Given** a listed resource, **When** the client reads its address, **Then** mcpproxy reads the original URI from the owning server and returns the server's contents, each content item carrying the proxied address instead of the original one.
4. **Given** a server that lists a template, **When** the client lists templates, **Then** the template appears with the server's namespace in front of the upstream pattern, and **When** the client reads an address produced by filling that template, **Then** mcpproxy reads, from the owning server, exactly the address the upstream template would have produced with the same values.
5. **Given** the feature is off (the default), **When** a client connects to any endpoint without the Spec 125 native skills surface, **Then** the handshake advertises no resources capability and `resources/*` calls are refused as today.
6. **Given** the feature is on, **When** a server is configured with `expose_resources: false`, or does not announce the resources capability, **Then** it contributes no resources or templates and nothing is requested from it.

---

### User Story 2 - Resources respect quarantine, reach and profiles (Priority: P1)

A team lead hands an agent token that reaches only the `docs` server, and uses a profile for a client that should see only two servers. A new server was added and sits in quarantine. Resources must follow the exact same boundaries tools follow: the agent token lists and reads `docs` resources only, the profile-bound client sees only its two servers, and the quarantined server contributes nothing until it is approved.

**Why this priority**: Shipping a listing that ignores reach would turn resources into a way around agent-token scoping and quarantine. It must land together with US1.

**Independent Test**: With three servers (one quarantined, one disabled, one healthy) and an agent token scoped to one server, list and read through `/mcp` as an administrator, as the scoped token, and as a client bound to a one-server profile. Each caller lists and reads exactly the resources of the servers it may reach; a read of a known address of an unreachable server is refused with the same "not found" answer as an address that does not exist.

**Acceptance Scenarios**:

1. **Given** a quarantined or disabled server, **When** any caller lists resources, **Then** that server's resources and templates are absent, and **When** any caller reads an address of that server it learned earlier, **Then** the read is refused and nothing is sent upstream.
2. **Given** an agent token whose reach excludes server B, **When** it lists resources, **Then** B's resources and templates are absent, and **When** it reads a B address, **Then** it gets the same "resource not found" refusal as an address that does not exist, and nothing is sent upstream.
3. **Given** a request resolved to a profile, **When** the caller lists or reads, **Then** only servers in that profile's scope count, with the same profile resolution the tool and prompt lists use (the list never varies with a connection's `set_profile` selection, Spec 058 FR-012; the read uses the full precedence).
4. **Given** a pinned agent token (Spec 118), **When** it lists or reads, **Then** only servers inside grant ∩ pin are reachable.
5. **Given** a server that becomes quarantined or disabled while a client holds its addresses, **When** the client reads one before the next list refresh, **Then** the read is refused (the gate runs at read time, not only at list time).

---

### User Story 3 - Resource content is checked on the way out and recorded (Priority: P1)

An upstream document contains an API key, or an injected instruction, or is 200 MB. The developer's output policy (redact, block, strip, spotlight) applies to it as it applies to tool responses, the read is bounded in size, and every read appears in the activity log with server, original address, outcome and size.

**Why this priority**: Resource content goes straight into the model's context. Without these protections the feature is a hole in the output pipeline.

**Independent Test**: A fixture resource contains a fake AWS key and an injection phrase. With output sanitisation set to redact, the read returns the content with the key redacted and the activity log shows one `resource_read` row naming the server and original URI with detections recorded. With block mode, the read is refused with a policy decision recorded. A fixture resource larger than the cap is refused with a clear "too large" error and a row with status error.

**Acceptance Scenarios**:

1. **Given** an output policy is configured, **When** a resource is read, **Then** the text contents are treated as untrusted upstream output and the policy is applied exactly as it is to an upstream tool response (block, redact, strip, spotlight), and a blocked read records a policy decision and returns an error instead of content.
2. **Given** sensitive-data detection is on, **When** a resource is read, **Then** the read's activity row carries the same detection results a tool call's row would.
3. **Given** a read whose upstream response is larger than the resource response cap, **When** the client reads it, **Then** mcpproxy refuses the read with an error that names the cap and records the outcome; on streamable HTTP it stops reading at the cap and never buffers more than the cap plus a fixed margin (FR-021 states the other transports).
4. **Given** any read that reaches a listed resource or a template of an eligible server (FR-022), **When** it completes, succeeds or fails, **Then** exactly one `resource_read` activity row is recorded with server, original URI, proxied URI, status, duration, total bytes, content count and MIME types, attributed to the caller as tool calls are.
5. **Given** a resource whose name, title or description trips the deterministic tool-poisoning scanner at the "dangerous" level, **When** the list is built, **Then** that resource (or template) is withheld from the list, the withholding is logged, and a read of its address is refused.

---

### User Story 4 - Skills stay behind their own approval (Priority: P1)

A server publishes skills (Spec 125) and also lists its skill files as plain resources. The developer has not approved one of the skills yet. No client may read that skill's files through the plain resource path, on any endpoint, whether or not the skills feature is on.

**Why this priority**: This is rule 3. Without it, enabling resources would undo Spec 125's quarantine.

**Independent Test**: A fixture server lists `skill://demo/SKILL.md` and a `file:///skills/demo/notes.md` that the skill names as one of its files, both as plain resources, and serves the skill through the extension. With both features on and the skill held: `/mcp` lists neither address and refuses a read of either; `/mcp/s/<server>` lists neither as a plain resource, the native surface refuses the held skill file as Spec 125 defines, and an approved skill's file is served by the native surface with its Spec 125 activity row, never by the plain resource path.

**Acceptance Scenarios**:

1. **Given** any server, **When** resources are aggregated, **Then** every resource whose URI is in the `skill://` namespace and every template whose pattern starts with `skill://` is excluded, whether or not the skills feature is on.
2. **Given** the skills feature is on and the server's skill catalog names a file under another scheme (any skill, whatever its approval state), **When** resources are aggregated, **Then** that address is excluded too.
3. **Given** a read whose original URI is excluded by the two rules above, **When** it reaches the plain resource path on any surface, **Then** it is refused with "resource not found" and nothing is sent upstream.
4. **Given** `/mcp/s/<server>` with the native skills surface, **When** a client reads a skill address, **Then** the native surface answers it exactly as Spec 125 defines, and the plain resource path never sees it.

---

### User Story 5 - My client learns when resources change (Priority: P2)

A server adds a file to its resource list. A client that already listed resources is told the list changed and re-lists. A client that subscribed to one resource is told when that resource changes and re-reads it.

**Why this priority**: Useful for live documents and clients that cache, but listing and reading work without it.

**Independent Test**: A fixture server adds a resource and emits `notifications/resources/list_changed`; a connected client receives `notifications/resources/list_changed` within the debounce window and the next list includes the new entry. A client subscribes to one proxied address; the fixture emits `notifications/resources/updated` for the original URI; only that client receives the notification, carrying the proxied address. After the client unsubscribes (or disconnects) and no other session holds the subscription, mcpproxy unsubscribes upstream.

**Acceptance Scenarios**:

1. **Given** an upstream emits `notifications/resources/list_changed`, **When** mcpproxy re-reads that server's resources and templates (debounced), **Then** connected clients receive one `notifications/resources/list_changed` if the aggregated set changed.
2. **Given** a server connects, disconnects, is quarantined, approved, enabled, disabled, or the configuration is reloaded, **When** the aggregated set is rebuilt, **Then** the same notification is sent when the set changed.
3. **Given** a client subscribes to a proxied address of a server it may reach, whose server announced `resources.subscribe`, **When** mcpproxy accepts, **Then** mcpproxy holds one upstream subscription per original URI for all sessions that want it.
4. **Given** an upstream emits `notifications/resources/updated` for a subscribed URI, **When** mcpproxy forwards it, **Then** only sessions that subscribed to the matching proxied address and whose caller may still reach the server receive it, with the proxied address.
5. **Given** a subscribe request for a server that does not announce subscriptions, for an unreachable server, or for an excluded address, **Then** it is refused with "resource not found" or "unsupported", and nothing is sent upstream.
6. **Given** an upstream reconnects, **When** it is ready again, **Then** mcpproxy re-establishes the upstream subscriptions still wanted.

---

### User Story 6 - Resources on a single-server endpoint (Priority: P2)

A client that connects to `/mcp/s/docs` (one upstream server, as if connected directly) sees that server's resources under their original addresses, beside Spec 125's native skills when those are on.

**Why this priority**: The single-server endpoint is the "as if direct" surface; resources belong there, but `/mcp` covers most clients.

**Independent Test**: With the feature on, `/mcp/s/docs` lists the docs server's resources with their original URIs (no namespace) and reads them; another server's resources never appear; with native skills on, skill reads still go to the native surface and the plain list excludes skill files.

**Acceptance Scenarios**:

1. **Given** the feature is on and server S is eligible, **When** a client lists resources or templates on `/mcp/s/S`, **Then** it gets S's resources and templates under their original URIs and patterns, filtered by the same exclusions and gates as `/mcp`.
2. **Given** the endpoint also carries the native skills surface, **When** the resources capability is advertised, **Then** it is advertised once, with the union of what both features support.

---

### Edge Cases

- **Two servers, same URI**: distinct proxied addresses (US1-2). Two servers whose names differ only in characters that need encoding still get distinct addresses (the encoding is injective).
- **Server name containing `/`, `:`, `{`, `}`, `%`, spaces or non-ASCII**: encoded so that the proxied address stays a valid URI, stays a valid prefix of a URI template (no template expression characters), and decodes back to exactly one server name.
- **Malformed proxied address** (wrong prefix, non-canonical encoding of the server, empty original URI): "resource not found", nothing sent upstream.
- **Upstream template that is not a valid RFC 6570 template**: dropped from the list with a log line.
- **A filled template that matches templates of two servers**: impossible, because every template starts with its own server's namespace.
- **An upstream listing that never ends** (fresh cursor on every page) or lists 100k entries: bounded by a page cap and per-server and total item caps; excess logged, never silent.
- **Upstream returns a different URI in the read contents than the one requested**: each content item's URI is mapped into the namespace when it is an address of the same server; the item keeps its content.
- **Upstream returns a blob**: a blob whose decoded bytes are text (valid UTF-8, no NUL byte) is treated as text whatever MIME type it declares: it is checked for a block decision and redacted, stripped or spotlighted, then encoded again. Any other blob is binary and passed unchanged within the size cap. The declared MIME type is never trusted, so a secret cannot pass by being labelled `image/png`.
- **The upstream answers "not found"**: the client gets "resource not found" with the protocol-era-appropriate code the SDK uses for the client's protocol version.
- **A reachable server that is disconnected** (and cannot be reconnected on use): the read fails with a connection error; the caller already may reach that server, so this reveals nothing it could not list.
- **A server listed resources, then lost the resources capability on reconnect**: its resources disappear at the next rebuild; reads are refused.
- **Feature switched off at runtime** (hot off, as distinct from off at start, FR-004): the aggregated set is cleared on every surface and reads are refused at once (the live checks of FR-015 apply before the rebuild); the capability stays advertised on instances that announced it (the SDK cannot withdraw a capability from a live instance) and simply lists nothing. From PR-2 on, clients also receive `list_changed`.
- **Upstream URI that cannot be namespaced** (it holds bytes not allowed in a URI path, for example the brackets of an IPv6 authority `https://[::1]/doc`, spaces or non-ASCII bytes; or a template whose literal text does): not published, logged. Expressions in templates are left to the template parser.
- **`code_execution` sandbox**: no resource API in this spec.
- **Resource links inside tool results** (`resource_link` content naming an upstream URI): not rewritten in this spec; such a URI is the upstream's original, which the client cannot read through `/mcp` unless the same address is listed or matches a template (out of scope, see below).

## Requirements *(mandatory)*

### Functional Requirements

**Switch and eligibility**

- **FR-001**: A global setting `aggregate_upstream_resources` (default **off**) MUST turn the feature on. It MUST be hot-reloadable.
- **FR-002**: A per-server setting `expose_resources` (tri-state: absent = include when the server announces the resources capability; `false` = exclude) MUST be honoured, with the same patch semantics as `expose_prompts`.
- **FR-003**: A server MUST contribute resources and templates only while it is enabled, not quarantined, connected, announces the resources capability, is not excluded by FR-002, and passes the skills gate (FR-017). Otherwise nothing is requested from it.
- **FR-004**: With the feature off from start ("cold off"), no endpoint MUST advertise the resources capability because of this feature, and the behaviour of every endpoint MUST be byte-identical to today's for `resources/*` (Spec 125's native skills surface is unchanged). Switching it off at runtime ("hot off") is covered by the edge case above.

**Addresses**

- **FR-005**: On aggregated surfaces (`/mcp` in every routing mode: `/mcp`, `/mcp/all`, `/mcp/call`, `/mcp/code`) every upstream resource URI MUST be exposed as `mcpproxy://upstream/<server>/<original-uri>`, where `<server>` is the server name with every byte outside the RFC 3986 unreserved set percent-encoded (uppercase hex) and `<original-uri>` is the upstream URI byte for byte.
- **FR-006**: Every upstream template MUST be exposed with the same prefix in front of the upstream pattern, byte for byte, so that filling the exposed template yields the prefix followed by what the upstream template yields.
- **FR-007**: Decoding MUST accept only the canonical form: the exact prefix, a server segment whose decoding re-encodes to the same bytes and names a configured server, and a non-empty original URI. Anything else MUST be "resource not found" with nothing sent upstream.
- **FR-008**: On `/mcp/s/<server>` the original URIs and patterns MUST be used unchanged (one server, no collision).
- **FR-009**: Content items in a read result MUST carry the address the client used for the item that answers it; other item URIs that are addresses of the same server MUST be mapped into the namespace the same way.

**Listing**

- **FR-010**: `resources/list` and `resources/templates/list` MUST return the aggregated, eligible, non-excluded, non-withheld entries the caller may reach (FR-014), with every other field passed through as the upstream sent it. Each list MUST re-check the live eligibility of every server it names (feature switch, enabled, not quarantined, not excluded, skills gate) before answering.
- **FR-011**: Upstream listings MUST be paged by mcpproxy with a page cap (50 pages), a per-server item cap (500 resources, 100 templates) and, on streamable HTTP, a byte cap of 4 MiB per page (a server whose page is over it contributes nothing that refresh); the rebuild as a whole MUST be bounded by a deadline (30 s); the aggregated set MUST be capped in total (2,000 resources, 500 templates). Every drop MUST be logged with counts.
- **FR-012**: The aggregated set MUST be rebuilt when a server connects, disconnects, changes admin state (quarantine, approval, enable, disable), when the configuration reloads, and (US5) on an upstream `list_changed`. Lists and reads do not depend on the rebuild having run: FR-010 and FR-015 re-read the live state on every request, so a registration a rebuild has not yet replaced is never served.
- **FR-013**: Resource and template metadata (name, title, description) MUST be scanned with the same deterministic tool-poisoning scanner used for aggregated prompts; an entry with a "dangerous" verdict MUST be withheld from the list and its reads refused. Withholding wins over every other match: a read of a withheld resource's URI, or of any address a withheld template matches, MUST be refused even when another template of the same server also matches it. Subscriptions (US5) use the same address admission as reads.

**Reach and gates**

- **FR-014**: A caller MUST see and read resources of a server only when the same caller may reach that server's tools. The profile scope MUST be resolved as tool discovery resolves it: the Spec 108 resolution (pin, URL, session, client binding, anonymous profile) whose dangling or guarded results are deny-all, over the legacy tiers; a list skips the session tier (Spec 058 FR-012), a read keeps the full precedence. On top: agent-token server scope with the Spec 118 pin bound, and the administrator rules that apply to tools.
- **FR-015**: Every read MUST re-check, at read time and before any upstream request: the feature switch, the address decodes (FR-007), the server is configured, enabled, not quarantined, not excluded by `expose_resources`, still announces the resources capability, passes the skills gate and is reachable by the caller (FR-014), and the original URI is not excluded (FR-017) and not withheld (FR-013). A failed check MUST give the same answer an address that matches nothing gets from the MCP layer, byte for byte in both protocol eras: code `-32002` (2025-11-25 era) or `-32602` (2026-07-28 era) and the message `handler not found for resource URI '<address>': resource not found`, with no data. It MUST send nothing upstream.
- **FR-016**: A read MUST be dispatched to the owning server through the same reconnect-on-use and connection-state rules tool calls use.

**Skills**

- **FR-017**: **Skills gate.** A server that announces the skills extension MUST be served on the plain resource path (lists and reads) only while its stored skill records are complete: the skills feature is on and the server is exposed to it, the server's last skills refresh finished without an error, a truncated listing or a failed entry, and no stored skill reached the per-skill file limit. Otherwise none of that server's resources or templates are listed or read (fail closed; a lookup error counts as incomplete). For every server, the following MUST also be excluded from listing and reading on every surface: a resource whose URI is in the `skill://` namespace (scheme compared case-insensitively, as RFC 3986 requires); a template whose filling could produce such a URI (its literal text before the first expression starts with `skill://`, or is a case-insensitive prefix of `skill://` followed by an expression); and any URI that a stored skill record of the server names as the skill's own address or as one of its files (current or approved copy, whatever the approval state). The read path applies the same rules to the final original URI, so a template such as `file:///{+path}` cannot be filled onto a skill file either.
- **FR-018**: On `/mcp/s/<server>` with the native skills surface, the native surface MUST keep answering the reads it claims (Spec 125); this feature MUST NOT change which reads it claims.

**Content**

- **FR-019**: Text contents of every read, and blobs that are text (valid UTF-8, no NUL byte, whatever MIME type they declare), MUST go through the configured output policy as untrusted upstream output, with the same decisions, policy-decision rows and errors as an upstream tool response; rewritten text blobs are encoded again.
- **FR-020**: Binary blobs (not text by the FR-019 test) MUST be passed through unchanged within the size cap. A redact or strip policy cannot inspect them; this is the same exposure binary tool content has.
- **FR-021**: A read MUST be bounded by the resource response cap (8 MiB of upstream JSON response): over the cap the read MUST be refused with an error naming the cap. On streamable HTTP, where each request has its own response body, mcpproxy MUST stop reading at the cap and never buffer more than the cap plus a fixed margin. On stdio and legacy SSE the response arrives on a stream shared by all messages and cannot be cut per request; there the decoded result MUST be measured and refused over the cap (the same exposure any tool result of that server already has).

**Activity**

- **FR-022**: Every read that reaches a listed resource or a template of an eligible server, on every surface, MUST record exactly one activity row (a read of an address that matches nothing registered is answered "not found" by the MCP layer before any server is involved, and records none), of a new type `resource_read` with: server, original URI, proxied URI (as the client used it), status (`success`, `error`, `blocked`), error message, duration, total content bytes, content count, MIME types, attribution (caller, profile, work session) as tool calls carry, and the response body under the same size and detection rules as a tool call's response. A blocked read MUST also record the policy decision as a companion row, as prompts do.
- **FR-023**: Listing and subscription requests MUST NOT record activity rows in this spec. A `resource_read` row is not a work-session attempt (Spec 124 counters do not change), but it is a request row of its credential's work session, like `prompt_get`.
- **FR-024**: The new activity type MUST appear wherever activity types are filtered or described (REST filter, CLI `activity list --type`, Web UI activity filter, OpenAPI).

**Changes and subscriptions (US5)**

- **FR-025**: An upstream `notifications/resources/list_changed` MUST trigger a debounced rebuild of that server's contribution. The SDK's automatic broadcast on every set replacement MUST be suppressed; mcpproxy MUST send at most one `notifications/resources/list_changed` per rebuild, and only to sessions whose authorized list view (FR-010/FR-014) changed. The upstream listener MUST receive resource notifications from a resource-only upstream: on streamable HTTP the standalone GET stream MUST be opened when the server announces `resources.listChanged` or `resources.subscribe`, list changes MUST be coalesced per server, and `resources/updated` MUST be keyed by URI and connection generation, never coalesced across URIs.
- **FR-026**: The resources capability MUST advertise `listChanged: true` from the PR that delivers FR-025, and `subscribe: true` only from the PR that delivers FR-027–FR-030.
- **FR-027**: A client subscription to a proxied address MUST be accepted only under the FR-015 checks (including the FR-013 address admission) and only when the owning server announced `resources.subscribe`. Subscription state MUST be mcpproxy's own, keyed by the authenticated session (2025-11-25 era) or listener (2026-07-28 era) and the surface, since the SDK keeps none.
- **FR-028**: mcpproxy MUST hold one upstream subscription per (server, original URI) for all client sessions that want it, release it when the last session unsubscribes or ends, and re-establish it after the upstream reconnects only after re-running the full FR-015 checks for each wanting session and confirming the reconnected server still announces `resources.subscribe`; entries that fail are retired.
- **FR-029**: An upstream `notifications/resources/updated` MUST be forwarded only to sessions subscribed to the matching proxied address for which the full FR-015 checks still pass at delivery time, with the proxied address (on `/mcp/s/<server>`, the original URI). A session that fails them is unsubscribed.
- **FR-030**: Subscriptions MUST work for clients on the `2025-11-25` era (`resources/subscribe` / `resources/unsubscribe`) and on the `2026-07-28` era (`subscriptions/listen`) as the SDK supports each.

**Single-server endpoint (US6)**

- **FR-031**: `/mcp/s/<server>` MUST expose that server's eligible resources and templates under original URIs (FR-008) with every gate above, and MUST advertise the resources capability once, combining this feature and Spec 125.

**Documentation**

- **FR-032**: A feature page `docs/features/upstream-resources.md` MUST describe the switch, the address form, the gates, the skills exclusion, the size cap and the activity row, and `docs/configuration.md` MUST list both settings.

### Key Entities

- **Proxied resource address**: `mcpproxy://upstream/<encoded-server>/<original-uri>`; decodes to exactly one (server, original URI) pair.
- **Aggregated resource set**: per server, the eligible resources and templates after caps, skill exclusion and the metadata scan; rebuilt on the events in FR-012.
- **Resource read**: one client request for one address; produces one `resource_read` activity row.
- **Upstream subscription**: one per (server, original URI), shared by the client sessions that subscribed to it.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With the feature on, a client lists and reads 100% of an eligible fixture server's resources and template-produced addresses through `/mcp`, and the content matches the upstream byte for byte when no output policy rewrites it.
- **SC-002**: With the feature off from start, every endpoint's handshake and `resources/*` answers are identical to the current release (golden comparison).
- **SC-003**: Across a matrix of callers (administrator, scoped agent token, pinned token, profile-bound client) and servers (healthy, quarantined, disabled, out of scope), 0 resources of an unreachable server are listed and 0 reads of one reach the upstream.
- **SC-004**: 0 skill files (approved, held or unusable) are listed or read through the plain resource path on any surface.
- **SC-005**: Every read that reaches a listed resource or a template of an eligible server produces exactly one `resource_read` row (100% of such reads in the test matrix, refusals by the read gate included). A read that matches nothing registered records none; a skill read answered by Spec 125's native surface records Spec 125's `skill_access` row and no `resource_read` row.
- **SC-006**: On streamable HTTP, a read of an upstream response 10× the cap completes with a refusal while mcpproxy's memory for that read stays under the cap plus margin; on every transport an over-cap response is refused.
- **SC-007**: (US5) A client receives `list_changed` within 2 seconds of the upstream's notification, and `resources/updated` reaches only subscribed sessions.

## Out of Scope

- Rewriting `resource_link` and embedded-resource URIs inside tool results into the proxied namespace.
- A rug-pull baseline (approval and hold) for resource metadata, as Spec 100 has for prompts. Resource lists are often large and change by design (files appear and disappear); metadata is scanned (FR-013) and content goes through the output pipeline. A baseline can be specified later if a need shows.
- Resource access from the `code_execution` sandbox.
- Resource completion (`completion/complete` for template arguments); see #1970.
- Admin REST, CLI or Web UI pages listing a server's resources (a follow-up may add `GET /api/v1/servers/{id}/resources`).
- Server edition specifics beyond what the shared code gives (the per-user reach rules apply as for tools).

## Assumptions

- Upstream servers are spoken to in the pinned upstream protocol era (Spec 058); upstream subscriptions use `resources/subscribe`.
- The FR-017 address exclusion reads the stored skill records whether or not the skills feature is on now (records stay after the feature is switched off). A server that announces the skills extension is withheld entirely while its records are not complete, including while the skills feature is off, so a user who wants such a server's plain resources turns skills on. A server that does not announce the extension has no skill files to protect beyond what its stored records (if any) name.
- A resource is not approval-gated, like a tool response; protections are the reach gates, the metadata scan, output policy, size cap and activity record.
- Caps are safety ceilings chosen as constants (as for prompts), not configuration.
- The 8 MiB cap is measured on the upstream JSON response, so base64 blobs count at their encoded size.

## PR Split

| PR | Delivers | Stories / FRs |
|----|----------|---------------|
| **PR-1** | Settings and wiring; upstream list/read with caps and response limit; address codec; aggregated list on `/mcp` (all routing modes) with reach filter, skill exclusion and metadata scan; read with gates, output policy, size cap and `resource_read` activity; rebuild on server/admin/config events; docs page. Capability advertised with `subscribe:false, listChanged:false`. | US1–US4; FR-001–FR-024, FR-032 |
| **PR-2** | Upstream `list_changed` forwarding (debounced), `listChanged:true`; `/mcp/s/<server>` resources beside Spec 125. | US5 (list part), US6; FR-025, FR-026 (listChanged), FR-031 |
| **PR-3** | Subscriptions and `resources/updated` (both protocol eras), refcounted upstream subscriptions, reconnect re-subscribe, `subscribe:true`. | US5 (subscriptions); FR-026–FR-030 |

Each PR is usable on its own and keeps the feature off by default.

## Commit Message Conventions *(mandatory)*

- Spec: `docs(spec): 139 upstream resources …`
- Code: `feat(resources): …`, `fix(resources): …`, `test(resources): …`, `docs(resources): …`
- Commits reference `Related #1969`. Only the description of PR-3, the PR that finishes the issue, carries `closes #1969`.
- No `Co-Authored-By` or "Generated with" lines.

# Feature Specification: Skills over MCP

**Feature Branch**: `125-skills-over-mcp`
**Created**: 2026-11-16
**Status**: Draft
**Input**: Issue #1895 "Support for Skills over MCP". Upstream MCP servers can expose agent skills through the official extension `io.modelcontextprotocol/skills` (SEP-2640). mcpproxy proxies everything else those servers offer and drops their skills. Bring the skills to every client connected to mcpproxy, without letting skill text bypass quarantine.

**Related**: Spec 032 (tool-level quarantine), Spec 100 (per-prompt baseline, the closest precedent), Spec 105 and Spec 118 (agent-token and pin scoping), Spec 108 (profiles), Spec 117 (review-bound approvals), Spec 119/120 (scan trust mode and structural holds), Spec 058 (upstream protocol pin).

**Web UI follow-up**: [Spec 130](../130-skills-review-ux/spec.md) defines the filtered Review queue entry and responsive Skills detail navigation.

---

## What a skill is, and the one rule that shapes this spec

A skill is a folder of instructions for an agent: a `SKILL.md` file with a short frontmatter (name, description) and a body of instructions, plus optional supporting files (reference text, scripts). An MCP server that implements the extension lists its skills and serves their files.

**A skill is instruction text that ends up in the model's context.** A tool description is a few lines; a skill is pages of "do this, then run that". If mcpproxy handed skills to agents the way it hands over nothing else today, a malicious or compromised server would have a way around tool quarantine that is wider than the door quarantine closes. So the rule for this whole spec is: **no skill content reaches an agent until that exact content has been approved**, and every way of reaching a skill goes through the same gate.

## Verified background (live check, 2026-11-16)

Checked against a public server that implements the extension (`https://huggingface.co/mcp`), with the handshake mcpproxy sends today (protocol `2025-11-25`, no client capabilities):

- The server announced the skills extension and the resources capability. No change to the upstream handshake is needed to see skills.
- The skill listing returned 25 skills. Each entry carried the address of its `SKILL.md`, the frontmatter (name, description) and the list of its files, each with a SHA-256 digest and a size in bytes.
- Reading a file returned text whose digest and size matched the listing.
- The extension also allows a skill to declare its file list as "dynamic", meaning the server does not commit to the content. The extension limits a skill to 512 files and 16 MiB.

Client support is thin: only a few hosts consume the extension today, and VS Code's support is experimental and off by default. Claude Code, Cursor, Codex and Copilot CLI do not consume it.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 - See which skills my servers offer (Priority: P1)

A developer connects an upstream server that serves skills and wants to know what it offers before any agent can use it. In mcpproxy they see, per server, the list of skills: name, description, the files with sizes, and whether each skill is approved, held for review, or unusable. They can open a skill and read its `SKILL.md` and its text files.

**Why this priority**: Nothing else in this feature can be trusted without it. A person cannot approve what they cannot read, and "which skills does this server push at my agents?" is the first question the feature raises.

**Independent Test**: Connect a fixture server that serves three skills (one with supporting files). With the feature on, the per-server skill list shows all three with their files and sizes; opening one shows the exact text the server serves. No agent-facing surface is needed for this test.

**Acceptance Scenarios**:

1. **Given** the feature is on and a connected server announces the skills extension, **When** an administrator lists that server's skills, **Then** each skill appears once with its name, description, origin server, file count, total size and approval state.
2. **Given** a scoped agent credential whose reach includes the server, **When** it lists that server's skills through the people-facing API or opens a held skill there, **Then** it sees only approved skills, and a held skill is "not found" for it.
3. **Given** a listed skill, **When** the person opens it, **Then** they see the full `SKILL.md` text and can open each supporting text file, and each file shows its size and digest.
4. **Given** a server whose served file does not match the digest or size in its own listing, or whose `SKILL.md` frontmatter differs from the listing, **When** the skill is read in, **Then** the skill is marked unusable with the reason (which file, what differed) and none of its content is offered to agents.
5. **Given** a server that does not announce the extension, **When** the person lists its skills, **Then** the list is empty and says the server offers none; nothing is requested from that server.
6. **Given** a skill larger than the extension's limits (more than 512 files or more than 16 MiB), **When** it is read in, **Then** it is marked unusable with the reason and its files are not fetched beyond the limit.

---

### User Story 2 - Skills are held until I approve them, and again when they change (Priority: P1)

The developer reviews a new skill and approves it. Weeks later the server changes the skill: a line is added to `SKILL.md`, or a script is swapped. The skill stops being available to agents until the developer has looked at the change and approved it again. A skill the scanner flags is held with the findings shown.

**Why this priority**: This is the rule above, made concrete. Without it the feature is a quarantine bypass and must not ship.

**Independent Test**: With a fixture server in the default trust mode, approve a skill, change one byte of one file on the server. Before the refresh an agent still gets the approved text. After the refresh the skill is held, the review shows what changed, and an agent asking for it is refused. Approve it; the agent gets the new content.

**Acceptance Scenarios**:

1. **Given** a skill never seen before on a server in `manual` trust mode, **When** it is read in, **Then** it is held: visible to the reviewer, absent for agents.
2. **Given** an approved skill on a server in `manual` or `scan` trust mode, **When** any of its files, its file list or its frontmatter changes upstream, **Then** the skill returns to held, the review shows the previous and the current state, and agents keep being refused until a person approves it again. A clean scan does not approve the change, in `scan` mode either.
3. **Given** a held skill whose text files produce scanner findings, **When** the person opens the review, **Then** the findings are listed against the file they came from.
4. **Given** a skill never seen before on a server in `scan` trust mode (the default), **When** the scan of every text file is clean and complete, **Then** it is approved without a person; with any finding or an incomplete scan it is held with the evidence.
5. **Given** a server in `auto` trust mode, **When** a skill is new or changed, **Then** it is approved for its current content without review, as that server's tools are; the approval is still bound to that content and recorded in the activity log.
6. **Given** a skill a person blocked, **When** the server changes it, refreshes, or a mode would approve it automatically, **Then** it stays blocked and unavailable to agents until a person unblocks it.
7. **Given** a skill that declares a "dynamic" file list, **When** it is read in, **Then** it is shown to people as "cannot be verified", cannot be approved, and is never available to agents. There is no setting that changes this in this version.
8. **Given** an approval made against what the person reviewed, **When** the skill changed between the review and the approval, **Then** the approval is refused, nothing is written, and the person is told the review is out of date.
9. **Given** a server that is removed, or removed and added again under the same name, **When** its skills are next listed, **Then** no earlier approval applies: the approvals went with the server.
10. **Given** a quarantined server, **When** its skills are listed, **Then** none of them is available to agents whatever their own state.

---

### User Story 3 - My agent finds and loads approved skills from any client (Priority: P1)

A developer works in a client that knows nothing about the skills extension. Their agent needs to do something a skill covers. The agent searches mcpproxy for a matching skill, gets a short list, loads the one it wants, and reads a supporting file when the skill refers to one. The developer did not paste anything and did not install anything in the client.

**Why this priority**: This is the value: skills from any upstream, usable in the clients people actually run, found by search instead of listed up front. Almost no client consumes the extension itself, so without this story the feature reaches nearly nobody.

**Independent Test**: With two approved skills on a fixture server, an agent session over the normal MCP endpoint searches by a phrase from one description, receives that skill first, loads it, and reads one supporting file. A held skill on the same server never appears and cannot be loaded by id.

**Acceptance Scenarios**:

1. **Given** approved skills on connected servers, **When** the agent searches with a phrase, **Then** it receives a short ranked list: skill id, name, description, origin server, file count, size. No skill body is in the list.
2. **Given** a skill id from the list, **When** the agent loads it, **Then** it receives the `SKILL.md` body preceded by a header that names the origin server and says the text is instructions from an upstream server and not from the user or from mcpproxy, together with the list of supporting files.
3. **Given** a loaded skill that refers to a supporting text file, **When** the agent reads that file by skill id and path, **Then** it receives the file, and the file is one of the approved set.
4. **Given** a held, blocked, unusable or unapproved skill, **When** the agent searches or asks for it by id, **Then** it is absent from search and the load is refused with the same answer as for a skill that does not exist.
5. **Given** a profile whose tool rules deny some tools of a server but whose reach includes the server, **When** the agent searches, **Then** that server's approved skills are found: tool rules do not hide skills.
6. **Given** a single-server endpoint, **When** an agent lists tools there, **Then** the skill tools are not among them.
7. **Given** a caller limited to some servers (agent token, profile, pinned profile), **When** it searches or loads, **Then** skills of servers outside its reach do not exist for it: absent from search, same "not found" on load, no difference in the answer that would reveal them.
8. **Given** an approved skill whose upstream content changed since approval, **When** the agent loads it before mcpproxy noticed, **Then** the agent receives the approved content, never the new unreviewed content; at the next refresh the skill is held per the trust mode.
9. **Given** a skill whose frontmatter asks for tools to be pre-allowed or for hooks to run, **When** it is loaded, **Then** mcpproxy grants nothing and runs nothing on that basis, and the header says so.
10. **Given** a request for a file path outside the skill, or a file of another skill or another server, **When** the agent asks for it, **Then** it is refused.
11. **Given** a supporting file that is not text, **When** the agent asks for it, **Then** it is refused with a message saying binary files are not served; the file is still shown in the file list.
12. **Given** any search, load or file read, **When** it completes or is refused, **Then** it is recorded in the activity log with the caller, the skill and the outcome.

---

### User Story 4 - I decide whether skills are on, per server (Priority: P2)

An operator wants skills from one server and not from another, or not at all. They turn the feature on, and exclude a server. When a server adds or drops skills, mcpproxy picks that up without a restart.

**Why this priority**: The feature adds built-in tools to every client's list and new instruction text to the system. It has to be a choice, and the first release ships with it off.

**Independent Test**: With the feature off, no skill tools are listed and no skill request is sent upstream. Turn it on: tools appear, skills are read in. Exclude one server: its skills disappear for agents and people see it as excluded.

**Acceptance Scenarios**:

1. **Given** a default installation, **When** nothing is configured, **Then** the feature is off: no skill tools are offered to agents and no skill request goes to any upstream.
2. **Given** the feature is turned on, **When** the configuration is applied, **Then** skills are read in from every connected server that announces the extension, without a restart.
3. **Given** a server excluded from skills, **When** skills are listed or searched, **Then** that server contributes none, and nothing is requested from it.
4. **Given** a connected server that adds, changes or removes a skill, **When** mcpproxy next refreshes (on reconnect, on the periodic pass, or on request), **Then** the catalog reflects it; added and changed skills follow User Story 2, removed skills are gone for agents.
5. **Given** the feature is turned off again, **When** the configuration is applied, **Then** the tools disappear and stored approvals are kept for the next time it is turned on.

---

### User Story 5 - A client that speaks the extension gets skills natively from one server (Priority: P3)

A developer uses a client that consumes the skills extension itself. They connect it to the single-server endpoint of one upstream. The client sees that server's approved skills in its own skills interface, with its own consent prompts.

**Why this priority**: It is what the issue literally asks for, and it lets a native client keep its own progressive loading. It comes last because the audience is small today and everything it needs (reading in, the gate) is built by the earlier stories.

**Independent Test**: A test client that implements the extension connects to the single-server endpoint of a fixture server with one approved and one held skill. It is told the extension is available, lists exactly the approved skill, and reads its files; the held skill is absent and cannot be read by address.

**Acceptance Scenarios**:

1. **Given** the feature is on and the server has approved skills, **When** a client connects to that server's single-server endpoint, **Then** the extension is announced and the listing contains exactly the approved skills of that server, with the addresses the upstream uses.
2. **Given** a held, blocked or unusable skill, **When** the client lists or reads by address, **Then** it is absent and the read is refused as not found.
3. **Given** an approved skill, **When** the client reads a file, **Then** the bytes match the digest in the listing the client received, and the read appears in the activity log.
4. **Given** the aggregated endpoints, **When** any client connects, **Then** the extension is not announced there.
5. **Given** a caller whose scope does not include the server, **When** it connects to the single-server endpoint, **Then** it is refused exactly as it is today for tools.

---

### Edge Cases

- A server announces the extension but its listing fails or times out: the server's other functions (tools, prompts) are unaffected; its skills keep their last known state and the failure is shown on the server.
- The listing is empty or partial: this is not proof the server has no skills (the extension says so). Skills already known are not deleted on one empty listing; they are marked not currently listed and are unavailable to agents until listed again.
- Two servers serve a skill with the same name: both exist, each under its own server. Search shows both with their origin; neither shadows the other.
- A skill name with a separator or a control character, or a skill or file address that is absolute outside the skill, escapes it by traversal, or contains control characters: the skill is unusable. Separators inside a valid relative path are normal (`references/guide.md`, a skill nested under `acme/billing/refunds`). No unvalidated name or path is ever used to build an id or a storage location.
- A `SKILL.md` or supporting file larger than one agent call can carry: the call returns the first part and says, in the answer, the offset to ask for next (FR-022a). The existing response truncation is not relied on for this; it has no way to continue plain text.
- A file is text by declaration but not valid text: treated as binary.
- Scanner cannot finish a file (size or time budget): the result is "analysis incomplete". In `scan` mode that holds a new skill. In `auto` mode, and with quarantine turned off globally, approval does not wait for or depend on the scan; the result is still stored as evidence.
- The server is disabled or quarantined: its skills are unavailable to agents at once, and available again when it is enabled or released, without a new review if the content did not change.
- The server is enabled but not connected at the moment (it is reconnecting, or asleep until used): its approved skills stay available, because agents are served from the approved copy and no upstream call is needed. This is a rule for skills; it does not describe how tool calls behave.
- The stored copy of an approved skill is damaged: the load is refused, the skill is unusable with that reason (FR-015), and the next refresh reads it in again as a skill to review.
- The server reconnects as a different incarnation: approvals do not carry over.
- Many skills (hundreds) on one server: reading in is bounded in count and total size per server; what is beyond the bound is reported as not read in, never silently dropped.
- A skill nested inside another skill's path: each is its own skill with its own approval.

---

## Requirements *(mandatory)*

### Functional Requirements

**Reading skills in**

- **FR-001**: With the feature on, mcpproxy MUST detect which connected upstream servers announce the skills extension, and MUST NOT send any skill request to a server that does not announce it or that is excluded.
- **FR-002**: For each such server, mcpproxy MUST obtain the full skill listing (following pagination) and, for every skill with a declared file list, obtain every file needed for review and scanning.
- **FR-003**: mcpproxy MUST verify every obtained file against the digest and size in the listing, and the `SKILL.md` frontmatter against the listing entry. A skill that fails any check MUST be recorded as unusable with the reason and MUST NOT be offered to agents.
- **FR-004**: mcpproxy MUST enforce the extension's limits (512 files, 16 MiB per skill) and its own per-server bounds on skill count and total size, MUST stop fetching beyond a bound, and MUST report what was not read in.
- **FR-005**: A skill MUST be identified by its origin server and its address on that server. The same name on two servers is two skills. Names and paths MUST be validated before any use; invalid ones make the skill unusable.
- **FR-006**: A failure to list or fetch skills MUST NOT affect the server's tools or prompts and MUST be visible on the server's status.
- **FR-007**: An empty or shorter listing MUST NOT delete knowledge of previously seen skills or their approvals; skills not currently listed MUST be unavailable to agents.

**The gate**

- **FR-008**: Each skill MUST have an approval state: pending (new), approved, changed, or unusable, and separately a blocked mark set by a person. A skill is available to agents, on every surface, only when it is approved, not blocked, currently listed by its server as of the last successful refresh (FR-007), and its server is enabled and not quarantined. A live connection at the moment of the call is not required. A blocked mark MUST survive refreshes, content changes and every automatic approval until a person removes it.
- **FR-008a**: mcpproxy MUST keep its own copy of the content it read in: for every skill the current content, and for an approved skill the approved content (the same copy when they are equal). The copies live in mcpproxy's own data store with the protection that store already has, count against the per-server bounds of FR-004, and are removed with the server (FR-010).
- **FR-008b**: Everything an agent receives about a skill MUST be derived from the approved copy only, never from a fresh upstream read, so an agent never receives unreviewed text even when the upstream has already changed; the change is picked up by the next refresh and handled per FR-013. The skill tools (FR-020 to FR-022) return that copy with the provenance header and after the sanitisation and size limits of FR-024, which may redact or shorten it but never add upstream text; native file reads (FR-029a) return it byte for byte.
- **FR-009**: An approval MUST be bound to the skill's content: the set of files (address, digest, size) and the frontmatter. Any difference between the approved and the current content MUST move the skill to changed.
- **FR-010**: An approval MUST be bound to the server's incarnation and MUST be removed with the server. A server removed and re-added starts with no skill approvals.
- **FR-011**: An approval request MUST carry what the reviewer saw; if the current content differs, the approval MUST be refused with nothing written.
- **FR-012**: The `SKILL.md` and every text file of a new or changed skill MUST be scanned with the existing tool-poisoning scanner, and the findings MUST be stored as hold evidence against the file.
- **FR-013**: The server's trust mode MUST decide skill approval as follows, and this table is the whole rule (it is deliberately stricter than the tool rule for changes):

  | Server trust mode | New skill | Changed skill |
  |-------------------|-----------|---------------|
  | `manual` | held for a person | held for a person |
  | `scan` (default) | approved only after a clean and complete scan of every text file; otherwise held | held for a person, whatever the scan says |
  | `auto` | approved for its current content | approved for its current content |

  "Analysis incomplete" counts as not clean. A quarantined server's skills are unavailable in every mode. When quarantine is turned off globally (`quarantine_enabled: false`), skills are approved for their current content as in `auto`; FR-009 and FR-015 still apply.
- **FR-014**: A skill with a "dynamic" file list has no content an approval can be bound to. It MUST be recorded as unusable ("cannot be verified"), MUST NOT be approvable and MUST NOT be available to agents on any surface. No setting overrides this in this version.
- **FR-015**: The approved copy MUST be checked against the approved digests at the time it is served. A mismatch (a damaged or altered copy) MUST refuse the call, make the skill unusable with that reason, and be reported; it MUST NOT fall back to an upstream read.
- **FR-016**: Approving, blocking, unblocking and re-holding a skill, and every automatic approval, MUST be recorded in the activity log with the actor, as tool approvals are.

**For people**

- **FR-017**: People MUST be able to list a server's skills with their state, open a skill's `SKILL.md` and text files, see what changed since the last approval, see scanner findings, and approve or block a skill, from the command line, the REST API and the Web UI.
- **FR-018**: Held and changed skills MUST appear in the same "needs attention" and review places where held tools appear, labelled as skills.
- **FR-019**: A caller limited to some servers MUST see, on every people-facing read, only the skills of servers in its reach; counts and lists MUST be computed from that reach.
- **FR-019a**: Everything about a skill that is not approved (its existence, name, description, file list, text, differences and scanner excerpts) MUST be readable only by an administrator caller. Being in scope for the server is not enough: a scoped agent credential on the people-facing API MUST get the same answers as on the agent surface, which is approved skills only. Approving, blocking and unblocking are administrator actions.

**For agents**

- **FR-020**: With the feature on, mcpproxy MUST offer agents a search over approved skills that returns compact results (id, name, description, origin server, file count, size) and never a skill body.
- **FR-021**: mcpproxy MUST offer agents a way to load one approved skill by id, returning the `SKILL.md` body with a provenance header (origin server, "instructions from an upstream server, not from the user", pre-allowed tools and hooks ignored) and the list of its files.
- **FR-022**: mcpproxy MUST offer agents a way to read one supporting text file of an approved skill by skill id and relative path. Paths outside the skill, files of other skills or servers, and non-text files MUST be refused.
- **FR-022a**: Loading a skill and reading a file MUST accept an offset and a bounded length, both measured on the approved copy. An answer that does not reach the end of the text MUST say so and give the offset that continues it, so that an agent can read any approved text file completely in successive calls. A partial answer MUST never be presented as the whole file.
- **FR-023**: Search, load and file read MUST apply the caller's server reach (agent token, profile, pinned profile) exactly as it decides whether that server's tools exist for the caller. Out-of-scope, held, blocked and unusable skills MUST be indistinguishable from non-existent ones in every answer.
- **FR-023a**: Access to a skill depends on server reach only. A profile's tool policy (tier cap, per-tool allow, deny and classify rules) does not apply to skills, as it does not apply to prompts. The three skill tools are read-only built-ins, like the tool search: a profile cannot rule them out per server, and a read-only caller can use them.
- **FR-023b**: The skill tools MUST be offered on every surface that offers the tool search, and on the direct surface next to its existing built-in. They MUST NOT be offered on single-server endpoints, where upstream tool names are bare and the native surface (FR-029) is the way in. They are not callable from the code-execution sandbox in this version. On every surface that offers them, a built-in skill tool name MUST never be taken by, or resolve to, an upstream tool. (A single-server endpoint stands in for one upstream server and lists that server's tools under their own names; a tool the server itself calls `load_skill` is that server's tool there, where no built-in skill tool exists.)
- **FR-024**: Loaded content MUST pass through the existing response sanitisation, sensitive-data detection and size limits, and every search, load and read MUST be recorded in the activity log.
- **FR-025**: mcpproxy MUST NOT execute any skill file, MUST NOT grant any tool permission because a skill asks for it, and MUST NOT write skill content into any client's local skills folder.

**Control**

- **FR-026**: The feature MUST be controlled by one global switch, off by default, applied without a restart. With it off, no skill tool is offered and no skill request is sent upstream; stored approvals are kept.
- **FR-027**: Each server MUST have its own setting to include or exclude it from skills, defaulting to "follow the global switch".
- **FR-028**: Skills MUST be refreshed on connect and reconnect, on the periodic discovery pass, and on an explicit request; refreshes of one server MUST be applied in the order they were captured.

**Native surface (single-server endpoint only)**

- **FR-029**: On a single-server endpoint, with the feature on, mcpproxy MUST announce the skills extension and answer the extension's listing, single-entry and file-read requests for the approved skills of that server only, with the addresses the upstream uses, under the same gate (FR-008, FR-015) and the same scope rules as that endpoint's tools.
- **FR-029a**: Native reads MUST return the approved copy byte for byte (FR-008b), because the client verifies digests itself; mcpproxy therefore neither rewrites this content nor adds a header to it, and the client is responsible for showing its origin. Sensitive-data detection MUST still run on it and record findings without altering the bytes, the extension's size limits apply, non-text files are not served in this version, and every native listing, single-entry and file read MUST be recorded in the activity log with the caller and the outcome.
- **FR-030**: mcpproxy MUST NOT announce the extension on aggregated endpoints.

### Key Entities

- **Skill**: one skill of one upstream server. Has the origin server, its address on that server, name, description, the frontmatter as listed, and a list of files or the mark "dynamic".
- **Skill file**: one file of a skill. Has its address, digest, size, whether it is text, and mcpproxy's stored copy of its content.
- **Skill approval**: the decision about one skill. Has the state (pending, approved, changed, unusable), the blocked mark, what content was approved (the file set and frontmatter, with the stored approved copy), the server incarnation it belongs to, when and by whom, the reason when unusable, and the scanner evidence.
- **Skill catalog entry**: what an agent can search: id, name, description, origin server, file count, size. Exists only for approved skills.

---

## Non-Goals *(stated loudly)*

- **No aggregated native skills on `/mcp` or `/mcp/all`.** The native surface is the single-server endpoint only.
- **No directory listing, no resource subscriptions, no generic resource pass-through.** Only what skills need.
- **No change to the upstream protocol version or handshake.**
- **No execution.** mcpproxy never runs a skill's script and never grants a permission a skill asks for.
- **No syncing into a client's local skills folder.** That would present untrusted upstream text to the client as a trusted local skill.
- **No local skill directories.** mcpproxy proxies upstream skills; it does not become a skill host for files on disk.
- **No binary files to agents** in this version.

## Assumptions

Each is a decision taken to keep the work moving; the maintainer can overrule any of them.

- **Built-in tools come before the native surface.** Almost no client consumes the extension today; tools reach every client and stay inside the existing scope, activity and sanitisation path.
- **Skills are stricter than tools on change.** In the default `scan` mode a clean changed tool is approved automatically (Spec 119), protected by the structural gate of Spec 120. Free instruction text has no structure to gate on, so a changed skill always waits for a person outside `auto` mode. A first-seen skill follows the tool rule (clean and complete scan). If the maintainer prefers "every skill needs a person", or "changes follow the tool rule", only the table in FR-013 changes.
- **Scripts are text.** A script file is served as text like any other text file. The agent's host decides whether anything is run; mcpproxy does not.
- **Off by default in the first release.** Each agent-facing addition lengthens every client's tool list, which matters for clients that list tools eagerly.
- **The scanner needs to handle long text.** It was built for tool descriptions. Whole-file scanning of long `SKILL.md` files may need chunking; in `scan` mode a new skill is held until every text file is scanned completely (FR-013).
- **No new dependencies.** The libraries already in use can make the requests the extension needs.
- **Server edition**: the same behaviour, with per-user scoping coming from the existing scope rules. No server-only additions in this spec.

## Dependencies

- A fixture upstream that implements the extension (listing, single entry, file read, a changeable skill, a "dynamic" skill, a skill with wrong digests) is needed for tests and is part of this work.
- The public server used for the live check is a convenience for manual verification only; no automated test may depend on it.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With the feature on, a person can see every skill of a connected skills server, with its files and state, within 30 seconds of the server connecting (25 skills, typical sizes).
- **SC-002**: 100% of skill content delivered to an agent, on every surface, is derived from the approved copy of a skill approved for exactly that content (byte for byte on native file reads; redacted or shortened at most through the skill tools); a test that changes one byte upstream shows the agent never receiving the changed text before a new approval.
- **SC-003**: An agent in a client with no knowledge of the extension goes from a task phrase to the loaded text of the right skill in at most two calls (search, load), and the search answer for 25 skills is under 2,000 tokens.
- **SC-004**: A caller scoped to one server cannot learn, from any answer, list, count or error, that a skill of another server exists.
- **SC-005**: With the feature off, the agent-facing tool list and the traffic to upstream servers are identical to a build without the feature.
- **SC-006**: A reviewer can tell what changed in a changed skill (which files, which lines of text) in one view, without leaving mcpproxy.
- **SC-007**: A failure of a server's skill listing leaves that server's tools usable in 100% of cases.
- **SC-008**: A client that consumes the extension lists and reads exactly the approved skills of one server through that server's single-server endpoint, with no manual configuration beyond connecting to it.

## Delivery order

The stories are independently shippable, in this order:

1. Reading in and the per-server listing for people (User Story 1), read-only, feature switch (part of User Story 4).
2. The gate: approval states, content binding, scanning, review and approve (User Story 2).
3. Agent tools: search, load, read file, with scope and activity (User Story 3).
4. Web UI for skills on the server page and in the review queue (completes User Stories 1 and 2 on the Web).
5. Native surface on the single-server endpoint (User Story 5).

Nothing agent-facing ships before step 2 is complete.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #1895` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #1895`, `Closes #1895`, `Resolves #1895` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(skills): read upstream skills into a per-server catalog

Related #1895

Detect the skills extension on connected upstream servers, list skills,
fetch and verify their files, and show them per server.

## Changes
- Skill listing and file verification for upstream servers
- Per-server skill list on the CLI and REST API

## Testing
- Fixture upstream with valid, changed, dynamic and wrong-digest skills
- Feature off: no skill request reaches any upstream
```

# Feature Specification: User-defined tool labels

**Feature Branch**: `136-tool-labels`
**Created**: 2026-11-25
**Status**: Draft (spec only, no implementation in this PR)
**Input**: Issue #2045 "Tool categories: group, filter and bulk-review tools" (felipecrs). Maintainer decision 2026-11-25: accept now only the local part, user-defined labels; defer server-declared groups and forwarding to clients until the MCP grouping work (SEP-1300, superseded by the Primitive Groups working group, modelcontextprotocol#1567) settles.

## Maintainer decisions still open

Each has a recommended default that the spec is written to; a different answer changes only the named requirements.

1. **`only_labels` in v1** (FR-030–FR-032). Recommended: ship it in PR-5. It is the issue's "only the issue tools" case, which Spec 108 cannot express today (`tools.allow` bypasses the tier cap, it is not an allowlist). Alternative: allow and deny references only; `only_labels` later.
2. **Regex rules** (FR-002, FR-011). Recommended: allow RE2 regex with the bounds of FR-010/FR-011 beside the Spec 108 glob. Spec 127 refused regex for argument rules because there a pattern decides a call; here a pattern only selects, and profile effect needs a confirmed set. Alternative: glob only in v1.
3. **Labels for scoped callers** (FR-060–FR-062). Recommended: administrator only in v1, uniform 403 for scoped callers. Alternative: let a scoped caller filter by label over its own reach, which needs label names to be treated as possibly revealing and a non-disclosing unknown-label answer.

## Clarifications

### Session 2026-11-25 (self-run clarify pass; assumptions documented, not asked)

- Q: Match on the bare tool name or on `server:tool`? → A: bare name with an optional exact `server` field (FR-002); identities in `include`/`exclude` and confirmed sets are canonical `server:tool`.
- Q: Do labels cover held (pending, changed, blocked) tools? → A: yes, membership is over present tools, and review rows get labels from the same matcher (Definitions), so review can filter held tools and retained records; profile effect is still limited by quarantine and FR-031.
- Q: Several labels in one filter: AND or OR? → A: OR within labels, AND with every other filter (FR-040, FR-041).
- Q: Where are labels stored? → A: config file `tool_labels` (FR-020); confirmed sets in the profile; membership derived in memory.
- Q: What does "fail-closed" mean for each reference kind? → A: allow/only admit M ∩ C; deny denies M live, and a label edit that shrinks a deny needs an acknowledgement token (FR-031, FR-031b). Adding to C goes only through a token-bound confirmation service (FR-033a). A confirmed identity that returns is admitted again, like a `tools.allow` exact entry. Drift is shown, never applied (FR-033).
- Q: Delete a referenced label? → A: refused, no force (FR-026); rename rewrites references (FR-027).
- Q: Invalid label in a hand-edited config? → A: dropped at load with a doctor warning; references to it are dangling and fail closed (FR-014, FR-034).
- Q: Rules on tier or approval state? → A: no (FR-006).
- Q: Audit of label writes? → A: admin-only activity type `tool_label_change`; Spec 133 action list if landed (FR-024).
- Q: Server edition? → A: instance-wide, `admin_user` only (FR-070).

## Why

A server such as the GitHub MCP server lists dozens of tools. Today the only grouping axis in mcpproxy is the server. The review screen can filter by tier, approval state and decision and select "all filtered" (`frontend/src/components/ReviewScreen.vue:58-78`), the Tools page filters by search, server, status, tier and approval (`frontend/src/views/Tools.vue:114-184`), and profiles decide per `server:tool` glob (`internal/config/profiles.go:59-70`, `internal/profile/policy.go:341-367`). There is no way to say "the issue tools" once and reuse it: to review the issue tools in one pass, to look at them on the Tools page, to deny them in a profile, or to list them from the CLI.

A **label** is a name plus a rule the operator wrote. Every tool that carries a label carries it because a rule the operator wrote matches its identity or because the operator picked it by hand. There is no heuristic, scan-based or model-based classification.

## Position (binding for every requirement below)

Tool names are chosen by the upstream server. A label whose rule matches on a name is therefore a selection over server-controlled input. A label can **filter** and **select**. It can never by itself:

- approve, unquarantine, enable or admit a tool;
- skip, pre-answer or shorten any confirmation (blind approval, force approval on dangerous findings, re-quarantine, any destructive confirmation);
- hide, reorder away or summarise away the tier, approval state, scan verdict, findings or counts that a surface shows today;
- widen what a profile admits when the set of tools matching it grows (a server adds or renames tools, or the label is edited): a tool reaches a profile through a label only if an administrator confirmed that exact identity for that profile (FR-031), and a label edit that shrinks a deny needs an explicit acknowledgement (FR-031b).

New and changed tools still pass tool-level quarantine (Spec 032, `internal/runtime/tool_quarantine.go`) and the structural change gate (Spec 120) exactly as today, whatever labels they carry.

## Scope Boundary

In scope:

- The label model, its rules, validation, limits and storage (config file, hot reload, export/import).
- One evaluator that computes label membership with one matcher, over present tools and review rows.
- Uses, in priority order: (1) a label filter on the Web UI Tools page and the review screen, combinable with today's filters; (2) bulk selection scoped to a label in review, through today's "Allow all filtered / Block all filtered" mechanism only; (3) label references in profile tool rules, fail-closed; (4) `mcpproxy tools list --label` and the REST filter behind it, plus label management in REST, CLI and Web UI.

Out of scope (see [Room left for later](#room-left-for-later)):

- Server-declared groups and tags as a second source.
- Forwarding labels or groups to MCP clients.
- Labels on servers, prompts, resources or skills.
- Rules on description text, schemas, annotations, tier or approval state (FR-006 explains why).
- Per-user labels in the server edition.

## Definitions

- **Tool identity**: the canonical `server:tool` registration identity (Spec 105), the same identity profile rules match (Spec 108 FR-004). Never a flattened display name (`server__tool`) or a display title.
- **Present tool**: a tool in the authoritative inventory last committed for a configured server (Spec 117), whatever its approval state (approved, pending, changed, blocked) or enabled state. Profile policy, drift and the label management counts use present tools.
- **Review row**: a row of the review payload `GET /api/v1/servers/{id}/review`, which may also include retained records of tools that are absent now and captures of quarantined servers (`internal/runtime/review.go`). Review filtering uses review rows.
- **Matcher**: the one pure function (label definition, identity) → bool of FR-004. Both universes above use it, so a tool's labels are the same wherever it appears.
- **Member**: a present tool for which the matcher is true.
- **Label reference**: a profile rule that names a label (FR-030).
- **Confirmed set**: the list of tool identities an administrator accepted for one label reference, stored in the profile beside the reference (FR-031).
- **Drift**: for one label reference, the difference between the current members and the confirmed set.
- **Administrator**: a positively authenticated administrator: API key, socket, or server-edition `admin_user`. The credentialless context (`auth.AnonymousContext()`, which is admin-typed for MCP compatibility, `internal/auth/context.go`) and an absent auth context are NOT administrators for any label data, even where `IsAdmin()` is true. **Scoped caller**: `auth.IsScopedCaller()` (agent tokens, client credentials, server-edition user sessions).

Terminology note: Spec 134 (`specs/134-webui-simplify`, FR-056) uses "labels" for the exception markers on a tool row ("Writes", "Destructive", "New, needs review" ...). Those are computed state, not this feature. The Web UI MUST render user labels with a different component and appearance from Spec 134's exception markers, and the filter is named "Label" (FR-041).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Filter tools by a label I wrote (Priority: P1)

An operator runs the GitHub MCP server with 90 tools. They create a label `issues` with the rule "on server `github`, tool name matches `issue`" and a label `prs` with "tool name matches `pull_request|^pr_`". On the Tools page they pick "Label: issues" and see only those tools, with tier, approval and every other column as today. They combine it with "Tier: write" to see the issue tools that write.

**Why this priority**: Every later use (review, profiles, CLI) needs the model, the evaluator and a way to see membership. Filtering is the smallest useful slice and has no access effect.

**Independent Test**: Create two labels through REST, load `GET /api/v1/tools?label=issues` and the Tools page with `?label=issues`, and assert the rows are exactly the members, with tier and approval fields unchanged.

**Acceptance Scenarios**:

1. **Given** label `issues` (server `github`, regex `issue`), **When** the Tools page is opened with `?label=issues`, **Then** it lists exactly the `github` tools whose name contains `issue`, each with its present tier, approval, status and actions.
2. **Given** `?label=issues&tier=write`, **When** the page loads, **Then** it lists members of `issues` whose tier is `write` (filters combine with AND).
3. **Given** `?label=issues&label=prs`, **When** the page loads, **Then** it lists tools that are members of either label (several labels combine with OR).
4. **Given** a label that matches nothing, **When** it is selected, **Then** the page shows the existing empty state with "Clear filters", and the label editor shows "0 tools match".
5. **Given** the label editor, **When** the operator types a rule, **Then** a live preview lists the tools that would match (server, name, tier, approval state) before saving, and an invalid pattern shows the validator's message without saving.
6. **Given** an operator who removes a tool from a label by hand (exclude list), **When** the page reloads, **Then** that tool no longer appears under the label even though the rule still matches it.

---

### User Story 2 - Review a big server in slices (Priority: P1)

A newly added GitHub server is quarantined with 90 tools. The operator opens its review screen, picks "Label: issues", reads those tools, presses "Allow all filtered (14)", then picks "Label: repo-admin" and presses "Block all filtered (6)". The approve button still counts every tool of the server ("Approve 84 of 90 tools"), the tier counts still cover all 90, and each existing confirmation appears exactly as today.

**Why this priority**: This is the issue's headline use. It reuses the existing bulk selection, so it adds no approval path.

**Independent Test**: With a fixture server of N tools, apply a label filter, use "Allow all filtered", and assert that only the client-side selection changed, that the approve request is the same request today's code builds for that selection (full block list over all tools, review-bound `expected_hashes`), and that tier counts and totals are computed over all N tools.

**Acceptance Scenarios**:

1. **Given** a quarantined server and a label filter, **When** the operator presses "Allow all filtered (k)", **Then** exactly the k filtered tools are marked allowed in the selection, nothing is sent to the server, and the other tools keep their choice.
2. **Given** that selection, **When** the operator presses the approve button, **Then** the request carries the block list computed over the full tool list and the review's expected hashes, as today (`ReviewScreen.vue:320-353`).
3. **Given** a label filter that hides tools an approving control will act on, **When** the screen renders, **Then** that control states how many hidden tools it will approve and block, "Show them" reveals them with their evidence, and the tier counts and "filtered from total" line are unchanged.
4. **Given** a label filter is active, **When** the server's tools change and the review is reloaded, **Then** membership is recomputed from the new review and the existing stale-review handling (409 `review_out_of_date`, `server_replaced`) applies unchanged.
5. **Given** dangerous scan findings, **When** the operator approves after selecting by label, **Then** the force-approval confirmation appears as today; no label setting suppresses it.

---

### User Story 3 - Use a label in a profile without it silently widening (Priority: P2)

The operator's `work-readonly` profile has `max_tier: read`. They want the agent to also use the GitHub issue-writing tools, and never the repo-admin tools. They add `allow_labels: issues` and `deny_labels: repo-admin`. The editor shows the 9 tools the allow reference would admit; the operator confirms them. A week later GitHub ships `transfer_issue`. It matches `issues`, but the profile does not admit it through the label: it goes through tool-level quarantine first, and once approved it still waits for the operator to confirm it into the profile. The profile editor, the attention list and `effective-tools` say so.

**Why this priority**: Valuable, but the riskiest use: it touches the profile decision on every dispatch path (Spec 108 FR-010).

**Independent Test**: Fixture profile with an allow reference and a confirmed set; add a matching tool to the fixture upstream; with the tool above the profile's tier cap and no other admission path, assert every dispatch door refuses it until the confirmed set is updated through the confirm call, and that the drift listing and attention item appear.

**Acceptance Scenarios**:

1. **Given** an allow reference to `issues` with confirmed set C, **When** the server adds a tool T that matches `issues`, **Then** T is not admitted by that reference (it is decided by the remaining steps as if the reference were absent), `effective-tools` lists T as "matching, not confirmed" beside its actual reason (for a tool above the cap, `above_tier_cap`), and an administrator attention item names the profile, the label and the count.
2. **Given** a deny reference to `repo-admin`, **When** the server adds a tool that matches `repo-admin`, **Then** it is denied immediately on every door.
3. **Given** a deny reference to label L and an administrator who edits L so that tool D no longer matches, **When** the edit is saved, **Then** the core refuses it with the list of tools that would stop being denied (D, its profile, tier and approval) and accepts it only when resubmitted with the acknowledgement for exactly that change.
4. **Given** a profile that references a label that no longer exists (hand-edited config), **When** the config loads, **Then** a dangling allow reference admits nothing, a dangling deny reference makes the profile admit no tool (reason `label_unavailable`), and a doctor warning plus an attention item say which profile and which label.
5. **Given** a label referenced by any profile, **When** an administrator tries to delete it, **Then** the delete is refused with the profile names; a rename rewrites every reference in the same config write.

---

### User Story 4 - List labelled tools from the CLI (Priority: P3)

`mcpproxy tools list --label issues --tier write -o json` prints the same rows the Web UI shows for that filter. `mcpproxy labels list|show|test|create|update|delete|rename|export|import` manages labels.

**Why this priority**: Useful for scripts and agents run by the administrator; the Web UI covers the main flows.

**Independent Test**: Run the CLI against a running core with fixture labels and compare its JSON rows to `GET /api/v1/tools?label=…`.

**Acceptance Scenarios**:

1. **Given** labels exist, **When** `mcpproxy tools list --label issues` runs against a daemon, **Then** it lists exactly the members, using the REST filter.
2. **Given** `--label` repeated, **When** it runs, **Then** members of any listed label are shown, combined by AND with `--server`, `--tier`, `--status`, `--approval`.
3. **Given** an unknown label, **When** `--label nope` runs as an administrator, **Then** it exits 1 with `unknown label "nope"`.
4. **Given** `mcpproxy labels test --server github --regex 'issue'`, **When** it runs, **Then** it prints the would-be members without saving.

---

### Edge Cases

- **Server renames a tool**: the old identity disappears (it is no longer a member and admits nothing); the new identity is a new tool, held by tool-level quarantine as today, and an unconfirmed member of any allow or only reference. A confirmed identity that no longer exists is shown as "no longer present" and stays in the confirmed set; if that exact identity returns and matches again it is admitted again, exactly as a `tools.allow` exact entry would be (the operator confirmed that identity).
- **Server removed and re-added with the same name**: membership is recomputed from the new inventory; confirmed sets are identity lists and still apply, which is no wider than today's `tools.allow` exact entries for the same identities. New and changed tools are held by quarantine first.
- **Tool names that are not valid identifiers or are very long**: names longer than 256 bytes are never matched by a pattern rule (they can still be picked by hand); this bounds matching cost.
- **Two labels with overlapping membership**: allowed; a tool may carry many labels.
- **A label filter in the URL that the core does not have**: the chip is shown as "unknown label" and removable; it contributes no tools to the OR (FR-041). A URL with only unknown labels shows the empty state, never "all tools".
- **Old core without labels**: the Web UI hides the label filter when the core does not report the feature (`GET /api/v1/status` `features.tool_labels`).
- **Hand-edited config with an invalid label**: at load the invalid label is dropped with a warning (lenient at load), and every profile reference to it is treated as dangling (FR-034). Writes through REST are strict and refuse it.
- **Very large confirmed sets**: capped (FR-012); a reference that would exceed the cap is refused at write with a message suggesting a narrower label.
- **Approve all / Approve update / Approve server**: these controls keep their present scope and effects whatever label filter is set; FR-052 makes visible what each will do to tools the filter hides.

## Requirements *(mandatory)*

### Functional Requirements

**Model and rules**

- **FR-001**: A label MUST have `name` (slug `^[a-z0-9][a-z0-9_-]{0,39}$`, unique, case-sensitive), optional `description` (≤ 200 characters, display only), `rules` (0–8 rules), `include` (explicit tool identities) and `exclude` (explicit tool identities). A label with no rules and no `include` is valid and has no members.
- **FR-002**: A rule MUST have an optional `server` (an exact configured server name; empty means any server) and exactly one of `glob` or `regex`:
  - `glob`: matched against the bare tool name with the Spec 108 grammar (`*` the only wildcard, anchored, case-sensitive, everything else literal; same compiler as `internal/profile/glob.go`);
  - `regex`: Go RE2 syntax (`regexp` package, never `regexp2`), unanchored unless the pattern anchors itself, case-sensitive unless it uses `(?i)`, matched against the bare tool name.
- **FR-003**: `include` and `exclude` entries MUST be exact canonical identities `server:tool` (no wildcard). An entry naming a tool that is not currently present is kept and reported as "not present"; it becomes a member when that identity appears.
- **FR-004**: Membership MUST be: (some rule matches, or the identity is in `include`) and the identity is not in `exclude`. A rule matches when its `server` is empty or equal to the tool's server, and its pattern matches the tool name.
- **FR-005**: Membership MUST depend only on the label definition and the tool identity. It MUST NOT depend on description, schema, annotations, tier, approval state, enabled state, scan verdict or any model output.
- **FR-006** (no tier or state rules, decision): rules MUST NOT accept tier, annotation or approval state as criteria. Reasons: (a) tier comes from server-declared annotations and changes when the server changes them, and approval state changes when an administrator approves; a label built on either would turn an approval or an upstream annotation change into a change of what a profile references; (b) both are already filters on every surface that shows labels, and combining "label AND tier" at filter time gives the same view without coupling. This is pinned by a validator test that rejects any other rule key.
- **FR-007**: There MUST be one evaluator for membership, used by REST, CLI (through REST), the Web UI (through REST payloads; the Web UI never recompiles rules except for the editor's unsaved preview, which also goes through REST `preview`), the review composer and the profile decision.

**Limits, validation, regex bounds**

- **FR-010**: Limits MUST be: at most 100 labels; at most 8 rules per label; a `glob` or `regex` at most 256 bytes; at most 1000 `include` and 1000 `exclude` entries per label; tool names longer than 256 bytes never match a pattern rule.
- **FR-011**: A `regex` MUST be parsed with `regexp/syntax` and compiled with `regexp`; it MUST be refused when it does not compile, when it exceeds the length limit, or when any repetition count exceeds 100 (`{n,m}` with n or m > 100). RE2 guarantees time linear in the input; with the name cap of FR-010 one match is bounded by a constant.
- **FR-012**: A profile label reference's confirmed set MUST hold at most 1000 identities.
- **FR-013**: Validation MUST be one function used by config load, REST writes, CLI (through REST), import and the editors' live validation (through REST), returning identical messages with a `field` path (`tool_labels[i].rules[j].regex` …). Fatal at write: invalid name, duplicate name, unknown rule key, both or neither of `glob`/`regex`, invalid or over-limit pattern, malformed identity in `include`/`exclude`, any limit exceeded. Warning (saved): a rule `server` or an `include`/`exclude` entry naming a server that is not configured.
- **FR-013a** (one commit boundary): label validation (FR-013), reference integrity (no write may leave a profile reference to a label that the write removes or makes invalid), the confirmation rule (FR-033a), the deny-narrowing acknowledgement (FR-031b), the binding guard (FR-028) and the activity row (FR-024) MUST be enforced at the shared config commit boundary, so `PATCH /config`, `POST /config/apply`, the label routes, profile routes, import and the MCP `profiles` tool all get the same refusals. API writes MUST inspect rule keys before typed decoding so an unknown key is refused rather than silently dropped.
- **FR-014**: At config load the validator MUST be lenient: an invalid label, including an entry whose JSON types are wrong in an otherwise valid file, is dropped and logged with its message, and a doctor check `tool_labels` lists dropped labels. Booting never fails because of a label.
- **FR-015**: Membership MUST be computed when labels change and, per server, after that server's tool inventory is committed as the authoritative inventory (Spec 117: an older, retired or reconnected capture, or a lenient empty observation that keeps the previous inventory, changes no membership), including at startup and on server removal, never per request. Membership MUST be published in the same snapshot generation as the config and inventory it was computed from, so the profile decision never pairs one generation's policy with another's membership. Requests read the cached membership. With 100 labels × 8 rules and 5,000 present tools, a full recompute MUST finish within 2 seconds on the CI runner, and a label filter MUST add no pattern evaluation to a request.

**Storage, reload, export/import**

- **FR-020**: Labels MUST live in the config file as a top-level array `tool_labels` (beside `profiles`), not in BBolt, so they are versioned, hand-editable and travel with the config. Confirmed sets live in the profile (FR-031), also in the config file. The cached membership is derived and never persisted. No label state is kept in BBolt: membership is a pure function of the label definition and the tool identity (FR-005), so it is recomputed identically after a restart.
- **FR-021**: A new config field MUST be wired at every config wiring point: `Validate`/`ValidateDetailed`, `PersistableConfig`/`PersistableConfigWithEdits` (`internal/config/process_overrides.go:473,500`), hot reload (`DetectConfigChanges`, `internal/runtime/config_hotreload.go:78`, a clause like the Profiles clause at `:218-235`), docs, swagger, contracts and generated TS types (`cmd/generate-types/main.go`).
- **FR-022**: A hot-reloaded change to `tool_labels` MUST recompute membership and emit an SSE event `tool_labels.changed` (administrators only; dropped for scoped subscribers).
- **FR-023**: Export MUST produce a JSON document `{ "tool_labels": [...] }` (REST `GET /api/v1/tool-labels/export`, CLI `mcpproxy labels export`). Import (`POST /api/v1/tool-labels/import`, `mcpproxy labels import <file> [--replace]`) MUST validate every label with FR-013 and write all or nothing; without `--replace` a name conflict refuses the whole import with the conflicting names; with `--replace` same-named labels are replaced. Import never touches profiles or confirmed sets.
- **FR-024**: Every label write (create, update, delete, rename, import, and the reload record of FR-031b) MUST be recorded synchronously as an administrator-only activity row of a new type `tool_label_change` (added to `storage.IsAdminOnlyActivityType`) carrying actor fields (#1842: `ContextWithActor`/`RequestActor`), the operation and the label names. If Spec 133 (`admin_action` audit lines) has landed, label writes are added to its action list in the same PR.

**Management surfaces**

- **FR-025**: REST MUST provide, administrator only (`requireServerOp(auth.ServerOpConfigWrite, …)` for writes, which agent tokens are denied, `internal/auth/server_ops.go:60-85`): `GET /api/v1/tool-labels` (definitions plus member count and "not present" entries), `GET /api/v1/tool-labels/{name}` (definition plus members), `POST /api/v1/tool-labels`, `PUT /api/v1/tool-labels/{name}`, `DELETE /api/v1/tool-labels/{name}`, `POST /api/v1/tool-labels/{name}/rename`, `POST /api/v1/tool-labels/preview` (members of an unsaved definition), export and import (FR-023).
- **FR-026**: `DELETE` of a label that any profile references MUST be refused with `409 {error: "label_in_use", profiles: [...]}`. There is no force option: removing a deny reference widens a profile, so the administrator edits the profiles first.
- **FR-027**: Rename MUST rewrite every profile reference in the same config write and keep each reference's confirmed set.
- **FR-028**: Every label write MUST run the whole-candidate binding guard of Spec 108 FR-008a, as profile writes do (`MutateConfig`), evaluated with the candidate's membership, because a label edit can change a referenced profile's reach (a deny reference growing can make `anonymous_profile` wider than a binding). The standing runtime guard MUST evaluate each published snapshot (FR-015), so an inventory-driven membership change cannot make a bound profile bypassable by omitting the bound credential; a test covers a newly denied tool reached through `anonymous_profile`. Both guards use the resolved label policy of one snapshot and keep the argument-rule containment of Spec 127 FR-016; its matrix gains a label-deny variant (bound profile denies by label, switch target admits with an argument restriction, anonymous profile admits unrestricted).
- **FR-029**: CLI MUST provide `mcpproxy labels list|show|test|create|update|delete|rename|export|import` through REST, with `-o json|yaml` and `--help-json` like the other management commands.

**Uses: profile rules**

- **FR-030**: `ProfileToolRules` (`internal/config/profiles.go:65`) MUST gain `allow_labels` and `only_labels`, each a list of `{label, confirmed: [identity, …]}`, and `deny_labels`, a list of `{label}`. They live under `tools`, so the edition-neutral `ProfileConfig` field-set test (Spec 108 FR-009) keeps passing; setting any of them makes the profile non-legacy (`IsLegacy`).
- **FR-031** (fail-closed admission): For each reference, let M be the label's current members restricted to the profile's `servers`, and C the reference's confirmed set.
  - `allow_labels`: admits exactly M ∩ C at Spec 108 FR-010 step 3, with the same meaning as a `tools.allow` exact entry: it bypasses only the tier cap and the unannotated policy; it never bypasses quarantine, approval, enabled state, token permissions, `read_only_mode` (Spec 108 FR-006) or the argument rules that Spec 127 applies right after the tool-level predicate on every call path. A tool in M \ C gets no effect from the reference.
  - `only_labels`: when at least one is set, a tool on an in-scope server that is not in M ∩ C of some `only_labels` reference is excluded at step 2c; no allow rule or allow reference can readmit it.
  - `deny_labels`: denies M at step 2b, live. A tool that joins the label (new upstream tool or label edit) is denied at once.
- **FR-031a** (why deny needs no confirmed set): the matcher depends only on (label definition, identity) (FR-005). A tool stops being denied by a deny reference only when (a) it disappears upstream (nothing to call; if the same identity returns it matches again), or (b) an administrator changes what the profile denies: edits, imports over or renames the rules of a deny-referenced label, removes a deny reference, or removes the last `only_labels` reference. Case (b) is covered by FR-031b; deleting a referenced label is refused (FR-026).
- **FR-031b** (widening acknowledgement): a write MUST be refused with `409 {error: "widening_unacknowledged", changes: [...], known_affected: [{profile, identity, tier, approval}…], ack}`, writing nothing, unless it carries a valid acknowledgement for exactly that change, when it: (1) changes the `rules`, `include` or `exclude` of a label that any deny reference names, whether or not any present tool is affected (the response flags "rules changed; tools that are absent now or added later may no longer be denied"); (2) removes a `deny_labels` reference; (3) removes the last `only_labels` reference of a profile; (4) leaves a profile without the dangling-reference block of FR-034 that it had before the write, however the write clears it (creating or renaming a label, removing one of several dangling references, editing the profile). This applies on every write door (label routes, import, profile routes, `PATCH /config`, `POST /config/apply`, MCP `profiles`). `POST /api/v1/tool-labels/preview` and the profile preview route return the same `changes`, `known_affected` and `ack`. At commit the core recomputes `changes` and `known_affected` against the same published snapshot (FR-015) it commits the candidate against; if either differs from what the `ack` was issued for (for example a matching tool was discovered in between), the write is refused and a fresh preview is needed. A hand edit of the config file is operator policy: at reload the core records the same `changes` and `known_affected` in an administrator-only `tool_label_change` row with operation `reload` (never in `config_change`, whose scoped visibility is per server).
- **FR-031c** (server-issued tokens): the `ack` of FR-031b and the `token` of FR-033a MUST be issued by the core: an HMAC over the bound content (operation, profile, reference kind, label, stored and candidate definition hashes, current confirmed set, the exact `changes`, `known_affected` and `add` lists) and an expiry 15 minutes after issue, keyed by a random secret generated once and kept in BBolt. A token a client builds itself, an expired token, or one whose bound content differs from what the core recomputes under the config write lock is refused (`409 confirmation_stale` / `widening_unacknowledged`). The secret survives restarts, so a token issued before a restart stays valid until it expires.
- **FR-032**: The decision chain (Spec 108 FR-010, `CompiledPolicy.Decide`) MUST become, first failure wins: (1) server ∉ servers → `server_not_in_profile`; (2a) the profile has a dangling deny or only reference (FR-034) → `label_unavailable`; (2b) matches `tools.deny` → `denied_by_rule`, else in a deny reference's M → `denied_by_label`; (2c) at least one `only_labels` reference and not in any reference's M ∩ C → `label_member_unconfirmed` when in M \ C of some `only_labels` reference, else `outside_label_limit`; (3) matches `tools.allow` or in an allow reference's M ∩ C → admitted at the tool level; (4)–(6) unchanged; then Spec 127 argument rules, unchanged. It MUST remain the one predicate used by enforcement on every dispatch door (including nested `code_execution` calls and REST replay), `effective-tools`, "view as", "Try it" (with sample arguments) and the access explainer; the explainer names the label step and the label for administrators.
- **FR-032a** (policy invalidation): the compiled profile policy fingerprint (Spec 108 FR-027) MUST cover the resolved label policy: each allow and only reference's M ∩ C, each deny reference's M, dangling state, and whether the `only_labels` gate is active (an active gate with an empty union differs from no gate). The label component of the fingerprint stamped on cache pages (`internal/server/cache_authz.go`) and the decision to send a label-driven `tools/list_changed` (`profile_change_notify.go`) MUST be computed over the caller's authorized projection (the label policy restricted to tools the caller can reach). This is new work: today's notifier and fingerprint are per profile. The label component MUST be checked on every redemption, including by a credentialless reader of an anonymous-produced page (the cache gate in `internal/cache/authorization.go` admits those before comparing fingerprints today), using the reader's effective profile resolved by the guard-aware Spec 108 v3 resolver (not the legacy resolver at `internal/server/cache_authz.go:27`); a reader the binding guard denies is refused before any caller-kind fast path. Tests refuse a previously valid page after an in-scope label deny, and an anonymous page produced under a label-free `anonymous_profile` after a label deny narrows only a bound profile and trips the guard. So a label edit that alters only tools on server `b` produces no notification and no cache refusal attributable to labels for a caller whose reach is server `a` only; a test covers it. The existing fleet-wide `ToolTierGeneration` stamp, which already advances on any discovery publish, is unchanged by this feature and out of its scope; labels add no new cross-server signal beyond it.
- **FR-033** (drift is diagnostic): for allow and only references, drift is M \ C ("matching, not confirmed") and C \ M ("confirmed, not matching now"). Drift MUST be reported separately from the enforcement reason, which is always the first failure of FR-032. For administrators, `effective-tools`, the explainer, the profile editor and `mcpproxy profile show` list both drift sets per reference with server, name, tier and approval state of each tool. Deny references have no drift.
- **FR-033a** (confirmation bound to what was shown): every online change that adds identities to a confirmed set, including its first population, MUST go through one confirmation service: `POST /api/v1/profiles/{name}/label-references/preview` with `{kind, label}` returns `{add: M \ C, token}`, and `POST /api/v1/profiles/{name}/label-references/confirm` with `{kind, label, add, token}` writes C := C ∪ add. The token is issued by the core (FR-031c) and bound to the profile, reference kind, label, label definition hash, the current C and the sorted `add` list; the core revalidates it under the config write lock and refuses with `409 {error: "confirmation_stale"}`, writing nothing, when it is not one the core issued, has expired, or no longer matches, or when `add` is not a subset of current M \ C. CLI `mcpproxy profile confirm-labels`, the MCP `profiles` operation `confirm_labels` and the editors' "Confirm" action use this service and send exactly what they displayed. Every other API write (profile PUT, `PATCH /config`, `POST /config/apply`, MCP `update`) MUST refuse a request that adds identities to any `confirmed` (`409 {error: "use_label_confirmation"}`); removing identities from a confirmed set, or removing an allow reference, is allowed there (it narrows); removing a deny reference or the last only reference needs the acknowledgement of FR-031b. An omitted `confirmed` in a PUT keeps the stored one. A confirmed set written by hand in the config file is operator policy, validated by FR-035 and still admits only M ∩ C. A confirmation is recorded as `profile_change` with the added identities. No attention item or link performs a confirmation.
- **FR-034**: A reference to a label that does not exist or was dropped at load (FR-014) is dangling. A dangling allow reference admits nothing; a dangling deny or only reference excludes every tool of the profile at step 2a (fail closed: the operator meant to deny or to restrict). API writes refuse a reference to an unknown label (strict at write, FR-013a).
- **FR-034a** (attention items, administrator only): `profile_label_unconfirmed`, id `profile_label_unconfirmed:<profile>:<kind>:<label>`, raised while M \ C of that allow or only reference is non-empty, summary "Profile <p>: N tools match label <l> and wait for confirmation", cleared when M \ C is empty or the reference is removed. `profile_label_missing`, id `profile_label_missing:<profile>:<label>`, raised while any reference of the profile names a missing label, summary "Profile <p> refers to missing label <l>; its tools are blocked" (or "… is ignored" when only allow references name it). Both rank immediately after `profile_missing` in the Spec 109 attention order, have one fix that opens the profile editor at the first such reference (order deny, only, allow), and are recomputed on every membership publish, profile write and label write. C \ M produces no item.
- **FR-034b** (doctor, administrator only): the doctor check `tool_labels` (dropped labels, dangling references, validation details) MUST be included only for administrators (Definitions: positively authenticated) in the shared diagnostics service (`internal/management/diagnostics.go`), so REST `/doctor`, `/diagnostics` and MCP `doctor` omit the whole section, including counts, for scoped and credentialless callers. Tests assert its absence for a pinned token, a server-edition tenant user and the credentialless MCP path.
- **FR-035**: Validation (Spec 108 FR-007's one validator) MUST refuse a reference whose `confirmed` holds a malformed identity or exceeds FR-012, and MUST warn (saved, entry ignored) on a confirmed identity whose server is outside the profile's `servers`.
- **FR-036**: Every profile management surface that carries `tools.allow/deny` today MUST carry the three new fields: config file, REST, CLI (`mcpproxy profile create|update --allow-label|--deny-label|--only-label`, `profile confirm-labels <profile> [<label>]`), the MCP `profiles` admin tool, the Web UI profile editor and the macOS profile editor. The macOS editor (`native/macos/MCPProxy/MCPProxy/API/ProfilesModels.swift`, `ProfileEditorView.swift`) MUST at minimum round-trip the fields unchanged on save, pinned by a test that fails when a save drops them.
- **FR-037** (rollout gate): following Spec 108 FR-009a, the one validator MUST refuse the three new fields with `profiles[%d]: tools.%s is not supported by this build` until the PR that makes every dispatch door enforce them, driven by one constant flipped in that PR.
- **FR-037a** (downgrade): an older core ignores the new fields, so `deny_labels` and `only_labels` restrictions are lost on downgrade (the same class of loss as Spec 127 FR-015). Docs MUST say so. `mcpproxy profile flatten-labels <profile> [--dry-run]` MUST rewrite allow references as exact `tools.allow` entries of M ∩ C and deny references as exact `tools.deny` entries of their current M, MUST refuse any identity that the Spec 108 rule grammar cannot express exactly (`*` or characters outside `internal/config/profiles.go:201-216`), MUST refuse a profile with `only_labels` or with any dangling reference (FR-034; flattening it would lift a deny-all), and for deny references MUST require `--accept-static-deny` after printing that absent and future matches will no longer be denied. For a profile it cannot flatten, the documented procedure is to remove every route to it before downgrading: repin or revoke every client binding and token that resolves to it, remove it from every `switchable_to` list, clear or change `anonymous_profile` if it names it, and then delete the profile or replace it in place with a policy the older build enforces at least as narrowly, because an unpinned caller can still select a configured profile directly (`/mcp/p/<slug>`, `set_profile`, Spec 108 FR-022); never unbind to a default or anonymous profile that may be wider. `flatten-labels --dry-run` lists those routes, including direct selectability; a test uses the old slug with an unpinned token after the documented migration.

**Uses: filter**

- **FR-040**: Labels on a row MUST come from the matcher over that row's identity: for tools endpoints over present tools, for the review payload over review rows (Definitions). Tool rows returned to an administrator by `GET /api/v1/tools`, `GET /api/v1/servers/{id}/tools` and the review payload `GET /api/v1/servers/{id}/review` MUST carry `labels: [name, …]` (sorted). `GET /api/v1/tools` and `GET /api/v1/servers/{id}/tools` MUST accept `label` (repeatable; OR within, AND with the other filters). The result is the intersection of label membership with the rows that endpoint returns today: a label filter never adds rows an endpoint omits (for example `GET /api/v1/tools` keeps omitting quarantined servers, `internal/httpapi/server.go:4380-4388`; their tools are reached through the review payload). For REST and CLI an unknown label is strict: `400 unknown label "<name>"`, even when other values are known. The feature is advertised as `features.tool_labels` in `GET /api/v1/status`.
- **FR-041**: The Web UI Tools page MUST offer a "Label" filter (multi-select, `?label=` repeatable). Spec 109's URL filter contract and `useScopeQuery` (which today read only the first value of a parameter) MUST be extended for this one parameter to keep every value, in order, and to write them back as repeated parameters; the control is hidden when `features.tool_labels` is absent; each value is a removable chip. The Web UI filters locally on the `labels` field of the rows it already loaded (it does not send `label` to REST) and decides whether a value exists from the label catalog `GET /api/v1/tool-labels`, never from the rows, so an unknown value is a chip marked "unknown label" that contributes no rows to the OR and a selection of only unknown values shows zero rows, never all rows. Tests cover mixed, all-unknown, unsupported-core, valid zero-member and members-only-on-quarantined-servers URLs. With Spec 134 merged it sits under "More filters" and an active label shows as a removable chip; without Spec 134 it sits in the present filter row. In the flat list a tool's labels are shown in its row; in Spec 134's grouped mode they are shown in the row's Details only, so FR-056's "exceptions only" row rule holds. Every existing Tools filter, sort, batch action and statistic is unchanged (Spec 134 R1).
- **FR-042**: The Web UI MUST provide label management (list, create, edit with live preview, rename, delete with the in-use refusal shown, export, import) reachable from the Tools page ("Manage labels"). The preview shows server, name, tier and approval state of each would-be member.
- **FR-043**: `mcpproxy tools list` MUST accept `--label` (repeatable). In daemon mode it sends `label` to `GET /api/v1/tools`, or to `GET /api/v1/servers/{id}/tools` with `--server`, keeping each path's present inventory semantics; it MUST first check `features.tool_labels` and exit 1 with "this mcpproxy core does not support labels" instead of printing unfiltered rows from an older core. In standalone mode, which today lists one server (`--server`), it evaluates labels from the config file with the same evaluator (FR-007) over that server's tools.

**Uses: review**

- **FR-050**: The review screen MUST offer a "Label" filter beside tier, state and decision, applied by the existing `filterReviewTools` (`frontend/src/utils/reviewPresentation.ts:189-199`) from the `labels` of each review row. `hasActiveFilters` counts it, so "Allow all filtered (N)" and "Block all filtered (N)" act on the label-filtered set exactly as they act on any filtered set today (`ReviewScreen.vue:77-78,222-231`).
- **FR-051**: Labels MUST NOT add, rename, remove or change any approve, block, reject, force, blind-approval, requarantine or "Approve all" control, nor change which tools any of them sends. The approve request is built from the full tool list as today (`ReviewScreen.vue:191-192`). A test MUST assert that for a given selection, the request with a label filter active is byte-identical to the request without it.
- **FR-052**: With any filter active, the review screen MUST, next to each approving control whose request covers tools the filters hide, state what that control will do to the hidden tools. The effect MUST come from the core, not from the checkbox state: `POST /api/v1/servers/{id}/review/impact` takes the exact request the control would send now (for a force retry, the saved request it would resend) and returns, per tool, its review disposition (approved, blocked, unchanged and still held) and its resulting availability (callable, remains disabled, config-denied, absent), computed by the same code path as the operation in dry-run mode (`internal/runtime/tool_approval_review.go`, `tool_quarantine.go`), administrator only. The screen groups hidden tools by both values (for example "Approve update: 3 changed tools not shown will be approved; 1 of them remains disabled"). A "Show them" action switches the list to exactly those rows, across pages, with their tier, scan verdict, diff and previous definition, and can be undone back to the previous filters. The impact response carries an `impact` token (FR-031c style, bound to the operation, the exact request and every effect-relevant state of the affected tools, including disabled and config-denied flags). When the control is pressed while filters hide affected tools, the Web UI sends that token in the `X-MCPProxy-Review-Impact` header, outside the JSON body (so FR-051's byte-identical body holds); the core recomputes and compares the impact inside the approval's locked commit section, against the same config and approval state the write consumes, and refuses with `409 review_impact_changed` when the impact differs, and the screen fetches a fresh impact. Without filters no header is sent and behaviour is today's. A test changes a hidden tool's disabled state after the preview and before the approval takes the lock and asserts `409 review_impact_changed` with nothing written. Tier counts, "Allowed X · Blocked Y of Z" and "filtered from total" keep counting the full review. Tests cover a quarantined server with pending, changed, blocked and disabled tools, a trusted server with a disabled changed tool under "Approve update", "Approve all", and a force retry, each with an active label filter.
- **FR-053**: The review queue page (`frontend/src/views/Review.vue`, one row per server) gets no label filter in this feature; its rows, badge and counts are unchanged. No label control is offered on skill review.

**Scoped callers and the pin boundary**

- **FR-060**: Labels MUST be visible to and editable by administrators only in this feature. A scoped caller (including a pinned agent token, Spec 118) MUST get: no `labels` field on any tool row; `403 {error: "labels_admin_only"}` on every `/tool-labels` route and on `GET /api/v1/tools?label=…`, before the value is resolved, so the bytes are the same whether the label exists or not; on `GET /api/v1/servers/{id}/tools?label=…` the existing uniform `404` for an unknown or out-of-reach server comes first (Spec 118), and only for a reachable server is the `label` parameter refused with the same `labels_admin_only` body; label management writes answer `labels_admin_only` rather than the generic `requireServerOp` refusal; no `tool_labels.changed`, `profile_label_*` SSE or attention items.
- **FR-061**: In a profile view returned to a scoped caller (`redactProfileView`, `internal/httpapi/profiles_views.go:143`), `allow_labels`, `deny_labels` and `only_labels` MUST be removed entirely. `effective-tools` for a scoped caller keeps its present reach filtering and maps label reasons onto the reason codes scoped callers already receive (`denied_by_label`, `label_unavailable`, `outside_label_limit` and `label_member_unconfirmed` → `denied_by_rule`), never naming the label and never listing drift.
- **FR-062**: No count, member list, preview or error returned by any label surface may be computed over tools outside the caller's reach. For administrators reach is everything; FR-060 keeps scoped callers out, so this requirement binds future relaxations of FR-060.
- **FR-063**: Labels MUST NOT appear on any MCP surface an agent sees (`tools/list`, `retrieve_tools`, `describe_tool`, tool `_meta`, instructions).

**Editions**

- **FR-070**: The feature MUST ship in the personal edition first. In the server edition labels are instance-wide, edited by `admin_user` only, and invisible to tenant users (FR-060). There are no per-user labels. The REST routes are registered in both editions; nothing is behind `//go:build server`.

### Key Entities

- **Tool label** (config `tool_labels[]`): name, description, rules[], include[], exclude[].
- **Label rule**: server (optional), glob | regex.
- **Label reference** (profile `tools.allow_labels|only_labels[]`: label, confirmed[]; `tools.deny_labels[]`: label).
- **Confirmation token / deny acknowledgement**: content-derived hashes binding a write to the exact change the administrator was shown (FR-031b, FR-033a); never stored.
- **Label membership** (derived, in memory): label → set of tool identities; per-identity → sorted label names.
- **Activity row** `tool_label_change`: operation, label names, actor.
- **Attention items** `profile_label_unconfirmed`, `profile_label_missing`.

## Room left for later

**Server-declared groups and tags.** When the MCP grouping shape settles, a second source can feed the same membership index: a server-declared group becomes a read-only label-like entry with `source: server` and a server-qualified name (`github/issues`), shown and filtered like a user label but distinguishable in the payload. Because FR-005 already keeps membership a function of identity only, and server-declared groups are server-controlled input, they would be display/filter only: they never appear as profile references (FR-030 accepts only user labels), never select in review unless the operator chooses the filter, and a change of a tool's declared groups is recorded as a contract change held for review through the Spec 120 structural gate. Nothing in this spec names `source`, so adding it is additive; user labels would default to `source: user`.

**Forwarding to clients.** FR-063 keeps labels off every agent surface today. A later spec can add opt-in forwarding (per label or per profile, default off) once the protocol's grouping capability exists, mapping a user label to the wire shape only for clients that negotiate it, and only for tools the caller can already see. The issue #2042 passthrough work (preserving `_meta`, `title`, `icons` on tool listings) is the groundwork; local labels would leave only through that explicit opt-in, never by default.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can create a label and see its members on the Tools page in under one minute, without editing the config file.
- **SC-002**: On a quarantined server with 90 tools, an operator can allow one label's tools and block another's in two bulk actions; the approve request for that selection is identical to the one built without labels.
- **SC-003**: For every existing review-screen and Tools-page function in Spec 134's function inventory, behaviour with no label filter set is unchanged (the existing tests pass unmodified).
- **SC-004**: In a fixture where the only admission path is the allow reference (tool above the profile's `max_tier`, no `tools.allow` match), adding a matching tool upstream and approving it in quarantine never makes it callable under that profile on any dispatch door of Spec 108 FR-015 until an administrator confirms it (0 of N doors admit it in the enforcement-matrix test); with an `only_labels` reference the same holds for a tool under the cap.
- **SC-005**: An API write that changes the rules of a deny-referenced label, removes a deny reference or the last only reference, or resolves a dangling deny or only reference is refused unless it carries a core-issued acknowledgement for exactly that change (0 of N write doors accept it: label PUT, import, profile PUT, `PATCH /config`, `POST /config/apply`, MCP), including when no present tool is affected, when one of several dangling references is removed, and when a matching tool is discovered between preview and commit; an API write that adds identities to a confirmed set outside the confirmation service is refused on every door.
- **SC-006**: A scoped caller receives zero label names, label counts or label-derived reasons on every REST, SSE and MCP surface (asserted by the Spec 118 visibility test suite extended with label fixtures).
- **SC-007**: With 100 labels × 8 rules and 5,000 tools, a full membership recompute takes under 2 seconds and a filtered tools request takes no longer than an unfiltered one plus 50 ms on the CI runner.

## Delivery

Independent PRs, in order, each test-first (failing tests in the first commit) and each mergeable alone:

1. **PR-1 Model, storage, REST**: `tool_labels` config field with all wiring points (FR-020–FR-022), validator and limits (FR-010–FR-014), evaluator and membership cache (FR-001–FR-007, FR-015), REST management, preview, export/import (FR-023, FR-025–FR-028), `labels` on admin tool rows and `?label=` (FR-040), activity row (FR-024), scoped-caller refusals (FR-060–FR-063), the shared commit boundary (FR-013a), administrator-only doctor check (FR-034b), docs `docs/features/tool-labels.md`. No UI.
2. **PR-2 CLI**: `mcpproxy labels …` (FR-029), `tools list --label` incl. standalone mode (FR-043).
3. **PR-3 Web UI Tools filter and label management**: FR-041, FR-042, Spec 134 inventory rows extended first (Spec 134 FR-065).
4. **PR-4 Review selection**: FR-050–FR-053, including the byte-identical request test (FR-051).
5. **PR-5 Profile rules**: FR-030–FR-037a behind the rollout gate, including the confirmation service (FR-033a), the deny-narrowing acknowledgement on every write door (FR-031b), policy invalidation per caller projection (FR-032a), `flatten-labels` (FR-037a), the enforcement-matrix fixtures extended (Spec 108), explainer and `effective-tools`, drift surfaces and attention items (FR-033, FR-034, FR-034a), Web UI and macOS editors, MCP `profiles` tool, `qapilot` scenarios for confirmed-set growth and deny shrink. Reviewed by an Opus-tier reviewer and the cross-model reviewer as a security change.

## Assumptions

- Labels are an administrator tool; agents and tenants do not need them in v1 (FR-060). This removes every pin-boundary question for v1 at the cost of not letting a scoped caller filter by label.
- Matching on the bare tool name with an optional server is enough; matching on `server:tool` as one string is not needed because `server` is its own field.
- Membership over present tools (including held ones) and labels on review rows from the same matcher is what review needs; profile effect is limited by FR-031 and by quarantine regardless.
- Existing `tools.allow` globs with `*` already admit new matching tools (Spec 108 FR-004); this spec does not change them. Label references are stricter because a label is edited away from the profile that uses it and a regex can be much wider than a glob.
- A deny reference denies its live membership, and label edits that would shrink it need an acknowledgement (FR-031b), so a label edit never silently widens a profile. A rename upstream still escapes a name-based deny (new identity); the tier cap and tool-level quarantine remain the guards for that, and docs say so plainly.
- 100 labels and 8 rules per label cover the issue's use cases with room; limits can be raised later without a format change.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #2045` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #2045`, `Closes #2045`, `Resolves #2045` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

# Feature Specification: Argument rules in profiles

**Feature Branch**: `127-profile-argument-rules`
**Created**: 2026-11-19
**Status**: Draft. It extends Spec 108 (profiles v3).
**Input**: Idea 1 of the LiteLLM MCP Gateway comparison (2026-11-19). LiteLLM has a per-tool
`allowed_params` allowlist, enforced in `MCPServerManager.validate_allowed_params`
(`litellm/proxy/mcp_server/mcp_server_manager.py:6215`), which refuses a call that sends a parameter
outside the list. mcpproxy profiles decide per tool (server scope, allow/deny, tier cap, unannotated) and
never look at argument values.

## Why

Many real tools are only dangerous with one argument. `git_push` is routine, and `git_push` with
`force: true` rewrites history. `delete_branch` is fine for `feature/*` and a disaster for `main`.
`write_file` is safe under the project directory and dangerous under `~/.ssh`. `run_query` is safe with
`readonly: true`. Today an operator who wants the safe half must deny the whole tool, so agents lose the
capability, or allow the whole tool, so the profile does nothing for the dangerous half. Tool annotations
cannot express this either, because a tier belongs to the tool, not to the call.

Argument rules let a profile keep a tool and refuse only the calls whose arguments cross a line the
operator draws. They are a narrowing step after the existing decision. They never admit a tool the
profile would otherwise refuse.

## Scope Boundary

In scope:

- A new optional profile field `tools.arguments`: a list of declarative rules, each bound to a
  `server:tool` pattern and one argument path.
- Call-time enforcement on every door that dispatches an upstream tool call under a profile:
  `call_tool_read|write|destructive`, the direct surface (`server__tool`), the single-server surface,
  nested calls from `code_execution`, and REST replay.
- The same management surfaces that carry `tools.allow/deny/classify` today: config file, REST, CLI,
  the MCP `profiles` tool and the Web UI editor. The explainer and "Try it" accept sample arguments.
- Telling the agent ahead of time: `describe_tool` lists the argument rules that apply to the tool under
  the caller's profile, so a well-behaved agent does not waste a call.

Out of scope:

- Regular expressions, numeric ranges, cross-argument conditions ("if A then B") and rewriting arguments.
  The rule language is deliberately small and an allowlist of shapes (see the scanner-exemptions
  lesson: no free-form deny-lists).
- Rules on agent tokens. Tokens keep server scope plus tier permissions. A token pinned to a profile
  gets that profile's argument rules.
- Inspecting `code_execution` JavaScript source. Only the concrete calls it dispatches are checked.
- Changing a tool's tier by argument (for example "read unless `write: true`"). This is an open question
  for a later spec.
- Resolving symlinks or the upstream's notion of a path. A prefix rule is a string rule on a cleaned
  path, and this residual risk is stated in the docs.

## Definitions

- **Argument path**: a top-level argument name, or a dotted path into nested objects (`options.force`),
  at most 8 segments. When the value at a path is an array, the rule applies to every element.
- **Rule kinds** (exactly one per rule):
  - `deny_values`: refuse the call when the value equals any listed value.
  - `allow_values`: refuse the call when the value is present and not one of the listed values.
  - `path_prefix`: refuse the call when the value is present and, after path cleaning, does not lie
    under one of the listed prefixes.
  - `forbid`: refuse the call when the argument is present at all.
- **When absent**: `admit` (default) or `refuse`. It decides the outcome when the argument is missing, so
  the upstream's default applies. `forbid` ignores it.
- **Path cleaning**: lexical clean of `.` and `..` segments with `/` as the separator (and `\` folded to
  `/`). A value that still contains a `..` segment after cleaning, or that is not absolute when every
  prefix is absolute, is refused. A `~` is not expanded and does not match an absolute prefix.

## User Scenarios & Testing *(mandatory)*

### User Story 1: Keep push, refuse force-push (Priority: P1)

The operator's `coding` profile allows `github:*`. They add one rule:
`{tool: "github:push", argument: "force", deny_values: [true]}`. An agent pushing normally succeeds. An
agent sending `force: true` gets the refusal "blocked by profile: github:push argument `force` may not be
`true` under this profile". No call reaches the upstream, and the activity log shows a policy decision
with block reason `profile_argument`.

**Why this priority**: it is the canonical case and it closes the most damaging gap. A tool can be admitted
while its one dangerous argument cannot be refused.

**Independent Test**: a fixture server with a `push(force bool)` tool that counts calls. Push without
force reaches the fixture once. Push with force is refused, the counter is unchanged, and one
`policy_decision` row carries `profile_argument`.

**Acceptance Scenarios**:

1. **Given** the rule above, **When** the agent calls `github:push` with `force: false`, **Then** the call
   is dispatched.
2. **Given** the rule above, **When** the agent calls it with `force: true` through `call_tool_write`, the
   direct surface, the single-server surface or `code_execution`, **Then** every door refuses with the
   same reason and message, and the upstream is never called.
3. **Given** the rule above and `force` omitted, **When** `when_absent` is unset, **Then** the call is
   dispatched.

---

### User Story 2: Writes only under the project directory (Priority: P1)

The operator adds `{tool: "fs:write_file", argument: "path", path_prefix: ["/Users/me/repos/app/"]}`.
Writes under the project directory succeed. `/Users/me/.ssh/authorized_keys`,
`/Users/me/repos/app/../../.ssh/x` and `~/.ssh/x` are refused.

**Why this priority**: filesystem MCP servers are among the most installed, and directory confinement is
the safeguard users ask for most often.

**Independent Test**: a table of 12 path values (normal, `..` escape, `~`, relative, a lookalike prefix
such as `/Users/me/repos/app-evil/`, a trailing-slash variant, a backslash variant), each with its
expected outcome.

**Acceptance Scenarios**:

1. **Given** the prefix `/Users/me/repos/app/`, **When** the path is `/Users/me/repos/app-evil/x`,
   **Then** the call is refused. A prefix matches whole path segments only.
2. **Given** the same rule, **When** the path is `/Users/me/repos/app/src/../README.md`, **Then** the call
   is admitted, because it cleans to a path under the prefix.
3. **Given** the same rule, **When** the value is a number, **Then** the call is refused (type mismatch
   fails closed).

---

### User Story 3: The agent sees the rules before calling (Priority: P2)

An agent calls `describe_tool` for `github:push` and gets, next to the schema, an `argument_rules` list:
"`force`: may not be `true`". It plans accordingly and never makes the refused call.

**Why this priority**: refusals cost a round trip and confuse weaker agents. Telling them first is
cheaper. It is P2 because enforcement alone is already safe.

**Acceptance Scenarios**:

1. **Given** a profile with rules for the tool, **When** the agent describes it, **Then** the rules for
   that tool appear in a fixed, human-readable form without the profile's name unless the existing
   disclosure rule (D39) allows it.
2. **Given** no rules apply, **When** the agent describes the tool, **Then** the response is unchanged.

---

### User Story 4: The operator tests a rule before saving (Priority: P2)

In the Web UI profile editor (and `mcpproxy profile try`, `POST /api/v1/profiles/try`), the operator
picks a tool, types sample arguments and sees "admitted" or "refused by argument rule 2 (`path` not under
`/Users/me/repos/app/`)". The explainer (`mcpproxy access explain`) accepts the same optional sample
arguments.

**Acceptance Scenarios**:

1. **Given** a draft profile, **When** the operator tries `fs:write_file` with `{"path": "/etc/passwd"}`,
   **Then** the answer names the refusing rule by its index and kind.
2. **Given** no sample arguments, **When** the operator runs Try it, **Then** the answer is the
   tool-level decision plus "argument rules apply at call time: N".

---

### User Story 5: Bad rules are caught at save time (Priority: P3)

A rule with two kinds, an empty value list, a path with 9 segments, a pattern naming a server outside the
profile or a `path_prefix` with a relative prefix is rejected or warned about when the profile is saved,
with the same validation on every surface.

### Edge Cases

- **A rule names a tool the profile does not admit**: saved with a warning (same as `tools.allow/deny`
  naming an out-of-profile server). It never admits the tool.
- **Several rules match one call**: all are evaluated, and the first refusing rule in list order is
  reported. Order never changes the outcome, only the message.
- **Tool patterns with `*`**: allowed (`github:*` with `argument: force`). A rule whose argument the
  tool's schema does not declare is inert for that tool and is listed as such in the effective-tools
  view.
- **Value comparison**: JSON equality on the decoded value (`true` ≠ `"true"`, `1` = `1.0`). Strings
  compare exactly (case-sensitive).
- **Large or deep arguments**: evaluation reads at most the named path. The total work per call is bounded
  by rules × path depth × array elements, capped at 10,000 element checks. Beyond the cap the call is
  refused as "argument too large to check under this profile".
- **Secret-shaped values**: refusal messages, activity rows, audit lines and the explainer name the
  argument and the rule, never the value sent. The value appears only where the existing redacted
  activity-args copy already records it.
- **Hot reload**: a profile edit takes effect on the next call, with the same reload semantics as Spec 108.
- **Legacy and downgrade**: a profile without `tools.arguments` behaves exactly as today. An older binary
  reading a profile with the field ignores it (the field is preserved opaquely) and logs a warning that
  argument rules are not enforced.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: A profile MUST accept an optional `tools.arguments` list. Each entry MUST have `tool` (a
  `server:tool` pattern, same grammar as `tools.allow/deny`), `argument` (an argument path) and exactly
  one of `deny_values`, `allow_values`, `path_prefix`, `forbid`, plus an optional `when_absent` (`admit`
  default, `refuse`).
- **FR-002**: Validation MUST reject: zero or several kinds; an empty value list; more than 32 values;
  a value larger than 1 KiB; an argument path over 8 segments or with an empty segment; a relative
  `path_prefix` entry; more than 64 rules per profile. It MUST warn (and save) when the tool pattern names
  a server outside `servers`.
- **FR-003**: Argument rules MUST be evaluated only for a call the existing tool decision admits, after
  that decision and before token-permission, intent, quarantine, approval and Spec 085 schema
  validation. They MUST NOT change discovery, listing, `retrieve_tools` ranking or the tool-level
  decision.
- **FR-004**: A call MUST be refused when any matching rule refuses it, per the kind semantics in
  Definitions. A value whose JSON type cannot be compared by the rule (e.g. an object under
  `path_prefix`) MUST be refused.
- **FR-005**: The same evaluation MUST run on every dispatching door: `call_tool_*`, the direct surface,
  the single-server surface, `code_execution` nested calls and REST replay. A parity test MUST drive one
  refusing call through each door and observe the same reason, message and zero upstream calls.
- **FR-006**: A refusal MUST use a new block reason `profile_argument` and access reason
  `denied_by_argument`, added to the profile contract enums, labels and tool-refusal fixtures (disclosed
  and undisclosed variants), the Go-to-TypeScript types and the macOS models.
- **FR-007**: The agent-facing refusal MUST name the tool, the argument path and the rule kind in fixed
  wording, MUST NOT include the sent value, and MUST follow the existing profile-name disclosure rule.
- **FR-008**: A refusal MUST produce a `policy_decision` activity row with the block reason and the rule
  index, an audit `authz deny` line, and the existing availability counter. Telemetry MUST count it under
  the closed profile block-reason set without the tool or argument name.
- **FR-009**: `describe_tool` MUST include an `argument_rules` list for the described tool when any rule
  applies under the caller's profile, with each rule rendered in one fixed sentence. Tools with no
  applicable rule MUST be unchanged.
- **FR-010**: Try it (REST, CLI, Web UI) and the access explainer MUST accept optional sample arguments
  and report the argument-rule outcome with the refusing rule's index and kind.
- **FR-011**: The effective-tools view MUST list, per tool, the argument rules that apply and mark rules
  whose argument the tool's schema does not declare as inert.
- **FR-012**: `tools.arguments` MUST be part of the profile policy fingerprint, the legacy-profile
  detection and the rollout-gate field list, so an edit is detected and a legacy profile stays legacy
  only without it.
- **FR-013**: The field MUST be editable from the config file, REST create/update, CLI create/update
  (`--argument-rule` repeated, JSON form), the MCP `profiles` tool and the Web UI editor. The parity
  matrix MUST gain the field on its profile-policy rows. The macOS app MUST display the rule count
  read-only.
- **FR-014**: Evaluation per call MUST be bounded (FR-002 limits and the 10,000 element-check cap). It
  MUST NOT allocate in proportion to the size of unrelated arguments.
- **FR-015**: A binary that does not know the field MUST preserve it on save, and a binary that does
  know it MUST NOT silently drop an invalid rule: an invalid rule fails the save on every surface.

### Key Entities

- **Argument rule**: `{tool, argument, kind + values, when_absent}`. Belongs to one profile, ordered,
  and identified in messages by its 1-based index.
- **Argument decision**: `admitted`, or `refused(rule index, kind, argument path)`. Computed per call,
  never stored except as the activity row.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can keep a tool and block its dangerous argument in one profile edit, without
  denying the tool. This is checked by the force-push and project-directory stories end to end on every
  surface.
- **SC-002**: Zero refused calls reach the upstream across the five dispatching doors (counting fixture
  shows 0 calls for every refused case in the parity test).
- **SC-003**: The path-prefix table (at least 12 cases including `..` escape, lookalike prefix, `~`,
  relative, backslash and type mismatch) passes with no false admits.
- **SC-004**: Profiles without argument rules show no behaviour change: the existing profile contract
  fixtures, goldens and qapilot scenarios pass unmodified.
- **SC-005**: Argument evaluation adds under 1% to p95 call overhead on the 3,000-tool perf rig with 64
  rules configured.
- **SC-006**: In a scripted trial, an agent that read `describe_tool` avoids the refused call in at least
  8 of 10 runs.

## Assumptions

- Upstream tools declare arguments as JSON objects (MCP `tools/call` arguments), so a path into the
  decoded object is well defined.
- Operators write rules against argument names they can read in the tool's schema. The effective-tools
  view's "inert" marker catches typos.
- A string prefix rule is useful even though the upstream may resolve symlinks. The docs state that it
  confines what the agent asks for, not what the upstream does.
- The existing call-time gate order (profile → token → intent → quarantine → approval → schema →
  connection) stays, and argument rules slot in right after the profile decision.

## Open Questions for the Plan

- Whether `allow_values` should also be shown to the agent by narrowing the schema's `enum` in
  `describe_tool` output, or only as a rule sentence.
- Whether a rule should be able to target the tool's tier ("classify as destructive when `force` is
  true") in a follow-up, reusing the same rule grammar.
- The CLI flag syntax for a rule (repeated `--argument-rule 'github:push force deny=true'` versus JSON
  only).

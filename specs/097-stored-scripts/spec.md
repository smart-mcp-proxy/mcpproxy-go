# Feature Specification: Server-Side Stored Scripts for Code Execution

**Feature Branch**: `097-stored-scripts`
**Created**: 2026-08-14
**Status**: Draft
**Input**: User description: "Server-side stored scripts for code execution so long workflows need not be re-sent inline on every call (GitHub issue #986)"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Invoke a stored workflow by name (Priority: P1)

An AI agent repeatedly runs the same orchestration workflow through the code-execution tool. Today every invocation ships the entire script inline (~4.8k tokens for a 19KB workflow), paid again on each run, retry, parameter tweak, and loop iteration. With stored scripts, the operator drops the workflow into a `scripts/` directory under the mcpproxy config directory once; the agent then invokes it with `script: "<name>"` plus its `input` — the per-run cost falls from the whole script to a name plus its parameters.

**Why this priority**: This is the entire value of the feature — without invocation-by-reference there is nothing else to build on.

**Independent Test**: Place a script file in the scripts directory, call the code-execution tool with `script` instead of `code`, and observe the identical result the inline equivalent produces.

**Acceptance Scenarios**:

1. **Given** a file `scripts/fetch-prs.js` exists under the config directory, **When** the code-execution tool is called with `script: "fetch-prs"` and an `input` object, **Then** the script executes with that input under exactly the same sandbox limits and returns the same result shape as if its contents had been passed inline as `code`.
2. **Given** both `code` and `script` are supplied in one call, **When** the tool is invoked, **Then** the call is rejected with an error explaining exactly one of the two must be provided.
3. **Given** `script` names a script that does not exist, **When** the tool is invoked, **Then** the call fails with an error listing the available script names (or stating none exist).

---

### User Story 2 - Same invocation from the CLI (Priority: P2)

An operator or script author runs `mcpproxy code exec --script fetch-prs --input '{"repo":"acme/api"}'` and the daemon executes the stored script — no file path juggling, no inlining, and the invocation works identically in daemon mode.

**Why this priority**: The CLI is the operator's test loop for authoring scripts; without it, validating a stored script means hand-crafting MCP calls.

**Independent Test**: With a script stored, run the CLI command and compare output to the equivalent `--code` invocation.

**Acceptance Scenarios**:

1. **Given** a stored script and a running daemon, **When** `mcpproxy code exec --script <name> --input '{...}'` runs, **Then** it produces the same result as the inline equivalent, and `--script` combined with `--code` or `--file` is rejected.
2. **Given** no daemon is running, **When** `mcpproxy code exec --script <name>` runs in standalone mode, **Then** the script is resolved from the same scripts directory and executed locally with identical semantics.

---

### User Story 3 - Discover what scripts exist (Priority: P2)

An agent (or operator) needs to know which workflows are available. The CLI offers `mcpproxy code scripts list` showing each script's name and source file (asking the daemon when one is running, so both always agree). MCP clients learn the valid names from the code-execution tool itself: the tool description documents the mechanism, and any invocation naming a nonexistent script returns the available names (bounded) in its error — so an agent recovers the name set in one failed call without out-of-band knowledge. Live enumeration surfaces (a dedicated listing tool, `tools/list_changed` notifications) are deliberately out of scope for v1: tool registrations are static, and error-driven discovery covers the recovery path.

**Why this priority**: Invocation-by-reference is unusable if callers cannot learn the valid names; ranks with the CLI story but below the core invocation.

**Independent Test**: Store two scripts, list them via CLI and observe both names; verify an MCP client can discover the same names.

**Acceptance Scenarios**:

1. **Given** two files in the scripts directory, **When** `mcpproxy code scripts list` runs, **Then** both names appear with their file paths (and `-o json` emits a machine-readable list).
2. **Given** an MCP client connected to the daemon, **When** it invokes the code-execution tool with a `script` name that does not exist, **Then** the error lists the currently available script names (up to 20), reflecting the directory's current contents.
3. **Given** an empty or absent scripts directory, **When** listing, **Then** the result is an explicit empty list, not an error.

---

### User Story 4 - Edit scripts without restarting (Priority: P3)

A script author edits `scripts/fetch-prs.js` while the daemon runs. The next invocation of `script: "fetch-prs"` executes the updated content; adding or deleting a script file likewise takes effect without a daemon restart.

**Why this priority**: Authoring convenience — the feature works without it, but restart-per-edit would make script development painful.

**Independent Test**: Invoke a stored script, edit the file, invoke again, observe the new behavior; add and remove files and observe the list change.

**Acceptance Scenarios**:

1. **Given** a stored script has been invoked, **When** its file content changes on disk, **Then** the next invocation runs the new content without any daemon restart.
2. **Given** a new file appears in (or is removed from) the scripts directory, **When** scripts are next listed or invoked, **Then** the addition/removal is reflected.

---

### Edge Cases

- **Name resolution is strictly confined, at open time**: a `script` value is validated as a restricted token (ASCII letters, digits, hyphen, underscore; 1–64 chars; case-sensitive) BEFORE any filesystem access — anything else (path separators, `..`, absolute paths, dots, Unicode) is rejected as an invalid name without touching the filesystem. Resolution then opens `<name>.js` / `<name>.ts` (lowercase extensions only) through a mechanism that confines the open to the scripts directory at open time — a symlink in place of the script file is rejected (scripts must be regular files), so no check-then-open race can escape the directory. The scripts directory itself may be a symlink (it is operator-controlled and resolved as the root of confinement).
- **Ambiguous name** (both `<name>.js` and `<name>.ts` exist): the call is rejected with an error naming both candidates; ambiguity is never resolved silently. Listings show the name flagged as ambiguous.
- **One invocation, one snapshot**: the script's bytes are opened, size-checked, and read exactly once per invocation; concurrent edits affect the next invocation, never the running one.
- **Unreadable or oversized file**: a script file that cannot be read, exceeds the stored-script size bound (256 KB), or is empty fails the invocation with a specific error; it does not crash or hang the daemon. (Inline `code` today has no explicit bound; the stored-script bound is new and applies only to stored scripts.)
- **Missing scripts directory**: treated as "no scripts" — listing returns empty, invocation returns the not-found error; the daemon does not create the directory on its own in v1.
- **Language selection**: `.ts` scripts run through the same TypeScript path as inline `language: "typescript"`; `.js` scripts run as JavaScript. Supplying an explicit `language` that contradicts the extension is rejected; supplying a matching one is accepted.
- **Listing hygiene**: files whose base name fails the token rules, with unrecognized or uppercase extensions, are ignored by listing and unreachable by invocation (they are not scripts).
- **Sandbox parity**: a stored script executes with exactly the sandbox limits, scope/permission enforcement, timeout/budget options, and activity logging of the equivalent inline invocation — `script` changes only where the source text comes from, nothing about how it runs.
- **No write path**: nothing in v1 creates, modifies, or deletes script files — no MCP tool, no REST endpoint, no CLI verb. The filesystem is the only authoring interface.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST resolve stored scripts from the `scripts/` directory next to the ACTIVE configuration file — the daemon uses the config file it loaded; the CLI in standalone mode uses the config file it resolves by the same rules; the CLI in daemon mode delegates resolution entirely to the daemon (the name, never the content, crosses the wire). Files with supported lowercase extensions (`.js`, `.ts`) and token-valid base names constitute the script set, addressed by base name.
- **FR-002**: The code-execution tool MUST accept `script: "<name>"` as an alternative to `code`, executing the named file's content with all other parameters (`input`, options) behaving identically to an inline invocation. Supplying both `code` and `script`, or neither, MUST be rejected with an explanatory error.
- **FR-003**: Script names MUST be validated (ASCII letters, digits, hyphen, underscore; 1–64 chars) before any filesystem access, and resolution MUST be confined to the scripts directory at open time: the open itself cannot traverse outside the directory, and a non-regular file (symlink, directory, device) at the script path is rejected. No check-then-open window may permit an escape.
- **FR-004**: An invocation naming a nonexistent script MUST fail with an error that includes up to 20 available script names (alphabetical) or states that none exist — this error is the MCP discovery mechanism.
- **FR-005**: A stored-script invocation MUST execute under exactly the same sandbox restrictions, scope/permission enforcement, execution options, budgets, and activity/history logging as the equivalent inline invocation. Records keep storing the executed source exactly as they do for inline code (Spec 024 parity) and additionally carry the script name.
- **FR-006**: The CLI MUST support `mcpproxy code exec --script <name>` in both daemon and standalone modes, mutually exclusive with `--code` and `--file`, resolving from the same scripts directory with identical semantics.
- **FR-007**: The CLI MUST provide `mcpproxy code scripts list` showing each available script's name and source path, honoring the standard output-format flags (`-o json|yaml`); an empty or absent directory yields an empty list, not an error.
- **FR-008**: MCP clients MUST be able to recover the currently available script names through the code-execution tool surface without out-of-band knowledge, via the FR-004 error listing; the tool description MUST document the `script` parameter and this discovery mechanism. Tool registrations remain static; no `tools/list_changed` notifications or dedicated listing tool in v1.
- **FR-009**: Script content MUST be read at invocation time (or equivalently freshened) such that file edits, additions, and deletions take effect on the next use without a daemon restart.
- **FR-010**: A script file exceeding 256 KB, unreadable, ambiguous (both extensions present), or empty MUST fail the invocation with a specific error; no partial execution. Each invocation executes exactly one atomically-read snapshot of the file.
- **FR-011**: v1 MUST NOT expose any write/upload/delete capability for scripts through any API surface; the filesystem is the sole authoring interface.
- **FR-012**: The REST code-execution endpoint MUST accept `script` as an alternative to `code` with the same exactly-one-of validation (HTTP 400 otherwise), and a read-only REST listing endpoint MUST expose the same name/path data as the CLI listing (this is what daemon-mode CLI uses). Both editions; every surface where the code-execution tool is available today. No write/upload/delete surface anywhere (see FR-011).

### Key Entities

- **Stored script**: a named, file-backed unit of executable workflow text — identified by base name, sourced from the scripts directory, with a supported-extension file as its content.
- **Script reference**: the `script` parameter value (or `--script` flag) — a validated name, never a path.
- **Script listing**: the enumerable set of currently available stored scripts (name + source path).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Invoking a 19KB stored workflow by name transmits over 95% fewer request bytes than the inline equivalent.
- **SC-002**: For deterministic scripts against stub upstreams, a stored-script invocation and the same source passed inline produce identical results in every tested scenario.
- **SC-003**: Every entry in a traversal corpus (relative traversal, absolute paths, separator injection, dot names, Unicode, symlinked script file, oversized name) is rejected with the invalid-name or non-regular-file error class; corpus entries that are invalid tokens are proven (via test instrumentation of the resolver) to be rejected before any filesystem call.
- **SC-004**: A script edit on disk is reflected in the very next invocation, with no daemon restart, in every trial of the freshness test.
- **SC-005**: Existing inline `code` behavior is unchanged: the full existing test suite passes without modification (beyond additions).

## Assumptions

- The scripts directory location is fixed (`<config-dir>/scripts/`) in v1; a `code_scripts` name→path config map from the issue is deferred — the directory convention alone covers the stated need without new config surface, and a map can be added compatibly later.
- Discovery for MCP clients is error-driven (FR-004/FR-008): tool registrations are built once from static descriptions in this codebase, so live name enumeration in the tool description would go stale; the not-found error is always current. A dedicated listing tool and change notifications are deferred.
- Inline `code` has no explicit size bound today; stored scripts get a fixed 256 KB bound (not configurable in v1) purely to bound daemon-side file reads.
- Hot-freshness is defined by read-at-invocation semantics (one open+read per invocation); no watcher, no cache, hence nothing to invalidate.
- TypeScript stored scripts are supported exactly insofar as inline TypeScript is supported today (same transpilation path).

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #986` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #986`, `Closes #986`, `Resolves #986` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat: server-side stored scripts for code execution

Related #986

[Detailed description]

## Changes
- [Bulleted list]

## Testing
- [Summary]
```

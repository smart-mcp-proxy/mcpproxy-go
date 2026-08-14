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

An agent (or operator) needs to know which workflows are available. The CLI offers `mcpproxy code scripts list` showing each script's name and source file; MCP clients see the available script names surfaced through the code-execution tool's own interface so they can invoke by name without out-of-band knowledge.

**Why this priority**: Invocation-by-reference is unusable if callers cannot learn the valid names; ranks with the CLI story but below the core invocation.

**Independent Test**: Store two scripts, list them via CLI and observe both names; verify an MCP client can discover the same names.

**Acceptance Scenarios**:

1. **Given** two files in the scripts directory, **When** `mcpproxy code scripts list` runs, **Then** both names appear with their file paths (and `-o json` emits a machine-readable list).
2. **Given** an MCP client connected to the daemon, **When** it inspects the code-execution tool, **Then** the currently available script names are discoverable without invoking anything.
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

- **Name resolution is strictly confined**: a `script` value that attempts to escape the scripts directory (path separators, `..`, absolute paths, symlinks pointing outside, names differing only by extension tricks) is rejected as an invalid name — never resolved. Valid names are a restricted token set (letters, digits, hyphen, underscore), mapped to `<name>.js` (or `<name>.ts`) inside the scripts directory only.
- **Ambiguous name** (both `<name>.js` and `<name>.ts` exist): the call is rejected with an error naming both candidates; ambiguity is never resolved silently.
- **Unreadable or oversized file**: a script file that cannot be read, or exceeds the same size bound that applies to inline `code`, fails the invocation with a clear error; it does not crash or hang the daemon.
- **Empty script file**: rejected the same way an empty inline `code` is.
- **Missing scripts directory**: treated as "no scripts" — listing returns empty, invocation returns the not-found error; the daemon does not create the directory on its own in v1.
- **Sandbox parity**: a stored script executes with exactly the sandbox limits, scope/permission enforcement, timeout/budget options, and activity logging of the equivalent inline invocation — `script` changes only where the source text comes from, nothing about how it runs.
- **No write path**: nothing in v1 creates, modifies, or deletes script files — no MCP tool, no REST endpoint, no CLI verb. The filesystem is the only authoring interface.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST resolve stored scripts from a `scripts/` directory under the mcpproxy configuration directory; files with supported extensions (`.js`, `.ts`) constitute the script set, addressed by their base name.
- **FR-002**: The code-execution tool MUST accept `script: "<name>"` as an alternative to `code`, executing the named file's content with all other parameters (`input`, options) behaving identically to an inline invocation. Supplying both `code` and `script`, or neither, MUST be rejected with an explanatory error.
- **FR-003**: Script names MUST be validated against a restricted token set (letters, digits, hyphen, underscore only) and resolved strictly inside the scripts directory; any name failing validation or any resolution escaping the directory (including via symlinks) MUST be rejected without filesystem access outside the directory.
- **FR-004**: An invocation naming a nonexistent script MUST fail with an error that includes the available script names (bounded to a reasonable count) or states that none exist.
- **FR-005**: A stored-script invocation MUST execute under exactly the same sandbox restrictions, scope/permission enforcement, execution options, budgets, and activity/history logging as the equivalent inline invocation, and activity records MUST identify the script by name.
- **FR-006**: The CLI MUST support `mcpproxy code exec --script <name>` in both daemon and standalone modes, mutually exclusive with `--code` and `--file`, resolving from the same scripts directory with identical semantics.
- **FR-007**: The CLI MUST provide `mcpproxy code scripts list` showing each available script's name and source path, honoring the standard output-format flags (`-o json|yaml`); an empty or absent directory yields an empty list, not an error.
- **FR-008**: MCP clients MUST be able to discover the currently available script names through the code-execution tool surface without out-of-band knowledge.
- **FR-009**: Script content MUST be read at invocation time (or equivalently freshened) such that file edits, additions, and deletions take effect on the next use without a daemon restart.
- **FR-010**: A script file exceeding the size bound that applies to inline `code`, unreadable, ambiguous (multiple extensions), or empty MUST fail the invocation with a specific error; no partial execution.
- **FR-011**: v1 MUST NOT expose any write/upload/delete capability for scripts through any API surface; the filesystem is the sole authoring interface.
- **FR-012**: The capability MUST be available in both editions and on every surface where the code-execution tool is available today.

### Key Entities

- **Stored script**: a named, file-backed unit of executable workflow text — identified by base name, sourced from the scripts directory, with a supported-extension file as its content.
- **Script reference**: the `script` parameter value (or `--script` flag) — a validated name, never a path.
- **Script listing**: the enumerable set of currently available stored scripts (name + source path).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Invoking a 19KB stored workflow by name transmits over 95% fewer request bytes than the inline equivalent, with identical execution results.
- **SC-002**: A stored script invocation returns byte-identical results to the same script passed inline as `code` (same input, same stub upstreams) in 100% of tested scenarios.
- **SC-003**: 100% of path-escape attempts in a traversal test corpus (relative traversal, absolute paths, separator injection, symlink escape, extension tricks) are rejected without any filesystem access outside the scripts directory.
- **SC-004**: A script edit on disk is reflected in the very next invocation in 100% of trials, with no daemon restart.
- **SC-005**: Existing inline `code` behavior is unchanged: the full existing test suite passes without modification (beyond additions).

## Assumptions

- The scripts directory location is fixed (`<config-dir>/scripts/`) in v1; a `code_scripts` name→path config map from the issue is deferred — the directory convention alone covers the stated need without new config surface, and a map can be added compatibly later.
- Discovery for MCP clients is satisfied by surfacing names through the code-execution tool surface (e.g., its description or a parameter-level enumeration); a dedicated `list_scripts` MCP tool is not required in v1 and would grow the tool surface for little gain.
- The inline-code size bound is the reference bound for stored scripts; v1 introduces no separate script-size configuration.
- Hot-freshness is defined by read-at-invocation semantics; no watcher-based cache is required so long as FR-009's observable behavior holds.
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

# Feature Specification: Save Tool Output to File

**Feature Branch**: `feat/tool-response-save-to-file`
**Created**: 2026-08-27
**Status**: Draft
**Input**: Internal request — agents driving `call_tool_read` / `call_tool_write` /
`call_tool_destructive` against upstream MCP servers regularly receive
responses far larger than the configured `tool_response_limit`, and the
truncated/cached response is not always what the caller actually needs:
sometimes the full untruncated payload should be written straight to disk
for the calling process (or a human) to consume directly.

## Background

`tool_response_limit` already protects the agent's context window by
truncating and caching oversized tool responses (see
`docs/configuration.md`). That mechanism is the right default, but it
throws away the byte-for-byte original response — a caller who actually
wants the full payload (a large log dump, a full file listing, a big JSON
API response) has no way to get it without re-running the tool against a
raised limit, which reintroduces the very context-window problem the
limit exists to prevent.

This feature adds an opt-in `save_to_file` parameter to the shared
`call_tool_*` handler. When present, mcpproxy writes the **full,
untruncated** upstream response to a file under an operator-configured
whitelist of directories and returns a small JSON envelope (path, byte
count, block counts) in place of the response body. The response-limit
truncator is bypassed entirely for a saved call — there is nothing left
to truncate.

Because this hands an MCP client-supplied string almost directly to the
filesystem, the design treats path resolution as security-critical from
the outset: every target path is resolved through a directory whitelist
(`tool_output_roots`), and the write is confined through a single Go 1.25
`os.Root` handle opened once, immediately after the whitelist match, and
used for every filesystem operation the write performs. This closes a
symlink or rename planted **inside** the root, or a replacement of the
root directory itself, in the gap between the check and the write — see
Security Considerations below for the precise boundary (what this does and
does not close).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Save a large response to a whitelisted file (Priority: P1)

An agent calls a tool through `call_tool_read` and expects a large
response (e.g. a full directory listing or log export). It sets
`save_to_file` to an absolute path under a directory the operator has
whitelisted in `tool_output_roots`. Instead of a truncated response, the
agent gets back a short envelope confirming the file was written, and can
then read the file directly (or hand the path to a human).

**Why this priority**: This is the entire point of the feature — without
it, large-response workflows have no better option than raising
`tool_response_limit` globally, which defeats its purpose for every other
call.

**Independent Test**: Configure `tool_output_roots` with a temp
directory, call a tool with `save_to_file` set to a path under it, and
confirm (a) the response is the JSON envelope, not the tool's own
content, and (b) the file on disk contains the full, untruncated
response.

**Acceptance Scenarios**:

1. **Given** `tool_output_roots` includes `/tmp/agent-out`, **When** a
   call sets `save_to_file: "/tmp/agent-out/result.txt"`, **Then** the
   file is created with the concatenated text content of the response and
   the tool call returns a JSON envelope with the fields `saved_to`,
   `bytes`, `sha256`, `format`, `content_blocks`, `non_text_blocks`,
   `preview`, `truncated_preview` instead of the raw content.
   `content_blocks` is the TOTAL content-block count (text and non-text
   together), not a text-only count.
2. **Given** `save_format` is omitted, **When** `save_to_file` is set,
   **Then** the default format is `"text"` (concatenated text blocks
   only).
3. **Given** `save_format: "json"`, **When** `save_to_file` is set,
   **Then** the file contains the full `json.Marshal` of the tool result,
   including non-text content blocks.

---

### User Story 2 - Reject paths outside the whitelist, including via symlink tricks (Priority: P1)

An operator has whitelisted `/data/agent-out` but not the rest of the
filesystem. A malicious or buggy tool call attempts to write outside that
directory — directly (`../../etc/passwd`), or indirectly by racing a
symlink into place between mcpproxy's validation check and its actual
write. Both must be rejected; the symlink race in particular must be
closed structurally, not just checked-then-hoped.

**Why this priority**: `save_to_file` is the first feature in this
codebase that lets an MCP client's string parameter reach the filesystem
almost directly. A path-escape or TOCTOU bug here is a full write-primitive
vulnerability, not a cosmetic bug.

**Independent Test**: Attempt a save outside every configured root
(rejected), attempt a save through a directory symlink that is swapped
after the whitelist check but before the write completes (rejected — this
is the case `os.Root` structurally closes, see Security Considerations),
and attempt a save whose target path is itself the whitelisted root
(rejected as `ErrInvalidPath` — "target must be a file inside a root, not
the root itself") or the filesystem root `/` (rejected as `ErrOutsideRoots`
— `/` does not fall inside any configured root prefix; these two take
different code paths even though both are rejected).

**Acceptance Scenarios**:

1. **Given** `tool_output_roots: ["/data/agent-out"]`, **When**
   `save_to_file` resolves outside that prefix (including via `..`
   segments or an absolute path elsewhere), **Then** the call returns a
   tool error (`ErrOutsideRoots`) and nothing is written.
2. **Given** an intermediate directory under a whitelisted root that is
   swapped for a symlink pointing outside the root between `Resolve` and
   the write, **When** the write executes, **Then** the write fails and
   nothing is written outside the root — the already-open `os.Root` handle
   opened by `Resolve` refuses to follow the symlink out of the root it
   was opened against (see Security Considerations for the exact boundary,
   including the case of the root's own path being swapped instead).
3. **Given** `save_to_file` is set to exactly a configured root directory
   (no filename), **When** the call is made, **Then** it is rejected as
   `ErrInvalidPath` rather than silently creating files inside the root
   under an unintended name.
4. **Given** `tool_output_roots` contains `/` (the filesystem root),
   **When** the configuration is validated, **Then** it is rejected at
   load time with an explanatory error, since a root of `/` cannot ever
   satisfy the whitelist's prefix-match and is therefore a silently
   useless (not merely permissive) configuration value.

---

### User Story 3 - Config changes take effect without a restart (Priority: P2)

An operator changes `tool_output_roots` or `tool_output_max_bytes` via
the Web UI, the REST config endpoint, or by editing the config file with
hot reload enabled. The very next `save_to_file` call must honor the new
value — not the value that was live when the proxy process started.

**Why this priority**: mcpproxy's config hot-reload already covers most
settings (including the closely related `tool_response_limit`); a setting
that silently requires a full process restart to take effect is a
foot-gun, especially for a security-relevant whitelist an operator might
urgently want to narrow.

**Independent Test**: Start the proxy with one `tool_output_roots` value,
change it via hot reload, and confirm the very next `save_to_file` call
is validated against the new roots without restarting the process.

**Acceptance Scenarios**:

1. **Given** the proxy is running with `tool_output_roots: ["/a"]`,
   **When** the config is hot-reloaded to `["/b"]`, **Then** a
   `save_to_file` call targeting `/a/...` is rejected and one targeting
   `/b/...` succeeds, without a restart.
2. **Given** `tool_output_max_bytes` is lowered via hot reload, **When**
   the next call attempts to save a response larger than the new limit,
   **Then** it is rejected against the new limit, not the one in effect
   at process start.

---

### User Story 4 - A failed save still produces an audit trail (Priority: P2)

An agent attempts a `save_to_file` call that fails (path outside the
whitelist, file already exists without `save_overwrite`, response too
large). The operator needs this to show up in the tool-call history and
activity feed like any other failed call — not vanish silently, which
would make troubleshooting and security review impossible.

**Why this priority**: A whitelist violation attempt is exactly the kind
of event an operator most wants visibility into; silently dropping it
from the audit trail defeats the purpose of having the whitelist be
observable at all.

**Independent Test**: Trigger a save failure (e.g. `save_overwrite:
false` against an existing file) and confirm a tool-call record is
persisted with the error text, session stats are updated, and an activity
event is emitted with `status: "error"` — exactly as for any other failed
tool call.

**Acceptance Scenarios**:

1. **Given** a `save_to_file` call fails validation, **When** the handler
   completes, **Then** `RecordToolCall`, `UpdateSessionStats`, and the
   activity-completed event all still fire, with the error text captured
   on the record.
2. **Given** a `save_to_file` call succeeds, **When** token metrics are
   recorded, **Then** `OutputTokens` reflects the size of the short
   envelope actually returned to the caller, not the size of the full
   upstream body that was diverted to disk — otherwise the tool-call
   history would over-report tokens the caller never actually received.

### Edge Cases

- **Deepest-existing-ancestor resolution for symlinked-but-not-fully-materialized paths**: a legitimate root or target can live under a path where only a prefix currently exists as a symlink (e.g. macOS's `/tmp` → `/private/tmp`, where `/tmp` exists but a deeper path segment doesn't yet). Resolution walks up to the deepest existing ancestor, resolves *that* through `EvalSymlinks`, and rejoins the remaining (not-yet-existing) suffix — a legitimate root under such a path must not be rejected merely because the full path doesn't exist yet. The root itself is then created (`MkdirAll`) before it is opened, so a configured root that doesn't exist yet at startup works end-to-end, not just at the resolution-check stage.
- **Zero text blocks with `save_format: "text"`**: if the upstream result has at least one content block but none of them is non-empty text (e.g. only image/audio blocks, or a lone empty-string text block), saving in text format must not silently write a 0-byte file and report success — this is treated as a tool error. A result with zero content blocks at all is different: it writes an empty file and succeeds, since a genuinely empty upstream response is a valid result rather than text dropped by the filter.
- **Legacy non-`*mcp.CallToolResult` result types**: a small number of code paths produce a result that is not the standard `*mcp.CallToolResult` type. `save_format: "json"` still saves a `json.Marshal` of whatever value it is; `save_format: "text"` (which has no text blocks to extract from an arbitrary type) is a tool error rather than a silent no-op.
- **File and directory permissions**: files written by `save_to_file` are created `0600` and any directories created along the way are `0700` — the feature must not leave saved tool output world- or group-readable by default.
- **Case-sensitive root matching**: whitelist prefix matching is case-sensitive (no `EqualFold`), consistent with POSIX filesystem semantics; this is called out explicitly in the docs so operators on case-insensitive filesystems (default macOS, Windows) understand two differently-cased configured roots are treated as distinct.
- **Daemon-mode file ownership**: when mcpproxy runs as a background daemon, `save_to_file` writes as whatever user/filesystem context the daemon process runs under, not the interactive CLI caller — operators must point `tool_output_roots` somewhere that user can write.
- **A directory component under a root is itself a symlink**: because the write is confined through a single `os.Root` handle rather than plain path-based filesystem calls, a symlink sitting where `save_to_file` needs to create or traverse a directory is not followed — the write fails with a plain filesystem error rather than silently landing wherever the symlink points.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST expose a `save_to_file` string parameter (an absolute filesystem path) on `call_tool_read`, `call_tool_write`, and `call_tool_destructive`, alongside `save_format` (`"text"` default or `"json"`) and `save_overwrite` (boolean, default `false`).
- **FR-002**: The system MUST expose a `tool_output_roots` configuration list (absolute directory prefixes) and a `tool_output_max_bytes` cap. `save_to_file` MUST be rejected as a no-op feature (or, if a path is supplied anyway, as a config/tool error) when `tool_output_roots` is empty.
- **FR-003**: Every `save_to_file` target path MUST resolve to a location inside one of the configured roots after resolving symlinks on existing ancestors. The write MUST be confined through a single Go 1.25 `os.Root` handle, opened once by `Resolve` immediately after the whitelist match, and used exclusively (never a path string again) for every filesystem operation the write performs — see Security Considerations for exactly what boundary this establishes.
- **FR-004**: A target path that resolves to a configured root itself (no filename) MUST be rejected as `ErrInvalidPath`, not silently accepted as a write inside the root.
- **FR-005**: A configured root equal to the filesystem root (`/`) MUST be rejected at config-validation time with a clear error, since it can never satisfy the whitelist's prefix-match semantics.
- **FR-006**: `save_format: "text"` MUST write the concatenation of the result's text content blocks (the same blocks the response-limit truncator would otherwise truncate), not a placeholder for non-text blocks; if at least one content block is present but none of them yields non-empty text, the call MUST fail as a tool error rather than writing an empty file and reporting success. A response with zero content blocks at all writes an empty file and succeeds — a genuinely empty upstream response is a valid result, not text dropped by the text-block filter.
- **FR-007**: `save_format: "json"` MUST write the full `json.Marshal` of the tool result, including non-text content.
- **FR-008**: `tool_output_roots` and `tool_output_max_bytes` MUST be read live at call time (mirroring the existing tokenizer-model-resolution pattern), so a hot-reloaded config change takes effect on the very next call without a process restart.
- **FR-009**: A `save_to_file` call — success or failure — MUST still produce a tool-call record, update session stats, and emit the standard tool-call-completed activity event; a failed save's error text MUST be captured on the record.
- **FR-010**: Token metrics for a saved call MUST be recounted against the actual envelope (or error text) returned to the caller, not the full upstream body diverted to disk; the metrics MUST record that the response was diverted (`SavedToFile`) separately from whether it was truncated (`WasTruncated`).
- **FR-011**: Files written by `save_to_file` MUST be created with `0600` permissions; any directories created along the way MUST be `0700`.
- **FR-012**: An existing file at the target path MUST cause the call to fail unless `save_overwrite: true` is set; the write MUST be atomic (write-then-rename) so a failed or interrupted write never leaves a partially-written file at the final path.
- **FR-013**: The CLI (`mcpproxy call`) MUST expose equivalent `--save-to-file` / `--save-format` / `--save-overwrite` flags mirroring the MCP tool parameters.

### Key Entities

- **`ToolOutputRoots`** (`[]string`, config): the whitelist of absolute directory prefixes `save_to_file` is allowed to write under. Empty disables the feature entirely.
- **`ToolOutputMaxBytes`** (`int64`, config): the cap on a single `save_to_file` write; `0` uses the built-in default, negative is a config error.
- **`Target`** (`internal/outputfile`): the result of resolving a candidate path against the whitelist. `Path` (the resolved absolute path) and `Root`/`Rel` (the matched root and the path relative to it) are retained for display and logging only; the actual write goes exclusively through `Handle`, an already-opened `*os.Root` — opened, and identity-checked against the directory it names, once by `Resolve` itself, immediately after the whitelist match succeeds. The caller owns the handle's lifecycle (`Target.Close()`) and must close it after the write, success or failure.
- **`TokenMetrics.SavedToFile`** (`bool`, storage): recorded alongside the pre-existing `WasTruncated` field, so a persisted tool-call record can distinguish "response was truncated in place" from "response was diverted to a file," independently.

## Security Considerations

- **What the confinement design closes**: `Resolve` matches the target
  against the whitelist, creates the matched root if needed, opens it as a
  single `*os.Root` handle, and identity-checks that handle against the
  directory it names (`os.SameFile` on `handle.Stat(".")` vs a fresh
  `os.Lstat` of the same path) before returning. `Write` uses only that
  handle — never a path string — for every filesystem operation it
  performs. This closes: (a) a symlink or rename planted **inside** the
  root, at any path component the write needs to create or traverse,
  between the resolve step and the write; (b) a replacement of the root
  directory itself, or its own path, **after** the handle is open — once
  open, a later swap of the root's path cannot move the underlying file
  descriptor, so the write keeps operating on the original directory
  `Resolve` validated, wherever it now lives.
- **What it does not close**: replacing an **ancestor** of a configured
  root in the microseconds between `Resolve` resolving that ancestor's
  symlinks and opening the root is not a boundary this design defends —
  ancestors of a configured root are admin-controlled, and a same-user
  process able to win that specific race window could already write
  anywhere the mcpproxy process itself can write. Earlier drafts of this
  feature described the confinement in unconditional terms ("cannot walk
  the write outside the whitelist," "structurally close the gap, no matter
  what happens"); this section is the precise replacement for those claims.
- **Saved content is not validated or spotlighted**: the redaction stage
  (`applyOutputSanitisation`) still runs before a response is saved, so a
  saved file never contains a secret redaction would otherwise have
  stripped. Output-schema validation (Spec 056) and response spotlighting
  (Spec 054) do not run on the save path at all — a saved file holds the
  full, un-spotlighted upstream text as redaction left it. An agent that
  reads a saved file back must treat its contents as untrusted data, the
  same as it would any other unvalidated tool output; strict-mode
  output-schema blocking does not apply to a saved response.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A `save_to_file` call against a whitelisted root writes a file containing the full, byte-for-byte (for `json` format) or fully-concatenated-untruncated (for `text` format) response, regardless of how far the response exceeds `tool_response_limit`.
- **SC-002**: Every attempted path-shape escape (relative traversal, absolute path outside all roots, target equal to a root or to `/`) and every intermediate-component symlink swap is rejected 100% of the time, with zero filesystem writes outside the whitelist in any test scenario. The root-swap case (the configured root directory itself renamed aside and a symlink planted at its path, after `Resolve` has already opened its `os.Root` handle) is different: the write is confined to the directory `Resolve` validated (which may since have been moved), never redirected to an attacker-chosen directory — see Security Considerations (b).
- **SC-003**: A `tool_output_roots` or `tool_output_max_bytes` change via hot reload is honored by the very next `save_to_file` call, with no process restart.
- **SC-004**: A failed `save_to_file` call always leaves exactly the same audit-trail shape (tool-call record, session stats, activity event) as any other failed tool call — never a silent no-record failure.
- **SC-005**: An existing configuration that never sets `tool_output_roots` behaves identically to before this feature existed (the parameter is accepted but the call fails cleanly with a clear "not configured" error rather than attempting a write).

## Assumptions

- Go 1.25's `os.Root` API is available in the pinned toolchain and provides the intended confinement semantics (verified against the actual `go.mod` toolchain version used to build this feature).
- Operators are expected to whitelist directories they control and trust for agent-written output; `save_to_file` is not a general-purpose sandboxed filesystem — it is a whitelist, not a jail against a root-equivalent adversary.
- Only the secret-redaction pipeline stage (`applyOutputSanitisation`) runs on a response before `save_to_file` diverts it — the response-limit truncator, output-schema validation (Spec 056), and response spotlighting (Spec 054) never run on the save path at all (see Security Considerations for the implications of this).
- The response-limit truncator specifically is bypassed entirely for a saved call: there is no truncated text to fall back to, since the point of `save_to_file` is to deliver the full untruncated body to disk instead.

## Out of Scope

- Extending the activity-event schema with dedicated `saved_to` / `saved_bytes` fields (rather than relying on the existing free-form response/status fields); may follow as a separate change.
- A general-purpose sandboxed filesystem, or defending `tool_output_roots` against a root-equivalent (same-user) local adversary — see Security Considerations for the precise threat model this feature does and does not cover.
- A dedicated array-of-strings control for `tool_output_roots` in the Settings UI; it is configured in the JSON config file only for now (the UI's existing free-text controls would silently corrupt a `[]string` value on save).
- Case-insensitive root matching (`EqualFold`); root matching stays case-sensitive, independent of the underlying filesystem's own case sensitivity.
- Cleaning up intermediate directories a `save_to_file` write created if a later step in that same write fails; a half-created, empty directory chain left behind is an accepted residual.

## Commit Message Conventions *(mandatory)*

- Conventional-commit style, e.g. `feat(server): save tool output to file`.
- Do **not** add AI co-authorship trailers (`AGENTS.md`: "avoid AI co-author tags").

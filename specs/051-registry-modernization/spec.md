# Feature Specification: Registry Modernization (Canonical MCP Registry as Source of Truth)

**Feature Branch**: `051-registry-modernization`
**Created**: 2026-05-20
**Status**: Draft
**Input**: User description: "Modernise the MCP server registry layer to use the canonical `registry.modelcontextprotocol.io` v0 schema as the primary source of truth, so server discovery and installation use the official `server.json` shape (packages[], runtimeArguments[], packageArguments[], environmentVariables[], transport, remotes[]) end-to-end."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Canonical registry is the default search source (Priority: P1)

A user opens the "Add server from registry" flow and searches for a server (e.g., "sqlite", "github"). The first registry queried is the official Model Context Protocol registry (`registry.modelcontextprotocol.io`). Results from the official registry appear first and the system clearly identifies that they came from the canonical source. Community registries (Pulse, Fleur, Remote MCP Servers) are still searchable, but the official one is the default starting point and is listed first in the registry chooser.

**Why this priority**: The canonical registry is the authoritative source of MCP server metadata as of the v0.1 API freeze (October 2025) and schema `2025-12-11`. Today mcpproxy treats it as one of several equally-weighted catalogues and parses it with a placeholder that throws away the most important fields (`packages[]`, `remotes[]`). Until the canonical registry is the primary source, every other improvement is built on a stale foundation.

**Independent Test**: With default config, open the registry search UI and the registry list shows the official MCP registry first. Searching for a common term ("sqlite", "github", "filesystem") returns results from the official registry, each labeled with that registry's name. The same query against a community registry produces a comparable result set but is reached only by explicitly choosing that registry.

**Acceptance Scenarios**:

1. **Given** a fresh install with default configuration, **When** the user opens the registry chooser, **Then** the official MCP registry is listed first and is the default selection.
2. **Given** the user searches for a server name that exists in the official registry, **When** results are returned, **Then** entries from the official registry are present and each is attributed to that registry.
3. **Given** the user has not modified their config, **When** they search without explicitly choosing a registry, **Then** the search hits the official registry by default (other registries are reachable explicitly, not silently merged).
4. **Given** the official registry is unreachable, **When** the user searches, **Then** the UI surfaces a clear error for that registry and does not silently fall back to a different one.

---

### User Story 2 - Schema-aware installation from canonical `server.json` (Priority: P1)

When the user picks a server from the official registry, mcpproxy installs it using the full canonical `server.json` payload — not a guess derived from a flattened install command string. Concretely:

- The user's selected `packages[]` entry deterministically picks the runner: `npm → npx -y <identifier>`, `pypi → uvx <identifier>`, `oci → docker run … <identifier>`, `nuget → dnx <identifier>`.
- `runtimeArguments[]` are placed in the runner command **before** the package identifier; `packageArguments[]` go **after** it; both honor positional vs. named argument shapes and templated value placeholders (e.g., `{source_path}`).
- `environmentVariables[]` are mapped into the new server's `env`. Variables with `isSecret: true` trigger a secret prompt at install time; non-secret variables are filled with their provided defaults (or prompted if no default exists and the variable is required).
- `transport.type` (`stdio`, `streamable-http`, `sse`) maps directly to mcpproxy's `protocol` field on the resulting server config.
- For servers with `remotes[]` (no local package), the user picks a remote entry; required headers — including ones with secret placeholders like `Authorization: Bearer {api_key}` — produce a header-value prompt at install time, and the resolved header is stored in the server's header configuration.

**Why this priority**: The current placeholder parser blindly unmarshals the canonical response into a flat `ServerEntry`, dropping every structured field the official schema added (packages, runtimeArguments, packageArguments, environmentVariables, transport, remotes, headers). As a result, the UI today has to guess transport from "is there an `installCmd` or a `url`" and cannot collect required env vars or headers. Without this story, story 1 only delivers a renamed source — the actual installation experience does not improve.

**Independent Test**:

- Pick a server from the official registry whose `server.json` has a single `npm` package with one positional runtime argument and one positional package argument: the resulting mcpproxy server config has `command: npx`, `args: ["-y", "<identifier>", "<package-arg>"]` with the runtime arg placed correctly between `-y` and the identifier (or in the documented position), and no fields are silently dropped.
- Pick a `pypi` server: result uses `uvx`. Pick an `oci` server: result uses `docker run` with the image.
- Pick a server requiring an `environmentVariables[]` entry with `isSecret: true`: the install flow blocks for input and the value lands in the server's `env`, not in `args`.
- Pick a server with only `remotes[]` and a header `Authorization: Bearer {api_key}`: the install flow prompts for `api_key`, the substituted full header value is stored, and the resulting server uses the matching transport (`streamable-http` or `sse`).

**Acceptance Scenarios**:

1. **Given** a canonical-registry server with one `npm` package, one runtime argument, and one package argument, **When** the user installs it, **Then** the resulting server config places the runtime argument before the package identifier and the package argument after it, exactly as the schema specifies.
2. **Given** a canonical-registry server with `environmentVariables[]` including an entry marked `isSecret`, **When** the user installs it, **Then** the secret value is prompted at install time and stored in `env`; non-secret variables are filled from their defaults without prompting unless required-without-default.
3. **Given** a canonical-registry server with `transport.type: "streamable-http"`, **When** the user installs it, **Then** the resulting mcpproxy server has `protocol: "http"` (or the project's equivalent) and not `stdio`.
4. **Given** a canonical-registry server with only `remotes[]` and a header containing a `{placeholder}`, **When** the user installs it, **Then** the placeholder is prompted at install time and the final header value (with the placeholder substituted) is stored on the server.
5. **Given** templated values appear in `runtimeArguments[]` or `packageArguments[]` (e.g., `{source_path}`), **When** the user installs the server, **Then** the user is prompted for the value and it is substituted into the final command, not stored verbatim with curly braces.

---

### User Story 3 - Docker isolation prefers OCI packages when available (Priority: P2)

When mcpproxy's Docker isolation is enabled (either by config or by first-run policy on hosts that have Docker), and the selected server's `server.json` lists both an `oci` package and an `npm` (or `pypi`) package, the installer defaults to the `oci` package. The user can still explicitly override and choose the alternative package, but the default selection matches their isolation policy.

**Why this priority**: Docker isolation is a core safety story for mcpproxy. If the user has it enabled but the registry picks an `npm` runner anyway, the isolation guarantee silently degrades. This is high-value but depends on US2's package selection plumbing, so P2.

**Independent Test**: Enable Docker isolation. From the canonical registry, install a server that exposes both `oci` and `npm` packages. The resulting mcpproxy server uses `docker run` with the `oci` image. Repeat with Docker isolation disabled — the installer selects the `npm` package as before (or honors the user's explicit choice).

**Acceptance Scenarios**:

1. **Given** Docker isolation is enabled and the selected server has both `oci` and `npm` packages, **When** the user installs without picking a package explicitly, **Then** the `oci` package is chosen and the resulting server uses `docker run` with that image.
2. **Given** Docker isolation is disabled and the selected server has both `oci` and `npm` packages, **When** the user installs without picking a package explicitly, **Then** the previously-established default (npm/pypi runner) is chosen.
3. **Given** the user explicitly picks a non-OCI package in the install UI even when Docker isolation is enabled, **When** they confirm the install, **Then** their explicit choice is honored.

---

### User Story 4 - Unified, schema-aware payload across all registries (Priority: P2)

A developer adds a new community registry source. They only need to write a mapper that produces the canonical structured payload (packages[], runtimeArguments[], packageArguments[], environmentVariables[], transport, remotes[]); they do **not** need to invent a new shape or extend the install flow. Existing community sources (Pulse, Fleur, Remote MCP Servers) are routed through the same normaliser, so the frontend and CLI receive one consistent shape regardless of source.

**Why this priority**: This collapses install logic into a single code path and prevents the "every parser invents its own shape" drift that exists today (each parser stuffs its results into a flat `ServerEntry`, dropping structured data). It depends on US1+US2 being in place and is internal scaffolding, so P2.

**Independent Test**: Search the same query against the official registry and a community registry (e.g., Pulse). The returned per-server payload has the same shape (same field names, same nesting) in both responses, just with different content. The frontend's "add server from this entry" flow uses identical code paths regardless of which registry the entry came from.

**Acceptance Scenarios**:

1. **Given** a search hits the official registry, **When** the response is returned to the UI/CLI, **Then** each entry carries a structured payload with `packages[]`/`remotes[]`/`transport`/`environmentVariables[]` (when the source data has them).
2. **Given** a search hits a community registry (Pulse, Fleur, Remote MCP Servers), **When** the response is returned, **Then** each entry carries the same structured payload shape, with fields populated as far as the source data allows and explicitly empty otherwise (no shape divergence between registries).
3. **Given** a developer adds a new community registry, **When** they implement only the mapping function to the canonical payload, **Then** install, env-var prompting, secret prompting, and transport selection all work without further changes to the UI or installer.

---

### User Story 5 - Remove demoted sources cleanly (Priority: P2)

`azure-mcp-demo` (a self-described demo deployment superseded by the official registry) and the Smithery fallback are removed from the default registry list. The `docker-mcp-catalog` source — which today points at Docker Hub's repository-listing API and returns image metadata only (no transport, no run args, no env) — is either reworked to consume the Docker MCP Toolkit's actual API and emit the canonical payload, or removed from the defaults. Users with these IDs already saved in their existing config are not broken: the system handles them gracefully (does not crash, gives a clear error or empty result), and surfacing a migration hint is acceptable but not required for v1.

**Why this priority**: Carrying demonstration-grade or shape-limited sources in the default list misleads users into thinking they are first-class. It also dilutes the canonical registry's "this is the source of truth" message. This is cleanup work that depends on US1 (canonical is primary) but is otherwise independent.

**Independent Test**: On a fresh install, the default registry list does not include `azure-mcp-demo` or a Smithery fallback. The `docker-mcp-catalog` source is either absent (preferred) or, if retained, returns entries with the canonical structured payload (not flat image metadata). A pre-existing user config that still references the removed IDs loads without crashing and produces a clear, recoverable message when those registries are queried.

**Acceptance Scenarios**:

1. **Given** a fresh install with default configuration, **When** the user inspects available registries, **Then** `azure-mcp-demo` and any Smithery fallback are absent.
2. **Given** the `docker-mcp-catalog` source remains in the defaults, **When** the user searches it, **Then** results carry the canonical structured payload (or, if it was removed, the source is not offered at all).
3. **Given** an existing user config still references a removed registry ID, **When** mcpproxy loads, **Then** it does not crash; queries to that ID yield a clear, non-fatal error.

---

### Edge Cases

- **Server has multiple packages of the same type**: When `packages[]` contains more than one entry of the user's chosen registryType (or when Docker isolation picks `oci` and there are two `oci` entries), the user is given an explicit pick (e.g., first listed by default with a way to choose another), not a silent first-wins.
- **Server has neither packages nor remotes**: The entry is shown for discoverability but its install action is disabled with a clear "this server is incomplete in its registry record" explanation; mcpproxy does not synthesize a runner.
- **`transport.type` is unknown / future value**: The install flow surfaces a clear "unsupported transport" message and does not coerce to a default.
- **`environmentVariables[]` defines a required, non-secret variable with no default**: The install flow prompts the user for it (the same UX as a secret, minus masking).
- **A header placeholder names a value that overlaps with an `environmentVariables[]` name**: The system treats them as the same logical secret — the user is prompted once and the value is reused. If they are meant to be different, the spec leaves this as an explicit assumption in the per-server `server.json`; mcpproxy does not invent a renaming policy.
- **Canonical registry returns a paginated response**: The search flow handles pagination transparently up to the user's `limit`. The UI shows the actual returned count and does not claim more results than were rendered.
- **A `runtimeArguments[]` or `packageArguments[]` entry has a templated value but no description**: The prompt asks for the value by its placeholder name; this is acceptable for v1 (improving the prompt copy is a UX iteration, not a spec requirement).
- **Community registry's source data is missing structured detail (e.g., no `packages[]`)**: The normaliser emits an entry with empty structured fields; the install action surfaces a clear "minimal metadata — please verify before installing" warning.
- **OCI default override + Docker daemon unavailable**: If Docker isolation is enabled (so OCI is preferred) but the Docker daemon is not actually reachable at install time, the installer surfaces this clearly and offers to install the non-OCI alternative if one exists.
- **A user already has a saved server originally added via the old flat-shape path**: Their existing server config is not migrated or rewritten by this feature. Only newly-added servers go through the new path.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The official MCP registry (`registry.modelcontextprotocol.io`, v0 API, `2025-12-11` schema) MUST be the first entry in the default registry list and the default-selected source in the search UI.
- **FR-002**: The system MUST parse the canonical `server.json` response into a structured payload that preserves `packages[]`, `runtimeArguments[]`, `packageArguments[]`, `environmentVariables[]`, `transport`, `remotes[]` (including `headers[]`), and the top-level descriptive fields (name, description, version, repository).
- **FR-003**: The structured payload returned to the UI/CLI from a search MUST be the same shape regardless of which registry produced it; community registries MUST be funneled through a single normaliser that emits this shape, leaving any unsupplied structured fields explicitly empty (not omitted as a different shape).
- **FR-004**: When installing a server with one or more `packages[]` entries, the system MUST deterministically choose the runner from `registryType`: `npm → npx -y <identifier>`, `pypi → uvx <identifier>`, `oci → docker run … <identifier>`, `nuget → dnx <identifier>`. Unrecognised `registryType` values MUST produce a clear "unsupported package type" error rather than a coerced default.
- **FR-005**: `runtimeArguments[]` MUST be placed in the resulting command **before** the package identifier; `packageArguments[]` MUST be placed **after** it. Argument shape (positional vs. named) MUST be honored as described in the schema.
- **FR-006**: Templated argument values (e.g., `{source_path}`) and templated header values (e.g., `Bearer {api_key}`) MUST trigger an install-time prompt for each unique placeholder; the user-provided value MUST be substituted into the final stored command/header. No curly-braced placeholders MUST remain in stored server configuration.
- **FR-007**: `environmentVariables[]` MUST be mapped onto the new server's `env`. Entries with `isSecret: true` MUST trigger a secret prompt with masked input. Required entries lacking a default MUST be prompted (without masking unless `isSecret`). Optional entries with defaults MUST be filled without prompting.
- **FR-008**: `transport.type` MUST be translated to the corresponding mcpproxy `protocol` value: `stdio → stdio`, `streamable-http → http` (or the project's HTTP designator), `sse → sse`. Unknown transport types MUST surface a clear unsupported-transport error.
- **FR-009**: For servers exposing only `remotes[]` (no `packages[]`), the user MUST be able to pick the remote entry to install; the resulting mcpproxy server MUST use the chosen remote's transport and store all required headers with placeholders fully substituted.
- **FR-010**: When mcpproxy's Docker isolation is enabled and the selected server's `packages[]` contains both an `oci` entry and at least one non-OCI entry, the installer MUST default to the `oci` entry. The user MUST be able to explicitly override this default before confirming the install.
- **FR-011**: When Docker isolation is enabled, OCI is the chosen package, and the Docker daemon is not reachable at install time, the installer MUST surface a clear actionable error and MUST offer to fall back to a non-OCI package if one exists in `packages[]`.
- **FR-012**: The `azure-mcp-demo` registry entry MUST be removed from the default registry list. Any Smithery fallback in the registry data layer MUST be removed.
- **FR-013**: The `docker-mcp-catalog` entry MUST be either (a) removed from the default registry list, or (b) reworked to source from the Docker MCP Toolkit's actual API and to emit the canonical structured payload. A flat-image-metadata path MUST NOT remain as a default. (The choice between (a) and (b) is an implementation decision recorded in the plan.)
- **FR-014**: User configurations that still reference a removed registry ID MUST NOT cause mcpproxy to fail to start; queries against missing/removed registries MUST produce a clear, non-fatal error.
- **FR-015**: When the canonical registry is unreachable, the search flow MUST surface a clear per-registry error rather than silently falling back to a different registry or merging stale community data into the result set.
- **FR-016**: Pagination from the canonical registry MUST be handled transparently up to the user-supplied `limit`; the UI MUST display the actual rendered count and MUST NOT misreport total counts.
- **FR-017**: Adding a new community registry source MUST require implementing only a source-specific mapper that emits the canonical structured payload; no changes to the install flow, env-var prompting, secret prompting, or transport selection MUST be necessary.
- **FR-018**: The user's existing saved servers (added before this feature shipped) MUST NOT be migrated, rewritten, or revalidated by this feature. The new code path applies only to newly-added servers.
- **FR-019**: The system MUST clearly label which registry each search result came from in both the UI and the CLI/MCP-tool output, so users can tell canonical vs. community results apart at a glance.
- **FR-020**: The `parseOpenAPIRegistry` parser in `internal/registries/search.go` (currently a placeholder that round-trips the response through a flat `ServerEntry`, dropping `packages[]` and `remotes[]`) MUST be replaced by a schema-aware parser that emits the new structured payload.

### Out of Scope (v1)

The following are deliberately deferred and MUST NOT be implemented in this feature:

- Publishing mcpproxy itself, or any first-party server, to the official MCP registry. (Tracked separately.)
- The narrow Issue #483 hotfix for the existing `docker-mcp-catalog` `installCmd` behavior — already being shipped on its own branch (`fix/483-docker-catalog-installcmd`). This spec subsumes and supersedes it once landed.
- Redesigning the "Add server from registry" UI/UX. The existing flow continues to be used; this feature only feeds it richer, schema-correct data.
- Migrating servers that the user already has saved from the old, flat-shape path to the new structured-shape path.
- Caching or offline mode for registry responses (the canonical registry is queried live, same as today).
- Server-side rate-limiting or quota handling beyond what the registry's HTTP responses already convey.
- Reputation, popularity, signing, or trust-scoring of registry entries. Quarantine semantics (Spec 032) continue to apply unchanged to newly-installed servers.

### Key Entities *(include if feature involves data)*

- **RegistrySource**: A configured source of MCP server metadata (canonical or community). Attributes: id, name, description, URL, search endpoint, attribution label, protocol/parser identifier. The canonical registry is one such RegistrySource and is first in the default list.
- **RegistryServerEntry (structured)**: A single MCP server as returned by a search, in the canonical shape. Attributes: id, name, description, version, repository/source-code URL, `packages[]`, `remotes[]`, `transport` (when expressed at the server level), top-level `environmentVariables[]` (when applicable), originating-registry attribution.
- **Package**: One installable package entry inside a server's `packages[]`. Attributes: `registryType` (npm/pypi/oci/nuget/…), identifier, `runtimeArguments[]`, `packageArguments[]`, `environmentVariables[]`, optional package-scoped transport details.
- **Remote**: One remote-server entry inside a server's `remotes[]`. Attributes: transport type, URL, `headers[]` (each with name, value, and any embedded placeholders).
- **EnvironmentVariable**: A declared input the server expects in its environment. Attributes: name, description, default value (optional), required (bool), `isSecret` (bool), choices (when constrained).
- **TemplatedValue**: Any value (in a runtime/package argument or in a header) containing one or more `{placeholder}` tokens. Attributes: literal template, set of placeholder names. Each placeholder name is what the install-time prompt asks for.
- **Install plan**: The resolved, ready-to-store mcpproxy server configuration derived from one RegistryServerEntry plus user choices: which package or remote, which placeholders resolved to what values, which environment variables resolved to what values. The install plan, not the raw registry entry, is what gets written to the user's mcpproxy server config.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With default configuration, 100% of searches with no explicit registry choice hit the canonical MCP registry first.
- **SC-002**: For canonical-registry servers that declare `packages[]`, the resulting mcpproxy server config is reconstructible from the `server.json` payload alone — i.e., no information needed to actually run the server has been dropped during parsing.
- **SC-003**: For a representative test set of canonical-registry servers covering all four supported `registryType` values (npm, pypi, oci, nuget), each installs to the correct runner without manual editing of the resulting `command`/`args`/`env`.
- **SC-004**: For canonical-registry servers declaring required `environmentVariables[]` or `headers[]` with placeholders, 100% of those values are collected from the user at install time, and 0% remain as literal `{placeholder}` strings in the stored config.
- **SC-005**: With Docker isolation enabled, for canonical-registry servers offering both `oci` and at least one non-OCI package, the default install path selects the OCI image 100% of the time (overridable by the user).
- **SC-006**: The structured payload shape returned to UI/CLI is identical across every default registry source (canonical and community). Adding a new community registry requires no changes to the install flow, only a new mapper.
- **SC-007**: Removing `azure-mcp-demo` and any Smithery fallback from the defaults does not cause any pre-existing user config to fail to load.

## Assumptions

- The canonical registry's v0 API and `2025-12-11` schema remain stable for the duration of this feature's implementation; if the schema bumps before merge, the parser is updated to the new version rather than versioned in parallel (the canonical registry is single-version-at-a-time for our purposes in v1).
- The user's existing "Add server from registry" UI is sufficient surface area to present package choice, transport choice, env-var prompts, header-placeholder prompts, and secret-input masking. No new screens or routes are introduced; the existing modal/flow is enriched with the data it already needed.
- `npx -y <identifier>` (npm), `uvx <identifier>` (pypi), `docker run <image>` (oci), `dnx <identifier>` (nuget) are the canonical runners. Anything more elaborate (extra flags, env-passing for docker run) is composed by the installer from the schema's runtimeArguments/packageArguments/environmentVariables; the runner choice itself is purely a function of `registryType`.
- For `oci` packages, the installer integrates with mcpproxy's existing Docker isolation runner (see `docs/docker-isolation.md`) rather than constructing `docker run` strings independently. This keeps Docker security policy in one place.
- The mapping from `transport.type` to mcpproxy's `protocol` field uses the project's existing enumeration; if a value is introduced upstream that mcpproxy does not yet support, the spec deliberately requires a clear error rather than a silent coercion, so users are never surprised by a wrong-transport server.
- Quarantine semantics (Spec 032) apply unchanged: newly-added servers from any registry, canonical or community, still go through the existing security quarantine flow before their tools become callable.
- Community-registry parsers (Pulse, Fleur, Remote MCP Servers) keep their existing bespoke parsing logic; only their **output** is reshaped through the normaliser. Rewriting those parsers wholesale is out of scope.
- The current `parseOpenAPIRegistry` placeholder is intentionally minimal because at the time of writing the canonical registry did not yet expose `packages[]`/`remotes[]` consistently. Replacing it now (per FR-020) is the cleanup payment for that earlier shortcut.
- "Default registry list" refers to `Registries: []RegistryEntry{...}` in `internal/config/config.go` (currently lines 697-742); reordering and demotion happen there.
- The frontend's `addServerFromRepository` (currently in `frontend/src/services/api.ts:636-662`) currently flattens to a single `command`/`args` pair and a guess at protocol from `installCmd` vs. `url`. The new structured payload makes this guesswork unnecessary, but the function's signature change is implementation detail — this spec just requires that the function be fed the canonical payload and produce a correct mcpproxy server config.

## References

- Canonical registry schema: <https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json>
- Generic `server.json` documentation: <https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/server-json/generic-server-json.md>
- Live API sample (returns canonical shape with `packages[]` and `remotes[]`):
  `curl -s "https://registry.modelcontextprotocol.io/v0/servers?search=sqlite&limit=3"`
- Existing code anchors:
  - `internal/registries/search.go` (placeholder `parseOpenAPIRegistry` at lines 227-251; per-registry parsers throughout)
  - `internal/registries/types.go` (flat `ServerEntry` to be supplemented by structured payload)
  - `internal/config/config.go:697-742` (default registry list)
  - `frontend/src/services/api.ts:636-662` (`addServerFromRepository` consuming the flat shape today)
- Related in-flight hotfix being subsumed: branch `fix/483-docker-catalog-installcmd` (Issue #483).

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #<issue>` — links the commit to the issue without auto-closing.
- ❌ **Do NOT use**: `Fixes #<issue>`, `Closes #<issue>`, `Resolves #<issue>`.

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors. (This also matches the repo-specific rule of no Claude git attribution in mcpproxy-go.)

### Example Commit Message
```
feat: canonical MCP registry as primary search source

Promotes registry.modelcontextprotocol.io to the first entry in the
default registry list and replaces the placeholder parseOpenAPIRegistry
with a schema-aware parser that preserves packages[], remotes[],
runtimeArguments[], packageArguments[], environmentVariables[],
transport, and headers[].

## Changes
- Default registries reordered; canonical MCP registry first
- Schema-aware parser for the 2025-12-11 server.json shape
- Structured payload normaliser shared by canonical + community sources
- Installer chooses runner from packages[].registryType; OCI preferred
  when Docker isolation is enabled
- Install-time prompts for required env vars, secrets, and templated
  placeholders in args and headers
- azure-mcp-demo and Smithery fallback removed from defaults

## Testing
- Table tests for each registryType → runner mapping
- Parser tests against fixtures captured from the live canonical API
- Frontend tests for install flow with secret/non-secret env prompts and
  header placeholder substitution
- E2E sweep that installs one server of each supported registryType
```

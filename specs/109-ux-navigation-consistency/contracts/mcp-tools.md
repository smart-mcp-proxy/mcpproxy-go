# Contract: MCP built-in tool changes (Spec 109)

Small by design. MCP is a client surface with per-tool authorization. Search and server-health reads are available to their authorized callers; `quarantine_security` is administrator-only for all operations, including inspection, under Spec 028 FR-009 and Spec 108 FR-016. Scoped review reads use the filtered REST routes instead. Frozen tool-surface goldens (`internal/server/testdata/*.golden.json`, `toolslist_goldens/`) change only where listed, and each PR declares the files it changes.

| Tool | Change | PR | Golden impact |
|---|---|---|---|
| `upstream_servers` `list` | each server's `health` gains `status`, `usable`, `actions` (shared struct) | 109-c | list-output goldens only (the tool schema is unchanged) |
| `quarantine_security` `inspect_quarantined`, `inspect_tools` | each tool gains `tier`, `annotations`, `scan_verdict` with the names and values of `GET /servers/{id}/review`; changed tools gain `previous` + `diff`; any server `command`/`url` in the output comes from the same redacted review composer (FR-021), never raw config. **The live inspection is kept**: when the composer reports `definitions_captured: false`, `inspect_quarantined` still performs today's temporary-exemption connect + `ListTools()` (`internal/server/mcp.go` ~4879–5018) and decorates those live tools with `tier` from `contracts.AnnotationTier` over their live annotations and `scan_verdict: "not_scanned"`, plus `definitions_source: "live"` (`"captured"` otherwise) — it never degrades to the composer's `tools: []`. `server_summary.scan` carries the same `coverage`, `tools_scanned` and `unscanned_tools` as the REST review (fix-review-screen) only on the captured branch (`definitions_source: "captured"`); the live branch returns no `server_summary` (only a `scan_status` line), so a client must not read `scan.coverage` from it and should treat it as not captured. Captured tools carry `default_allowed` (the composer value); live tools carry `default_allowed: false`; `inspect_tools` does not carry it (fix-review-defaults, D43.7). No `quarantine_security` operation approves a server, and `approve_tool` and `approve_all_tools` approve only what they name | 109-f | output goldens only |
| `search_servers` | `registry` becomes optional (omitted = all sources through `registries.SearchAll`); results gain `title`, `publisher`, `verified`, `official`, `popularity`, `source`, in the FR-060 order (research D37; the tool description text still says "official-first" and is frozen by the schema goldens, so changing it is a follow-up), and `from_cache: true` when a source's cached listing answered (its `unavailable[]` entry then carries `fallback` and `cached_at`, D35); descriptions say "catalog" and "catalog source". The retained `tag` parameter accepts only an empty value; a non-empty value returns a visible error because catalog entries carry no tags. | 109-j | **schema golden changes** (`registry` no longer `required`; description text). Declared in the 109-j PR body |
| `list_registries` | description wording "catalog sources" | 109-j | schema golden (description text) |

## Unchanged, deliberately

- No attention tool. Every attention fix is an operator action; agents already get per-server `health` (parity matrix row 1).
- No server approval: `quarantine_security` still cannot unquarantine (tool description already says so; contradiction register X9).
- No clients tool (Spec 108 adds `profiles list_clients`).
- `retrieve_tools`, `call_tool_*`, `describe_tool`, `code_execution`: no change from this spec.

## Tests

- `internal/server/quarantine_inspect_review_parity_test.go`: for the review fixture, the MCP `inspect_quarantined` per-tool `tier`/`annotations`/`scan_verdict` equals the REST review payload field by field; for a quarantined server whose definitions were never captured, `inspect_quarantined` still returns the live tool list (non-empty, `definitions_source: "live"`, `scan_verdict: "not_scanned"`) while `GET /servers/{id}/review` returns `definitions_captured: false, tools: []` (T076a).
- `internal/server/search_servers_all_sources_test.go`: omitted `registry` → the same order as `GET /catalog/search` on the fixture registries; one source timing out → results from the others plus an `unavailable` note in the text result.

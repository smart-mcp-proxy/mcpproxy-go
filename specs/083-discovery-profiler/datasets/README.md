# Spec 083 — Discovery-profiler datasets

Versioned, frozen fixtures for the discovery-effectiveness profiler
(`bench/`). **Immutable once committed — a refresh is `*_v3.*`, never an edit
of a `*_v2.*` file** (Spec 065 CN-002 precedent).

| File | What it is | Committed? |
|------|------------|------------|
| `corpus_v2.tools.json` | Schema-bearing frozen corpus: **45 tools** from the 7 no-auth Spec-065 reference servers, exported WITH full JSON input schemas — the universe for all encoding-arm comparisons (FR-005/006) | yes |
| `result_fixtures_v1.json` | Deterministic tool-call outputs for the TOON-results arm (FR-007) — see [result_fixtures_v1.json](#result_fixtures_v1json) below | yes |
| `fetch_fixture.html` | Frozen HTML document served over an ephemeral local HTTP server during result-fixture capture so `fetch:fetch` has a deterministic input | yes |
| `livemcptool_snapshot/` | Frozen LiveMCPTool corpus + `ATTRIBUTION.md` (Apache 2.0) — token/scale measurement (FR-011) | yes (planned, T029) |

ToolRet is **never** committed (license unstated upstream, FR-013): it is
fetched at runtime by `scripts/fetch-toolret.sh` into `bench/results/cache/`
(gitignored).

## corpus_v2.tools.json

- **Tool count**: 45 (all 7 servers connected; no reduced-corpus caveat).
  Every tool carries a non-empty JSON input schema; 3 tools
  (`filesystem:list_allowed_directories`, `memory:read_graph`,
  `sqlite:list_tables`) are legitimately parameter-less and carry
  `{"properties":{},"required":[],"type":"object"}`.
- **Capture date**: 2026-07-14 (version string `corpus_v2@2026-07-14`).
- **Source servers**: the committed Spec-065 snapshot config
  (`specs/065-evaluation-foundation/datasets/snapshot-servers.config.json`):
  filesystem, git, memory, sqlite, fetch, time, sequential-thinking — no-auth,
  license-clean reference servers (Spec 065 CN-001/005 precedent).
- **License**: own capture of public no-auth reference-server metadata; the
  file itself is repo-licensed.
- **Canonical form**: tools sorted by `tool_id`; ALL object keys sorted
  (`jq -S`); schemas are the exact `ParamsJSON` bytes the production Bleve
  index ingests. `bench.LoadCorpusV2` compacts each schema at load so the
  in-memory bytes are canonical-compact regardless of on-disk pretty-printing.
- **Provenance note**: the export reads the Bleve index
  (`scripts/gen-corpus-v2-dump`), NOT `GET /api/v1/tools` — that REST endpoint
  currently serves stub schemas (`{"type":"object","properties":{}}`) because
  the supervisor StateView never parses `ParamsJSON`
  (`internal/runtime/supervisor/supervisor.go`, "TODO: Parse ParamsJSON").
  The index is the authoritative record of what the retrieval funnel ingests,
  which is exactly the text arm comparison must measure (research D4/D7).

### Regenerate (maintainers only — cuts corpus_v3, never edits v2)

```bash
./scripts/gen-corpus-v2.sh          # ~2-4 min: builds mcpproxy, boots the
                                    # snapshot config on 127.0.0.1:8093 with a
                                    # throwaway --data-dir, waits for 45 tools,
                                    # exports + canonicalizes, cleans up
# env overrides: PORT, EXPECTED_TOOLS, WAIT_SECS, OUT
```

Requirements: `jq`, `node`/`npx`, `uv`/`uvx`, network (first run downloads the
reference servers). Transient npx/uvx failures are retried once; a permanently
down server produces a reduced corpus with an explicit warning — record the
reduced count here before committing such a corpus.

## result_fixtures_v1.json

Deterministic tool-call outputs for the TOON-results arm (`toon_results`,
FR-007, research D10): 6 payloads captured once THROUGH the proxy from 5 of
the 7 snapshot reference servers, classified tabular vs non-tabular, then
frozen. License-clean — every payload is the output of our own reference
servers over our own seed data.

- **Fixture ID**: `result_fixtures_v1` · **captured**: 2026-07-14 · all 5
  targeted servers were up (no reduced-capture caveat; memory and
  sequential-thinking are intentionally not sampled — no deterministic
  read-only output).
- **Shape**: `{fixture_id, captured, results:[{tool_id,
  payload_class_hint, payload}]}`; results sorted by `tool_id`, ALL object
  keys sorted (`jq -S`-canonical), trailing newline.
- **Classification rule** (`payload_class_hint`): `tabular` = the payload is a
  uniform JSON array of flat objects (same scalar-valued keys per row);
  everything else is `non_tabular`. Split: 2 tabular
  (`sqlite:read_query`, `sqlite:list_tables`), 4 non-tabular
  (`filesystem:list_directory`, `git:git_log`, `time:get_current_time`,
  `fetch:fetch`).
- **Payload encoding**: structured outputs are stored as native JSON values
  (sqlite rows, time object); plain-text/markdown outputs are stored as JSON
  strings. The sqlite server returns rows as a **Python-repr** string
  (`[{'id': 1, ...}]`); it is parsed to a real JSON array at capture time so
  the arm can encode identical structures as compact JSON vs TOON.

### Capture procedure (T037 — one-time; a refresh is `result_fixtures_v2`)

1. **Seed deterministic inputs** (the snapshot servers are read against
   fixed local state):
   - sqlite seed DB at `/tmp/mcpproxy-corpus-snapshot/snapshot.db` (the path
     hard-wired in the snapshot config): one `servers` table with 7 rows
     mirroring the reference-server roster
     (`id,name,protocol,tool_count,quarantined`).
   - filesystem fixture dir `/tmp/result-fixtures-083/` (under the
     filesystem server's `/tmp` root): `config.json`, `data.csv`,
     `notes.txt`, `subdir/`.
   - `python3 -m http.server 8096` serving this directory's committed
     `fetch_fixture.html` (the fetch server speaks HTTP only — no `file://`
     / `data:` — so the committed document is served locally).
2. **Boot the snapshot proxy** from the worktree exactly as
   `gen-corpus-v2.sh` does, but on port **8095**: build `./cmd/mcpproxy`,
   copy the Spec-065 snapshot config (the proxy persists config changes back
   to the file it serves), `MCPPROXY_API_KEY=eval-corpus-snapshot mcpproxy
   serve --config <copy> --listen 127.0.0.1:8095 --data-dir <tempdir>`, wait
   until `GET /api/v1/tools` reports 45 tools.
3. **Call each tool through the proxy** via `POST /api/v1/tools/call`.
   Direct upstream names are rejected (Spec 018) — wrap in the intent
   variant: `{"tool_name":"call_tool_read","arguments":{"name":
   "<server>:<tool>","args":{...},"intent":"..."}}`. Calls made:
   `filesystem:list_directory` (path=/tmp/result-fixtures-083) ·
   `sqlite:read_query` (`SELECT id, name, protocol, tool_count, quarantined
   FROM servers ORDER BY id`) · `sqlite:list_tables` ·
   `time:get_current_time` (timezone=UTC) · `fetch:fetch`
   (url=http://127.0.0.1:8096/fetch_fixture.html) · `git:git_log`
   (repo_path=<worktree>, max_count=3).
4. **Kill the proxy + HTTP server**, then **post-process** to strip
   nondeterminism:
   - wall-clock values → fixed: `time` `datetime` →
     `2026-07-14T12:00:00+00:00`; the 3 `git_log` `Date:` lines →
     `2026-07-14 12:00:00+00:00` (hashes/messages are immutable history of
     already-public commits and stay verbatim).
   - capture-host specifics → placeholders: the ephemeral fetch origin
     `http://127.0.0.1:8096` → `http://fixture.local`; assert no `/Users/…`
     or other host paths leaked into any payload.
   - sqlite Python-repr strings → parsed JSON arrays (see above).
   - canonicalize: sort results by `tool_id`, sort all object keys, write
     with 2-space indent + trailing newline; verify `jq -S` idempotence.

If a reference server is down at capture time, capture from the rest and
record the reduced tool list here before committing.

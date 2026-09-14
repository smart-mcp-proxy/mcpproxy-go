# Contract: refusal shapes per surface (FR-010, FR-004, FR-001, FR-012)

Caller in every row: agent token `allowed={a}`, `perms={read}`, no pin, unless stated. Fixture A = `{a, b, a__b}` with sentinel strings in every hidden artifact; fixture B = `{a}`. "≡" means byte-identical after `normalizeScopeResponse`.

## Retrieve surface (`/mcp`, `/mcp/call`, `/mcp/code`)

| operation | hidden target (A) | nonexistent target (B) | contract |
|---|---|---|---|
| `retrieve_tools q` | hidden hits excluded **before** top-K | — | results, `total`, `filter_diagnostics`, `debug.total_indexed_tools`, `usage_summary.top_tools`, `session_risk` ≡ across A/B; no sentinel |
| `describe_tool b:read` (definition) | not-found + suggestion over authorized corpus | not-found + same suggestion | ≡ |
| `describe_tool b:read` (check) | already one-policy | — | ≡ (regression) |
| `call_tool_read b:t` (no client for target) | scope refusal; `Available servers:` ⊆ effective scope | same | ≡, no sentinel |
| `call_tool_read b:t` with pin `P={a,b}` | effective set = P ∩ token = `{a}` ⇒ same body as `zzz:t` | same | ≡ |
| `call_tool_write a:write_tool` (authorized, over-tier) | `Permission denied … 'write'`, zero upstream | n/a | tier refusal is **not** non-disclosing by design |
| `call_tool_read a:ghost` (unresolved identity, scoped caller) | insufficient-permission, zero upstream | same | admin keeps fail-open (D4) |
| `read_cache K` (K produced under broader scope) | `cache key not found` | `cache key not found` | ≡ on MCP and REST `/api/v1/tools/call`; internal/legacy keys same body |
| `code_execution script=missing` | error without `Available scripts` | same | ≡; admin enumerates |
| `upstream_servers tail_log b` | `tailLogNotFound` (exists) | same | ≡ (regression) |
| `set_profile <not selectable>` | one format string (exists) | same | ≡ (regression) |

## Direct surface (`/mcp/all`)

| operation | hidden (A) | nonexistent (B) | contract |
|---|---|---|---|
| `tools/list` during origin flip / plain addition / reverse flip / tier change | definition withheld or listed per **producing** catalog's identity | — | no unauthorized definition at any seam, full and deferred; `*`-token and admin see the registry |
| `tools/list` with `__a` server tool | withheld for a-only; listed for `*` and admin | — | steady state **and** seam |
| `tools/list` with empty raw name `a__` | withheld from every caller | — | SC-005 exception |
| `tools/call a__b__c` inside seam | text ≡ not-found shape, never names `a__b` | `-32602 not found` | message parity (envelope code may differ: -32603 vs -32602) |
| `tools/call a__write_tool` `{read}` token | `Permission denied` isError, zero upstream | — | tier out of `WithToolFilter` |
| `describe_tool x__y:z` with hidden `x` exposing `y:z` | resolves to authorized `x__y:z` | same | ≡ (shadow canonical map); admin `not_found` control unchanged |
| `describe_tool b:read` with hidden `B:read` and authorized `b:Read` | suggestion `b:Read` | same | ≡ |

## Profile URLs (`/mcp/p/<slug>`, `/mcp/p`, `/mcp/p/`) — scoped callers only

| caller | slug | status/body |
|---|---|---|
| unpinned, allowed `{research-srv}` | `deploy` (disjoint), `nonexistent`, `` (empty), deleted `deploy` | **one** 404 body, no `available` list, ≡ across all four |
| unpinned, same | `research` | 200 (positive control) |
| pinned `research` | `deploy`, `nope`, `research` after pin deleted, `research` with zero reach (D1) | **one** 404 body ≡ |
| pinned `research`, fleet `[deploy]` vs `nil` | `research`, `deploy` | ≡ across fleets per slug (no-profiles branch after the gate) |
| admin / anonymous | any | today's three branches unchanged |

`set_profile` on `/mcp/p/<slug>`: `servers` reports selected ∩ URL-scope ∩ token; clearing a pinned selection reports `active_profile == ""` (doc line `profiles.md:70` updated).

## Logs and containers (agent-invokable `tail_log`; housekeeping)

| case | contract |
|---|---|
| `a/b` and `a_b` share `server-a_b.log` | `tail_log a_b` returns only `app.mcpproxy/owner=a_b` lines; `lines_returned` counts them; filter before limit |
| unattributed/legacy line | withheld from scoped callers; admin whole-file reader unchanged |
| OAuth callback stop for `a` | record lands in `a`'s subject-bound logger only, both start orders |
| Docker connect/disconnect for `a` with foreign `mcpproxy-a-b-wxyz` present | foreign container never logged into `a`, never removed; `mcpproxy-a-wxyz` with label `a` is removed |

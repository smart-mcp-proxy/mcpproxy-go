# Phase 1 Data Model: Registry — Easy Upstream-Add

**Feature**: 070-registry-easy-upstream-add · **Date**: 2026-05-31

No new persistent storage. Reuses the existing upstream BBolt bucket via `storage.SaveUpstreamServer` and the existing `mcp_config.json` `Registries` list. The entities below are mostly existing types; **new/changed fields are marked**.

## Registry (`config.RegistryEntry` / `registries.RegistryEntry`)
Source: `internal/config/config.go:866-912`, `internal/registries/types.go:6-15`.

| Field | Type | Notes |
|-------|------|-------|
| ID | string | Stable identifier (e.g. `pulse`). Lookup key. |
| Name | string | Display name. |
| Description | string | |
| URL | string | Human catalog URL. |
| ServersURL | string | API endpoint queried for servers. |
| Tags | []string | e.g. `verified`, `community`. |
| Protocol | string | Parser selector (`custom/pulse`, `mcp/v0`, …). |
| Count | int | Runtime-populated server count (-1 = unknown). |
| **RequiresKey** | bool | **NEW (FR-008)** — when true and no key configured, registry is skipped/marked unavailable, not erroring the whole search. |
| **Builtin** | bool | **NEW (derived, FR-006)** — true for the 5 defaults; used to render merge provenance, not persisted. |

**Merge rule (FR-006 / decision D4)**: effective list = built-in defaults ∪ user config `Registries`, keyed by `ID`; a user entry with a colliding ID overrides the default. Today `SetRegistriesFromConfig` *replaces* — change to merge (`registry_data.go:10-42`).

## Normalized server search result (`registries.ServerEntry`)
Source: `internal/registries/types.go:18-32`.

| Field | Type | Notes |
|-------|------|-------|
| ID | string | Identifier within the registry — the **add-by-reference key** (FR-001/FR-005). |
| Name | string | Proposed upstream server name (override allowed). |
| Description | string | |
| URL | string | For http/remote servers → upstream `url`. |
| SourceCodeURL | string | Repo link (display only). |
| InstallCmd | string | For stdio → split into `command` + `args` **server-side** (no longer client-side). |
| Registry | string | Source registry ID. |
| RepositoryInfo | *RepositoryInfo | npm/PyPI enrichment incl. install command. |
| **RequiredInputs** | []RequiredInput | **NEW (FR-003 plumbing)** — declared env/keys needed before a working add. Best-effort (decision D3 / O1). |

### RequiredInput (**NEW**)
| Field | Type | Notes |
|-------|------|-------|
| Name | string | Env var name (e.g. `GITHUB_TOKEN`). |
| Description | string | Optional human hint. |
| Secret | bool | Mask in UI/logs. |

Population: (a) explicit registry payload fields where present; (b) heuristic scan of `InstallCmd`/result for `${VAR}` / `$VAR` placeholders. No rich per-registry schema in this spec (O1).

## Unified add operation (input → output)
The keystone. Input is a **reference**, not a config blob (security decision D1 — server re-derives).

**Input** `AddFromRegistryRequest`:
| Field | Type | Required | Notes |
|-------|------|----------|-------|
| RegistryID | string | yes | Must resolve via `FindRegistry`. |
| ServerID | string | yes | Resolved via new `FindServerByID`. |
| Name | string | no | Override the proposed name; default = result Name. |
| Env | map[string]string | conditional | Required if result declares `RequiredInputs` not otherwise satisfied. |
| Enabled | bool | no | Default true. |

**Derivation → `config.ServerConfig`** (`internal/config/config.go:224-251`):
- stdio: `Command` + `Args` from `InstallCmd`/`RepositoryInfo`; `Protocol="stdio"`.
- http/remote: `URL` from result `URL`; `Protocol="http"`.
- `Env` merged from overrides.
- `Quarantined = cfg.DefaultQuarantineForNewServer()` (default true — CN-002, never overridable to false on this path).
- `Created = now`.

**Output**: persisted `ServerConfig` (via `SaveUpstreamServer`) + the same server echoed back with `quarantined: true`.

**Validation / refusal (edge cases)**:
| Condition | Result |
|-----------|--------|
| Registry not found | error `registry_not_found`. |
| Server ID not found | error `server_not_found`. |
| Neither install_cmd nor url derivable | error `no_install_info` (never persist a broken entry). |
| Required input missing | error `missing_required_input` (lists names). |
| Duplicate upstream name | error `duplicate_name` (consistent across surfaces). |

**Consistency invariant (CN-004 / FR-010)**: the same `(RegistryID, ServerID, Env, Name)` produces a byte-identical persisted `ServerConfig` (modulo `Created` timestamp) regardless of calling surface — asserted by the cross-surface regression test.

## Registry cache (`cache` package)
Source: `internal/cache/manager.go` (TTL 2h, `manager.go:19`).
- **NEW (FR-007)**: `Refresh(key)` / `Invalidate(key)` to force re-fetch; surface `Age = now - record.CreatedAt` and `Stale = IsExpired()` on search responses.

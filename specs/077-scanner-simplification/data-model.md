# Phase 1 Data Model: Scanner Simplification

Entities are described at the domain level and mapped to the existing Go types they
extend (this is a refactor, not a greenfield schema). Field names in code may differ
slightly; the contract JSON in `./contracts/` is authoritative for wire shapes.

---

## ScanReport (per server)

The single consolidated result for one server's tool set. Extends the existing
`ScanSummary` / aggregated report.

| Field | Type | Notes |
|-------|------|-------|
| `server` | string | Server name. |
| `status` | enum `clean` \| `warning` \| `dangerous` | **Derived from baseline findings ONLY** (FR-014). No `degraded`/`failed` from deep-scan gaps. |
| `risk_score` | number | From `CalculateRiskScore` over the merged, deduped findings (consensus-weighted). |
| `findings` | Finding[] | Merged + deduplicated across all scanners that ran (FR-010/FR-011). |
| `deep_scan` | DeepScanDescriptor | Separate availability dimension (FR-008). |
| `scanned_at` | timestamp | When the scan settled. |

**Validation / rules**:
- `status` MUST be a function of baseline findings only; deep findings never change it.
- `findings` MUST contain no `(rule_id, location)` duplicates.
- A `dangerous` status MUST correspond to at least one hard-tier baseline finding.

**State**: A report is produced per scan and replaces the prior report for that
server. It is emitted to clients via a single settled event (see below).

---

## Finding

One detected issue. Extends the existing `ScanFinding`.

| Field | Type | Notes |
|-------|------|-------|
| `rule_id` | string | Detection rule / check id (e.g. `phrase_injection`, `unicode.hidden`). |
| `location` | string | `server:tool` (detect vocabulary), the dedup key with `rule_id`. |
| `severity` | enum `info` \| `low` \| `medium` \| `high` \| `critical` | User-readable severity (FR-013). |
| `tier` | enum `hard` \| `soft` | Hard → contributes to blocking verdict; soft → review-only. |
| `threat_type` | string | Classified category; consensus match key with `location`. |
| `confidence` | number | Raised when multiple independent sources agree (FR-012). |
| `sources` | string[] | Contributing scanner ids (e.g. `tpa-descriptions`, `cisco-mcp-scanner`). ≥1. |
| `signals` | Signal[] | Detect-engine signals (present for baseline findings; empty for external). |
| `message` | string | Human-readable description. |

**Validation / rules**:
- `sources` MUST be non-empty; a merged finding lists all contributing sources.
- `confidence` for a finding agreed by N distinct sources MUST exceed the
  single-source confidence (monotonic in consensus).
- Only `tier == hard` baseline findings gate approval (FR-021).

---

## DeepScanDescriptor

Informational status of the opt-in layer. New object on the report.

| Field | Type | Notes |
|-------|------|-------|
| `enabled` | bool | From `security.deep_scan.enabled`. |
| `ran` | bool | Whether any deep scanner executed this scan. |
| `available` | bool | False when Docker/source-extraction/prereqs are unavailable. |
| `scanners_failed` | { id, reason }[] | Per-scanner best-effort failures (e.g. AppArmor/snap, extraction failure). Informational only. |

**Rules**: This object MUST NOT influence `ScanReport.status`. When `enabled=false`,
`ran=false`, `available=false`, `scanners_failed=[]`.

---

## SecurityConfig (config surface changes)

Under `security` in `mcp_config.json`. Extends the existing `SecurityConfig`.

| Field | Type | Default | Notes |
|-------|------|---------|-------|
| `deep_scan.enabled` | bool | `false` | Master opt-in for the heavy layer (FR-006). |
| `deep_scan.fetch_package_source` | *bool | `true` (within deep scan) | Absorbs `scanner_fetch_package_source`. |
| `deep_scan.disable_no_new_privileges` | bool | `false` | Absorbs `scanner_disable_no_new_privileges` (snap/AppArmor escape hatch). |
| `deep_scan.scanners` | string[] | `[]` | Optional per-scanner enable list under the umbrella. |
| ~~`auto_scan_quarantined`~~ | — | removed | Orphaned/never-consumed (FR-016). |

**Migration (FR-017 / SC-007)**: On load, top-level `scanner_fetch_package_source`
and `scanner_disable_no_new_privileges` map into `deep_scan.*` with identical effect;
`auto_scan_quarantined` is ignored if present. Old configs load without edits.

**Unchanged**: `quarantine_enabled` (global), `auto_approve_tool_changes` (per-server),
and all tool-approval hashing/state (Spec 032) — out of scope (FR-019).

---

## BundledScanner registry defaults (FR-018)

| Scanner | Default `enabled` |
|---------|-------------------|
| `tpa-descriptions` (in-process detect engine) | **true** |
| `cisco-mcp-scanner`, `mcp-ai-scanner`, `mcp-scan`, `nova-proximity`, `ramparts`, `semgrep-mcp`, `trivy-mcp` | **false** |

Docker scanners only run when `deep_scan.enabled=true` AND individually enabled.

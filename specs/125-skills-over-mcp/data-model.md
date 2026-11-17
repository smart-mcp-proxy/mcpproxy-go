# Data Model: Skills over MCP

## `skills.Entry` (wire, `internal/skills`)

| Field | Type | Source | Rule |
|---|---|---|---|
| `URI` | string | `uri` | ends in `/SKILL.md`; no control characters; ≤ 2,048 bytes |
| `Frontmatter` | map[string]any | `frontmatter` | has string `name` and `description`; ≤ 64 KiB as JSON |
| `Files` | []FileRef | `resources` array | 1..512 entries; contains the `SKILL.md` URI; all under the skill root; no query or fragment; unique relative paths; sizes sum ≤ 16 MiB |
| `Dynamic` | bool | `resources == "dynamic"` | makes the skill unusable (FR-014) |

`FileRef{URI string; Digest string; Size int64}`: `Digest` is `sha256:` + 64 lower-case hex; `Size` ≥ 0.

Derived: `Root` (URI without `/SKILL.md`), `Name` (last segment of `Root`, must equal `Frontmatter["name"]`), `Path` (the root without scheme, e.g. `hf-cli` or `acme/billing/refunds`), `ID` (`<server>:<Path>`).

## `storage.SkillApprovalRecord` (bucket `skill_approvals`, key `<server>:<skill URI>`)

| Field | Type | Meaning |
|---|---|---|
| `ServerName`, `SkillURI`, `SkillPath`, `Name`, `Description` | string | identity and display |
| `Status` | string | `pending` \| `approved` \| `changed` \| `unusable` |
| `Blocked` | bool | set and cleared only by a person |
| `Listed` | bool | present in the last applied listing |
| `UnusableReason` | string | machine code + detail, e.g. `digest_mismatch: scripts/x.py` |
| `Current` | SkillContent | what the last applied refresh read in |
| `Approved` | *SkillContent | what was approved; nil until the first approval |
| `CurrentHash`, `ApprovedHash` | string | `sk1-…` content hashes over the skill URI, every file's `(URI, relative path, digest, size)` and the frontmatter |
| `HashSchemaVersion` | int | 1 |
| `Incarnation` | int64 | server incarnation the record belongs to |
| `ApprovedAt`, `ApprovedBy` | time, string | `ApprovedBy` is an actor name, `auto` or `scan` |
| `Scan` | *SkillScan | verdict, complete flag, findings `{file, offset, rule, severity, evidence}` (capped) |
| `FirstSeen`, `UpdatedAt`, `CaptureSeq` | time, time, uint64 | ordering |

`SkillContent{Frontmatter json.RawMessage; Files []SkillFile}`; `SkillFile{RelPath, URI, Digest string; Size int64; Text bool; Stored bool}`.

Unusable reason codes: `invalid_uri`, `name_mismatch`, `dynamic`, `too_many_files`, `too_large`, `server_bound_exceeded`, `unsupported_digest`, `digest_mismatch`, `size_mismatch`, `frontmatter_mismatch`, `frontmatter_invalid`, `read_failed`, `stored_copy_damaged`.

## `skill_files` bucket (key `<server>:<sha256 hex>`, value raw bytes)

Text files only. Referenced by `Current.Files[*].Digest` and `Approved.Files[*].Digest` of that server's records, listed or not. Rows are written while a refresh works through the skills (staging) and a row no record references is deleted in the transaction that applies a refresh's records. The server's budget (`MaxStoredTextPerServer`) counts the distinct referenced rows plus the staged ones; a stored row is re-verified against the listing's size and digest every time a refresh reuses it, and against the approved digest every time it is served.

## State transitions

```text
                 (first seen)
                      │
        ┌─────────────┼───────────────────────────┐
        ▼             ▼                           ▼
     pending       approved                    unusable
   (manual; scan   (scan clean+complete;      (validation, digest, size,
    not clean)      auto; quarantine off)      frontmatter, dynamic, bounds)
        │  person approves ▲                      ▲
        └──────────────────┘                      │ any refresh that fails
                           │ content changes      │ a check
                           ▼                      │
                        changed ──────────────────┘
              (manual, scan: held;  auto / quarantine off: → approved)
                           │ person approves, or content returns to ApprovedHash
                           ▼
                        approved
```

- `Blocked` is orthogonal: available = `Status == approved && !Blocked && Listed`, the feature is on, and the server is enabled, not quarantined and not excluded by `expose_skills: false`. This is one function (`Runtime.SkillAvailable`), evaluated at request time against the live config.
- `unusable` → the next refresh that passes every check re-enters at "first seen" if there is no `Approved`, or compares with `ApprovedHash` if there is.
- Removing the server deletes both buckets' rows for it; a re-added server starts empty (new incarnation).

## Config

| Field | Where | Type | Default | Reload |
|---|---|---|---|---|
| `enable_skills` | top level | bool | false | hot |
| `expose_skills` | per server | bool, optional | follow global | hot |

## Activity types

| Type | When | Metadata |
|---|---|---|
| `skill_quarantine_change` | approve, auto-approve, hold on change, block, unblock, unusable | `action`, `skill`, `previous_hash`, `current_hash`, `reason`, actor |
| `skill_access` (PR-3, PR-5) | search, load, file read, native list/get/read | `action`, `skill`, `file`, `outcome`, caller |

## Events

`skills.changed` (SSE): `{server, counts:{available, pending, changed, unusable, blocked}}`; a scoped caller receives it only for servers in its reach, only when the set of skills available to it changed, and with only the `available` count (FR-019a).

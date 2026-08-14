# Data Model: Stored Scripts (Spec 097)

## StoredScript (filesystem-backed, never persisted elsewhere)
| Property | Rules |
|----------|-------|
| name | ASCII [A-Za-z0-9_-], 1–64 chars, case-sensitive |
| file | `<scripts-dir>/<name>.js` or `.ts` (lowercase ext only), regular file |
| size | 1 byte .. 256 KB (empty and oversize invalid) |
| language | derived from extension; explicit contradicting `language` param rejected |

## ScriptRef
The `script` MCP/REST parameter or `--script` flag: a validated name, never a path. XOR with `code`.

## ListEntry
| Field | Values |
|-------|--------|
| name | token-valid base name |
| paths | 1 (ok/invalid) or 2 (ambiguous) source paths |
| status | `ok` \| `ambiguous` \| `invalid` |
| reason | present when invalid: empty / oversized / unreadable |

Only `ok` entries are invocable; FR-004 error lists first 20 `ok` names + total count.

## Typed resolution errors (package codescripts)
- NotFound{Available (≤20, alphabetical), Total}
- Ambiguous{Paths}
- Invalid{Reason} (empty | oversized | unreadable | non-regular)
- InvalidName (pre-filesystem)

## Resolution state machine
```
name ─invalid token─▶ InvalidName            [no fs access — SC-003 proof point]
name ─valid─▶ probe .js/.ts via Root.Lstat
  none found ─▶ NotFound(+listing)
  both found ─▶ Ambiguous
  one found, non-regular ─▶ Invalid(non-regular)
  one found ─▶ Root.Open → fd Stat size check ─over─▶ Invalid(oversized)
             └─▶ single bounded read ─empty─▶ Invalid(empty)
                                     └─▶ (source, derived language)
```

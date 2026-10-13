# Contract: Bundle Status Surface

**Feature**: `101-tpa-db` · Resolves FR-020.

The operator question this surface must answer is not "what version am I on" but **"am I current and verified, or stuck on last-known-good — and why?"** Today's `BundleInfo` cannot answer the second half at all.

## 1. Fields

Existing fields keep their meaning and their names. `Fingerprint` stays the 12-hex short form (it is load-bearing in current operator output and in `logBundle`); the full digest is **added beside it**, not substituted.

| Field | Existing | Purpose |
|---|---|---|
| `bundle_version`, `schema_version`, `source`, `generated_at`, `load_error` | ✅ | unchanged |
| `runnable_rules`, `skipped_rules`, `declared_skipped` | ✅ | unchanged — un-evaluated coverage, never clean coverage |
| `fingerprint` | ✅ | 12-hex short form, human-comparable |
| `full_digest` | **new** | complete SHA-256; the **only** value rollback accepts (FR-010) |
| `sequence` | **new** | decimal string, as published |
| `authority` | **new** | `publisher` \| `operator` — which authority is serving |
| `trust_epoch` | **new** | the epoch it was published under |
| `signature_verified` | **new** | bool |
| `staleness_days` | **new** | derived from `generated_at` |
| `coverage_degraded` | **new** | bool |
| `degraded_reason` | **new** | enum, §3 |
| `last_rejection` | **new** | `{reason, detail, at}`, §2 — retained across a successful load so "what did it refuse, and when" survives. `detail` is a short human string carrying the part the enum cannot (which ceiling was exceeded and by how much, which key id was untrusted, which epoch was expected); it is never parsed, and `reason` is the only field anything branches on |

## 2. Rejection reasons

The vocabulary an operator reads to tell a tamper attempt from a stale epoch from their own misconfiguration:

| Reason | Means |
|---|---|
| `tamper` | signature did not verify over the raw bytes |
| `downgrade` | sequence below this authority's watermark for this epoch, or equal sequence with a different fingerprint |
| `stale_epoch_replay` | manifest epoch below the binary-trusted epoch — a validly-signed but superseded artifact |
| `unsigned_refused_by_ratchet` | this install has activated a signed bundle before; unsigned drops are refused thereafter |
| `authority_not_active` | verified against a real key of the **non-active** authority (FR-004b) |
| `rollback_target_mismatch` | the loaded artifact's full digest ≠ the digest the operator named |
| `gate_failure` | activation self-check (Spec 087 FR-008) rejected the candidate |
| `ceiling_exceeded` | over one of FR-011's four limits — which limit, and the observed value, in `detail` |
| `not_regular_file` | candidate was a FIFO/device/socket, or a symlink to one |
| `read_timeout` | the 5 s read budget expired; the single-flight slot was released |
| `sidecar_*`, `key_*`, `signature_invalid`, `sequence_mismatch` | the sidecar grammar — see [sidecar-format.md](sidecar-format.md) §3 |

**Every one of these keeps last-known-good serving.** There is no rejection path that leaves the scanner with an empty or partial rule set.

## 3. Degraded-coverage reasons

`coverage_degraded` suspends `scan`-mode auto-approval, routing changes to human review. It **must** be visible: a silently suspended auto-approval is indistinguishable from a quiet install, which is the failure this field exists to prevent.

| `degraded_reason` | Condition |
|---|---|
| `unsigned_external_active` | an unsigned file-dropped/fetched bundle is active (FR-007) |
| `embedded_below_watermark` | the embedded default is serving and its sequence is below the **publisher** watermark (FR-009a a) |
| `operator_bundle_missing` | the operator authority is active and its previously-activated external bundle is gone or invalid (FR-009a b) |
| `operator_authority_unbaselined` | an operator-signed bundle is active under an authority configured without a baseline (FR-009b) |

**Not degraded** — the two cases a careless implementation gets wrong:

- an ordinary fresh install serving the embedded default with no retained watermark and no missing external bundle. The embedded default is binary-trusted; degrading here would ship every fresh install with `scan` mode silently disabled.
- an embedded default **at or above** the publisher watermark after an external file was removed. It is demonstrably no older than what went missing — the ordinary "external file removed after a binary upgrade shipped a newer corpus" case.

## 4. Surfaces

Same field set everywhere; three renderings.

| Surface | Rendering |
|---|---|
| **CLI** (`mcpproxy security …`) | human table + `-o json`/`yaml`; the full digest shown in full (it is the rollback input) |
| **REST** (security overview) | the JSON object above; `make swagger` regenerated |
| **Web UI** (Security page) | short fingerprint with the full digest on demand; degraded state as a banner with its reason, never a silent flag |

The Web UI rule follows the house preference already established for status surfaces: **mark only the states that need action**, and always say *why* rather than showing a bare badge.

## 5. Activation history

`(authority, trust_epoch, sequence, full digest, generated_at, activated_at, source)` per activation, append-only, retained across restarts.

Exposed read-only. It exists because rollback is authorized by full digest and there is otherwise **no trustworthy way to learn the digest of the known-good artifact you want to revert to** — reading it from the candidate file would defeat the purpose, since the file is what the attacker controls.

Data-dir resident, so it carries the same caveat as the ratchet: it makes a legitimate rollback *possible*; it does not *authorize* one.

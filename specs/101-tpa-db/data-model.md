# Phase 1 Data Model: tpa-db

**Feature**: `101-tpa-db` · **Plan**: [plan.md](plan.md) · **Decisions**: [research.md](research.md)

Three stores, deliberately separated by who can write them and what survives a data-directory wipe. That separation *is* the security model — every FR that says "config-resident" is choosing store 2 over store 3 for exactly that reason.

| # | Store | Written by | Survives data-dir wipe | Trust role |
|---|---|---|---|---|
| 1 | **Binary** (`go:embed`) | a release build | yes | publisher keys, publisher epoch, embedded corpus + its sequence (the FR-009b publisher baseline) |
| 2 | **`mcp_config.json`** | the operator | yes | operator keys, operator baseline, operator epoch, active-authority declaration, `require_signed_bundle` |
| 3 | **`config.db`** (BBolt) | the running proxy | **no** | watermarks, ratchet, pin, deny-list, activation history — all best-effort (plan Complexity Tracking row 1) |

---

## 1. Bundle manifest additions (inside the signed bytes)

Additive to the Scanner Bundle Contract v0.1 line, so a v0.1 loader ignores them (FR-002).

| Field | Type | Rules |
|---|---|---|
| `generated_at` | RFC3339 string | derived from release metadata (`SOURCE_DATE_EPOCH` or the release commit), **never** build wall-clock |
| `sequence` | **decimal string** | `^[1-9][0-9]{0,19}$`, value ≤ 2^64−1. Never a bare JSON number — double-precision round-trip corrupts >2^53−1 and admits exponent notation (FR-002) |
| `key_id` | 64 lowercase hex | canonical fingerprint of the signing public key; must equal the sidecar's `key_id` and the verifying trust-set key's id (FR-003) |
| `trust_epoch` | integer ≥ 0 | the authority's epoch this artifact was published under; must **equal** the binary-trusted epoch of the verifying authority (FR-005a) |
| `signatures[]` | array | see [contracts/bundle-signatures-section.md](contracts/bundle-signatures-section.md) (FR-001a/D4) |

**Why these live inside the signed bytes**: the epoch especially. If it lived only in the binary, a bump-and-revoke would let an old bundle signed by a surviving key be silently reinterpreted as belonging to the new epoch, clear the re-baselined watermark and activate at full coverage — reinstating the blind spots the recovery release existed to close (FR-005a).

## 2. Sidecar

Separate file, never signed by itself. Five keys, total rejection grammar — [contracts/sidecar-format.md](contracts/sidecar-format.md).

## 3. Signing authority

```go
type Authority string // "publisher" | "operator"
```

Not a key set — a **namespace for anti-downgrade state** (FR-004a). All publisher keys, including ones mid-rotation, belong to one publisher authority precisely so a rotated-in key inherits the existing watermark instead of starting a fresh one.

| Property | Publisher | Operator |
|---|---|---|
| Keys | embedded in the binary (store 1) | config (store 2) |
| Epoch advanced by | a binary release | a config edit |
| Baseline (FR-009b) | the embedded corpus's sequence, **re-applied every startup** | declared in config; **may be absent** → authority runs explicitly degraded |
| Active by default | yes (FR-004b) | no |

Exactly one authority is **active** at a time, declared in config. A candidate whose verifying key belongs to a non-active authority is refused `authority_not_active`. Without that declaration, per-authority watermarks open the hole they were introduced to close: whoever writes the bundle path would choose the authority, and any operator-signed artifact clearing the operator baseline would swap the install onto a narrower corpus while still reporting `signature_verified=true` at full coverage.

**The declaration governs the serving bundle, not only future candidates.** On a change, an active external bundle of the now-inactive authority is deactivated and the install falls back to the embedded default under FR-009a's rules.

## 4. Per-authority persisted state (BBolt, store 3)

One record per authority in the new `TPABundleStateBucket`, plus one history list. **All members of one `Update` transaction** (D6/FR-011a).

```go
type authorityState struct {
    Authority        Authority
    Epoch            uint64   // the epoch these values are scoped to
    Watermark        uint64   // highest sequence ever activated for (authority, epoch)
    WatermarkSetBy   string   // full SHA-256 of the bundle that set it
    SignatureRatchet bool     // has a signature-verified bundle ever activated
    Pin              *pinEntry
    DenyList         []denyEntry // keyed (authority, epoch, sequence, fingerprint)
}
```

**Scoping is `(authority, epoch, sequence)`, ordered by epoch first** (FR-005a). Every comparison is same-authority and same-epoch: a cross-authority numeric comparison is meaningless — an operator corpus at sequence 900 says nothing about a publisher corpus at 100 — and making one would permanently suspend auto-approval on an install whose publisher fallback is perfectly current (FR-009a).

**`WatermarkSetBy` and `DenyList` are load-bearing transaction members, not bookkeeping.** A crash that preserved a rollback while losing the deny-list would let the next refresh immediately re-activate the exact release the operator just rejected — silently undoing the rollback (FR-011a).

### Watermark lifecycle

```text
seed from baseline (every startup, publisher)  ──►  raised by each higher-sequence activation
        ▲                                                        │
        │                                              rollback does NOT lower it
   epoch bump re-baselines                             (expressed as pin + deny-list instead)
   and retires prior-epoch pins/deny-list
```

Never empty for any authority that has a baseline — an absent watermark accepts anything, which is the whole point of FR-009b. The single exception is a baseline-less operator authority, which is run **explicitly degraded** rather than as a silent zero.

## 5. Activation history (append-only, store 3)

One record per activation, retained across restarts (FR-010):

`(authority, trust_epoch, sequence, full SHA-256 digest, generated_at, activated_at, source)`

**This is new.** `BundleInfo` reports only the currently active artifact, so without a history an operator has no trustworthy way to learn the digest of the known-good artifact they intend to revert to — and rollback authorization is by full digest. Being data-dir resident, it carries the same best-effort caveat as the ratchet: it makes a legitimate rollback *possible*, it does not *authorize* one.

## 6. `BundleInfo` additions (FR-020)

Existing fields keep their meaning. `Fingerprint` stays the 12-hex short form — it is load-bearing in current operator output — and a full digest is **added** beside it rather than widening it (R3).

| New field | Purpose |
|---|---|
| `FullDigest` | complete SHA-256; the only value rollback accepts |
| `Sequence` | decimal string, as published |
| `Authority`, `TrustEpoch` | which authority is serving, under which epoch |
| `SignatureVerified` | bool |
| `StalenessDays` | derived from `generated_at` |
| `CoverageDegraded`, `DegradedReason` | FR-007/FR-009a — a silently suspended `scan`-mode auto-approval is otherwise indistinguishable from a quiet install |
| `LastRejection` | `{reason, at}` — one of `tamper`, `downgrade`, `stale_epoch_replay`, `unsigned_refused_by_ratchet`, `authority_not_active`, `rollback_target_mismatch`, `gate_failure`, `ceiling_exceeded`, `not_regular_file`, `read_timeout` |

## 7. Signature (TPA record)

The catalog unit SC-004 counts: `TPA-YYYY-NNNN` id, category, gating intent, detector(s), level, confidence, provenance reference, licence, and the `rule_ids` implementing it. Many-to-one to `rules[]` — one campaign class routinely needs several patterns (a hidden-instruction signature already implies distinct rules for `<IMPORTANT>` blocks, HTML comments and zero-width runs).

## 8. Coverage state (the derived value that matters most)

`coverageOK` today is `bundlePresent && ChecksFailed == 0` (R5). It becomes:

```text
degraded  ⟸  an unsigned EXTERNAL bundle is active                        (FR-007)
          ∨  the embedded default is serving AND its sequence is below
             the PUBLISHER watermark                                       (FR-009a a)
          ∨  the operator authority is active AND its previously-activated
             external bundle is gone or invalid                            (FR-009a b)
          ∨  a baseline-less operator authority's bundle is active         (FR-009b)
```

**And explicitly NOT degraded** in the two cases a careless reading would catch: an ordinary fresh install on the embedded default, and an embedded default at or above the publisher watermark after an external file was removed. A false degrade silently suspends `scan`-mode auto-approval fleet-wide, which is why both negatives are named here and tested in the plan's Test Strategy.

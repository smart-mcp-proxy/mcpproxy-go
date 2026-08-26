# Contract: Signature Sidecar Format v1

**Feature**: `101-tpa-db` · Resolves FR-003's "the sidecar wire format MUST be specified in design, not left to the implementation".

This is the one artifact an independent publisher must be able to produce and every loader must reject **identically**. It is also the only artifact read and parsed *before* any signature has been verified — its `algorithm`, `key_id` and `signature` are the inputs to verification — which is why the grammar below is total and why the byte cap (FR-011: **4 KiB**) applies before parsing.

## 1. Encoding

A single JSON object. UTF-8. No BOM. Trailing newline optional and ignored; **any other trailing bytes are a rejection**.

File naming: `<bundle-filename>.sig` beside the bundle (e.g. `scanner-bundle.json.sig`).

## 2. Fields — exactly these five, no others

```json
{"sidecar_version":"1","algorithm":"ed25519","key_id":"3f0a…64hex","signature":"MEUCIQ…","sequence":"101"}
```

| Field | Type | Accepted values |
|---|---|---|
| `sidecar_version` | string | exactly `"1"` |
| `algorithm` | string | exactly `"ed25519"` |
| `key_id` | string | exactly 64 lowercase hex chars — SHA-256 of the 32-byte raw Ed25519 public key |
| `signature` | string | standard base64 **with** padding, decoding to exactly 64 bytes |
| `sequence` | string | `^[1-9][0-9]{0,19}$`, value ≤ 2^64−1 |

`key_id` is a fingerprint **of the key itself**, never an operator-chosen label (FR-003). That is what makes "the manifest names one publisher while the bytes are signed by another" detectable rather than cosmetic.

`sequence` is duplicated from the manifest deliberately: it is the one manifest field whose disagreement is cheap to detect *before* parsing the thing the signature protects.

## 3. Rejection grammar — total, deterministic, and reason-bearing

Every row is a hard rejection. There is no leniency, no fallback and no skip anywhere in this table. Each rejection records its own reason (surfaced via FR-020's `LastRejection`), because a conformance test that only asserts "failed" cannot show two implementations agree.

| # | Condition | Reason code |
|---|---|---|
| 1 | file exceeds 4 KiB (checked **before** parsing) | `sidecar_too_large` |
| 2 | not a single JSON object / trailing bytes | `sidecar_malformed` |
| 3 | a required key is missing | `sidecar_missing_field` |
| 4 | any key outside the five | `sidecar_unknown_field` |
| 5 | a key appears more than once | `sidecar_duplicate_field` |
| 6 | any value has the wrong JSON type | `sidecar_type` |
| 7 | `sidecar_version != "1"` | `sidecar_version_unsupported` |
| 8 | `algorithm != "ed25519"` | `sidecar_algorithm_unsupported` |
| 9 | `key_id` not 64 lowercase hex | `sidecar_key_id_malformed` |
| 10 | `signature` not valid padded base64, or not 64 bytes decoded | `sidecar_signature_malformed` |
| 11 | `sequence` fails the grammar or exceeds 2^64−1 | `sidecar_sequence_malformed` |
| 12 | `key_id` not in the active authority's trust set | `key_not_trusted` |
| 13 | signature does not verify over the raw bundle bytes | `signature_invalid` |
| 14 | manifest `key_id` ≠ sidecar `key_id` ≠ verifying key's id | `key_id_mismatch` |
| 15 | manifest `sequence` ≠ sidecar `sequence` | `sequence_mismatch` |

Row 8 deserves emphasis: **an unknown algorithm identifier is a hard rejection, never a fallback to Ed25519 and never a skip.** The field exists so the scheme can evolve without breaking verification of *existing* artifacts — a v2 loader will accept both; a v1 loader accepts one.

Rows 5 and 6 need a strict decoder. Go's `encoding/json` silently accepts duplicate keys (last wins) and ignores unknown fields unless told otherwise — use `Decoder.DisallowUnknownFields` for row 4, an explicit duplicate scan for row 5, and `Decoder.More()` after the object for row 2.

## 4. What the signature does and does not cover

The signature is over **the exact raw bundle bytes**, not over the sidecar. Therefore:

- **Covered**: every byte of the bundle. Any alteration → row 13.
- **Covered by agreement rules**: the sidecar's identity fields, because FR-003 requires manifest/sidecar/verifying-key id agreement → row 14.
- **NOT covered**: the sidecar's own encoding. Whitespace and key-order changes that alter neither the signature value nor an identity field are **not required to be rejected** — a detached signature cannot detect them.

SC-001 is written against exactly this boundary, which is why it enumerates four tamper classes rather than claiming "every byte of the sidecar".

## 5. Fixtures (shipped in `internal/security/bundlesig/testdata/`)

**Accept** — one canonical valid pair (`valid.json` + `valid.json.sig`) plus:

| Fixture | Property demonstrated |
|---|---|
| `accept-min-sequence` | `sequence: "1"` |
| `accept-max-sequence` | `sequence: "18446744073709551615"` (2^64−1) — the value SC-002a fixture (a) signs |
| `accept-trailing-newline` | a trailing `\n` is ignored |

**Reject** — one fixture per numbered row above, named `reject-<reason_code>`. Each asserts the **reason code**, not merely that loading failed.

Two implementations agreeing on the accept set is not conformance. Agreeing on the reject set, reason by reason, is.

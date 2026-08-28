# Contract: Signature Sidecar Format v1

**Feature**: `101-tpa-db` · Resolves FR-003's "the sidecar wire format MUST be specified in design, not left to the implementation".

This is the one artifact an independent publisher must be able to produce and every loader must reject **identically**. It is also the only artifact read and parsed *before* any signature has been verified — its `algorithm`, `key_id` and `signature` are the inputs to verification — which is why the grammar below is total and why the byte cap (FR-011: **4 KiB**) applies before parsing.

## 1. Encoding

A single JSON object. UTF-8. No BOM. Trailing newline optional and ignored; **any other trailing bytes are a rejection**.

File naming: `<bundle-filename>.sig` beside the bundle (e.g. `scanner-bundle.json.sig`).

## 2. Fields — exactly these four, no others

```json
{"sidecar_version":"1","algorithm":"ed25519","key_id":"3f0a…64hex","signature":"<88 canonical base64 chars>"}
```

| Field | Type | Accepted values |
|---|---|---|
| `sidecar_version` | string | exactly `"1"` |
| `algorithm` | string | exactly `"ed25519"` |
| `key_id` | string | exactly 64 lowercase hex chars — SHA-256 of the 32-byte raw Ed25519 public key |
| `signature` | string | **exactly 88 characters** of canonical RFC 4648 standard base64 ending `==`, decoding to exactly 64 bytes |

`key_id` is a fingerprint **of the key itself**, never an operator-chosen label (FR-003). That is what makes "the manifest names one publisher while the bytes are signed by another" detectable rather than cosmetic.

**`sequence` is deliberately NOT duplicated here.** An earlier draft carried it, reasoning that a manifest disagreement would be "cheap to detect before parsing". That rationale was wrong: detecting a disagreement requires parsing the manifest, which happens only *after* verification, so the duplicate bought no pre-parse check and added a whole inconsistency state — plus two rejection rows — for nothing. The sequence lives in the signed manifest, where it is authenticated, and nowhere else.

**Canonical base64 is a requirement, not a formality.** Go's default `base64.StdEncoding` is lenient in two ways that both break the "two implementations reject identically" property this contract exists to provide: it silently accepts **embedded newlines** inside the value, and it accepts non-canonical trailing bits — an 88-character string that decodes without error yet does **not** round-trip to itself. Verified against Go 1.25: `StdEncoding.DecodeString` returns `err=nil` for both, while `StdEncoding.Strict().DecodeString` rejects the non-canonical case at the offending byte. Implementations MUST therefore use the strict decoder **and** assert that re-encoding the decoded bytes reproduces the input exactly.

## 3. Rejection grammar — total, deterministic, and reason-bearing

Every row is a hard rejection. There is no leniency, no fallback and no skip anywhere in this table. Each rejection records its own reason (surfaced via FR-020's `LastRejection`), because a conformance test that only asserts "failed" cannot show two implementations agree.

**Rows are evaluated in the numerical order below and the FIRST match wins.** The conditions deliberately overlap — `{"foo":1}` satisfies both "a required key is missing" and "a key outside the four" — so without a stated precedence two conforming implementations could report different reasons for the same input, which is exactly the divergence the reason codes exist to rule out.

| # | Condition | Reason code |
|---|---|---|
| 1 | file exceeds 4 KiB (checked **before** parsing) | `sidecar_too_large` |
| 2 | not a single JSON object / trailing bytes | `sidecar_malformed` |
| 3 | a required key is missing | `sidecar_missing_field` |
| 4 | any key outside the four | `sidecar_unknown_field` |
| 5 | a key appears more than once | `sidecar_duplicate_field` |
| 6 | any value has the wrong JSON type | `sidecar_type` |
| 7 | `sidecar_version != "1"` | `sidecar_version_unsupported` |
| 8 | `algorithm != "ed25519"` | `sidecar_algorithm_unsupported` |
| 9 | `key_id` not 64 lowercase hex | `sidecar_key_id_malformed` |
| 10 | `signature` is not exactly 88 canonical base64 chars, does not decode strictly, does not round-trip, or is not 64 bytes decoded | `sidecar_signature_malformed` |
| 11 | `key_id` not in the active authority's trust set | `key_not_trusted` |
| 12 | signature does not verify over the raw bundle bytes | `signature_invalid` |
| 13 | manifest `key_id` ≠ sidecar `key_id` ≠ verifying key's id | `key_id_mismatch` |

Row 8 deserves emphasis: **an unknown algorithm identifier is a hard rejection, never a fallback to Ed25519 and never a skip.** The field exists so the scheme can evolve without breaking verification of *existing* artifacts — a v2 loader will accept both; a v1 loader accepts one.

Rows 2, 4 and 5 each need their own mechanism, and one of them is easy to get wrong. Verified against Go 1.25:

- **Row 4** (unknown key): `Decoder.DisallowUnknownFields()` — works as expected.
- **Row 5** (duplicate key): needs an **explicit scan**. `encoding/json` silently accepts duplicates and takes the LAST one, and `DisallowUnknownFields` does **not** catch them. Measured: `{"algorithm":"ed25519","algorithm":"rsa"}` decodes with `err == nil` and `algorithm == "rsa"`. On a field whose whole job is to pin the algorithm, that is the difference between a hard rejection and quietly reading a different value than a reviewer would.
- **Row 2** (trailing bytes): plain `json.Unmarshal` already rejects them — `invalid character '{' after top-level value` — so it is the simplest correct choice. If a streaming `Decoder` is used instead, decode once and then require the next `Decode` to return `io.EOF`; `Decoder.More()` also reports `true` for trailing bytes and garbage and `false` for trailing whitespace, but the `io.EOF` form states the intent directly and does not depend on how `More()` treats the top level.

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
| `accept-trailing-newline` | a trailing `\n` is ignored |
| `accept-all-zero-signature` | the 88-char canonical encoding of 64 zero bytes — pins the canonical form itself |

**Reject** — one fixture per numbered row above, named `reject-<reason_code>`. Each asserts the **reason code**, not merely that loading failed.

Two implementations agreeing on the accept set is not conformance. Agreeing on the reject set, reason by reason, is.

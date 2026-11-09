# Data model

All types are in-memory Go values inside `internal/security/detect` (and `checks`); nothing is
persisted. Existing types (`Signal`, `Finding`, `Span`) are unchanged in shape.

## Field (new, `detect.Field`)

| Attribute | Type | Rule |
|---|---|---|
| `Path` | string | `description`, or `input_schema:<json-pointer>` / `output_schema:<json-pointer>` (RFC 6901 escaping of `~` and `/`) |
| `Field` | `SpanField` | `description` / `input_schema` / `output_schema` |
| `RawStart`, `RawEnd` | int | byte range of the JSON string token (quotes included) in the raw schema text; `0, len(description)` for the description |
| `Text` | string | decoded string value (the description verbatim) |
| `Exact` | bool | true when decoded offset `i` ↔ raw offset `RawStart+1+i` (no escapes in the token; always true for the description) |

Produced by `FieldStrings(tool)`; order: description first, then input-schema strings in document
order, then output-schema strings. Caps: depth 32, 512 strings per schema, 64 KiB per string
(truncated → `Exact=false`).

## Span (existing) — new producer

`SchemaSpan(checkID, tier, field Field, byteStart, byteEnd)` returns a span with `Field.Field` and
UTF-16 offsets measured against the RAW schema text (`RawStart+1+byteStart …`), only when
`field.Exact`; otherwise nil. The frontend already renders `input_schema` / `output_schema` spans.

## Padding run (`checks.paddingRun`, internal)

| Attribute | Rule |
|---|---|
| `Kind` | `horizontal` / `vertical` / `block` / `repeated-char` / `repeated-line` |
| `Start`, `End` | byte range in the field text |
| `Length` | characters (horizontal, repeated-char), lines (vertical, repeated-line), bytes (block) |
| `Line` | 1-based start line |
| `FollowedByContent` | must be true to be reported |

Dedup: a `block` overlapping a `horizontal`/`vertical` run is dropped; a `repeated-*` run overlapping
either is dropped. One signal per tool: the run with the highest confidence, ties by document order.

## Reconstruction (`checks.markerDirective`, `checks.reconstruction`, internal)

| Attribute | Rule |
|---|---|
| `Marker` | 1–16 bytes, literal |
| `Form` | `quoted` / `backtick` / `tag` / `passive` / `empty-replacement` |
| `DirectiveStart`, `DirectiveEnd` | byte range of the directive sentence head |
| `PayloadStart`, `PayloadEnd` | byte range of the payload in the field text (≤ 700 bytes) |
| `Occurrences` | sorted, non-overlapping marker positions in the payload (≤ 64) |
| `Reconstructed` | payload with occurrences removed |
| `OffsetMap` | `[]int`, reconstructed byte → original byte |
| `Limit` | "" or the limit name that made the directive unsupported |

State: a directive is **inactive** (no action-governed payload → nothing emitted), **active-supported**
(reconstructed, inner checks run), or **active-unsupported** (one "analysis incomplete" signal).

## Composite indicator (`checks.exfilIndicator`, internal)

| Attribute | Rule |
|---|---|
| `Class` | `credential` / `transmission` / `destination` |
| `Start`, `End` | byte range in the normalized field text |
| `Text` | matched text (capped) |
| `Commodity` | destination only: true for a known collector host |

A finding = the smallest window holding one indicator of each class, ≤ 400 bytes and ≤ 1 sentence
boundary inside.

## Signal detail / evidence conventions

See `contracts/check-ids.md`.

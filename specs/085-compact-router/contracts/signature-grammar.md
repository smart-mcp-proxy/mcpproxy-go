# Contract: Compact Signature Grammar

Normative grammar for `internal/toolsig.Render`. This is the single source of truth shared by
the production compact entry builder and the spec-083 bench `compact_sig` arm (FR-019). It
finalizes the plan-level details the spec left open (spec Assumptions: "type abbreviations,
marker characters are plan-level; the spec's hard requirements are determinism, required-param
completeness, lossiness legibility").

## 1. Grammar (EBNF)

```ebnf
signature   = "(" [ paramlist ] ")" | "(~)" ;   (* "(~)" = whole-schema-unparseable fallback — see below *)
paramlist   = param { ", " param } ;
param       = name markers ":" typespec [ "=" default ] ;
markers     = [ "*" ] [ "~" ] ;                 (* "*" = required; "~" = lossy collapse *)
name        = atom ;                            (* verbatim JSON property name, quoted if it contains a metachar — §3.5 *)
typespec    = scalar | array | enum | union | "obj" | "any" ;
scalar      = "str" | "int" | "num" | "bool" ;
basetype    = scalar | "obj" | "any" ;
union       = basetype "|" basetype { "|" basetype } ;  (* non-null JSON Schema type union, declared order — §3 *)
array       = "[" basetype "]" ;                (* e.g. [str], [obj] *)
enum        = "enum[" value { "|" value } "]" ;       (* only when value count <= 5; value = atom *)
default     = atom ;                            (* optional scalars only, <= 20 chars, quoted if it contains a metachar *)
atom        = bareatom | quotedatom ;
```

Description and lossy flag are **not** part of the `signature` string — they are returned as the
separate `Signature.Desc` (first-sentence) and `Signature.Lossy` (bool) fields and serialized as
sibling JSON fields `desc` / `lossy` (see data-model.md §4). `Signature.Lossy == true` **iff**
the signature string contains at least one `~`.

**Whole-schema-unparseable fallback (preserves the Lossy⟺`~` invariant)**: when `ParamsJSON`
cannot be parsed at all (not valid JSON, or not an object schema), the compiler renders **`(~)`**
— an empty parameter list carrying the bare lossy marker — with `Lossy=true`. It must NOT render
`()` with `Lossy=true` (that would break the invariant `Lossy ⟺ contains(sig,"~")`) and must NOT
error the whole response. `(~)` reads as "params unknown — describe me before calling", which is
exactly the intended signal. (Contrast the no-params tool E1, whose schema parses fine to an empty
property set → `()`, `Lossy=false`.)

`any` is the fallback typespec for a *parameter* with no resolvable type (§3 last two rows);
`(~)` is the fallback for an unparseable *whole schema*. `union` renders a non-null type union.

## 2. Marker order and combination

- Marker slot is between `name` and `:`. Order is fixed: `*` before `~`.
- `param*:str` — required scalar.
- `param:str` — optional scalar.
- `param~:obj` — optional, collapsed (nested object / unrepresentable) → lossy.
- `param*~:obj` — **required and** collapsed (FR-003: required name + `*` kept, internal
  structure collapses under FR-004's `~`) → lossy.

## 3. Type mapping (JSON Schema → typespec)

| JSON Schema | typespec |
|---|---|
| `"type":"string"` | `str` |
| `"type":"integer"` | `int` |
| `"type":"number"` | `num` |
| `"type":"boolean"` | `bool` |
| `"type":"array"`, `items` scalar | `[str]` / `[int]` / `[num]` / `[bool]` |
| `"type":"array"`, `items` object/unknown/absent | `[obj]` / `[any]` → **lossy** |
| `"type":"object"` with `properties` | `obj` → **lossy** (`~`) |
| `"type":["string","null"]` / `anyOf:[{string},{null}]` | `str` (null dropped — nullable = omittable, encoded by required/optional split) |
| type union (non-null) e.g. `["string","integer"]` | `str|int` |
| enum present, ≤5 values, scalar | `enum[v1|v2|…]` (values verbatim, `|`-joined) |
| enum present, >5 values | scalar base type + **lossy** (`~`) — values dropped |
| `$ref`, `oneOf` of objects, recursion, no resolvable type | `obj` (or base) + **lossy** (`~`) |
| **required name absent from `properties`** (or a param whose schema resolves to no type at all) | `any` + **lossy** (`~`) — the name still renders with its `*` (FR-003 hard invariant) |

**Never-elide overrides "no type info" (FR-003/SC-004)**: this is the key divergence from the
existing bench arm, which *skips* a required name that has no property declaration. The
production compiler MUST NOT skip it — a required name with no resolvable schema renders
`name*~:any` (marked required, marked lossy, typed `any`). Dropping the name is a hard invariant
violation; dropping the *type* is fine (that is what lossy means).

**Determinism**: unions preserve JSON declared order; enum values preserve declared order;
duplicate/`null` members dropped. No Go map iteration order in output.

## 3.5. Atom escaping / quoting (delimiter safety)

Names, enum values, and defaults are arbitrary JSON strings and may contain signature
metacharacters. The **signature metacharacter set** is: space, `,` `:` `|` `=` `(` `)` `*` `~`
`[` `]` `"`. Rendering rule for every `atom` (name / enum value / default):

- **Bare** (`bareatom`) when the string is non-empty and contains **no** metacharacter — rendered
  verbatim (`origin`, `3600`, `auto`, `full`).
- **Quoted** (`quotedatom`) otherwise (contains a metachar, or is the empty string): wrap in
  double quotes and backslash-escape embedded `"` and `\`. Example: an enum value `a|b` →
  `"a|b"`; a property named `x:y` → `"x:y"*:str`; a default `1,000` → `="1,000"`.

This makes the signature **unambiguous and reversible** (a parser can recover each atom) and keeps
the common case (identifier-like names, numeric/keyword defaults) unquoted. Quoting is applied by
`internal/toolsig`, deterministically, before joining — so E2/E4 examples below stay bare.

## 4. Defaults (optional scalars only)

- Rendered as `name:type=literal` when: the param is **optional**, has a `default`, the default
  is a scalar (string/number/bool/null), and the literal is ≤20 chars. The literal is an `atom`
  (§3.5): bare when metachar-free (`ttl:int=3600`, `mode:str=auto`); quoted when it contains a
  metachar (a default value of `=|` renders `sep:str="=|"`).
- Required params never carry `=default` (a required param has no meaningful default in the
  signature; its `*` is the contract). Object/array defaults are dropped (lossiness already
  covers those).

## 5. Parameter ordering

1. Required params first, in the schema `required` array order (verbatim), de-duplicated.
2. Optional params after, in ascending Unicode code-point (`sort.Strings`) name order.

(Matches the existing bench arm's ordering so migration is order-preserving.)

## 6. Description (first sentence)

`Signature.Desc = FirstSentence(description)` — verbatim prefix, deterministic, no paraphrase.
The terminator rule differs by script because CJK text is unspaced (this resolves the E6
contradiction Codex flagged):

- **CJK terminators** `。 ！ ？` — match **unconditionally** (return the verbatim prefix through
  the terminator). CJK sentences are not space-separated, so requiring a trailing space would
  never fire.
- **ASCII terminators** `. ! ?` — match **only** when immediately followed by whitespace, EOF, or
  a closing `" ) ] }`. This avoids splitting `e.g.`, `3.14`, `v1.2` mid-token. (v1 uses this
  simple boundary test, not an abbreviation dictionary — deterministic and good enough; the
  verbatim + length-cap fallback bounds any mis-split.)
- **No terminator match** ⇒ verbatim first 200 runes (rune-boundary safe; a trailing `…` marker
  is appended and counts toward measured size only when truncation actually occurred).
- **Empty/whitespace-only description** ⇒ empty `Desc`.

The scan takes the **earliest** matching terminator of either class.

## 7. Worked examples (all six spec edge cases + the design example)

Each shows: input schema → `sig` / `desc` / `lossy`.

### E1 — No-params tool (edge: empty parens, never lossy)
```json
{"type":"object","properties":{}}      desc: "List all configured servers."
```
→ `sig: "()"`  `desc: "List all configured servers."`  `lossy: false`

### E2 — All-scalar tool (required + optional with default; design's cdn_create)
```json
{"type":"object",
 "properties":{"origin":{"type":"string"},"ttl":{"type":"integer","default":3600},
               "certificate_id":{"type":"string"},"custom_domain":{"type":"string"}},
 "required":["origin"]}
```
→ `sig: "(origin*:str, certificate_id:str, custom_domain:str, ttl:int=3600)"`
   `lossy: false`
(required `origin*` first; optionals sorted: certificate_id, custom_domain, ttl; ttl carries
its default.)

### E3 — Nested REQUIRED param (never-elide-required + lossy collapse; FR-003)
```json
{"type":"object",
 "properties":{"name":{"type":"string"},
               "account":{"type":"object","properties":{"id":{"type":"string"},"tier":{"type":"string"}}}},
 "required":["name","account"]}
```
→ `sig: "(name*:str, account*~:obj)"`  `lossy: true`
(`account` is required → keeps name + `*`; object → collapses with `~`. Both markers, order
`*~`. Contrast the design's `account~:obj` which was optional.)

### E4 — Long enum vs short enum (edge: inline only when ≤5)
Short (≤5):
```json
{"type":"object","properties":{"type":{"enum":["full","partial"]}},"required":["type"]}
```
→ `sig: "(type*:enum[full|partial])"`  `lossy: false`

Long (>5):
```json
{"type":"object",
 "properties":{"region":{"type":"string",
   "enum":["nyc1","nyc3","sfo3","ams3","sgp1","lon1","fra1","tor1","blr1"]}},
 "required":["region"]}
```
→ `sig: "(region*~:str)"`  `lossy: true`
(9 values > 5 → values dropped, base type `str`, collapsed with `~`.)

### E5 — Empty / stub description (edge: id + sig only)
```json
{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}
                                       description: ""
```
→ `sig: "(path*:str)"`  `desc: ""`  `lossy: false`
(Entry renders `id`, `score`, `sig`, `lossy`, and an empty `desc`. Profiler's
degenerate-description counter flags it; no fabrication.)

### E6 — Non-Latin description (edge: verbatim first sentence, deterministic)
```json
{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}
   description: "検索クエリを実行します。結果はJSONで返されます。"
```
→ `sig: "(q*:str)"`  `desc: "検索クエリを実行します。"`  `lossy: false`
(The first `。` is a **CJK terminator** → matches unconditionally even though the next rune
`結` is not whitespace; verbatim prefix incl. the terminator. This is why §6 splits CJK
unconditionally but requires a boundary after ASCII terminators. If the description had **no**
terminator, the deterministic fallback returns the first 200 runes verbatim, rune-boundary safe.)

### E7 — Array-of-string + array-of-object (type mapping + lossy)
```json
{"type":"object",
 "properties":{"tags":{"type":"array","items":{"type":"string"}},
               "filters":{"type":"array","items":{"type":"object","properties":{"k":{"type":"string"}}}}}}
```
→ `sig: "(filters~:[obj], tags:[str])"`  `lossy: true`
(both optional → sorted: filters, tags; `[str]` non-lossy; `[obj]` element is an object →
lossy `~`.)

### E8 — Required name absent from `properties` (never-elide over missing type; §3 last row)
```json
{"type":"object","properties":{"path":{"type":"string"}},"required":["path","token"]}
```
→ `sig: "(path*:str, token*~:any)"`  `lossy: true`
(`token` is in `required` but has **no** property schema. The bench arm would drop it; the
production compiler MUST render it with `*`, typed `any`, collapsed `~`. This is the FR-003
hard-invariant case Codex asked to pin.)

### E9 — Metacharacter in a name / enum value (§3.5 quoting)
```json
{"type":"object","properties":{"filter:key":{"enum":["a|b","c"]}},"required":["filter:key"]}
```
→ `sig: "(\"filter:key\"*:enum[\"a|b\"|c])"`  `lossy: false`
(the name contains `:` → quoted; enum value `a|b` contains `|` → quoted; `c` is bare.)

### E10 — Non-null type union (§3 union row + `union` production)
```json
{"type":"object","properties":{"id":{"type":["string","integer"]}},"required":["id"]}
```
→ `sig: "(id*:str|int)"`  `lossy: false`
(union members mapped and `|`-joined in declared order; not lossy — the union is fully
represented. A union that includes `"null"` drops the null member, e.g. `["string","null"] → str`,
per §3.)

### E11 — Whole schema unparseable (`(~)` fallback, Lossy⟺`~` invariant)
```json
"not valid json {"                     (the stored ParamsJSON string is malformed)
```
→ `sig: "(~)"`  `lossy: true`
(the compiler cannot parse the schema at all → bare lossy marker, no params, `Lossy=true`.
Never `()`+lossy, which would violate the invariant; never an error that fails the response.)

## 8. Test obligations (contract → tasks)

- Table test in `internal/toolsig/signature_test.go` covering E1–E11 asserting exact `sig`,
  `desc`, `lossy` bytes.
- **Never-elide over missing type (E8)**: an explicit synthetic test where a `required` name is
  absent from `properties` — assert it still renders `name*~:any` and is never dropped (FR-003).
- **Unparseable schema (E11)**: assert `Render` on malformed `ParamsJSON` yields `(~)` with
  `Lossy=true` (never `()`), and never returns an error that fails the whole response.
- Property test: every name in schema `required` appears in `sig` with `*` (SC-004,
  never-elide) across the frozen 45-tool corpus.
- **Escaping round-trip (E9)**: a name/enum/default containing each metacharacter renders quoted
  and a reference parser recovers the original atom (unambiguous/reversible).
- **Union (E10)**: non-null type unions render `a|b` in declared order; `null` member dropped.
- **First-sentence terminator matrix**: CJK `。` unconditional (E6); ASCII `.` split only at a
  boundary (`e.g. text` vs `3.14`); no-terminator length-cap fallback; empty ⇒ empty.
- Determinism test: `Render` called twice on the same input yields identical bytes (FR-019);
  shuffled `properties` map insertion order yields identical output.
- **Lossy-legibility invariant**: `Lossy == strings.Contains(sig, "~")` for **all** corpus tools
  **and** the E11 unparseable case — the single source of truth for the invariant.
- Migration parity: after `bench/arms/compact.go` imports `internal/toolsig`, its golden files
  are regenerated from the shared grammar and the arm's output matches `Render` byte-for-byte
  (sequencing: see tasks.md **T040** — happens on the 085 branch after 083/PR #851 merges).

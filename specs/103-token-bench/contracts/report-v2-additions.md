# Contract: report v2 additions

## Versioning

**No `report_version` bump.** New blocks are additive optional top-level fields with
`omitempty`, plus matching optional properties declared in the v2 JSON schema. A version bump
is reserved for changing an existing field's meaning or shape; this feature changes none.

Precedent: the latency block's nested fields were added beside the existing flat fields the
same way.

> The schema has no `additionalProperties: false`, so an undeclared block would validate
> silently. Do not rely on that — the schema file is the reviewed contract. Declare every new
> block.

## New blocks

### `replay` (deterministic, US1)

Per-cell recomputed cost for a real recorded workload, plus the exclusion accounting:
sessions supplied, sessions used, and a count per exclusion reason. Carries the fleet shape.

### `agent_loop` (live, US2)

Per-cell tokens-per-completed-task, completion rate, first-attempt success rate, corrective
and infrastructure retry counts, run count and spread. **Carries its own accounting-source
field** (provider + pinned model).

### `payload_decomposition`

Per fleet shape: attribution across names, descriptions, annotations and schemas; the
recomputed achievable ceiling; and an explicit `confirmed` / `corrected` verdict on spec 102.

## Two required changes to existing structures

### 1. Scope the tokenizer identity

The report envelope carries **one** tokenizer identity for the whole document. Once
provider-reported usage enters the same envelope, that field silently claims the entire report
was tokenizer-counted.

**Do not redefine the existing field.** This contract states that a version bump is required
whenever an existing field's meaning changes, and narrowing the document-level tokenizer to
"the deterministic sections only" would be exactly such a change — the two rules contradict.

Resolve it additively instead: leave the existing field's meaning intact (it names the
deterministic estimator, which remains true of every section that has one), and give each new
block its own explicit `accounting_source` naming either that estimator or the provider plus
pinned model. A reader then never has to infer scope from a document-level field.

If a future change really must narrow the existing field, bump the version — that is what the
rule is for.

### 2. Add per-row provenance

Provenance today is **section-level**: a map from section key to
`measured` / `computed` / `estimated`, rendered as a badge on each section heading.

FR-013 lets measured and estimated figures coexist *inside* one section, which the section
badge cannot express. Every new row type therefore carries its own `provenance` field,
constrained to the same three-value enum.

This matters concretely: the per-arm retry-rate lookup returns `0.0` for unknown arms, which
is indistinguishable from a measured `0.0`. Without per-row provenance, one table can mix a
measured rate and a defaulted one under a single `estimated` badge.

## Aggregation rules

1. **Never sum across accounting sources.** A cross-source aggregate is **withheld with a
   stated reason**, never computed — reusing the harness's existing withhold-rather-than-compute
   pattern for non-authoritative headlines.
2. **A model-dependent figure needs `runs >= 4`** plus a spread, or it is not a headline.
3. **Every percentage carries its fleet shape.**
4. **Partial runs are marked and excluded from headlines.**

## Enforcement gaps to close

- **`SC-011` has no gate today.** "Reports are never committed" is gitignore plus convention;
  no CI job or test fails on a committed report.
  **`git status --porcelain bench/results` is NOT a valid gate** — porcelain does not list
  ignored files, and it would also stay silent about a report that is already tracked. The
  correct assertion is that no file under the results directory is tracked at all:
  `test -z "$(git ls-files bench/results)"`.
- **`SC-004` is not currently achievable offline.** The tokenizer fetches its vocabulary over
  the network unless a cache directory env var is set, and nothing in the repo or CI sets it.
  Setting the variable is necessary but **not sufficient** — it names a cache without filling
  one. CI must also populate or restore that cache, and the reproduction procedure must tell
  an outsider to warm it once with network access.
- **`SC-002` (byte-identical reruns) collides with the report's wall-clock `generated_at`
  stamp.** Replay must pin or exclude that field, or every determinism check fails on it
  alone.

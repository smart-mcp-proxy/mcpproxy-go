# Contract: Encoding Arm

Every encoding arm registered in `bench/arms` MUST satisfy:

## Behavioral contract

1. **Determinism** (FR-010): `EncodeTool(t)` and `EncodeListing(ts)` return byte-identical output for identical input across runs, machines, and Go versions. No wall-clock, randomness, map-iteration-order, or locale dependence. JSON rendering uses canonical key ordering.
2. **Totality with explicit failure** (FR-009): on unencodable input, return an error — never panic, never return a silently truncated encoding. The harness counts the skip and records `(tool_id, error)` examples.
3. **Description preservation labeling** (US2/AC2): arms that drop or truncate descriptions MUST self-report `LowerBound: true`, rendered as "lower-bound estimate" in reports.
4. **Index-altering declaration** (FR-008): `IndexAltering()` MUST be true iff the arm changes any text the retrieval index ingests. The mapping is explicit, not inferred: every arm implements `EncodeIndexMetadata(t Tool) (config.ToolMetadata, error)` returning the exact `Name`/`Description`/`ParamsJSON` the production index ingests for that arm (rendering-only arms return the input unchanged). The armindex builder consumes ONLY this method. Contract test: on **corpus_v2** (schema-bearing — corpus_v1 lacks schemas and cannot catch parameter-text changes), diff `EncodeIndexMetadata` output vs baseline; any difference with `IndexAltering()==false` fails, and zero difference with `IndexAltering()==true` fails (over-declaration wastes CI scoring time and mislabels the report).
5. **Runtime absence** (FR-006): an arm whose external runtime is missing MUST return `ErrArmUnavailable` with a human-readable reason at registry-resolution time — before any tool is processed — so the harness reports arm-level skip-with-reason. In CI, arm-level skip of a mandatory arm fails SC-002 verification.
6. **Amortized preambles**: formats with shared preamble/dictionary (TRON classes, TOON header) account for them in `EncodeListing`, not per-tool, so per-tool means remain comparable.

## Registry contract

- Names are unique, lowercase snake_case, stable across releases (report consumers key on them).
- Mandatory set: `baseline_json`, `compact_sig`, `tscg`, `toon_listing`. Optional: `toon_results` (fixture-driven), `tron_dedup`.
- `baseline_json` MUST reproduce the exact rendering used for the naive-menu count (FR-004 parity): the canonical full-definition text `name + description + canonical-JSON schema`, the same shape counted by the existing `CountToolWithSchema`. This single renderer feeds the baseline arm, naive full-menu tokens, proxy-menu tokens, savings-% denominators, and break-even inputs (research D7b).

## Golden-output tests

Each arm commits a fixture: first 3 tools of corpus_v2 → expected encoded bytes (checked into `bench/arms/testdata/`). Changing an arm's output requires updating the fixture in the same PR — encoding drift is a reviewed event, because it invalidates cross-run comparisons.

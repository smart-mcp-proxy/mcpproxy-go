# Spec 065 — Evaluation datasets

## `security_corpus_v1.json` (D2)

Labeled security regression corpus the D2 detection scorer measures against
(precision / recall / F1 / FPR per detector). Conforms to
[`../contracts/security-corpus.schema.json`](../contracts/security-corpus.schema.json)
and the cross-entity invariants in [`../data-model.md`](../data-model.md)
(INV-3, INV-4). Validated by `corpus_test.go` in this directory.

**Composition (43 entries):**

| Label | Category | Count |
|-------|----------|-------|
| malicious | `tool_poisoning` | 6 |
| malicious | `prompt_injection` | 6 |
| malicious | `shadowing` | 4 |
| malicious | `rug_pull` | 4 |
| benign | `benign` (clean base rate) | 15 |
| benign | `hard_negative` (attack-resembling) | 8 |

Hard negatives are benign descriptions that *superficially resemble* an attack
(e.g. a secrets-scanner that lists `~/.ssh/id_rsa` as an example, a tool that
legitimately says "ignore case"). They exist to expose noisy detectors
(SC-004 / INV-3). Each hard-negative `id` is `hn_<attack_category>_<n>`, encoding
the attack it mimics so false positives map back to a category.

## Provenance & licensing (FR-007 / CN-005 / R-07 / R-A)

Every entry carries `provenance.{source,license}`, and the test fails the build
if any license is outside the redistributable allowlist (CN-005 / INV-4).

- **`self-authored` / `self-authored`** — the dominant source; short
  tool-description strings written from scratch, modeled on public attack
  taxonomies. Redistributable by construction.
- **`DVMCP` / `MIT`** — a subset adapted from the MIT-licensed
  [Damn Vulnerable MCP](https://github.com/harishsg993010/damn-vulnerable-MCP-server)
  project.

### External benchmarks (referenced, NOT vendored)

Per CN-005 and risk R-A, the following are **referenced externally only** and no
text from them is vendored into this repo:

- **MCPTox** and **MCP-AttackBench** — restrictive / research-only licenses.
- **`mcp-injection-experiments`** — LICENSE unconfirmed (research.md R-A); where it
  inspired a pattern, the corresponding entry was rewritten from scratch and
  labeled `self-authored`. The corpus test rejects any entry sourced from these.

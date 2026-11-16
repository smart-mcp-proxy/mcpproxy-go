# Contract: Bundle `signatures[]` Section

**Feature**: `101-tpa-db` · Resolves FR-001a.

## 1. Why this exists

FR-001 says the publishable artifact is "the existing compiled bundle". FR-012 says "each signature MUST carry provenance … and a redistributable license". **Both cannot hold today**, because the shipped `scanner-bundle.json` has nowhere to put either:

```text
top level : bundle_version, generated_from, rules, schema_version, signature_count, skipped
rules[]   : category, confidence, detector, engine, flags, id, indicators,
            level, pattern, severity, target, type
```

No provenance. No licence. No campaign identity — `signature_count` counts *rules*, so SC-004's "at least 25 signatures spanning at least 8 distinct classes" is not even expressible.

## 2. Shape

A new **additive** top-level array. A v0.1 loader ignores unknown top-level keys, so contract §4 forward-compat is preserved (FR-001a).

```json
{
  "signatures": [
    {
      "id": "TPA-2026-0001",
      "category": "instruction_injection",
      "gating": true,
      "provenance": {
        "source": "https://invariantlabs.ai/blog/mcp-github-vulnerability",
        "published": "2026-01-14",
        "note": "hidden <IMPORTANT> block coaxing ~/.ssh/id_rsa"
      },
      "license": "CC-BY-4.0",
      "rule_ids": ["TPA-2026-0001/hidden_important_block", "TPA-2026-0001/hidden_html_comment"]
    }
  ]
}
```

(amended by US2, 2026-10-20: `rule_ids` entries are `"<TPA-id>/<detector>"`, not free-form rule names. `rules[].id` stays equal to the owning `TPA-YYYY-NNNN` id and `rules[].detector` distinguishes the rules of one signature, so `(id, detector)` is the rule's key. The earlier example ids `r-hidden-important-block` and `r-hidden-html-comment` would have broken the CheckID shape `tpa.<TPA-id>.<detector>` that the CLI HELD column, the Web UI hold-evidence parser and the runtime prefix tests depend on, and `<id>/<detector>` is already the loader's own error format. The detector names in the example above are illustrative.)

| Field | Type | Rules |
|---|---|---|
| `id` | string | `^TPA-[0-9]{4}-[0-9]{4}$`, unique across the bundle |
| `category` | string | the campaign/technique class; must have a `categoryCheck` entry in the gate (FR-016a). (amended by US2, 2026-10-20: a snake_case slug with the prefix `tpa_`, drawn from the single class vocabulary `tpacorpus.Classes`; no class name is a prefix of another. `rules[].category` keeps its v0.1 meaning, the threat-type mapping.) |
| `gating` | bool | `true` ⇒ must emit hard-tier signals and must ship eval evidence (FR-013/FR-014). (amended by US2, 2026-10-20: `true` exactly when the signature owns at least one runnable rule, see §4 rule 2) |
| `provenance.source` | URL | the public disclosure that motivated the signature |
| `provenance.published` | `YYYY-MM-DD` | |
| `provenance.note` | string | optional, human context |
| `license` | SPDX id | from the redistributable allowlist below (policy layer only, see §4) |
| `rule_ids` | array | ≥1, each `"<TPA-id>/<detector>"` naming an existing `rules[]` entry whose `id` equals this signature's `id` (amended by US2, 2026-10-20) |

**Licence allowlist** (redistributable, FR-012): `CC0-1.0`, `CC-BY-4.0`, `MIT`, `Apache-2.0`. (amended by US2, 2026-10-20: `CC-BY-SA-4.0` is removed. Its share-alike terms could attach to the MIT-licensed binary that embeds the bundle, which makes it not safely redistributable here. The maintainer can re-add it in this contract. Patterns written by the project are `MIT`, the repo `LICENSE`. `provenance.source` is the factual public disclosure; its licence is not the signature's licence. The dataset's own `provenance.license` keeps a separate allowlist, where `self-authored` stays valid.) Anything else is refused at corpus build — including "no licence stated", which is the common case for a pattern lifted from a blog post and is precisely what FR-012 exists to stop shipping.

## 3. Relationship to `rules[]`

**Many-to-one, explicitly.** Several rules may implement one signature — a hidden-instruction class already needs distinct patterns for `<IMPORTANT>` blocks, HTML comments and zero-width runs. One rule belongs to at most one signature.

```text
signatures[] ──1─────*── rules[]
   TPA id                 pattern
```

## 4. Referential integrity — enforced at build, in both directions

A bundle failing any of these is **rejected whole**, exactly like any other contract violation:

(amended by US2, 2026-10-20: the rules below are enforced in two layers that share one implementation. The **load layer** (`internal/security/scanner/tpa_bundle_signatures.go`, `validateSignatures`, called from `loadBundleCheck`) runs in the binary at every load and activation, only when `signatures[]` is present so v0.1 bundles keep loading. It enforces: id grammar and uniqueness (rule 3); `rule_ids` resolution, the `(id, detector)` uniqueness and the id-part-equals-owner check (rule 1); the gating derivation (rule 2); `rules[]` sorted by `(id, detector)`; `signature_count == len(signatures)`; non-empty `provenance.source`, `provenance.published` in `YYYY-MM-DD` form and `license`; and every `skipped[].id` resolving to a signature. The **policy layer** (`internal/security/tpacorpus/policy.go`) runs at build and in CI only. It enforces: the licence allowlist (rule 4), the class vocabulary, the eval pair (rule 5), and category registration (rule 6, closed by a scan-eval test, because `categoryCheck` lives in `package main`). The licence allowlist stays out of the load layer so that operators who build private corpora (FR-004a) are not refused at runtime.)

1. every `signatures[]` entry names ≥1 rule id that exists in `rules[]`;
2. every **hard-tier** rule is named by exactly one `gating: true` signature entry — not zero (an ungoverned gating rule has no provenance and no eval evidence) and not two (ambiguous campaign attribution).

   (amended by US2, 2026-10-20: gating is derived from **runnable**, not from `level`. A rule is gating if and only if `engine=="regex"` and `target=="tool_description"`. `BundleCheck.Inspect` hard-codes the hard tier for every runnable rule, so `level` is a severity label only and deriving from it would mean nothing. A signature declares `gating: true` exactly when it owns at least one runnable rule, and a mismatch in either direction rejects the bundle. A gating signature may also own non-runnable companion rules, such as `structural_diff` rules. A signature owning only non-runnable rules, such as a `resource_content` one, is the non-gating case. The paragraph below is kept for the reasoning, with "derive it from `level`" read as "derive it from runnable".)

   **"Gating" must be mechanical, because `rules[]` has no such field.** Its keys are `category, confidence, detector, engine, flags, id, indicators, level, pattern, severity, target, type` — nothing that says "this rule gates approvals". Left as prose about intent, the check is unenforceable and a hard-tier rule can evade attribution entirely, or be attached to a `gating: false` signature and thereby dodge the FR-013 eval-evidence requirement while still driving the approval gate at runtime. The build MUST therefore derive it from `level` — a rule is gating **iff** it emits at the hard tier, which is exactly what FR-014 says gating means and exactly what the eval gate and the `scan`-mode approval gates score — and MUST reject any bundle where a hard-tier rule's owning signature has `gating: false`. `signatures[].gating` is then a declaration that is *checked* against the rules it names, not an independent switch;
3. `id` values are unique;
4. `license` is in the allowlist;
5. every `gating: true` signature has its FR-013 eval pair in the frozen dataset — ≥1 labeled gated-malicious sample and ≥1 category-matched hard negative following the hard-negative id prefix the dataset validator registers for the category (amended by US2, 2026-10-20: `hn_<class>_`, for example `hn_tpa_exfil_redirect_01` with `resembles: "<class>"`; the prefix is derived from `tpacorpus.Classes`). "The frozen dataset" is read as "the dataset, extended in place": the samples are added to `specs/065-evaluation-foundation/datasets/detect_corpus_v1.json`;
6. every `gating: true` signature's `category` resolves to a **registered** `categoryCheck` entry in the gate. §5 below explains why an unmapped category is scored vacuously; that explanation is not enforcement, so this rule is what actually stops one shipping. A bundle naming a category the gate cannot enforce is rejected at build, not merely noted.

Direction 2 is the one a naive implementation drops, and it is the one that matters: checking only "signatures point at real rules" lets a gating rule ship with no campaign record at all, which is the state the corpus is in today. Rule 6 is the same failure one level up — a campaign record that exists but that the gate silently declines to score.

## 5. Interaction with the eval gate

`gating: true` is not self-certifying. FR-016a's `categoryCheck` map decides whether a category is *enforced*: `gatedCategory()` enforces a category only when its mapped check id is registered, so **a new class whose category is missing from that map is tallied and reported but excluded from `gatedMalicious`/`OverallRecall`** — its misses can never fail the gate. Adding a signature class therefore means adding its `categoryCheck` entry in the same change, or the coverage is decorative.

(Its false positives still count either way, since `FPRate` is computed over the whole hard-negative set regardless of category mapping. So an unmapped class can only ever hurt — never help — the gate outcome. That asymmetry is the tell.)

## 6. Counting for SC-004

- **signatures** = `len(signatures)` — must be ≥ 25
- **classes** = distinct `category` — must be ≥ 8
- every signature carries provenance + licence (rule 4 above)
- every `gating: true` signature has its eval pair (rule 5)

Note `signature_count` (existing, top-level) counts **rules** and keeps that meaning. Renaming it would break the v0.1 line for no gain; SC-004 counts `signatures[]`.

(amended by US2, 2026-10-20: this is reversed. `signature_count` is `len(signatures)`, which equals the number of distinct TPA ids, and it is **not** the rule count. The file shipped before US2 held 6 `signature_count` and 10 rules, so the two already differed. The load layer rejects a bundle where `signature_count != len(signatures)` when `signatures[]` is present; a v0.1 bundle without `signatures[]` keeps its old value untouched. The embedded bundle moves to `bundle_version` `0.1.1`, a patch bump for the additive section, which the loader's `0.1` gate still accepts.)

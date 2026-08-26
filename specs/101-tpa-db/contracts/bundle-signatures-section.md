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
      "rule_ids": ["r-hidden-important-block", "r-hidden-html-comment"]
    }
  ]
}
```

| Field | Type | Rules |
|---|---|---|
| `id` | string | `^TPA-[0-9]{4}-[0-9]{4}$`, unique across the bundle |
| `category` | string | the campaign/technique class; must have a `categoryCheck` entry in the gate (FR-016a) |
| `gating` | bool | `true` ⇒ must emit hard-tier signals and must ship eval evidence (FR-013/FR-014) |
| `provenance.source` | URL | the public disclosure that motivated the signature |
| `provenance.published` | `YYYY-MM-DD` | |
| `provenance.note` | string | optional, human context |
| `license` | SPDX id | from the redistributable allowlist below |
| `rule_ids` | array | ≥1, each naming an existing `rules[]` entry |

**Licence allowlist** (redistributable, FR-012): `CC0-1.0`, `CC-BY-4.0`, `CC-BY-SA-4.0`, `MIT`, `Apache-2.0`. Anything else is refused at corpus build — including "no licence stated", which is the common case for a pattern lifted from a blog post and is precisely what FR-012 exists to stop shipping.

## 3. Relationship to `rules[]`

**Many-to-one, explicitly.** Several rules may implement one signature — a hidden-instruction class already needs distinct patterns for `<IMPORTANT>` blocks, HTML comments and zero-width runs. One rule belongs to at most one signature.

```text
signatures[] ──1─────*── rules[]
   TPA id                 pattern
```

## 4. Referential integrity — enforced at build, in both directions

A bundle failing any of these is **rejected whole**, exactly like any other contract violation:

1. every `signatures[]` entry names ≥1 rule id that exists in `rules[]`;
2. every rule with gating intent is named by **exactly one** signature entry — not zero (an ungoverned gating rule has no provenance and no eval evidence) and not two (ambiguous campaign attribution);
3. `id` values are unique;
4. `license` is in the allowlist;
5. every `gating: true` signature has its FR-013 eval pair in the frozen dataset — ≥1 labeled gated-malicious sample and ≥1 category-matched hard negative following the `hn_<attack_category>_*` naming the dataset validator enforces.

Direction 2 is the one a naive implementation drops, and it is the one that matters: checking only "signatures point at real rules" lets a gating rule ship with no campaign record at all, which is the state the corpus is in today.

## 5. Interaction with the eval gate

`gating: true` is not self-certifying. FR-016a's `categoryCheck` map decides whether a category is *enforced*: `gatedCategory()` enforces a category only when its mapped check id is registered, so **a new class whose category is missing from that map is tallied and reported but excluded from `gatedMalicious`/`OverallRecall`** — its misses can never fail the gate. Adding a signature class therefore means adding its `categoryCheck` entry in the same change, or the coverage is decorative.

(Its false positives still count either way, since `FPRate` is computed over the whole hard-negative set regardless of category mapping. So an unmapped class can only ever hurt — never help — the gate outcome. That asymmetry is the tell.)

## 6. Counting for SC-004

- **signatures** = `len(signatures)` — must be ≥ 25
- **classes** = distinct `category` — must be ≥ 8
- every signature carries provenance + licence (rule 4 above)
- every `gating: true` signature has its eval pair (rule 5)

Note `signature_count` (existing, top-level) counts **rules** and keeps that meaning. Renaming it would break the v0.1 line for no gain; SC-004 counts `signatures[]`.

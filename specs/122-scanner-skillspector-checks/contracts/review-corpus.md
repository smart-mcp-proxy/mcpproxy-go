# Contract: review-tier eval corpus and `--review-gate`

File: `specs/065-evaluation-foundation/datasets/detect_review_corpus_v1.json`

```json
{
  "version": "review-v1",
  "description": "...",
  "entries": [
    {
      "id": "ar_never_refuse",
      "label": "malicious",            // or "benign"
      "category": "anti_refusal",      // anti_refusal | padding | composite_exfil | prohibition | reconstruction_limits | hard_negative | benign
      "resembles": "anti_refusal",     // hard negatives only
      "expect_tier": "soft",           // malicious only: "soft" (must NOT be dangerous) | "hard" | "any"
      "server": "srv", "tool": {"name": "...", "description": "...", "input_schema": {...}},
      "provenance": {"source": "self-authored | NVIDIA/SkillSpector tests/<path> (adapted)", "license": "self-authored | Apache-2.0"}
    }
  ]
}
```

Category → check id: `anti_refusal → refusal.directive`, `padding → padding.hidden`,
`composite_exfil → exfil.composite`, `prohibition → phrase.injection`,
`reconstruction_limits → payload.reconstructed`.

Semantics (`go run ./cmd/scan-eval --corpus <file> --review-gate [--min-review-recall 0.90]
[--max-review-fp 0.10]`):
- malicious entry detected ⇔ the tool's finding lists the category's check id in `signals` (any tier);
  for `prohibition`, detected ⇔ the finding is present AND its threat level matches `expect_tier`
  (`soft` → warning, `hard` → dangerous).
- hard negative false positive ⇔ the resembled category's check id appears in `signals`; for
  `prohibition` (which measures the TIERING of `phrase.injection`, whose descriptive matches are review
  findings by design) ⇔ the verdict is `dangerous`.
- `expect_tier: any` marks a sample the existing hard families also catch (the verdict is dangerous
  on their account); the review check must still contribute its signal.
- breach ⇔ any `expect_tier: soft` entry is `dangerous`, or recall < min, or FP > max. Exit code 6.
- Output: per-category JSON scorecard plus the id of every miss / false positive.

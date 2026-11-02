# Contract: check ids, tiers, confidences, wording

| Check id | Tier | Threat type | Confidence | Evidence | Detail (template) |
|---|---|---|---|---|---|
| `refusal.directive` | soft | `prompt_injection` | AR1 0.6, AR2 0.5, AR3 0.65, risk-concealment 0.55; × position discount; floor 0.3 | matched phrase (CapEvidence) | `Description contains an anti-refusal directive (%q) — an attempt to suppress the agent's refusal or safety behaviour, surfaced for review.` + ` [field <path>]` when not the description |
| `padding.hidden` | soft | `tool_poisoning` | 0.45; 0.6 at ≥2× threshold or two kinds | `<kind> run: <length> <unit> at line <n>` | `Description hides text behind a <kind> padding run (<length> <unit>, line <n>) — content after the run is pushed out of view; surfaced for review.` |
| `payload.reconstructed` | soft (provenance / incomplete) | `malicious_code` for provenance of a shell match, else `prompt_injection`; `uncategorized` for incomplete | 0.5 | provenance: `reconstructed payload: <text>`; incomplete: `declared marker directive: <head>` | provenance: `Removing the declared marker %q from the nearby payload (as the description instructs) reveals %s.` incomplete: `A declared-marker transformation could not be analyzed (%s); the payload after it was NOT inspected — analysis incomplete, surfaced for review.` |
| inner signals forwarded by reconstruction | as inner | as inner | as inner | as inner | inner detail prefixed `After removing declared marker %q: ` |
| `exfil.composite` | soft | `exfiltration` | 0.55 (0.65 commodity collector); × position discount of the transmission verb; floor 0.35 | `credential access (%q) + transmission (%q) + external destination (%q)` | `Description combines credential access (%q), a transmission action (%q) and an external destination (%q) in one passage — co-occurrence, not proven data flow; surfaced for review.` |
| `phrase.injection` (prohibition cap) | soft | unchanged | 0.55 | unchanged | `… surfaced for review, not auto-blocked (reads as a direct prohibition: a negation immediately governs the action).` |

Rules:
- One signal per tool per check (highest confidence; document order on ties), except
  `payload.reconstructed`, which may forward several inner signals (distinct inner check ids) plus one
  provenance signal; forwarded inner signals are deduplicated by (CheckID, Evidence).
- Every field other than the description is named in the detail as ` [field input_schema:/properties/x/description]`.
- Spans: description spans via `DescriptionSpan`; schema spans via `SchemaSpan` only for exact fields.
- No new check ever emits `TierHard` itself; only forwarded inner signals can be hard.

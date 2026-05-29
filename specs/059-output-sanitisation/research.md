# Phase 0 Research: Output Sanitisation Enforcement (Track B)

## D1 — Getting secret spans for in-place redaction without breaking the detector

**Decision**: Add an additive `func (d *Detector) Redact(content string) (redacted string, detections []security.Detection)` method on the existing detector. It iterates the same `d.patterns + d.customPatterns`, respects `IsCategoryEnabled` + `IsValid` + `IsKnownExample`, and replaces each valid match via `regex.ReplaceAllStringFunc` with `[REDACTED:<category>]`. High-entropy and file-path scanners are NOT used for redaction (too false-positive-prone for mutation); redaction is pattern-based only.

**Rationale**: The current `Detection` struct carries no byte offsets and `Scan()` is consumed widely (activity log). Adding offsets would be a broad breaking change. `ReplaceAllStringFunc` gives in-place replacement and lets us collect the categories replaced for the policy_decision record — reuse without API churn. `Match()` already returns matched strings; replacement at the regex level is the natural inverse.

**Alternatives considered**: (a) Add `StartOffset/EndOffset` to `Detection` — rejected, breaks/bloats the widely-stored type. (b) Re-run a second regex pass in the sanitizer with copied patterns — rejected, duplicates pattern ownership and drifts from the detector's config gating.

## D2 — Spotlight delimiter format + spoof escaping (FR-B1, FR-B2)

**Decision**: Wrap untrusted text as:

```
«untrusted:SERVER/TOOL»
<escaped content>
«/untrusted:SERVER/TOOL»
```

Use the guillemet sentinel `«…»` (rare in tool output). Before wrapping, **escape** any occurrence of the sentinel characters `«` / `»` in the body by replacing them with their HTML-style entity (`&laquo;`/`&raquo;`) so content cannot forge a closing delimiter (FR-B2). The wrapper names the originating `server/tool` so an evaluator/model can attribute the data. Spotlighting is **lossless**: the only transformation to the body is the reversible sentinel escape.

**Rationale**: Explicit, human- and model-readable, attributable, and the escape closes the breakout vector. Guillemets are extremely uncommon in real tool payloads, minimising escape noise.

**Alternatives considered**: XML-ish `<tool_output>` tags (more likely to collide with real content); random per-call nonce fences (harder for a static evaluator to anchor on, and overkill given escaping already prevents breakout).

## D3 — Control-sequence classes (FR-B4)

**Decision**: Four independently-toggleable classes in `internal/security/sanitizer.go`:
- **ansi**: CSI/SGR escape sequences `\x1b[ ... ]` (and `\x1b]` OSC) → removed.
- **c0c1**: C0 (U+0000–U+001F except `\t \n \r`) and C1 (U+0080–U+009F) control chars → removed.
- **zero_width**: U+200B, U+200C, U+200D, U+2060, U+FEFF → removed.
- **bidi**: U+202A–U+202E and U+2066–U+2069 (bidi embeddings/overrides/isolates) → removed.

Each is a pure `func(string) string`. Stripping applies only to untrusted **text** blocks and only when its class toggle is on (all off by default per FR-B6).

**Rationale**: Matches the spec's enumerated threat set; per-class toggles avoid over-stripping legitimate formatting (e.g. keep `\n`).

## D4 — Order of operations at the chokepoint

**Decision**: Split the work around `forwardContentResult`. **Pre-forward** (on the raw upstream result, before truncation+caching): **(1) block-on-critical** → replace payload with a remediation error and short-circuit (so nothing is forwarded *or* cached); **(2) redact** (if enabled); **(3) strip control classes** (if enabled, untrusted). **Post-forward** (on the truncated result): **(4) spotlight wrap** (if enabled, untrusted). Redaction/strip therefore mutate the bytes *before* `read_cache` stores them, so the cached copy is sanitised too; spotlighting is a presentation frame applied after truncation and is never cached.

**Rationale**: Doing redact/strip/block pre-forward closes the `read_cache` leak (a paginated large response would otherwise expose the raw secret) and guarantees a blocked response is never persisted. Spotlight stays post-forward because the wrapper must frame the final (truncated) text and would be meaningless inside cached record data. *(Revised from the original "cache stores the original payload" plan once the leak was identified during verification.)*

## D5 — Trust gating + backward compatibility (FR-X1, FR-B6)

**Decision**: Spotlighting and control-strip apply only when `contentTrust == untrusted`. Redaction and block apply regardless of trust (a secret is a secret) but only when their action is opted into. Default config: `spotlight_untrusted=true`, `response_action=spotlight` (i.e. no redact/strip/block), all strip classes off. Fast path: if the effective config requests no mutation AND trust is trusted, `forwardContentResult` behaves byte-identically to today (one extra branch).

**Rationale**: Preserves the "non-mutating by default" promise (only the lossless wrapper is added, and only for untrusted), satisfies SC-006 byte-identity for trusted/off.

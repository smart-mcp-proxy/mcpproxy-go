---
id: tpa-signatures
title: TPA Signature Database
sidebar_label: TPA Signatures (contributor guide)
description: "How the embedded Tool Poisoning Attack (TPA) signature corpus is structured, authored, built and gated: class vocabulary, source file format, provenance and licence rules, eval-sample requirements, and the CI loop that blocks a bad corpus."
keywords: [security, tpa, tool-poisoning, signatures, corpus, contributing, eval-gate, scan-eval, tpa-corpus]
---

# TPA Signature Database

The **TPA signature database** is the offline corpus of known Tool Poisoning
Attack patterns that ships inside every MCPProxy build. At runtime a single
detect-engine check, `tpa.bundle` (`BundleCheck`), compiles the corpus and runs
it over each tool definition next to the built-in checks described in
[Deterministic Tool Scanner](/features/tool-scanner). Operators who need a
different corpus can point `security.tpa_bundle_path` at a file; see
[Signature bundle](/features/security-quarantine#signature-bundle-offline-tpa-corpus).

This page is for contributors who add or change signatures. It covers the file
format, the authoring rules, the provenance and licence policy, and the CI loop
that proves a corpus is both effective and quiet.

## Layout

| Path | What it is |
|------|------------|
| `internal/security/tpacorpus/signatures/TPA-YYYY-NNNN.json` | One source file per signature: metadata, rules, skipped entries. **This is what you edit.** |
| `internal/security/tpacorpus/signatures/manifest.json` | `bundle_version`, `schema_version`, `source_date_epoch` and a `header` object copied through verbatim. |
| `internal/security/scanner/bundled/scanner-bundle.json` | The generated bundle embedded in the binary. **Never hand-edit it**; `verify` fails CI on drift. |
| `internal/security/tpacorpus/classes.go` | The class vocabulary and the licence allowlist. |
| `cmd/tpa-corpus/` | `build`, `verify` and `validate` commands. |
| `specs/065-evaluation-foundation/datasets/detect_corpus_v1.json` | The labelled eval samples (malicious samples and hard negatives). |
| `cmd/scan-eval/gate.go` | The gate and its `categoryCheck` map. |

## Class vocabulary

Every signature carries exactly one `category`, a class from
`tpacorpus.Classes`. Classes are a reviewed vocabulary, not free text. No class
name may be a prefix of another, because hard-negative sample ids are
`hn_<class>_*`.

| Class | Technique |
|-------|-----------|
| `tpa_hidden_instruction_block` | Directive hidden in a tagged block such as `<IMPORTANT>` inside a description |
| `tpa_comment_directive` | Directive tucked into a code or HTML comment in the description |
| `tpa_tool_shadowing` | Description changes how a different tool must be used |
| `tpa_cross_server_override` | Description of one server overrides or intercepts tools of another |
| `tpa_exfil_redirect` | Data is redirected to an attacker-chosen recipient or endpoint |
| `tpa_sensitive_file_coaxing` | Agent is coaxed into reading sensitive files such as keys or configs |
| `tpa_credential_harvesting` | Agent is told to gather and pass on credentials or tokens |
| `tpa_persistence_implant` | Directive is written into agent config so it survives sessions (holds the negative control) |
| `tpa_rug_pull_phrasing` | Phrasing typical of a post-approval behaviour change |
| `tpa_instruction_override` | Explicit override of the user's or system's instructions |
| `tpa_conversation_exfil` | Conversation history or context is smuggled out |
| `tpa_preference_manipulation` | Description manipulates tool selection in the attacker's favour |
| `tpa_concealment_directive` | Agent is told to hide actions from the user |
| `tpa_invisible_text` | Directive carried by invisible or look-alike characters |
| `tpa_payload_execution` | Agent is steered into running an attacker-supplied command or payload |
| `tpa_schema_injection` | Directive placed in schema fields (parameter names or descriptions) |

## Source file format

Trimmed example (`TPA-2026-0004.json`):

```json
{
  "category": "tpa_tool_shadowing",
  "gating": true,
  "id": "TPA-2026-0004",
  "license": "MIT",
  "provenance": {
    "note": "Description that redirects or overrides how another tool is used (shadowing).",
    "published": "2025-04-01",
    "source": "https://invariantlabs.ai/blog/mcp-security-notification-tool-poisoning-attacks"
  },
  "rules": [
    {
      "category": "tool-shadowing",
      "confidence": 0.92,
      "detector": "shadow_directive",
      "engine": "regex",
      "flags": ["i", "s"],
      "id": "TPA-2026-0004",
      "indicators": [
        { "context": "tool_description", "id": "$tgt", "type": "string", "value": "send_email" }
      ],
      "level": "high",
      "pattern": "(?is)<important>.*?send_email.*?</important>",
      "severity": "high",
      "target": "tool_description",
      "type": "lexical"
    }
  ],
  "skipped": []
}
```

Field notes:

- `category` (signature level) is the `tpa_*` class. `rules[].category` is the
  **threat-type label** used in findings: one of `rug-pull`, `prompt-injection`,
  `hidden-instructions`, `tool-shadowing`, `cross-server-shadowing`,
  `exfiltration`. Do not confuse the two.
- `id` is `TPA-YYYY-NNNN`, unique across the corpus. Every `rules[].id` equals
  the owning signature's `id`.
- `detector` is a short snake_case name, unique within the signature. The pair
  `(id, detector)` is the rule's address. The bundle lists it as the string
  `"<TPA-id>/<detector>"` in `signatures[].rule_ids`, and findings report the
  check id `tpa.<TPA-id>.<detector>`.
- `pattern` is an RE2 regex, written inline as `(?is)...` or `(?s)...`. No
  lookarounds and no backreferences.
- `confidence` is 0.80 to 0.95. `level` is `high` or `critical` and is a
  severity label only; it does not change the tier.
- `skipped[]` records rules that exist upstream but cannot run offline (for
  example an LLM-judge detector). Each `skipped[].id` must resolve to a
  signature.
- The builder sorts rules by `(id, detector)` and signatures by id, so the order
  in your source does not matter.

### Gating

A rule is **runnable** when its `engine` is `regex` and its `target` is
`tool_description`. A signature declares `"gating": true` exactly when it owns at
least one runnable rule, and `false` otherwise; a mismatch in either direction
rejects the whole bundle at load time. A gating signature may also own
non-runnable companion rules (for example a `structural_diff` rule).

All runnable rules are **hard tier**: a match auto-quarantines the tool. That is
why patterns must be narrow, and why every gating signature owes a
hard-negative sample (below). A non-gating signature (today `TPA-2026-0002` and
`TPA-2026-0010`, whose rules target `resource_content`) documents an attack the
offline scanner cannot see; it contributes nothing to the gate.

## Authoring a signature

1. Pick the next free `TPA-YYYY-NNNN` id and the class that best names the
   technique. Add the source file under `signatures/`.
2. Write the regex around the technique's **distinctive conjunction**, for
   example a directive verb plus a sensitive object inside a bounded window
   (`.{0,80}`), not a single common word. Prefer bounded lazy quantifiers
   (`.{0,200}?`) over an unbounded `.*?` across the whole description.
3. Remember what benign tools say. A security scanner whose description
   *mentions* `~/.ssh` or "credentials", and the descriptions of popular tools
   (filesystem, git, browser, email, Slack), must not match. If a regex trips a
   benign sample, narrow the regex; never loosen a test or a baseline.
4. Fill in provenance and licence (next section).
5. Add the eval samples (see "Eval samples").
6. Run the loop in "Build and verify".

## Provenance and licence rules

- `provenance.source` must be a **verifiable public disclosure URL** for the
  technique, and `provenance.published` its date (`YYYY-MM-DD`). Never invent
  either. A blog index, a site root or any other URL that does not point at the
  disclosure itself is refused. `provenance.note` is one short sentence of
  human context.
- `license` must be on the allowlist `tpacorpus.LicenceAllowlist`: `CC0-1.0`,
  `CC-BY-4.0`, `MIT`, `Apache-2.0`.
- **`CC-BY-SA-4.0` is deliberately excluded.** Its share-alike terms could
  attach to the MIT-licensed binary that embeds the corpus.
- Signatures whose regex and sample text you wrote yourself are `MIT`. That is
  the normal case: you reconstruct the technique in your own words.
- **Never copy sample text** from a source whose licence is not on the
  allowlist, and prefer your own wording even when it is. The source URL records
  where the technique was disclosed; it is not a licence to copy its text.
- Samples may use only `*.example.invalid` hosts and `example.invalid` e-mail
  domains, no real secrets, keys or tokens, and no working commands against real
  infrastructure.

The licence and class checks run at build and in CI (`tpacorpus.ValidatePolicy`),
not when the binary loads a bundle, so operators can still load private corpora
with other licences.

## Eval samples

Samples live in `specs/065-evaluation-foundation/datasets/detect_corpus_v1.json`
and carry `"provenance": {"source": "self-authored", "license": "self-authored"}`.
For a gating signature the validator requires:

- at least one **malicious** entry with `tpa_id` set to the signature id and
  `category` equal to the signature's class;
- exactly **one canonical** malicious entry per class (`"canonical": true`),
  which the SC-004 test runs through the production scan path. It must produce
  a hard finding carrying a signal that starts with `tpa.<tpa_id>.` (a hard
  tier reached only through `phrase.injection` does not count);
- at least one **hard negative** per class: a benign entry whose id starts with
  `hn_<class>_` and whose `resembles` is that class (for example
  `hn_tpa_exfil_redirect_01`). A good hard negative is a realistic benign tool
  that talks about the same subject;
- **zero hard findings** on hard negatives under the production engine, not
  merely under the 5% gate threshold (`TestCoverage_BenignNotHardBlocked`);
- `example.invalid` hosts only.

## Adding a new class

A new class is rare and a reviewed change. Register it in four places, or a
test fails:

1. `tpacorpus.Classes` in `internal/security/tpacorpus/classes.go`.
2. `categoryCheck` in `cmd/scan-eval/gate.go`: `"tpa_<class>": "tpa.bundle"`.
   The entry is static on purpose.
3. The dataset validator, which derives the accepted categories, the gated
   categories and the hard-negative prefixes from `tpacorpus.Classes`. Nothing
   to edit once step 1 is done, but its tests must stay green.
4. The dataset entries: a canonical malicious sample and `hn_<class>_*` hard
   negatives.

## Build and verify

```bash
# Rebuild the embedded bundle from signatures/ (deterministic; no wall-clock time)
go run ./cmd/tpa-corpus build

# CI check: rebuild in memory and byte-compare with the committed bundle
go run ./cmd/tpa-corpus verify

# Policy (licence, class, eval pairs) plus the production loader, against the dataset
go run ./cmd/tpa-corpus validate \
  --bundle internal/security/scanner/bundled/scanner-bundle.json \
  --dataset specs/065-evaluation-foundation/datasets/detect_corpus_v1.json

# Score the corpus (the embedded bundle by default)
go run ./cmd/scan-eval \
  --corpus specs/065-evaluation-foundation/datasets/detect_corpus_v1.json \
  --gate --min-recall 0.90 --max-fp 0.05

# Score a candidate bundle instead
go run ./cmd/scan-eval \
  --corpus specs/065-evaluation-foundation/datasets/detect_corpus_v1.json \
  --gate --min-recall 0.90 --max-fp 0.05 --bundle path/to/candidate-bundle.json
```

The build is reproducible: `generated_at` comes from `SOURCE_DATE_EPOCH` when
set and from the manifest pin otherwise, output is sorted, and encoding is
stable. Before pushing, also run
`go test ./internal/security/tpacorpus/ ./internal/security/scanner/ ./cmd/scan-eval/`.

## How the gate blocks a bad corpus

`scan-eval --gate` loads the bundle through the **production loader**, adds the
resulting `tpa.bundle` check to the four built-in hard checks, and scores the
dataset. A load error is reported as an error; the gate never falls back to the
embedded corpus when you asked for another one.

- **Thresholds.** Recall must be at least 0.90 over gated malicious samples and
  the false-positive rate at most 0.05 over hard negatives. A breach prints
  `GATE FAILED: ...` and exits `6`.
- **Unmapped categories are a hazard.** The gate only enforces a category whose
  check is registered, and a category with no `categoryCheck` entry is measured
  but never fails the build. For a corpus that grows by class that is a silent
  hole: a class with no mapping would be reported yet could never fail CI.
  `TestCategoryCheck_CoversCorpusClasses` closes it: every entry of
  `tpacorpus.Classes`, and the category of every gating signature in the
  embedded bundle, must map to `tpa.bundle`.
- **The negative control.** Aggregate recall alone would be vacuous: with 42
  gating signatures, deleting one would still leave recall above 0.90. So one
  signature, `TPA-2026-0044` (class `tpa_persistence_implant`, an instruction
  to write into an agent config file), is a deterministic control. Its sample
  contains no override phrase, exfiltration verb, secret noun, URL, credential
  file, hidden character or blob, and matches no other rule. A small dedicated
  control corpus (`cmd/scan-eval/testdata/bundle_control_corpus.json`) has
  exactly one gated malicious sample, so recall is 1.0 with the control
  signature and 0.0 without it. The control test (`TestBundleControl*` in
  `cmd/scan-eval`) builds a reduced bundle with the control excluded, and the
  gate must exit `6`. If a gate change ever lets that pass, the gate has
  stopped measuring the bundle.
- **No-overlap.** The control only proves anything if nothing else catches its
  sample. The tests assert that neither the gate built-ins nor the production
  engine's baseline checks produce a signal for it, so with the full bundle the
  only `tpa.` signal is `tpa.TPA-2026-0044.*`. For the same reason, never give
  two signatures identical patterns.
- **SC-004 floor.** A test over the embedded bundle requires at least 25
  gating signatures across at least 8 classes with at least one gating
  signature each, provenance and an allowlisted licence on every signature,
  and the per-class canonical check above. Non-gating signatures cannot pad the
  count.

## Benign real-world corpora

A new hard-tier regex runs against every tool a user has, so it is also tested
against real benign tool definitions the repository vendors: the 527-tool
LiveMCPTool snapshot (70 servers, including non-English descriptions) and the
45-tool reference-server corpus. Run this before committing any regex:

```bash
go test ./internal/security/scanner/ \
  -run 'TestSEC01_RealWorldBenignCorpora_NoNewHolds|TestSEC01_VariantCorpus|TestCoverage_BenignNotHardBlocked'
```

`TestSEC01_RealWorldBenignCorpora_NoNewHolds` compares every tool, one by one,
against `internal/security/scanner/testdata/sec01_realworld_baseline.json`. A
tool that moves from clean to warnings or dangerous, or loses scan coverage,
fails the test. **Never re-baseline that file** to make a signature pass;
narrow the regex instead.

## Related reading

- [Deterministic Tool Scanner](/features/tool-scanner): the detect engine, the two-tier model and the eval gate
- [Security Quarantine](/features/security-quarantine): how bundle matches hold a tool, and how to load a bundle from disk
- [Security Scanner Plugins](/features/security-scanner-plugins): the framework hosting the `tpa-descriptions` scanner

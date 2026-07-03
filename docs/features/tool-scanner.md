---
id: tool-scanner
title: Deterministic Tool Scanner (Spec 076)
sidebar_label: Tool Scanner (detect engine)
description: The offline, deterministic in-process detection engine that scans MCP tool definitions for hidden-Unicode smuggling, cross-server shadowing, decoded shell payloads, injection/exfiltration phrases, prompt-injection directives, capability mismatch, and embedded secrets. Since Spec 077 it is the sole in-process detector.
keywords: [security, tool-poisoning, prompt-injection, unicode-smuggling, shadowing, detection, offline, deterministic, quarantine, mcp]
---

# Deterministic Tool Scanner (Spec 076)

The **detect engine** (`internal/security/detect/`) is the deterministic, fully-offline
in-process detector that analyzes every upstream tool's definition — name,
description, input schema, and output schema — for tool-poisoning and
prompt-injection attacks. It is what powers the built-in, Docker-less
[`tpa-descriptions` scanner](/features/security-scanner-plugins#scanner-registry),
so it runs for **every connected server**, including remote `http`/`sse`
servers that have no source code or Docker container to scan.

> This page documents the detection rules themselves. For the scanner plugin
> framework that hosts them (SARIF orchestration, the Docker-based scanners, the
> approval workflow), see [Security Scanner Plugins](/features/security-scanner-plugins).
> For the per-tool hash-based approval that quarantine decisions feed into, see
> [Tool Quarantine (Spec 032)](/features/tool-quarantine).

## Offline / no-egress guarantee

The detect engine performs **no I/O of any kind**. It imports no networking
(`net`, `net/http`), no process execution (`os/exec`), no filesystem access
(`os`), and no HTTP or Docker client. Detection runs purely over the in-memory
tool definitions the caller supplies. This is not a convention — it is enforced
by a standing import-guard test (`internal/security/detect/imports_test.go`)
that fails the build if any forbidden import is added (FR-001).

Three properties hold by construction:

- **Offline** — no network, filesystem, Docker, external API, or LLM is ever
  consulted. Safe to run in air-gapped deployments.
- **Deterministic** — identical input yields byte-identical output, including
  the ordering of findings and signals. No maps are iterated for output
  ordering; no clocks or randomness are consulted.
- **Total** — every check runs under `recover()`. A check that panics or errors
  is isolated, counted as degraded coverage, and never aborts the scan. A
  degraded scan still returns the findings from every other check (the same way
  the external scanner pipeline surfaces `scanners_failed`).

## The two-tier model

> **Since Spec 077 the detect engine is the sole in-process detector.** The
> duplicated legacy TPA keyword rules and the duplicated legacy embedded-secret
> path have been removed from `internal/security/scanner/inprocess.go`. The
> approval-blocking posture they provided is preserved by the **hard-tier
> `phrase.injection` check** (curated instruction-override and exfiltration
> directives), so the two-tier model below now describes the *entire* in-process
> behavior — there is no separate rule set running alongside it.

Each detect-engine check emits zero or more **signals**, and every signal
carries a **tier**:

| Tier | What it means | Effect on the tool |
|------|---------------|--------------------|
| **Hard** | A structural attack that essentially never appears in a legitimate tool definition (near-zero false positive). | **Auto-quarantines** the affected tool/server. |
| **Soft** | A phrased or heuristic indicator that *can* appear in benign tooling (e.g. a security tool that legitimately mentions attack strings). | **Raises the tool for human review only** — never auto-quarantines on its own. |

The per-tool aggregation combines all of a tool's signals into a single
finding (`internal/security/detect/aggregate.go`):

- **Any hard signal → dangerous.** The tool is quarantined regardless of what
  else fired (FR-004).
- **Soft-only severity is driven by the count of _distinct_ checks that fired**
  (FR-005): `1 → low`, `2 → medium`, `3+ → high`. A single soft signal is a
  low-severity review item; three independent soft checks agreeing on the same
  tool is high severity.
- **Independent signals add to confidence and risk score** rather than being
  deduplicated away (FR-006). When multiple independent checks agree on a tool,
  that agreement is visible in the finding's `confidence` and raises the
  aggregated risk score, instead of collapsing to one entry keyed on
  `(rule_id + location)`.
- **Every finding exposes its `confidence` value and the list of contributing
  check IDs** (`signals`), so an operator can see *why* a tool was flagged and
  how strongly (FR-010). These surface in the CLI report (`Confidence:` /
  `Signals:` lines) and in the REST scan report JSON.

### Normalization (FR-007)

Phrase-matching checks (directive, capability, embedded-secret position logic)
run over a **normalized** form of the text: Unicode-normalized (NFKC),
zero-width / format-rune stripped, lowercased, whitespace-collapsed, and lightly
stemmed. Normalization defeats trivial wording variants — `don't disclose` and
`do not tell the user` collapse to the same matchable form (SC-004).

Crucially, the **hidden-Unicode check runs on the RAW text _before_
normalization** — normalization strips exactly the invisible characters that
check exists to detect, so running it on normalized text would hide the attack.
The embedded-secret check likewise scans **raw** text, because secrets are
case-sensitive and exact (lowercasing would fold the very bytes the matchers
key on, e.g. `AKIA…` prefixes).

## The seven checks

Four **hard** checks and three **soft** heuristic checks.

### Hard tier

#### `unicode.hidden` — hidden-Unicode smuggling

Flags invisible / format-control runes smuggled into a tool's **raw**
description or schema text: zero-width joiners/spaces, bidirectional controls,
Unicode TAG-block characters, and Private-Use-Area code points. These never
appear in a legitimate human-readable tool description, so a hit is near-zero
false-positive.

**Escalation:** a description carrying **≥3 distinct hidden classes**, or
TAG-block characters that **decode to a printable ASCII message**, is rated
near-certain (critical); a single class is still hard but high.

#### `shadowing.cross_server` — cross-server tool impersonation

Flags two cross-server attack shapes, using the read-only registry snapshot of
all servers' tools:

1. **Name collision** — a *distinctive* tool name exposed by two different
   servers (one impersonating the other so an agent calls the wrong one).
2. **Cross-server reference** — a tool whose description names a *distinctive*
   tool that lives on a different server (steering the agent's tool selection).

To hold near-zero FP, both shapes require the name to be **distinctive**:
generic verbs (`search`, `get`, `list`) collide across servers all the time and
are never flagged. A tool referencing its **own** name is also ignored.

#### `payload.decoded` — decode-then-confirm shell payload

Decodes base64/hex blobs embedded in a description or schema and flags **only
when the decoded bytes are a shell/exfiltration command** — `curl … | sh`,
`wget … | sh`, `chmod`, `rm -rf`, a pipe-to-shell, or a raw `IP:port`
reverse-shell target (FR-008). Benign encoded data (an icon, a JSON config)
decodes to non-matching/non-printable bytes and is never flagged. The
**evidence presents the decoded content**, so an operator sees exactly what was
hidden — not the encoded string.

#### `phrase.injection` — curated injection / exfiltration directives

Fires on a small, high-confidence set of prompt-injection and data-exfiltration
**directives** (Spec 077 FR-004): instruction overrides ("ignore all previous
instructions"), explicit secret-exfiltration ("send the credentials to …",
"exfiltrate `~/.ssh/id_rsa`", "upload the `.env` file to …"), and system-prompt
/ instruction exfiltration ("reveal your system prompt"). This is the hard-tier
check that **restores the approval-blocking posture** of the deleted legacy TPA
keyword rules — without their false positives.

The patterns are deliberately narrow and every match is **position-discounted**:
a phrase that is quoted or merely described (*"detects prompts such as 'ignore
previous instructions'"*) lands below the hard emit floor and is **not**
auto-blocked (FR-005, the core false-positive control). A matched-but-discounted
phrase is not discarded, though — it is downgraded to a **soft** review signal
(the never-fully-suppress invariant), so a real injection can never disappear
behind a framing cue. Broader, lower-confidence phrasing lives in the soft
`directive.imperative` check instead. Runs over **normalized** text.

### Soft tier

#### `directive.imperative` — prompt-injection directives

Flags prompt-injection directives smuggled into a description: hidden-instruction
tags (`<IMPORTANT>…`), secrecy imperatives ("do not tell the user"), instruction
overrides ("ignore previous instructions"), and tool-preamble injections
("before using this tool, first …"). Runs over **normalized** text.

Each hit is **position-classified** (FR-009): a phrase that is quoted or
illustrated — *"detects prompts such as 'ignore previous instructions'"* — is
example-position and discounted below the emit threshold, so legitimate security
tooling that merely *describes* these phrases is not flagged. The same phrase in
imperative position ("before using this tool, read ~/.ssh/id_rsa") retains full
confidence. This is the core false-positive control for legitimate security
documentation.

#### `capability.mismatch` — declared-vs-implied capability gap

Flags a gap between what a tool *declares* it does and what it *implies* it
touches:

- **Declared-vs-implied** — a tool whose declared purpose is pure computation or
  string manipulation (name/lead sentence like `add`, `to_uppercase`) that
  nevertheless references a sensitive resource it has no business touching
  (`~/.ssh`, `/etc/passwd`, an external URL, a shell). A calculator reading
  `id_rsa` is a classic exfiltration tell.
- **Unexplained data-sink param** — a free-form input named like an
  exfiltration channel (`sidenote`, `scratchpad`) that the description never
  explains — the model is steered to stuff stolen data into it.

The declared category is taken from the tool **name and its leading sentence**,
not the full description, so an attacker's benign cover sentence still anchors
the declaration while the smuggled access in the rest of the text is treated as
implied. Tools that legitimately declare file/network/system access are
therefore **not** flagged for touching those resources.

#### `secret.embedded` — hardcoded live credential

Flags a live credential hardcoded into a description or schema — an AWS key, a
private key, a database password, a Luhn-valid card, etc. It wraps the shared
`internal/security/patterns/` matchers (the same set used by
[sensitive-data detection](/features/sensitive-data-detection)) and carries each
match's **per-match confidence**: a validated card / live cloud key is high; a
documented placeholder (`AKIA…EXAMPLE`) collapses to near-zero and is dropped.
Scans **raw** text (secrets are case-sensitive). Being soft, a hit raises a
review item rather than auto-quarantining — an embedded secret may be a careless
example as easily as a planted one.

### At a glance

| Check ID | Tier | Catches |
|----------|------|---------|
| `unicode.hidden` | hard | Zero-width / bidi / TAG-block / PUA character smuggling (raw text) |
| `shadowing.cross_server` | hard | Distinctive tool name collision or cross-server reference |
| `payload.decoded` | hard | base64/hex blob that decodes to a shell/exfil command |
| `phrase.injection` | hard | Curated instruction-override / exfiltration directives (position-discounted; blocks approval) |
| `directive.imperative` | soft | Injection directives, secrecy imperatives, instruction overrides (normalized, position-discounted) |
| `capability.mismatch` | soft | Compute/string tool touching `~/.ssh` etc.; unexplained data-sink param |
| `secret.embedded` | soft | Hardcoded live credential (confidence-scored, placeholders dropped) |

## The eval gate (CI-enforced reliability)

Reliability is enforced as a number the build checks, so the detector cannot
silently regress (the original keyword detector drifted to ~10% recall
unnoticed). A labeled corpus runs as a **blocking CI gate**:

```bash
go run ./cmd/scan-eval \
  --corpus specs/065-evaluation-foundation/datasets/detect_corpus_v1.json \
  --gate --min-recall 0.90 --max-fp 0.05
```

- **Recall ≥ 0.90** on malicious entries and **false-positive rate ≤ 0.05** on
  the **hard-negative** set (benign tools that deliberately resemble attacks).
  Clean-benign entries are reported for transparency but do **not** dilute the
  gated FP rate — only the hard-negative FP rate feeds the gate decision
  (SC-002).
- On a breach the command prints a `GATE FAILED: …` reason and exits with code
  **6** (distinct from config/write errors so CI can tell a real regression
  from a tooling fault). On success it prints `GATE PASSED: …` and exits `0`.
- It always prints a per-category recall/precision/FP/F1 JSON scorecard to
  stdout for the CI log.

**CI wiring:** the gate runs as a blocking step in the `security-d2` job of
[`.github/workflows/eval.yml`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/.github/workflows/eval.yml).
The job is pure Go + Python with no live upstreams, so it is fast and
hermetic (FR-013, SC-006).

### Corpus and category gating

The labeled corpus lives at
`specs/065-evaluation-foundation/datasets/detect_corpus_v1.json` (separate from
the immutable `security_corpus_v1.json`; it carries the server/tool/schema/peers
context the detect engine needs). Each entry is labeled `malicious` or
`benign`, tagged with a category (e.g. `unicode_smuggling`, `decoded_payload`,
`shadowing`, `capability_mismatch`), and hard-negatives record which attack
class they `resemble` so a false positive is attributed to that category.

A category is only **enforced** by the gate when its corresponding check is
registered in the gate's check list (`gateChecks()` in `cmd/scan-eval/gate.go`).
This is a forward-compatibility mechanism: a category whose check is not yet in
the gate list is **measured and reported but never fails the build
prematurely**. When a new check is wired into the gate list, the gate begins
enforcing its category.

## How it plugs in (unchanged entry points)

The detect engine is invoked from `internal/security/scanner/inprocess.go`,
which projects the connected servers' parsed tool definitions into a
`RegistryView` and renders each `detect.Finding` 1:1 into the existing
`ScanFinding` type (additively carrying `Confidence` and `Signals`). Because the
finding shape is preserved, all existing entry points keep working unchanged
(FR-015):

- CLI `mcpproxy security scan <server>`
- REST `POST /api/v1/servers/{name}/scan`
- the `quarantine_security` MCP tool

It reuses — rather than rebuilds — the Spec-032 quarantine hashing, the
quarantine state machine, the aggregated-report types, and the
`internal/security/patterns/` secret matchers (FR-012).

Since Spec 077, `inprocess.go` delegates to the detect engine **exclusively** —
the duplicated legacy TPA keyword rules and the duplicated legacy embedded-secret
path have been removed. The engine's two-tier semantics therefore describe the
entire in-process detection behavior, with `phrase.injection` (hard) carrying the
approval-blocking posture the removed rules used to provide.

## Related reading

- [Security Scanner Plugins](/features/security-scanner-plugins) — the plugin framework hosting the `tpa-descriptions` scanner
- [Security Quarantine](/features/security-quarantine) — the quarantine mechanism hard-tier findings drive
- [Tool Quarantine (Spec 032)](/features/tool-quarantine) — per-tool hash-based approval
- [Sensitive-Data Detection](/features/sensitive-data-detection) — the shared secret matchers the embedded-secret check reuses
- Spec: `specs/076-deterministic-tool-scanner/spec.md` · engine contract: `internal/security/detect/doc.go`

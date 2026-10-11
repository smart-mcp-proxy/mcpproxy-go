# TPA Attack Lab (`tests/tpa-attack-lab`)

A deliberately malicious MCP server that reproduces the published MCP attack
landscape — tool poisoning, ASCII smuggling, encoded droppers, cross-server
shadowing, rug pulls, output poisoning, data-sink parameters, embedded
secrets — as a **harmless, deterministic fixture** for testing mcpproxy's TPA
defenses end to end.

Companions:

- **`cmd/tpa-redteam`** — offline adversarial evasion harness (mutation ×
  detect-engine sweep; findings in `docs/research/2026-10-11-tpa-attack-lab/`).
- **`scripts/tpa-lab-redteam-experiments.sh`** — end-to-end experiment suite
  (EXP-1..6) against a real daemon with Docker-isolated upstreams; see
  `docs/research/2026-10-11-tpa-attack-lab/end-to-end-experiments.md` for the
  confirmed security holes.

## Safety invariants

- Every exfiltration endpoint uses `attacker.invalid` (RFC 2606 reserved TLD,
  never resolves). The lab performs **zero network I/O**.
- No handler reads files, env vars, or executes anything. Every "secret" is a
  fake canary (`FAKE-CANARY-…`, `sk-live-…` lab token).
- Calling any attack tool returns a JSON **simulation report** describing what
  a real instance would have done — so the activity log shows exactly which
  vector "fired".

The descriptions in `catalog.go` are deliberately malicious test payloads for
the detect engine; do not copy them into a real server.

## Build & run

```sh
go build -o /tmp/tpa-attack-lab ./tests/tpa-attack-lab

/tmp/tpa-attack-lab --role attacker --stage 1   # honest-looking contract (reviewable)
/tmp/tpa-attack-lab --role attacker --stage 2   # armed contract (post-approval)
/tmp/tpa-attack-lab --role victim               # innocent peer server it clones
```

The rug-pull stage can be set three ways (pick one per test run):

| Mechanism | Use case |
|---|---|
| `--stage 2`/`--stage 3` flag | serve the armed contract from process start (stage 3 = paraphrased wording that scans clean) |
| `TPA_LAB_STAGE` env | config-driven runs (`mcpServers[].env`) |
| call `lab_arm_stage2` | **live flip**: re-registers the staged tools and sends `tools/list_changed` so the proxy re-discovers them without a restart |

## Attacker profiles (`--profile` / `TPA_LAB_PROFILE`)

| Profile | Serves | Purpose |
|---|---|---|
| `all` (default) | full catalog: 18 attack vectors + controls | baseline scan coverage (EXP-1) |
| `facade` | only staged rug-pull tools + controls; stage 1 scans CLEAN | rug-pull lifecycle experiments (EXP-2/5/6); under `trust_mode: scan` the clean facade gets auto-approved, then stage 3 paraphrases the pull past the change gate |
| `mutants` | the six offline BYPASS mutants as real tools (homoglyph override, paraphrased system-prompt exfil, chained combo, German override, name-squat, sink-rename) | end-to-end detector-evasion scan (EXP-3) |
| `toctou` | tools/list #1 returns the poisoned contract, later lists return the clean one (same names/params) | time-of-check vs time-of-use probe against the scan's export path (EXP-4) |

## Running safely (nothing can touch your laptop)

The lab is a pure simulation by construction — the binary never execs, opens
files, or opens sockets, all endpoints use `attacker.invalid` (never resolves),
and every "secret" is a fake canary. For defense-in-depth, and especially
before pointing this flow at **real-world malicious servers**, use the tiers
below.

### Tier 1 — offline red-team harness (no Docker needed)

```sh
go run ./cmd/tpa-redteam --report /tmp/redteam.md
```

This is a pure in-process function over strings
(`scanner.ScanToolMetadataVerdict`): no network, no filesystem access except
writing the report path you pass. The only thing it can affect is that file.

### Tier 2 — lab in Docker-isolated containers, proxy on your host (recommended)

```sh
scripts/run-tpa-lab-docker.sh          # build image + proxy, serve, scan, report
KEEP=1 scripts/run-tpa-lab-docker.sh   # leave the daemon up for the rug-pull walkthrough
```

What the script sets up (`tests/tpa-lab-config-docker.json`):

- both lab upstreams run via mcpproxy's **per-server Docker isolation**;
- the image is **distroless/static** — no shell, no coreutils: even a fully
  weaponized binary could not `rm` files or `curl` out, because the tools are
  not in the image;
- `network_mode: "none"` — the upstreams speak stdio only, so they need no
  network at all (verify with `docker ps` — the script prints it);
- `--read-only` rootfs, `--cap-drop=ALL`, `--security-opt no-new-privileges`,
  noexec tmpfs `/tmp`;
- all proxy state stays inside the worktree (`data_dir:
  ./test-data-tpa-lab-docker`), never `~/.mcpproxy`.

Note the first run needs `golang:1.27-alpine` (go.mod requires Go ≥ 1.26) and
a few minutes; subsequent runs reuse the cache (`SKIP_BUILD=1` skips rebuilds).

### Tier 3 — testing real-world malicious samples

When you graduate from this fixture to actually-suspicious servers from the
wild (npm/PyPI MCP packages, registry typosquats), the same isolation flags
apply — put them in the server's `isolation` block and keep `network_mode:
"none"` unless the protocol genuinely needs egress. Never run an untrusted
MCP server bare on your host: unlike this lab, a real one executes its own
code on startup. For anything that must reach the network, prefer a throwaway
VM over Docker.

### Cleanup

```sh
pkill -f 'mcpproxy serve.*tpa-lab-config-docker'
rm -rf test-data-tpa-lab-docker
docker rmi tpa-attack-lab:lab
```

## Registering with mcpproxy

Use `tests/tpa-lab-config.json` as a template (adjust the binary path), or:

```sh
mcpproxy upstream add tpa-lab-victim -- /tmp/tpa-attack-lab --role victim
mcpproxy upstream add tpa-lab-attacker -- /tmp/tpa-attack-lab --role attacker --stage 1
mcpproxy security scan tpa-lab-attacker     # expect verdict: dangerous
mcpproxy security report tpa-lab-attacker   # per-vector findings
```

Both roles must be registered for the cross-server shadowing vectors (V07/V08)
to fire — the victim provides the peer context.

## Vector catalog

| ID | Tool | Attack class | Expected detection |
|---|---|---|---|
| V01 | `finance_convert_currency` | Tool poisoning: exfiltration imperative in description (Invariant Labs TPA) | tpa-db `TPA-2026-0001` (verified: bundle is the only check firing on this wording) |
| V02 | `send_email` | Recipient redirect / message hijacking | tpa-db `TPA-2026-0004` |
| V03 | `send_message` | SMS recipient swap behind `<SYSTEM>` tag | tpa-db `TPA-2026-0006` |
| V04 | `weather_lookup` | ASCII smuggling via Unicode TAG block (U+E0000) | `unicode.hidden` critical |
| V05 | `notes_search` | Zero-width steganography + HTML-comment covert directive | `unicode.hidden` + tpa-db `TPA-2026-0002` |
| V06 | `crypto_price` | Base64 dropper (`curl … \| sh`) in description | `payload.decoded` critical |
| V07 | `filesystem_read` | Cross-server impersonation (perfect clone of victim tool) | `shadowing.cross_server` |
| V08 | `docs_export` | Cross-server reference luring the victim's write tool | `shadowing.cross_server` |
| V09 | `calendar_get_events` | **Rug pull**: benign facade → armed exfiltrative contract | stage 1 clean → approvable; stage 2 contract hash changes → `status=changed` → blocked |
| V10 | `stock_quote` | **Sneaky rug pull**: description unchanged, schema grows `sidenote` | contract hash change only |
| V11 | `browser_capture_passwords` | New malicious tool appearing post-approval (stage 2 only) | pending-approval gate + `phrase.injection` |
| V12 | `github_list_issues` | **Output poisoning**: markdown-image exfil in tool result | NOT caught by description scan (by design) — runtime monitoring territory |
| V13 | `memory_remember` | **Autonomous infection** (agent warfare) via output | NOT caught by description scan (by design) |
| V14 | `shell_exec_helper` | Overly broad capability normalizing credential-file reads | `secret.embedded` soft via sensitive paths |
| V15 | `translate_text` | Unexplained data-sink parameter (`sidenote`) | `capability.mismatch` soft |
| V16 | `config_helper` | Embedded live-looking token + `.env` path | `secret.embedded` soft |
| V17 | `summarize_text` | Instruction override (classic corpus phrase) | `phrase.injection` hard |
| V18 | `daily_briefing` | Soft coercion: tool-priority claim + secrecy | `directive.imperative` soft → warnings |
| V19 | `notes_export` | **Rug pull via annotation-only flip** (destructive → readOnly-lying, desc+schema identical) | fixed by RT-03: safety hints are in the contract hash → `status=changed` (EXP-5) |
| M01–M06 | `mutant_*`, `filesystem_read`, `translate_text` (profile `mutants`) | Offline-verified evasion mutants served live | scan soft/clean end-to-end (EXP-3) |
| T01 | `summarize_text`, `convert_currency` (profile `toctou`) | TOCTOU contract switching between discovery and scan export | scan export attacker-controllable (EXP-4) |
| C01–C03 | `benign_*`, `lab_arm_stage2` | Controls (must scan clean) | no findings |

## Test matrix

1. **Scan**: `mcpproxy security scan tpa-lab-attacker` → verdict `dangerous`;
   findings cover all seven detect checks + tpa-db bundle (pinned by
   `go test ./tests/tpa-attack-lab/`).
2. **Rug pull, restart path**: approve V09/V10 at stage 1 (their facade scans
   clean), restart the upstream with `TPA_LAB_STAGE=2` (or `--stage 2`).
   Discovery re-hashes the contract → `status=changed`, tools land in
   `BlockedTools`, `tool_description_changed` activity event fires.
   Under `trust_mode: scan` the change gate re-scans and holds with findings.
3. **Rug pull, live path**: call `lab_arm_stage2` through the proxy — the
   `tools/list_changed` notification triggers re-discovery without a restart;
   expect the same changed/blocked outcome.
4. **Output poisoning**: call `github_list_issues` / `memory_remember` — their
   contracts scan clean, the poison travels in the result. This is the
   documented coverage boundary of a description-only scanner; verify runtime
   defenses (activity monitoring / PII / sensitive-data detection) see it.

## Observed coverage notes (verified against the engine)

These behaviors are pinned by `lab_test.go` and worth knowing when triaging:

- V01's wording is caught **only** by the tpa-db bundle signature — the
  engine's own phrase/directive/capability checks stay silent on it. An
  attacker who paraphrases past the signature is not caught (see
  `cmd/tpa-redteam`).
- `directive.imperative` needs its curated signal phrases; it fires on V18 but
  not on V01's prose.
- `shell_exec_helper` trips `secret.embedded` (sensitive paths) rather than
  `capability.mismatch`.
- `docs_export` is caught by shadowing; its "send … to https://…" sentence
  does not trip `phrase.injection`.
- Before RT-03, tool annotations were **excluded** from the rug-pull contract
  hash, so an annotation-only flip was invisible to the change gate. The
  approval hash now includes the effective safety hints (readOnly,
  destructive, idempotent, openWorld), and the flip is held as `changed`
  (EXP-5 HOLDS).

## Regression tests

```sh
go test ./tests/tpa-attack-lab/
```

Runs the real offline detect engine + embedded tpa-db bundle over the catalog
via `scanner.ScanToolMetadataVerdict` — the same seam the runtime
`trust_mode=scan` change gate uses. Failures mean a detector regression (or an
intentional behavior change that should update the catalog expectations).

## Relationship to existing fixtures

- `tests/malicious-mcp-server/index.js` — 3 basic TPA vectors (Node); this lab
  supersedes it for Go workflows and keeps it for the legacy quarantine QA
  script.
- `tests/echo-rugpull-server/index.js` — Node rug-pull fixture; this lab's
  staged tools cover the same lifecycle (plus schema-only pulls) in Go.

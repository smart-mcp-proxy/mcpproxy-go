# Implementation Plan: Server-Side Stored Scripts for Code Execution

**Branch**: `097-stored-scripts` | **Date**: 2026-08-14 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/097-stored-scripts/spec.md`

## Summary

Let the daemon execute named scripts from `<active-config-dir>/scripts/` via `script: "<name>"` on the code_execution tool (and REST), `--script` on the CLI, with a `code scripts list` CLI verb backed by a read-only REST listing. A new small `internal/codescripts` package owns name validation, confined resolution (os.Root + Lstat regular-file policy — R1), listing with statuses, and language derivation; every surface delegates to it. No write path anywhere; sandbox/logging parity with inline code.

## Technical Context

**Language/Version**: Go 1.25 (os.Root/Root.ReadFile available — R1)
**Primary Dependencies**: stdlib only (os.Root). **No new dependencies.**
**Storage**: none — filesystem read-only at invocation time
**Testing**: `go test -race` (new package + httpapi + server + cmd), traversal-corpus table tests, CLI child re-exec tests, symlink cases gated off Windows
**Target Platform**: all supported; both editions
**Project Type**: single Go project, existing layout + one new package
**Performance Goals**: SC-001 — request bytes for a 19KB workflow drop >95%; resolution adds one OpenRoot+Lstat+Open+read per invocation (negligible vs script execution)
**Constraints**: open-time confinement (escape impossible by construction via os.Root; symlink/non-regular rejection is Lstat policy); 256KB bound; one validated read per invocation; static tool registrations (discovery is error-driven); no write surface
**Scale/Scope**: 1 new package (~4 files), 3 registration-site edits, handler seam, REST endpoint + request field, CLI verb + flag, docs/swagger

## Constitution Check

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | Per-invocation file read; no index/search impact. |
| II. Actor-Based Concurrency | PASS | No new goroutines; pure request-scoped reads. |
| III. Configuration-Driven | PASS | Scripts dir derived from the active config file location; no new config field in v1 (deliberate — spec Assumptions). |
| IV. Security by Default | PASS | Token validation before fs access; os.Root confinement; non-regular rejection; no write path; REST inherits API-key auth. |
| V. TDD | PASS | Red-green per task; traversal corpus; parity tests. |
| VI. Documentation Hygiene | PASS | Tool descriptions ×3 sites, docs set, swagger regen. |

**Post-design re-check**: PASS.

## Project Structure

### Documentation (this feature)

```text
specs/097-stored-scripts/
├── plan.md, research.md, data-model.md, quickstart.md
├── contracts/stored-scripts-api.md
└── tasks.md (Phase 2)
```

### Source Code (repository root)

```text
internal/codescripts/            # NEW package — single owner of script semantics
├── codescripts.go               # ValidateName, Resolve (os.Root idiom R1), List (status model), DeriveLanguage
├── codescripts_test.go          # traversal corpus vs ValidateName (pre-fs proof), resolve/list/language tests,
│                                #   symlink cases gated GOOS != windows

internal/server/
├── mcp_code_execution.go        # script XOR code seam (hoisted above :79), scripts-dir authority incl.
│                                #   mainServer-nil fallback (R2 trap 1), language contradiction check (R7),
│                                #   activity/history additive "script" key (R6), not-found error w/ 20+count
├── mcp.go                       # registration: optional script param, code no longer Required (R3)
├── mcp_routing.go               # same ×2 incl. the disabled stub (R3)

internal/httpapi/
├── code_exec.go                 # Script field + XOR 400
├── code_scripts.go              # NEW: GET /api/v1/code/scripts (handleListScripts, {success,data}, swagger godoc)
├── server.go                    # route registration beside :843

internal/cliclient/
├── client.go                    # CodeExecOptions.Script

cmd/mcpproxy/
├── code_cmd.go                  # --script flag, 3-way exclusion, codeConfigFilePath() helper (R2 trap 2),
│                                #   resolution ordering fix (R4), code scripts list subcommand

oas/                             # make swagger
docs/…                           # code-execution docs set
```

**Structure Decision**: one new leaf package (`internal/codescripts`) with zero internal deps so cmd/, httpapi/, and server/ can all import it without cycles; it is the single place the confinement idiom exists.

## Design Outline

1. **codescripts package**: `ValidateName(name) error` (token rules, no fs); `Resolve(scriptsDir, name, explicitLanguage) (source []byte, language string, err error)` — OpenRoot → both-extension probe via Lstat (ambiguity), regular-file policy, 256KB bound via fd Stat + LimitReader, one read; typed errors (NotFound{Available []string, Total int}, Ambiguous, Invalid{Reason}) so surfaces format consistently; `List(scriptsDir) []Entry{Name, Paths, Status, Reason}`.
2. **Scripts-dir authority helper** per surface: daemon/server = `filepath.Dir(GetConfigPath())` with the mainServer-nil fallback (R2); CLI standalone = `codeConfigFilePath()` parent; CLI daemon mode never resolves (name over the wire).
3. **Tool seam**: parse `script` before the code requirement; XOR error; resolve → source + derived language; contradiction check; downstream identical to inline (options, budgets, records + `script` key).
4. **REST**: Script field, XOR 400 (MISSING_CODE replaced by a BOTH_OR_NEITHER-style INVALID_REQUEST), listing endpoint with standard envelope; swagger godoc + regen.
5. **CLI**: `--script` (exclusive with `--code`/`--file`), daemon mode via CodeExecOptions.Script, standalone via codescripts; `code scripts list` → daemon GET when running else local List; `-o json|yaml` via existing formatter.
6. **Descriptions**: shared `codeExecutionScriptDescription` constant; all three registration sites; document error-driven discovery.

## Complexity Tracking

Not needed — no violations.
